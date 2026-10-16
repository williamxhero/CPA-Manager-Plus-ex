#!/usr/bin/env python3
"""Private deployment checks; no configuration values are printed."""
import argparse
import json
import os
from pathlib import Path
import re
import socket
import sys
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, ProxyHandler


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        # The checked listener must itself answer; never borrow another process
        # or leave loopback while following a deployment health URL.
        return None


def health(pid, binary, url, marker):
    """Require the expected MainPID to own both the binary and listening socket."""
    if pid <= 1 or Path(f"/proc/{pid}/exe").resolve() != Path(binary).resolve():
        return False
    parsed = urlsplit(url)
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost", "::1"):
        return False
    if parsed.username or parsed.password:
        return False
    port = parsed.port or 80
    inodes = set()
    for fd in Path(f"/proc/{pid}/fd").iterdir():
        try:
            link = os.readlink(fd)
            if link.startswith("socket:["):
                inodes.add(link[8:-1])
        except OSError:
            continue
    # A reachable endpoint is not enough: the expected process must own its
    # listener. In particular, another service returning 401 must never pass.
    addresses = set(socket.getaddrinfo(parsed.hostname, port, type=socket.SOCK_STREAM))
    hosts = {entry[4][0] for entry in addresses}
    owns_listener = False
    for table in ("tcp", "tcp6"):
        for line in Path(f"/proc/net/{table}").read_text().splitlines()[1:]:
            fields = line.split()
            address, p = fields[1].split(":")
            if int(p, 16) != port or fields[3] != "0A" or fields[9] not in inodes:
                continue
            if table == "tcp":
                host = socket.inet_ntop(socket.AF_INET, bytes.fromhex(address)[::-1])
                owns_listener |= host == "0.0.0.0" or host in hosts
            else:
                raw = bytes.fromhex(address)
                host = socket.inet_ntop(socket.AF_INET6, b"".join(raw[i:i+4][::-1] for i in range(0, 16, 4)))
                owns_listener |= host == "::" or host in hosts
    if not owns_listener:
        return False
    opener = build_opener(ProxyHandler({}), NoRedirect())
    try:
        with opener.open(url, timeout=2) as response:
            code, body = response.status, response.read(65536)
    except HTTPError as response:
        code, body = response.code, response.read(65536)
    if Path(f"/proc/{pid}/exe").resolve() != Path(binary).resolve():
        return False
    if marker:
        return code == 200 and marker.encode() in body
    return code in (200, 401)


def sandbox_config(source, destination, sandbox, root, port):
    """Adapt supported legacy/new core YAML paths without parsing secrets.

    The binary remains the syntax validator. Ambiguous server/port/plugin
    layouts are rejected rather than starting with a possibly live address.
    """
    text = Path(source).read_text()
    text = text.replace(str(root), str(sandbox))
    text = text.replace("/data/cpa/oauth", str(sandbox / "auth"))
    text = text.replace("/data/cpa/qwen-quota", str(sandbox / "quota"))
    lines = text.splitlines(keepends=True)
    section = None
    ports = 0
    plugin_dirs = 0
    has_plugins = False
    for index, line in enumerate(lines):
        top = re.match(r"^([a-zA-Z][\w-]*):", line)
        if top:
            section = top[1]
            has_plugins |= section == "plugins"
        match = re.match(r"^(\s*)(host|port|auth-dir|dir):\s*.*$", line)
        if match:
            indent, key = match[1], match[2]
            if key in ("host", "port") and (not indent or section == "server"):
                value = '"127.0.0.1"' if key == "host" else str(port)
                lines[index] = f"{indent}{key}: {value}\n"
                ports += key == "port"
            elif key == "auth-dir":
                lines[index] = f'{indent}auth-dir: {json.dumps(str(sandbox / "auth"))}\n'
            elif key == "dir" and section == "plugins":
                lines[index] = f'{indent}dir: {json.dumps(str(sandbox / "plugins"))}\n'
                plugin_dirs += 1
        # Quota commands are not needed to prove config startup and must not
        # run a real host credential/console helper during rehearsal.
        lines[index] = re.sub(r'("command"\s*:\s*)"[^"\n]*"', r'\1"/bin/false"', lines[index])
        lines[index] = re.sub(r'^(\s*command:\s*).+$', r'\1"/bin/false"\n', lines[index])
    if ports != 1 or (has_plugins and plugin_dirs != 1):
        raise ValueError("ambiguous core listen port or plugin directory")
    (sandbox / "auth").mkdir()
    (sandbox / "quota").mkdir()
    Path(destination).write_text("".join(lines))


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("health")
    check.add_argument("--pid", type=int, required=True)
    check.add_argument("--binary", required=True)
    check.add_argument("--url", required=True)
    check.add_argument("--marker", default="")
    config = sub.add_parser("sandbox-config")
    config.add_argument("source")
    config.add_argument("destination")
    config.add_argument("sandbox", type=Path)
    config.add_argument("root", type=Path)
    config.add_argument("port", type=int)
    args = parser.parse_args()
    try:
        if args.command == "health":
            return 0 if health(args.pid, args.binary, args.url, args.marker) else 1
        sandbox_config(args.source, args.destination, args.sandbox, args.root, args.port)
        return 0
    except (OSError, ValueError, TimeoutError):
        # Never echo the exception: a config parser or URL may contain secrets.
        return 1


if __name__ == "__main__":
    sys.exit(main())
