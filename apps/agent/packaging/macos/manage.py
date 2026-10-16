#!/usr/bin/env python3
"""Install, upgrade, start, stop and restore the current user's Bloomer LaunchAgent."""
import argparse
import datetime as dt
import hashlib
import fcntl
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

LABEL = "dev.bloomer.agent"


class InstallError(Exception):
    pass


def run(args, check=True, timeout=30):
    try:
        result = subprocess.run([str(x) for x in args], capture_output=True, timeout=timeout)
    except (OSError, subprocess.TimeoutExpired):
        raise InstallError("Command unavailable or timed out: " + str(args[0]))
    if check and result.returncode:
        raise InstallError("Command failed: " + " ".join(str(x) for x in args[:2]))
    return result


def private_dir(path):
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink() or not path.is_dir():
        raise InstallError("Runtime directory must not be a symlink")
    path.chmod(0o700)


def atomic_write(path, data, mode=0o600):
    fd, name = tempfile.mkstemp(prefix=".bloomer-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "wb") as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(name, mode)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def verify_package(directory):
    manifest = json.loads((directory / "manifest.json").read_text())
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?", manifest["version"]):
        raise InstallError("Invalid release version")
    for name, digest in manifest["sha256"].items():
        path = directory / name
        if Path(name).is_absolute() or ".." in Path(name).parts or path.is_symlink():
            raise InstallError("Unsafe package path")
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise InstallError("Package checksum mismatch: " + name)
    required = {"bloomer-agent", "config.example.json", "manage.py", "dev.bloomer.agent.plist.template", "openapi.yaml", "INSTALL.md"}
    if not required.issubset(manifest["sha256"]):
        raise InstallError("Incomplete release package")
    return manifest


class Manager:
    def __init__(self, home=None):
        self.home = Path(home) if home else Path.home()
        self.base = self.home / "Library/Application Support/Bloomer/agent"
        self.config = self.base / "config.json"
        self.current = self.base / "bin/current"
        self.plist = self.home / "Library/LaunchAgents" / (LABEL + ".plist")
        self.domain = "gui/" + str(os.getuid())
        self.job = self.domain + "/" + LABEL
        self.tailscale = next((Path(p) for p in ["/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"] if Path(p).is_file()), None)

    def expand(self, text):
        if text == "~":
            return self.home
        if text.startswith("~/"):
            return self.home / text[2:]
        path = Path(text)
        return path if path.is_absolute() else self.config.parent / path

    def settings(self):
        settings = json.loads(self.config.read_text())
        settings["data_dir"] = self.expand(settings.get("data_dir", str(self.base)))
        settings["log_dir"] = self.expand(settings.get("log_dir", str(self.home / "Library/Logs/Bloomer/agent")))
        settings["access_token_file"] = self.expand(settings.get("access_token_file", str(settings["data_dir"] / "access.token")))
        settings.setdefault("listen", "127.0.0.1:17890")
        return settings

    def loaded(self):
        return run(["/bin/launchctl", "print", self.job], check=False).returncode == 0

    def stop(self, disable=True):
        if disable:
            run(["/bin/launchctl", "disable", self.job])
        if self.loaded():
            run(["/bin/launchctl", "bootout", self.job])
        deadline = time.monotonic() + 45
        while True:
            locked = False
            if self.config.exists():
                lock_path = self.settings()["data_dir"] / "agent.lock"
                if lock_path.exists():
                    fd = os.open(str(lock_path), os.O_RDONLY | os.O_NOFOLLOW)
                    try:
                        try:
                            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                            fcntl.flock(fd, fcntl.LOCK_UN)
                        except BlockingIOError:
                            locked = True
                    finally:
                        os.close(fd)
            if not self.loaded() and not locked:
                break
            if time.monotonic() >= deadline:
                raise InstallError("LaunchAgent did not stop and release its database lock")
            time.sleep(0.1)

    def start(self):
        run(["/bin/launchctl", "enable", self.job])
        if not self.loaded():
            run(["/bin/launchctl", "bootstrap", self.domain, self.plist])
        return self.wait_ready()

    def health(self):
        cfg = self.settings()
        token = cfg["access_token_file"].read_text().strip()
        request = urllib.request.Request("http://" + cfg["listen"] + "/v1/health", headers={"Authorization": "Bearer " + token})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(request, timeout=3) as response:
            return json.load(response)

    def wait_ready(self):
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            try:
                return self.health()
            except (OSError, ValueError):
                time.sleep(0.2)
        raise InstallError("LaunchAgent did not become healthy; inspect launchctl or run the binary in foreground")

    def tailscale_state(self):
        if self.tailscale is None:
            raise InstallError("Tailscale CLI unavailable")
        status = json.loads(run([self.tailscale, "status", "--json"]).stdout)
        if status.get("BackendState") != "Running":
            raise InstallError("Tailscale is not running")
        dns = status.get("Self", {}).get("DNSName", "").rstrip(".")
        if not dns.endswith(".ts.net"):
            raise InstallError("Tailscale HTTPS DNS name unavailable")
        return dns, json.loads(run([self.tailscale, "serve", "status", "--json"]).stdout)

    @staticmethod
    def check_serve(config, port, target):
        if str(port) not in config.get("TCP", {}):
            return False
        for host, value in config.get("Web", {}).items():
            if host.endswith(":" + str(port)) and value.get("Handlers", {}).get("/", {}).get("Proxy") == target:
                if config.get("AllowFunnel", {}).get(host):
                    raise InstallError("Requested port is publicly exposed by Funnel")
                return True
        raise InstallError("Tailscale HTTPS port already serves another application")

    def install(self, package, port=8443, plan=False):
        manifest = verify_package(package)
        version = manifest["version"] + "-" + manifest["sha256"]["bloomer-agent"][:12]
        release = self.base / "bin" / version
        if plan:
            return {"version": manifest["version"], "binary": str(release / "bloomer-agent"), "config": str(self.config), "plist": str(self.plist), "https_port": port}
        if sys.platform != "darwin":
            raise InstallError("LaunchAgent installation requires macOS")
        run(["/bin/launchctl", "print", self.domain])
        dns, serve_before = self.tailscale_state()
        private_dir(self.base)
        private_dir(self.base / "bin")
        private_dir(release)
        private_dir(self.home / "Library/LaunchAgents")
        if self.current.exists() and not self.current.is_symlink():
            raise InstallError("Managed current binary path is not a symlink")
        previous = os.readlink(self.current) if self.current.is_symlink() else None
        if previous and not (self.current.parent / previous).resolve().is_relative_to((self.base / "bin").resolve()):
            raise InstallError("Current binary link escapes managed versions")
        old_plist = self.plist.read_bytes() if self.plist.exists() else None
        if self.config.is_symlink():
            raise InstallError("Managed configuration must not be a symlink")
        if not self.config.exists():
            cfg = json.loads((package / "config.example.json").read_text())
            atomic_write(self.config, json.dumps(cfg, indent=2).encode() + b"\n")
        self.config.chmod(0o600)
        cfg = self.settings()
        for path in [cfg["data_dir"], cfg["log_dir"]]:
            private_dir(path)
        candidate = release / "bloomer-agent"
        atomic_write(candidate, (package / "bloomer-agent").read_bytes(), 0o755)
        run([candidate, "-config", self.config, "-check"])
        target = "http://" + cfg["listen"]
        owned_serve = self.check_serve(serve_before, port, target)
        token_before = cfg["access_token_file"].read_bytes() if cfg["access_token_file"].exists() else None
        was_running = self.loaded()
        backup = None
        published = False
        try:
            self.stop(disable=False)
            host, listen_port = cfg["listen"].rsplit(":", 1)
            host = host.strip("[]")
            with socket.socket(socket.AF_INET6 if ":" in host else socket.AF_INET) as probe:
                probe.bind((host, int(listen_port)))
            if (cfg["data_dir"] / "agent.db").exists():
                private_dir(self.base / "backups")
                backup = self.base / "backups" / ("before-upgrade-" + str(time.time_ns()) + ".db")
                old_binary = self.current / "bloomer-agent" if previous else candidate
                run([old_binary, "-config", self.config, "-backup", backup], timeout=120)
            link = self.current.with_name(".current-" + str(os.getpid()))
            if link.is_symlink():
                link.unlink()
            link.symlink_to(version)
            os.replace(link, self.current)
            plist = plistlib.loads((package / "dev.bloomer.agent.plist.template").read_bytes())
            plist["ProgramArguments"] = [str(self.current / "bloomer-agent"), "-config", str(self.config)]
            atomic_write(self.plist, plistlib.dumps(plist))
            self.start()
            if not owned_serve:
                run([self.tailscale, "serve", "--bg", "--https=" + str(port), target])
                published = True
            _, after = self.tailscale_state()
            if not self.check_serve(after, port, target):
                raise InstallError("Tailscale HTTPS route was not installed")
            if token_before is not None and token_before != cfg["access_token_file"].read_bytes():
                raise InstallError("Upgrade replaced the dashboard token")
            private_dir(self.base / "management")
            atomic_write(self.base / "management/manage.py", (package / "manage.py").read_bytes(), 0o700)
            endpoint = "https://" + dns + (":" + str(port) if port != 443 else "")
            state = {"version": manifest["version"], "sha256": manifest["sha256"]["bloomer-agent"], "installed_at": dt.datetime.now(dt.timezone.utc).isoformat(), "endpoint": endpoint, "serve_port": port}
            atomic_write(self.base / "install-state.json", json.dumps(state, indent=2).encode() + b"\n")
            return dict(state, token_preserved=token_before is not None)
        except Exception:
            self.stop(disable=False)
            if published:
                run([self.tailscale, "serve", "--https=" + str(port), "off"], check=False)
            if backup:
                run([candidate, "-config", self.config, "-restore", backup], timeout=120)
            if previous:
                temporary = self.current.with_name(".rollback-" + str(os.getpid()))
                temporary.symlink_to(previous)
                os.replace(temporary, self.current)
            elif self.current.is_symlink():
                self.current.unlink()
            if old_plist:
                atomic_write(self.plist, old_plist)
            elif self.plist.exists():
                self.plist.unlink()
            if was_running and previous:
                self.start()
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["install", "upgrade", "start", "stop", "status", "restore"])
    parser.add_argument("--package-dir", type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument("--https-port", type=int, default=8443)
    parser.add_argument("--plan", action="store_true")
    parser.add_argument("--backup", type=Path)
    args = parser.parse_args()
    manager = Manager()
    try:
        if not 1 <= args.https_port <= 65535:
            raise InstallError("Invalid HTTPS port")
        if args.command in {"install", "upgrade"}:
            result = manager.install(args.package_dir.resolve(), args.https_port, args.plan)
        elif args.command == "stop":
            manager.stop()
            result = {"state": "stopped"}
        elif args.command == "start":
            manager.start()
            result = {"state": "running"}
        elif args.command == "restore":
            if args.backup is None:
                raise InstallError("--backup is required")
            running = manager.loaded()
            manager.stop(disable=False)
            run([manager.current / "bloomer-agent", "-config", manager.config, "-restore", args.backup.resolve()], timeout=120)
            if running:
                manager.start()
            result = {"state": "restored"}
        else:
            if manager.loaded():
                health = manager.health()
                result = {"state": "running", "version": health["version"], "data_stale": health["data_stale"], "last_successful_sync_at": health["last_successful_sync_at"], "collection_state": health["collection_state"]}
            else:
                result = {"state": "stopped"}
        print(json.dumps(result, indent=2))
    except (InstallError, OSError, ValueError, KeyError) as error:
        print("Bloomer management failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
