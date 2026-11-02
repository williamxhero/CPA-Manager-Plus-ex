"""Exercise the build entrypoint without touching the installed CPA stack."""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ENTRY = Path(__file__).resolve().parents[2] / "ops/windows/build-local-stack.py"
NAMES = ("CPA-Core-ex", "CPA-Manager-Plus-ex", "CPA-Usage-Keeper-ex",
         "CPA-OpenCode-ex", "CPA-Qwen")


class BuildSourceGateTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="cpa-build-gate-")
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


if __name__ == "__main__":
    unittest.main()
