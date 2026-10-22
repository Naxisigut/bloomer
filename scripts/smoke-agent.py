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
    parser.add_argument("--require-host-metrics", action="store_true", help="require working macOS CPU/memory/thermal/disk readings")
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
            "darkbloom_binary": str(root / "nonexistent-darkbloom"),
            "listen": f"127.0.0.1:{port}",
            "upstream_credential_file": str(root / "nonexistent-official-token"),
            "provider_state_file": str(root / "nonexistent-daemon-state.json"),
            "network_rates": {"enabled": False},
        }))
        command = [str(binary), "-config", str(config)]
        processes = []
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        def request(token=None, path="/v1/health", origin=None, method="GET", data=None, extra_headers=None):
            headers = {"Authorization": "Bearer " + token} if token else {}
            if origin is not None:
                headers["Origin"] = origin
            headers.update(extra_headers or {})
            endpoint = f"http://127.0.0.1:{port}" + path
            return opener.open(urllib.request.Request(endpoint, headers=headers, method=method, data=data), timeout=1)

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
            assert health["collection_state"] in {"idle", "collecting", "degraded", "rate_limited"}
            assert health["data_stale"] is True
            assert health["last_successful_sync_at"] is None
            assert health["schema_version"] == 8
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
            origin = "http://127.0.0.1:18080"
            for method in ("GET", "POST"):
                with request(path="/v1/device/control/jobs", origin=origin, method="OPTIONS", extra_headers={
                    "Access-Control-Request-Method": method,
                    "Access-Control-Request-Headers": "authorization,content-type",
                }) as response:
                    assert response.status == 204
                    assert response.headers["Access-Control-Allow-Origin"] == "*"
            for source in (origin, "http://localhost:18081", "https://dashboard.example", "null"):
                with request(token, origin=source) as response:
                    assert response.status == 200
                    assert response.headers["Access-Control-Allow-Origin"] == "*"
                    assert response.headers.get("Access-Control-Allow-Credentials") is None
            for auth, source, method, path, data, expected in (
                (None, origin, "GET", "/v1/health", None, 401),
                ("incorrect", origin, "POST", "/v1/device/control/jobs", b"{}", 401),
                (None, "https://dashboard.example", "GET", "/v1/health", None, 401),
                (token, origin, "POST", "/v1/device/control/jobs", b"{}", 400),
            ):
                try:
                    request(auth, path, source, method, data, {"Content-Type": "application/json"})
                    raise AssertionError("expected API rejection")
                except urllib.error.HTTPError as error:
                    assert error.code == expected
                    assert error.headers.get("Access-Control-Allow-Origin") == "*"
                    error.close()
            assert timestamp() is None
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                with request(token) as response:
                    health = json.load(response)
                if health["last_error"] == "credential_unavailable" and health["cleanup"]["last_success_at"]:
                    break
                time.sleep(0.02)
            else:
                raise RuntimeError("collection failure and cleanup were not exposed")
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                with request(token, "/v1/device/state") as response:
                    device = json.load(response)
                if device["sample"] is not None:
                    break
                time.sleep(0.05)
            else:
                raise RuntimeError("device collection did not continue after earnings failure")
            sample = device["sample"]
            assert sample["device_id"] == device["device"]["device_id"]
            assert sample["provider_running"] is None
            assert sample["quality"]["provider_running"] == "unavailable"
            assert sample["cpu_temperature_c"] is None and sample["dispatch_tier"] is None
            assert sample["inference_active"] is None and sample["request_work_pending"] is None
            assert sample["is_idle"] is None
            assert sample["activity_error"] == "task_state_unavailable"
            assert "routing_state" not in sample
            if sample["gpu_percent"] is not None:
                assert sample["quality"]["gpu_percent"] == "ok"
                assert 0 <= sample["gpu_percent"] <= 100
            if args.require_host_metrics:
                for field in ("cpu_percent", "memory_total_bytes", "memory_available_bytes", "memory_pressure", "swap_used_bytes", "thermal_state", "disk_available_bytes"):
                    assert sample["quality"][field] == "ok", f"host metric unavailable: {field}"
                    assert sample[field] is not None
            try:
                request(token, "/v1/device/state/history?limit=1").close()
                raise AssertionError("device state history must stay removed")
            except urllib.error.HTTPError as error:
                assert error.code == 404
                error.close()
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
            # Persisted throttling must gate the first round after a real restart.
            database = sqlite3.connect(str(state / "agent.db"))
            try:
                previous_attempt = database.execute("SELECT last_attempt_at_us FROM maintenance_state").fetchone()[0]
                deadline_us = int((time.time() + 120) * 1_000_000)
                database.execute("UPDATE maintenance_state SET retry_not_before_us=?", (deadline_us,))
                database.commit()
            finally:
                database.close()
            fourth, _ = start()
            time.sleep(0.05)
            with request(token) as response:
                limited = json.load(response)
            assert limited["collection_state"] == "rate_limited"
            database = sqlite3.connect(str(state / "agent.db"))
            try:
                current_attempt = database.execute("SELECT last_attempt_at_us FROM maintenance_state").fetchone()[0]
            finally:
                database.close()
            assert previous_attempt == current_attempt
            stop(fourth)
            print("Lifecycle/device smoke passed: metrics, history, CORS/preflight, auth, earnings failure isolation, cleanup, single instance, SIGTERM, SIGKILL restart, persisted throttling, private paths")
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=5)
                if process.stderr:
                    process.stderr.close()


if __name__ == "__main__":
    main()
