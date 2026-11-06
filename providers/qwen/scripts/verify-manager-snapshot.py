"""Verify Manager's Qwen copy against a clean canonical CPA-Qwen checkout.

Never synchronizes, deletes or overwrites source files. An initial receipt may
be written only after the entire snapshot already matches canonical bytes.
"""
import argparse
import hashlib
import json
import os
import stat
import subprocess
import sys
from pathlib import Path

REPOSITORY = "williamxhero/CPA-Qwen"
RECEIPT = "CPA_QWEN_SOURCE.json"


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], stderr=subprocess.DEVNULL)


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def is_link(path):
    if path.is_symlink() or getattr(path, "is_junction", lambda: False)():
        return True
    try:
        attributes = getattr(path.lstat(), "st_file_attributes", 0)
    except FileNotFoundError:
        return False
    return bool(attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0))


def canonical_inputs(root):
    if Path(git(root, "rev-parse", "--show-toplevel").decode().strip()).resolve() != root:
        raise ValueError("canonical source must be its own CPA-Qwen checkout, not a nested consumer copy")
    if git(root, "status", "--porcelain=v1", "--untracked-files=normal").strip():
        raise ValueError("canonical checkout has uncommitted changes; commit and review its source before assigning provenance")
    for entry in git(root, "ls-files", "-v", "-z").decode("utf-8").split("\0"):
        if entry and (entry[0].islower() or entry[0] == "S"):
            raise ValueError("canonical index hides changes with assume-unchanged or skip-worktree")
    names = [name for name in git(root, "ls-files", "-z").decode("utf-8").split("\0") if name]
    head_files = {}
    for entry in git(root, "ls-tree", "-r", "-z", "HEAD").decode("utf-8").split("\0"):
        if not entry:
            continue
        metadata, name = entry.split("\t", 1)
        mode, kind, object_id = metadata.split()
        if mode not in ("100644", "100755") or kind != "blob":
            raise ValueError("canonical commit contains links or non-file inputs")
        head_files[name] = object_id
    if set(names) != set(head_files):
        raise ValueError("canonical index file set does not match its HEAD commit")
    files = {}
    for name in names:
        path = root / name
        if is_link(path) or not path.is_file() or not path.resolve().is_relative_to(root):
            raise ValueError("canonical tracked input is not a regular file: " + name)
        for parent in path.parents:
            if parent == root:
                break
            if is_link(parent):
                raise ValueError("canonical tracked input has a linked parent: " + name)
        # Use Git's text/EOL normalization, not a raw Windows-byte comparison.
        # Reject arbitrary clean filters/encoding transforms rather than letting
        # them conceal a different payload behind a valid committed blob ID.
        attrs = git(root, "check-attr", "--cached", "-z", "filter", "working-tree-encoding", "--", name).decode("utf-8").split("\0")
        if any(attrs[i] not in ("unspecified", "unset") for i in range(2, len(attrs)-1, 3)):
            raise ValueError("canonical inputs use unsupported Git content transforms: " + name)
        body = path.read_bytes()
        object_id = subprocess.check_output(["git", "-C", str(root), "hash-object", "--path=" + name, "--stdin"], input=body, stderr=subprocess.DEVNULL).decode().strip()
        if object_id != head_files[name]:
            raise ValueError("canonical working bytes do not belong to the declared HEAD commit: " + name)
        files[name] = hashlib.sha256(body).hexdigest()
    if (root / "go.mod").read_text(encoding="utf-8").splitlines()[0].strip() != "module cpa-credential-qwen":
        raise ValueError("canonical checkout is not the Qwen module")
    return {"repository": REPOSITORY, "commit": git(root, "rev-parse", "HEAD").decode().strip(), "tree": git(root, "rev-parse", "HEAD^{tree}").decode().strip(), "files": files}


def snapshot_names(root):
    names = set()
    for directory, dirs, files in os.walk(root, followlinks=False):
        base = Path(directory)
        for name in dirs:
            if name == ".git" or is_link(base / name) or not (base / name).resolve().is_relative_to(root):
                raise ValueError("snapshot contains nested git metadata or a linked directory")
        for name in files:
            path = base / name
            if is_link(path) or name == ".git" or not path.resolve().is_relative_to(root):
                raise ValueError("snapshot contains linked files or git metadata")
            relative = path.relative_to(root).as_posix()
            if relative != RECEIPT:
                names.add(relative)
    return names


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--canonical-source", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--snapshot", type=Path, required=True, help="Explicit Manager providers/qwen snapshot directory")
    parser.add_argument("--write-receipt", action="store_true", help="Create a new receipt only when all source files already match; never overwrite a receipt")
    args = parser.parse_args()
    if is_link(args.canonical_source) or is_link(args.snapshot):
        parser.error("canonical checkout and snapshot roots must not be links or junctions")
    canonical, snapshot = args.canonical_source.resolve(), args.snapshot.resolve()
    if not snapshot.is_dir() or snapshot == canonical or canonical in snapshot.parents or snapshot in canonical.parents:
        parser.error("canonical checkout and existing snapshot must be separate directories")
    try:
        expected = canonical_inputs(canonical)
        present = snapshot_names(snapshot)
        expected_names = set(expected["files"])
        mismatches = [name for name in sorted(expected_names & present) if sha256(snapshot / name) != expected["files"][name]]
        missing, extra = sorted(expected_names - present), sorted(present - expected_names)
        if missing or extra or mismatches:
            print(json.dumps({"status": "snapshot_mismatch", "missing": missing, "extra": extra, "different": mismatches}, indent=2))
            return 1
        if expected != canonical_inputs(canonical):
            raise ValueError("canonical source changed during verification")
        if snapshot_names(snapshot) != present or any(sha256(snapshot / name) != expected["files"][name] for name in expected_names):
            raise ValueError("snapshot changed during verification")
        receipt_path = snapshot / RECEIPT
        receipt = {"schemaVersion": 1, "canonical": expected}
        if args.write_receipt:
            with receipt_path.open("x", encoding="utf-8", newline="\n") as output:
                output.write(json.dumps(receipt, indent=2) + "\n")
        else:
            if json.loads(receipt_path.read_text(encoding="utf-8")) != receipt:
                raise ValueError("snapshot receipt does not match the verified canonical commit and files")
        print(json.dumps({"status": "verified", "repository": REPOSITORY, "commit": expected["commit"], "files": len(expected["files"]), "receipt": str(receipt_path)}, indent=2))
        return 0
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print("snapshot verification failed: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
