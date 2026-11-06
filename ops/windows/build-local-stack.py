"""Build five local CPA components from explicit, immutable Git revisions.

No default branch, installed service, configuration, credential or database is used.
Plugins use an external modfile bound to the exact selected Core checkout.
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path


NAMES = ("CPA-Core-ex", "CPA-Manager-Plus-ex", "CPA-Usage-Keeper-ex",
         "CPA-OpenCode-ex", "CPA-Qwen")
CORE_MODULE = "github.com/router-for-me/CLIProxyAPI/v8"
TARGETS = {
    "CPA-Core-ex/cli-proxy-api.exe": ["CPA-Core/app/cli-proxy-api.exe"],
    "CPA-Manager-Plus-ex/cpa-manager-plus.exe": ["CPA-Manager-Plus/app/cpa-manager-plus.exe"],
    "CPA-Manager-Plus-ex/management.html": ["CPA-Core/static/management.html",
                                          "CPA-Manager-Plus/panel/management.html"],
    "CPA-Usage-Keeper-ex/cpa-usage-keeper.exe": ["CPA-Usage-Keeper/cpa-usage-keeper.exe"],
    "CPA-OpenCode-ex/cpa-credential-opencode.dll": ["CPA-Core/plugins/windows/amd64/cpa-credential-opencode.dll"],
    "CPA-Qwen/cpa-credential-qwen.dll": ["CPA-Core/plugins/windows/amd64/cpa-credential-qwen.dll"],
    "CPA-Qwen/bailian-quota.exe": ["CPA-Core/tools/bailian-quota.exe"],
}
CANDIDATE_ONLY = ("CPA-Qwen/aliyun-bss-quota.dll",)


def verify_qwen_snapshot(checkouts, sources, output):
    canonical = checkouts["CPA-Qwen"]
    snapshot = checkouts["CPA-Manager-Plus-ex"] / "providers/qwen"
    with (output / "qwen-snapshot-verification.log").open("w", encoding="utf-8") as log:
        subprocess.run([sys.executable, "-B", str(canonical / "scripts/verify-manager-snapshot.py"),
                        "--canonical-source", str(canonical), "--snapshot", str(snapshot)],
                       stdout=log, stderr=subprocess.STDOUT, check=True)
    path = snapshot / "CPA_QWEN_SOURCE.json"
    receipt = json.loads(path.read_text(encoding="utf-8"))
    if receipt["canonical"]["repository"] != "williamxhero/CPA-Qwen" or receipt["canonical"]["commit"] != sources["sources"]["CPA-Qwen"]["head"]:
        raise ValueError("qwen_snapshot_source_mismatch")
    return dict(canonical=receipt["canonical"], receipt_sha256=sha256(path))


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], text=True,
                                   stderr=subprocess.PIPE).strip()


def source_gate(path):
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("schema") != 1 or set(data.get("sources", {})) != set(NAMES):
        raise ValueError("source_manifest_schema")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,80}", data.get("version", "")):
        raise ValueError("release_version_required")
    for name, item in data["sources"].items():
        if not re.fullmatch(r"[0-9a-f]{40}", item.get("head", "")):
            raise ValueError("full_source_revision_required:" + name)
        if not item.get("ref") or item["ref"].startswith("-"):
            raise ValueError("source_ref_required:" + name)
        if git(item["repository"], "rev-parse", "--verify", item["ref"] + "^{commit}") != item["head"]:
            raise ValueError("source_ref_mismatch:" + name)
    data["sdk_revision"] = data["sources"]["CPA-Core-ex"]["head"]
    return data


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, data):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def build(args, sources):
    output = args.output.resolve()
    for item in sources["sources"].values():
        repo = Path(item["repository"]).resolve()
        if output == repo or repo in output.parents or output in repo.parents:
            raise ValueError("output_source_overlap")
    if output.exists():
        raise ValueError("output_must_be_new")
    if not args.node or not args.npm_cli:
        raise ValueError("explicit_node_and_npm_cli_required")
    output.mkdir(parents=True)
    receipt = dict(sources, accepted=False, completed=[], errors=[])
    write_json(output / "build-result.json", receipt)
    env = dict(os.environ, GOOS="windows", GOARCH="amd64", GOWORK="off",
               CGO_ENABLED="1", VERSION=sources["version"],
               CC=args.cc or shutil.which("gcc") or "gcc")
    env["PATH"] = str(Path(args.node).resolve().parent) + os.pathsep + env["PATH"]
    checkouts = {}
    # All snapshots exist before any plugin can resolve its SDK dependency.
    for name, item in sources["sources"].items():
        checkout = output / "sources" / name
        checkout.parent.mkdir(exist_ok=True)
        subprocess.run(["git", "clone", "--quiet", "--no-hardlinks", "--no-checkout",
                        str(Path(item["repository"]).resolve()), str(checkout)], check=True)
        git(checkout, "-c", "core.autocrlf=false", "checkout", "--quiet", "--detach", item["head"])
        if git(checkout, "rev-parse", "HEAD") != item["head"] or git(checkout, "status", "--porcelain"):
            raise ValueError("snapshot_not_clean:" + name)
        checkouts[name] = checkout
    # Fail closed before Go, npm or any other component can compile. Never enroll
    # or synthesize provenance for the Manager copy during a build.
    receipt["qwen_snapshot"] = verify_qwen_snapshot(checkouts, sources, output)
    write_json(output / "build-result.json", receipt)
    stamp = datetime.datetime.now(datetime.timezone.utc).isoformat()
    files = []
    for name in NAMES:
        source = checkouts[name]
        dest = output / "artifacts" / name
        dest.mkdir(parents=True)
        local_env = dict(env)
        with (output / (name + "-build.log")).open("w", encoding="utf-8") as log:
            def run(command, cwd=source):
                log.write("COMMAND: " + subprocess.list2cmdline(list(map(str, command))) + "\n")
                log.flush()
                subprocess.run(list(map(str, command)), cwd=cwd, env=local_env,
                               stdout=log, stderr=subprocess.STDOUT, check=True)

            def npm(*command, cwd=source):
                run([args.node, args.npm_cli, *command], cwd)

            revision = sources["sources"][name]["head"]
            version = sources["version"]
            mod_flags = []
            if name in ("CPA-OpenCode-ex", "CPA-Qwen"):
                modfile = output / (name + ".mod")
                shutil.copy2(source / "go.mod", modfile)
                shutil.copy2(source / "go.sum", modfile.with_suffix(".sum"))
                run(["go", "mod", "edit", "-modfile=" + str(modfile),
                     "-replace=" + CORE_MODULE + "=" + str(checkouts["CPA-Core-ex"])])
                # Core's Go directive may require updating this external modfile.
                # Never update the reviewed plugin checkout or rely on GOFLAGS.
                mod_flags = ["-mod=mod", "-modfile=" + str(modfile)]
                plugin = "cpa-credential-opencode" if name == "CPA-OpenCode-ex" else "cpa-credential-qwen"
                run(["go", "build", *mod_flags, "-buildvcs=true", "-trimpath", "-buildmode=c-shared",
                     "-ldflags=-s -w", "-o", dest / (plugin + ".dll"), "."])
                run(["go", "test", *mod_flags, "./..."])
                if name == "CPA-Qwen":
                    run(["go", "build", *mod_flags, "-buildvcs=true", "-trimpath", "-buildmode=c-shared",
                         "-ldflags=-s -w", "-o", dest / "aliyun-bss-quota.dll", "./cmd/aliyun-bss-quota-plugin"])
                    run(["go", "build", *mod_flags, "-buildvcs=true", "-trimpath", "-ldflags=-s -w",
                         "-o", dest / "bailian-quota.exe", "./cmd/bailian-quota"])
            elif name == "CPA-Core-ex":
                flags = f"-s -w -X main.Version={version} -X main.Commit={revision} -X main.BuildDate={stamp}"
                run(["go", "build", "-buildvcs=true", "-trimpath", "-ldflags=" + flags,
                     "-o", dest / "cli-proxy-api.exe", "./cmd/server"])
            elif name == "CPA-Usage-Keeper-ex":
                npm("ci", "--no-audit", "--no-fund", cwd=source / "web")
                npm("run", "typecheck", cwd=source / "web")
                # The fresh checkout contains tracked dist/.gitkeep. Preserve
                # it so Vite does not dirty VCS metadata before Go embeds dist.
                npm("run", "build", "--", "--emptyOutDir=false", cwd=source / "web")
                run(["go", "build", "-buildvcs=true", "-trimpath",
                     "-ldflags=-s -w -X cpa-usage-keeper/internal/version.Version=" + version,
                     "-o", dest / "cpa-usage-keeper.exe", "./cmd/server"])
            else:
                npm("ci", "--no-audit", "--no-fund")
                npm("run", "build")
                embedded = source / "apps/manager-server/internal/httpapi/web"
                web = source / "apps/web/dist"
                for generated, retained in (("index.html", "management.html"),
                                            ("favicon.ico", "favicon.ico"),
                                            ("apple-touch-icon.png", "apple-touch-icon.png")):
                    # Tracked embedded assets must already be a reviewed packaging commit.
                    if sha256(web / generated) != sha256(embedded / retained):
                        raise ValueError("embedded_asset_out_of_sync:" + retained)
                shutil.copy2(web / "index.html", dest / "management.html")
                local_env["CGO_ENABLED"] = "0"
                prefix = "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo."
                flags = f"-s -w -X {prefix}Version={version} -X {prefix}SourceCommit={revision}"
                run(["go", "build", "-buildvcs=true", "-trimpath", "-ldflags=" + flags,
                     "-o", dest / "cpa-manager-plus.exe", "./cmd/cpa-manager-plus"],
                    source / "apps/manager-server")
            if git(source, "status", "--porcelain"):
                raise ValueError("build_changed_tracked_source:" + name)
            for binary in sorted(dest.iterdir()):
                if binary.suffix in (".exe", ".dll"):
                    metadata = subprocess.check_output(["go", "version", "-m", str(binary)], text=True)
                    (output / (name + "-" + binary.name + "-metadata.txt")).write_text(metadata, encoding="utf-8")
                    if "vcs.revision=" + revision not in metadata or "vcs.modified=false" not in metadata:
                        raise ValueError("binary_vcs_identity_mismatch:" + name)
                    if binary.suffix == ".dll":
                        normalized = metadata.replace("\\", "/")
                        if str(checkouts["CPA-Core-ex"]).replace("\\", "/") not in normalized:
                            raise ValueError("binary_sdk_identity_mismatch:" + name)
            receipt["completed"].append({"name": name, "source_head": revision,
                                          "sdk_revision": sources["sdk_revision"] if mod_flags else None,
                                          "tree": git(source, "rev-parse", "HEAD^{tree}")})
            write_json(output / "build-result.json", receipt)
    for relative, targets in TARGETS.items():
        candidate = output / "artifacts" / relative
        for target in targets:
            name = relative.split("/")[0]
            files.append(dict(relative=relative, target=target, sha256=sha256(candidate),
                              source_head=sources["sources"][name]["head"],
                              sdk_revision=sources["sdk_revision"] if candidate.suffix == ".dll" else None))
    candidate_only = []
    for relative in CANDIDATE_ONLY:
        candidate_only.append(dict(relative=relative, policy="candidate-only",
                                   sha256=sha256(output / "artifacts" / relative),
                                   source_head=sources["sources"]["CPA-Qwen"]["head"],
                                   sdk_revision=sources["sdk_revision"]))
    write_json(output / "candidate-manifest.json", dict(schema=1, files=files, candidate_only=candidate_only,
               build_result=str(output / "build-result.json"), sdk_revision=sources["sdk_revision"]))
    # Acceptance is the final published marker, never a promise that a later
    # manifest write will succeed. Any publication failure retains accepted=false.
    receipt["accepted"] = True
    write_json(output / "build-result.json", receipt)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--preflight-only", action="store_true")
    parser.add_argument("--node")
    parser.add_argument("--npm-cli")
    parser.add_argument("--cc")
    args = parser.parse_args()
    try:
        sources = source_gate(args.sources)
        if args.preflight_only:
            print(json.dumps(sources))
        else:
            build(args, sources)
    except (ValueError, OSError, subprocess.SubprocessError, KeyError) as error:
        # Source-only build diagnostics never read provider or installed runtime secrets.
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
