#!/usr/bin/env python3
"""Exercise an offline built agent with temporary runtime state and loopback HTTP."""
import argparse
import json
from pathlib import Path
import signal
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=Path("apps/agent/bin/bloomer-agent"))
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="bloomer-smoke-") as directory:
        root = Path(directory)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        config = root / "config.json"
        state = root / "state"
        logs = root / "logs"
        config.write_text(json.dumps({
            "data_dir": str(state), "log_dir": str(logs),
            "listen": f"127.0.0.1:{port}",
            "upstream_credential_file": str(root / "nonexistent-official-token"),
        }))
        command = [str(binary), "-config", str(config)]
        processes = []
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        url = f"http://127.0.0.1:{port}/v1/health"

        def request(token=None):
            headers = {"Authorization": "Bearer " + token} if token else {}
            return opener.open(urllib.request.Request(url, headers=headers), timeout=1)

        def start():
            process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            processes.append(process)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError("agent exited before becoming ready: " + process.stderr.read().decode())
                try:
                    request()
                except urllib.error.HTTPError as error:
                    if error.code == 401:
                        error.close()
                        break
                except OSError:
                    pass
                time.sleep(0.05)
            else:
                raise RuntimeError("agent did not become ready")
            token = (state / "access.token").read_text().strip()
            with request(token) as response:
                health = json.load(response)
            assert health["collection_state"] == "disabled"
            assert health["data_stale"] is True
            assert health["last_successful_sync_at"] is None
            assert health["schema_version"] == 1
            return process, token

        def timestamp():
            # Allow SQLite to recreate WAL bookkeeping after the Go process exits;
            # query_only prevents this verification connection changing data.
            database = sqlite3.connect(f"file:{state / 'agent.db'}?mode=rw", uri=True)
            try:
                database.execute("PRAGMA query_only=ON")
                return database.execute("SELECT last_clean_shutdown_at_us FROM maintenance_state WHERE id=1").fetchone()[0]
            finally:
                database.close()

        def stop(process):
            process.send_signal(signal.SIGTERM)
            if process.wait(timeout=12) != 0:
                raise RuntimeError("unclean SIGTERM shutdown: " + process.stderr.read().decode())

        try:
            first, token = start()
            assert timestamp() is None
            duplicate = subprocess.run(command, capture_output=True, timeout=5)
            assert duplicate.returncode != 0 and b"lock" in duplicate.stderr
            stop(first)
            clean_shutdown = timestamp()
            assert clean_shutdown is not None
            second, same_token = start()
            assert token == same_token
            second.kill()
            second.wait(timeout=5)
            assert timestamp() == clean_shutdown
            third, same_token = start()
            assert token == same_token
            stop(third)
            assert timestamp() > clean_shutdown
            for log in logs.glob("agent.log*"):
                assert token not in log.read_text()
            assert (state / "agent.db").stat().st_mode & 0o777 == 0o600
            assert state.stat().st_mode & 0o777 == 0o700
            print("Offline lifecycle smoke passed: auth, single instance, SIGTERM, SIGKILL restart, persistence, private paths")
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=5)
                if process.stderr:
                    process.stderr.close()


if __name__ == "__main__":
    main()
