"""Build both native plugins and the one quota CLI from this Qwen module.

Uses an explicit local CPA Core SDK, without editing either checkout's module
files. This builds versioned candidates only; it never installs or restarts.
"""
import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_inputs(root):
    paths = list(root.glob("*.go")) + [root / "go.mod", root / "go.sum"]
    for name in ("internal", "sdk", "cmd", "resources"):
        if (root / name).is_dir():
            paths.extend(p for p in (root / name).rglob("*") if p.is_file() and not {"__pycache__", ".scratch", ".git"}.intersection(p.parts))
    if (root / "scripts/build-artifacts.py").is_file():
        paths.append(root / "scripts/build-artifacts.py")
    paths = sorted(set(paths))
    return {str(p.relative_to(root)).replace("\\", "/"): digest(p) for p in paths if p.is_file()}


def git_state(root):
    def read(*args):
        return subprocess.check_output(["git", "-C", str(root), *args], stderr=subprocess.DEVNULL).decode().strip()
    try:
        return {"head": read("rev-parse", "HEAD"), "status": read("status", "--porcelain=v1", "--untracked-files=normal")}
    except (subprocess.CalledProcessError, FileNotFoundError):
        return {"head": None, "status": "git metadata unavailable; source files fingerprinted"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--core-source", required=True, type=Path, help="Explicit checkout of the compatible CPA-Core-ex SDK")
    parser.add_argument("--output", type=Path, help="New candidate directory; existing paths are never overwritten")
    parser.add_argument("--go", default=shutil.which("go"), help="Go >=1.26.10 executable")
    parser.add_argument("--cc", default=os.environ.get("CC"), help="CGO C compiler (or use CC/PATH)")
    parser.add_argument("--allow-module-downloads", action="store_true", help="Allow Go module downloads; default is offline")
    args = parser.parse_args()
    core = args.core_source.resolve()
    if not args.go or not (core / "go.mod").is_file() or not (core / "sdk/pluginapi").is_dir():
        parser.error("a Go executable and compatible explicit Core source are required")
    module = re.search(r"(?m)^module\s+(\S+)", (core / "go.mod").read_text(encoding="utf-8"))
    if not module or module[1] != "github.com/router-for-me/CLIProxyAPI/v8":
        parser.error("core-source is not the required Core SDK module")
    # Apply the same bounded/offline toolchain policy before *every* Go call.
    # GONOPROXY/GOPRIVATE can bypass GOPROXY=off via direct VCS acquisition.
    env = os.environ.copy()
    env.pop("GOROOT", None)
    env.update(GOTOOLCHAIN="local", GOWORK="off", GOENV="off", GOFLAGS="", GOTELEMETRY="off")
    if not args.allow_module_downloads:
        env.update(GOPROXY="off", GOSUMDB="off", GONOPROXY="none", GOPRIVATE="", GONOSUMDB="none", GOVCS="*:off")
    try:
        version = subprocess.check_output([args.go, "version"], text=True, stderr=subprocess.DEVNULL, env=env)
    except (OSError, subprocess.CalledProcessError):
        parser.error("cannot run the selected Go executable")
    parsed = re.search(r"\bgo(\d+)\.(\d+)\.(\d+)", version)
    if not parsed or tuple(map(int, parsed.groups())) < (1, 26, 10):
        parser.error("Go >=1.26.10 is required; use --go to select it explicitly")
    target_os = os.environ.get("GOOS") or {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    target_arch = os.environ.get("GOARCH") or {"AMD64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine())
    if target_os not in ("windows", "linux", "darwin") or not target_arch:
        parser.error("unsupported build target")
    suffix = {"windows": ".dll", "linux": ".so", "darwin": ".dylib"}[target_os]
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    output = (args.output or ROOT / "dist-local" / ("unified-" + stamp)).resolve()
    if output.exists():
        parser.error("output already exists; refusing to overwrite a candidate")
    before = source_inputs(ROOT)
    sdk_before = source_inputs(core)
    source_git, core_git = git_state(ROOT), git_state(core)
    output.mkdir(parents=True, exist_ok=False)
    inputs, logs = output / "build-inputs", output / "logs"
    temp = inputs / "temp"
    mod = inputs / "source.mod"
    env.update(CGO_ENABLED="1", GOOS=target_os, GOARCH=target_arch, GOCACHE=str(inputs / "go-cache"), TEMP=str(temp), TMP=str(temp))
    if args.cc:
        env["CC"] = args.cc
    receipts = []

    def run(label, command):
        with (logs / (label + ".stdout.log")).open("wb") as stdout, (logs / (label + ".stderr.log")).open("wb") as stderr:
            result = subprocess.run(command, cwd=ROOT, env=env, stdout=stdout, stderr=stderr)
        receipts.append({"label": label, "command": command, "exitCode": result.returncode})
        if result.returncode:
            raise RuntimeError("build step failed: " + label + "; see candidate logs")

    try:
        inputs.mkdir()
        logs.mkdir()
        temp.mkdir()
        shutil.copyfile(ROOT / "go.mod", mod)
        shutil.copyfile(ROOT / "go.sum", inputs / "source.sum")
        run("module-sdk", [args.go, "mod", "edit", "-modfile=" + str(mod), "-replace=github.com/router-for-me/CLIProxyAPI/v8=" + str(core)])
        run("go-environment", [args.go, "env", "-json", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GOEXPERIMENT", "GOVERSION"])
        go_environment = json.loads((logs / "go-environment.stdout.log").read_text(encoding="utf-8"))
        if not isinstance(go_environment, dict):
            raise ValueError("Go environment did not return a JSON object")
        products = [
            ("qwen", "qwen-cliproxyapi" + suffix, ".", True),
            ("aliyun-quota-compat", "aliyun-bss-quota" + suffix, "./cmd/aliyun-bss-quota-plugin", True),
            ("bailian-quota", "bailian-quota" + (".exe" if target_os == "windows" else ""), "./cmd/bailian-quota", False),
        ]
        entries = []
        for label, filename, package, native in products:
            command = [args.go, "build", "-modfile=" + str(mod), "-mod=mod", "-trimpath"]
            if native:
                command.append("-buildmode=c-shared")
            command.extend(["-o", str(output / filename), package])
            run(label, command)
            artifact = output / filename
            entries.append({"component": label, "path": filename, "sha256": digest(artifact), "bytes": artifact.stat().st_size})
            header = artifact.with_suffix(".h")
            if native and header.is_file():
                entries.append({"component": label + "-header", "path": header.name, "sha256": digest(header), "bytes": header.stat().st_size})
        if before != source_inputs(ROOT) or sdk_before != source_inputs(core):
            raise RuntimeError("build source changed during execution; candidate is not verified")
        manifest = {"schemaVersion": 1, "status": "built_not_deployed", "createdAt": datetime.now(timezone.utc).isoformat(), "source": {"path": str(ROOT), **source_git, "files": before}, "coreSDK": {"path": str(core), **core_git, "files": sdk_before}, "target": {"os": target_os, "arch": target_arch}, "goVersion": version.strip(), "goEnvironment": go_environment, "moduleInputs": {p.name: digest(p) for p in (mod, mod.with_suffix(".sum"))}, "artifacts": entries, "commands": receipts}
        pending_manifest = output / "manifest.json.tmp"
        pending_manifest.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        pending_manifest.replace(output / "manifest.json")
        print(json.dumps({"manifest": str(output / "manifest.json"), "status": manifest["status"], "artifacts": entries}, indent=2))
        return 0
    except (OSError, RuntimeError, ValueError) as error:
        # Preserve candidate logs and partial artifacts for diagnosis, but never
        # emit a success manifest or touch a previous candidate/installed DLL.
        (output / "FAILED.json").write_text(json.dumps({"status": "failed_not_deployed", "reason": str(error), "commands": receipts}, indent=2) + "\n", encoding="utf-8")
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
