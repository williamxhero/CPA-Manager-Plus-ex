"""Exercise the build entrypoint without touching the installed CPA stack."""

import importlib.util
import json
import shutil
from argparse import Namespace
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path


ENTRY = Path(__file__).resolve().parents[2] / "ops/windows/build-local-stack.py"
NAMES = ("CPA-Core-ex", "CPA-Manager-Plus-ex", "CPA-Usage-Keeper-ex",
         "CPA-OpenCode-ex", "CPA-Qwen")


class BuildSourceGateTest(unittest.TestCase):
    def setUp(self):
        temporary = ENTRY.parents[1].parent / "bin/tmp"
        temporary.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="cpa-build-gate-", dir=temporary)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.sources = {}
        for name in NAMES:
            repo = self.root / name
            repo.mkdir()
            self.git(repo, "init", "-q")
            self.git(repo, "config", "user.email", "fixture@example.invalid")
            self.git(repo, "config", "user.name", "Build fixture")
            (repo / "source.txt").write_text("old SDK/source\n", encoding="utf-8")
            self.git(repo, "add", ".")
            self.git(repo, "commit", "-qm", "fixture main")
            self.git(repo, "branch", "-M", "main")
            self.git(repo, "switch", "-qc", "spec/92-integration")
            (repo / "source.txt").write_text("reviewed source\n", encoding="utf-8")
            self.git(repo, "commit", "-qam", "fixture gate contract")
            head = self.git(repo, "rev-parse", "HEAD").strip()
            self.sources[name] = {"repository": str(repo), "ref": "spec/92-integration",
                                  "head": head}

    def git(self, repo, *args):
        return subprocess.check_output(["git", "-C", str(repo), *args], text=True,
                                       stderr=subprocess.STDOUT)

    def invoke(self):
        manifest = self.root / "sources.json"
        manifest.write_text(json.dumps({"schema": 1, "version": "spec92-fixture",
                                       "sources": self.sources}), encoding="utf-8")
        return subprocess.run([sys.executable, str(ENTRY), "--sources", str(manifest),
                               "--output", str(self.root / "output"), "--preflight-only"],
                              text=True, capture_output=True)

    def test_source_ref_drift_is_rejected_before_build(self):
        plugin = self.sources["CPA-OpenCode-ex"]
        plugin["head"] = self.git(Path(plugin["repository"]), "rev-parse", "main").strip()
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source_ref_mismatch:CPA-OpenCode-ex", result.stdout + result.stderr)
        self.assertFalse((self.root / "output").exists())

    def test_exact_refs_are_accepted_with_full_sdk_source_identity(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        receipt = json.loads(result.stdout)
        self.assertEqual(receipt["sdk_revision"], self.sources["CPA-Core-ex"]["head"])
        self.assertEqual(set(receipt["sources"]), set(NAMES))
        self.assertFalse((self.root / "output").exists())

    def exercise_build(self, *, fail_publication=None, invalid_snapshot=False):
        """Real Git/filesystem lifecycle, synthetic compiler/Node boundary only."""
        spec = importlib.util.spec_from_file_location("cpa_stack_build_under_test", ENTRY)
        module = importlib.util.module_from_spec(spec)
        with patch.object(sys, "dont_write_bytecode", True):
            spec.loader.exec_module(module)
        for name, item in self.sources.items():
            repo = Path(item["repository"])
            for relative in ("go.mod", "go.sum"):
                (repo / relative).write_text("module cpa-credential-qwen\n" if name == "CPA-Qwen" and relative == "go.mod" else "fixture\n", encoding="utf-8")
            if name == "CPA-Qwen":
                (repo / "scripts").mkdir()
                shutil.copy2(ENTRY.parents[1].parent / "providers/qwen/scripts/verify-manager-snapshot.py",
                             repo / "scripts/verify-manager-snapshot.py")
            if name == "CPA-Manager-Plus-ex":
                for generated, retained in (("index.html", "management.html"),
                                             ("favicon.ico", "favicon.ico"),
                                             ("apple-touch-icon.png", "apple-touch-icon.png")):
                    for relative in ("apps/web/dist/" + generated,
                                     "apps/manager-server/internal/httpapi/web/" + retained):
                        path = repo / relative
                        path.parent.mkdir(parents=True, exist_ok=True)
                        path.write_bytes(b"synthetic-embedded-asset\n")
            self.git(repo, "add", ".")
            self.git(repo, "commit", "-qm", "synthetic build input")
            item["head"] = self.git(repo, "rev-parse", "HEAD").strip()
        canonical = Path(self.sources["CPA-Qwen"]["repository"])
        manager = Path(self.sources["CPA-Manager-Plus-ex"]["repository"])
        snapshot = manager / "providers/qwen"
        snapshot.mkdir(parents=True)
        for relative in self.git(canonical, "ls-files").splitlines():
            target = snapshot / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(canonical / relative, target)
        subprocess.run([sys.executable, "-B", str(canonical / "scripts/verify-manager-snapshot.py"),
                        "--canonical-source", str(canonical), "--snapshot", str(snapshot), "--write-receipt"],
                       check=True, capture_output=True)
        if invalid_snapshot:
            (snapshot / "source.txt").write_text("not canonical\n", encoding="utf-8")
        self.git(manager, "add", ".")
        self.git(manager, "commit", "-qm", "controlled snapshot fixture")
        self.sources["CPA-Manager-Plus-ex"]["head"] = self.git(manager, "rev-parse", "HEAD").strip()
        output = self.root / "candidate"
        args = Namespace(output=output, node=sys.executable, npm_cli="fixture-npm.js", cc="fixture-gcc")
        sources = {"schema": 1, "version": "spec92-fixture", "sources": self.sources,
                   "sdk_revision": self.sources["CPA-Core-ex"]["head"]}
        real_run, real_output, real_write = subprocess.run, subprocess.check_output, module.write_json
        go_commands = []

        def run(command, **kwargs):
            command = list(map(str, command))
            if command[0] == "git" or command[:2] == [sys.executable, "-B"]:
                return real_run(command, **kwargs)
            if command[0] == "go":
                go_commands.append(command)
                if "-o" in command:
                    Path(command[command.index("-o") + 1]).write_bytes(b"synthetic-artifact\n")
            return subprocess.CompletedProcess(command, 0)

        def check_output(command, **kwargs):
            if command[:3] == ["go", "version", "-m"]:
                binary = Path(command[-1])
                revision = self.sources[binary.parent.name]["head"]
                return ("vcs.revision=" + revision + "\nvcs.modified=false\n" +
                        str(output / "sources/CPA-Core-ex") + "\n")
            return real_output(command, **kwargs)

        def write_json(path, data):
            if fail_publication and path.name == fail_publication and (
                    path.name == "candidate-manifest.json" or data.get("accepted") is True):
                raise PermissionError("synthetic publication failure")
            real_write(path, data)

        with patch.object(module.subprocess, "run", side_effect=run), \
                patch.object(module.subprocess, "check_output", side_effect=check_output), \
                patch.object(module, "write_json", side_effect=write_json):
            if fail_publication or invalid_snapshot:
                with self.assertRaises(subprocess.CalledProcessError if invalid_snapshot else PermissionError):
                    module.build(args, sources)
            else:
                module.build(args, sources)
        return output, go_commands

    def test_plugin_commands_explicitly_allow_external_modfile_updates(self):
        output, commands = self.exercise_build()
        plugin_commands = [command for command in commands
                           if any(flag.startswith("-modfile=") for flag in command)
                           and command[1] in ("build", "test")]
        self.assertEqual(len(plugin_commands), 6)
        edits = [command for command in commands if command[:3] == ["go", "mod", "edit"]]
        self.assertEqual(len(edits), 2)
        for command in edits:
            self.assertIn("-replace=github.com/router-for-me/CLIProxyAPI/v8=" +
                          str(output / "sources/CPA-Core-ex"), command)
        for command in plugin_commands:
            self.assertIn("-mod=mod", command,
                          "external module updates must not depend on inherited GOFLAGS")
            self.assertTrue(any(flag.startswith("-modfile=" + str(output)) for flag in command))
        self.assertTrue(json.loads((output / "build-result.json").read_text())["accepted"])

    def test_bad_qwen_snapshot_stops_all_compilation_and_candidate_publication(self):
        output, commands = self.exercise_build(invalid_snapshot=True)
        self.assertEqual(commands, [])
        self.assertFalse(json.loads((output / "build-result.json").read_text())["accepted"])
        self.assertFalse((output / "candidate-manifest.json").exists())

    def test_compatibility_dll_is_hashed_but_has_no_default_deployment_target(self):
        output, commands = self.exercise_build()
        compat = output / "artifacts/CPA-Qwen/aliyun-bss-quota.dll"
        self.assertTrue(compat.is_file())
        manifest = json.loads((output / "candidate-manifest.json").read_text())
        self.assertFalse(any("aliyun-bss-quota" in entry["relative"] for entry in manifest["files"]))
        entry, = manifest["candidate_only"]
        self.assertEqual(entry["relative"], "CPA-Qwen/aliyun-bss-quota.dll")
        self.assertEqual(entry["policy"], "candidate-only")
        self.assertNotIn("target", entry)
        self.assertEqual(len(entry["sha256"]), 64)
        self.assertEqual(entry["source_head"], self.sources["CPA-Qwen"]["head"])
        self.assertEqual(entry["sdk_revision"], self.sources["CPA-Core-ex"]["head"])
        self.assertEqual(sum(command[-1] == "./cmd/bailian-quota" for command in commands), 1)
        receipt = json.loads((output / "build-result.json").read_text())
        self.assertEqual(receipt["qwen_snapshot"]["canonical"]["commit"], self.sources["CPA-Qwen"]["head"])

    def test_manifest_publication_failure_never_leaves_an_accepted_receipt(self):
        output, _ = self.exercise_build(fail_publication="candidate-manifest.json")
        self.assertFalse(json.loads((output / "build-result.json").read_text())["accepted"])
        self.assertFalse((output / "candidate-manifest.json").exists())

    def test_final_success_receipt_failure_does_not_reuse_an_earlier_success(self):
        output, _ = self.exercise_build(fail_publication="build-result.json")
        self.assertFalse(json.loads((output / "build-result.json").read_text())["accepted"])


if __name__ == "__main__":
    unittest.main()
