"""Run Qwen Go checks against an explicitly selected compatible CPA Core SDK."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--core-source", required=True, type=Path)
    parser.add_argument("--mode", choices=("test", "vet"), default="test")
    args, flags = parser.parse_known_args()
    core = args.core_source.resolve()
    if not (core / "sdk/pluginapi").is_dir():
        parser.error("core-source must contain the compatible CPA Core plugin SDK")
    env = {**os.environ, "GOWORK": "off", "GOTOOLCHAIN": "local"}
    with tempfile.TemporaryDirectory(prefix="cpa-qwen-check-") as directory:
        mod = Path(directory) / "check.mod"
        shutil.copyfile(ROOT / "go.mod", mod)
        shutil.copyfile(ROOT / "go.sum", mod.with_suffix(".sum"))
        subprocess.run(["go", "mod", "edit", f"-modfile={mod}",
                        f"-replace=github.com/router-for-me/CLIProxyAPI/v8={core}"],
                       cwd=ROOT, env=env, check=True)
        if flags[:1] == ["--"]:
            flags = flags[1:]
        return subprocess.run(["go", args.mode, f"-modfile={mod}", "-mod=mod",
                               *flags, "./..."], cwd=ROOT, env=env).returncode


if __name__ == "__main__":
    raise SystemExit(main())
