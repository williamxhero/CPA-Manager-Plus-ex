#!/usr/bin/env python3
"""Fake systemd boundary; actually starts/stops the synthetic HTTP executable."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

state = Path(os.environ["SERVICE_STATE"])
root = Path(os.environ["DEPLOY_ROOT"])
kind = os.environ["DEPLOY_KIND"]
args = sys.argv[1:]
pidfile = state / "pid"
pid = int(pidfile.read_text()) if pidfile.exists() else 0
if "show" in args:
    print(pid)
    sys.exit(0)
if "is-active" in args:
    try:
        os.kill(pid, 0) if pid > 1 else sys.exit(1)
        sys.exit(0)
    except OSError:
        sys.exit(1)
if "restart" not in args:
    sys.exit(1)
count = int((state / "restarts").read_text()) if (state / "restarts").exists() else 0
(state / "restarts").write_text(str(count + 1))
current = (root / "app/current").resolve()
new = current.name != "old"
failure = os.environ.get("FAILURE", "")
if failure == "restart-all" or (failure == "restart-new" and new) or (failure == "restart-old" and not new):
    sys.exit(1)
if pid > 1:
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    # Like systemd restart, wait for the old process to release its listener.
    for _ in range(100):
        try:
            if Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0] == "Z":
                break
        except FileNotFoundError:
            break
        time.sleep(.01)
    else:
        sys.exit(1)
if failure in ("foreign", "foreign401") and new:
    current = root / "app/old"
name = "cli-proxy-api" if kind == "core" else "cpa-manager-plus"
env = dict(os.environ, HTTP_ADDR="127.0.0.1:" + os.environ["SERVICE_PORT"])
if failure == "foreign401" and new:
    env["AUTH_REQUIRED"] = "1"
if failure == "redirect-new" and new:
    env["REDIRECT_TO"] = "http://127.0.0.1:" + os.environ["REDIRECT_PORT"] + "/health"
if failure == "health-all" or (failure in ("health-new", "restart-old") and new):
    env["BAD_HEALTH"] = "1"
command = [str(current / name)]
if kind == "core":
    command += ["--config", str(root / "config.yaml")]
process = subprocess.Popen(command, cwd=root, env=env, stdout=subprocess.DEVNULL,
                           stderr=subprocess.DEVNULL, start_new_session=True)
pidfile.write_text(str(process.pid))
