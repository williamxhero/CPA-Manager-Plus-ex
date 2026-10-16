"""CLI acceptance tests; no host ports or real configuration/service access."""
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import unittest

SCRIPTS = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def snapshot(root):
    return {str(p.relative_to(root)): ("link", os.readlink(p)) if p.is_symlink()
            else ("file", p.read_bytes()) if p.is_file() else ("dir",)
            for p in root.rglob("*")}


@unittest.skipUnless(os.environ.get("DEPLOY_TEST_CONTAINER") == "1", "requires disposable Linux container")
class DeployTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = Path(tempfile.mkdtemp(prefix="deploy-fixture-"))
        cls.binary = cls.build / "service"
        subprocess.run(["gcc", "-Wall", "-Wextra", "-Werror", str(SCRIPTS / "tests/service.c"),
                        "-o", str(cls.binary)], check=True)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.build)

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="deploy-test-"))
        self.root = self.tmp / "instance"
        self.commands = self.tmp / "commands"
        self.commands.mkdir()
        self.state = self.tmp / "service-state"
        self.state.mkdir()
        self.port, self.rehearsal_port = free_port(), free_port()
        self.env = dict(os.environ, PATH=f"{self.commands}:{os.environ['PATH']}",
                        DEPLOY_ROOT=str(self.root), SERVICE_STATE=str(self.state),
                        SERVICE_PORT=str(self.port), PID_LOG=str(self.tmp / "pids"))
        self.result = self.tmp / "result.json"
        self.command("systemctl", f'exec python3 "{SCRIPTS / "tests/service-control.py"}" "$@"\n')
        # SSH/SCP are replaced only at their external boundary. The actual
        # deployment remote shell commands run unchanged in this container.
        self.command("ssh", f'exec python3 "{SCRIPTS / "tests/ssh.py"}" "$@"\n')
        self.command("scp", 'dest="${@: -1}"; dest="${dest#*:}"; exec /bin/cp "${@: -2:1}" "$dest"\n')
        self.command("pkill", 'printf unsafe-pkill >> "$SERVICE_STATE/unsafe"; exit 0\n')
        self.command("fuser", 'printf unsafe-fuser >> "$SERVICE_STATE/unsafe"; exit 0\n')
        self.command("sleep", 'if [[ "$1" == 4 || "$1" == 5 || "$1" == 6 ]]; then exec /bin/sleep 0.1; fi; exec /bin/sleep "$@"\n')

    def tearDown(self):
        pidlog = self.tmp / "pids"
        if pidlog.exists():
            for pid in map(int, pidlog.read_text().split()):
                try:
                    os.kill(pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        shutil.rmtree(self.tmp)

    def command(self, name, body):
        path = self.commands / name
        path.write_text("#!/bin/bash\n" + body)
        path.chmod(0o755)

    def fixture(self, kind):
        self.kind = kind
        self.env["DEPLOY_KIND"] = kind
        self.name = "cli-proxy-api" if kind == "core" else "cpa-manager-plus"
        self.version = "CLIProxyAPI_8.0.15_linux_amd64" if kind == "core" else "cpa-manager-plus_vnew_linux_amd64"
        for version in ("old", self.version) if kind == "core" else ("old",):
            release = self.root / "app" / version
            release.mkdir(parents=True)
            shutil.copy2(self.binary, release / self.name)
        (self.root / "app/current").symlink_to(self.root / "app/old")
        if kind == "core":
            self.old_config = f'port: {self.port}\nplugins:\n  dir: "{self.root}/plugins"\n# synthetic-secret-canary-DO-NOT-LOG\n'
            (self.root / "config.yaml").write_text(self.old_config)
            (self.root / "config.yaml.final").write_text(self.old_config + "# new-config\n")
            plugins = self.root / "plugins/linux/amd64"
            plugins.mkdir(parents=True)
            for name in ("qwen-cliproxyapi.so", "opencode-go-cliproxyapi.so"):
                (plugins / name).write_text("original-plugin")
            (self.tmp / "oauth").mkdir()
            quota = self.tmp / "quota"
            quota.write_text("#!/bin/sh\nexit 0\n")
            quota.chmod(0o755)
        else:
            (self.root / "panel/old").mkdir(parents=True)
            (self.root / "panel/old/management.html").write_text("old-panel")
            (self.root / "panel/current").symlink_to(self.root / "panel/old")
            (self.root / "current_release.txt").write_text("old\n")
            (self.root / "deploy-backups").mkdir()
            self.artifacts = self.tmp / "artifacts"
            self.artifacts.mkdir()
            shutil.copy2(self.binary, self.artifacts / self.name)
            (self.artifacts / "management.html").write_text("new-panel")
        subprocess.run(["systemctl", "--user", "restart", "fixture"], env=self.env, check=True)
        self.wait_service()

    def wait_service(self):
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", self.port), timeout=.1):
                    return
            except OSError:
                time.sleep(.02)
        self.fail("fixture service did not bind")

    def run_deploy(self, *args, failure=""):
        command = ["bash", str(SCRIPTS / f'deploy-{"core-yosef" if self.kind == "core" else "cpamp"}.sh'),
                   "--root", str(self.root), "--service", "fixture.service", "--result-path", str(self.result),
                   "--health-url", f'http://127.0.0.1:{self.port}/{"v1/models" if self.kind == "core" else "health"}',
                   "--isolated-port", str(self.rehearsal_port), "--health-timeout", "2"]
        if self.kind == "core":
            command += ["--oauth-dir", str(self.tmp / "oauth"), "--quota-command", str(self.tmp / "quota")]
        else:
            command += ["--host", "fixture-host", "--artifact-dir", str(self.artifacts), "--version", "new"]
        self.result.unlink(missing_ok=True)
        self.run = subprocess.run(command + list(args), env=dict(self.env, FAILURE=failure),
                                  capture_output=True, text=True, timeout=20)
        self.assertNotIn("synthetic-secret-canary", self.run.stdout + self.run.stderr)
        self.assertTrue(self.result.exists(), self.run.stdout + self.run.stderr)
        self.report = json.loads(self.result.read_text())
        self.assertNotIn("synthetic-secret-canary", self.result.read_text())
        return self.run

    def assert_restored(self):
        self.assertEqual((self.root / "app/current").resolve(), self.root / "app/old")
        if self.kind == "core":
            self.assertEqual((self.root / "config.yaml").read_text(), self.old_config)
            self.assertEqual((self.root / "plugins/linux/amd64/qwen-cliproxyapi.so").read_text(), "original-plugin")
            self.assertFalse((self.root / "plugins/added.so").exists())
        else:
            self.assertEqual((self.root / "panel/current").resolve(), self.root / "panel/old")
            self.assertEqual((self.root / "current_release.txt").read_text(), "old\n")
        pid = (self.state / "pid").read_text()
        check = subprocess.run(["python3", str(SCRIPTS / "deploy-check.py"), "health", "--pid", pid,
                                "--binary", str(self.root / "app/old" / self.name),
                                "--url", f"http://127.0.0.1:{self.port}/health"], capture_output=True)
        self.assertEqual(check.returncode, 0, "old service must really recover")

    def test_core_success_and_complete_backup(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy().returncode, 0, self.run.stdout + self.run.stderr)
        self.assertEqual(self.report["status"], "deployed")
        self.assertEqual((self.root / "app/current").resolve().name, self.version)
        backup = Path(self.report["backup"])
        self.assertFalse((backup / "app-target.txt").is_symlink())
        self.assertEqual((backup / "app-target.txt").read_text().strip(), str(self.root / "app/old"))
        self.assertEqual((backup / "config.yaml.old").read_text(), self.old_config)
        self.assertEqual((backup / "plugins/linux/amd64/qwen-cliproxyapi.so").read_text(), "original-plugin")

    def test_core_restart_failure_restores_pointer_config_and_health(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy(failure="restart-new").returncode, 3, self.run.stdout + self.run.stderr)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_core_failed_rollback_is_not_false_success(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy(failure="restart-all").returncode, 4)
        self.assertEqual(self.report["status"], "rollback_failed")
        self.assertEqual((self.root / "app/current").resolve(), self.root / "app/old")

    def inject(self, command, condition):
        self.command(command, f'''if [[ ! -e "$SERVICE_STATE/injected" ]] && {condition}; then
  touch "$SERVICE_STATE/injected"
  printf 'synthetic-secret-canary-DO-NOT-LOG' >&2
  exit 1
fi
exec /bin/{command} "$@"
''')

    def test_dry_run_has_no_persistent_or_service_side_effects(self):
        for kind in ("core", "manager"):
            with self.subTest(kind=kind):
                if self.root.exists():
                    shutil.rmtree(self.root)
                self.fixture(kind)
                before = snapshot(self.root)
                pid, count = (self.state / "pid").read_text(), (self.state / "restarts").read_text()
                stages = set(Path("/tmp").glob("*rehearsal.*"))
                self.assertEqual(self.run_deploy("--dry-run").returncode, 0, self.run.stdout + self.run.stderr)
                self.assertEqual(snapshot(self.root), before)
                self.assertEqual((self.state / "pid").read_text(), pid)
                self.assertEqual((self.state / "restarts").read_text(), count)
                self.assertEqual(set(Path("/tmp").glob("*rehearsal.*")), stages)
                self.assertFalse((self.state / "unsafe").exists())

    def test_core_invalid_config_rejected_despite_successful_help(self):
        self.fixture("core")
        config = self.root / "config.yaml.final"
        config.write_text(config.read_text() + "INVALID\n")
        before = snapshot(self.root)
        self.assertEqual(self.run_deploy().returncode, 2)
        self.assertEqual(snapshot(self.root), before)
        self.assertEqual(self.report["status"], "preflight_failed")

    def test_core_backup_failure_aborts_before_install(self):
        self.fixture("core")
        pid = (self.state / "pid").read_text()
        self.inject("cp", '[[ "${@: -1}" == *config.yaml.old ]]')
        self.assertEqual(self.run_deploy().returncode, 2)
        self.assertEqual(self.report["status"], "preflight_failed")
        self.assertEqual((self.state / "pid").read_text(), pid)
        self.assert_restored()

    def test_core_config_write_failure_restores_previous_state(self):
        self.fixture("core")
        self.inject("cp", '[[ "${@: -1}" == *instance/.config-new-* ]]')
        self.assertEqual(self.run_deploy().returncode, 3, self.run.stdout + self.run.stderr)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_core_pointer_failure_rolls_back(self):
        self.fixture("core")
        self.inject("mv", '[[ "${@: -1}" == "$DEPLOY_ROOT/app/current" ]]')
        self.assertEqual(self.run_deploy().returncode, 3, self.run.stdout + self.run.stderr)
        self.assert_restored()

    def test_core_health_failure_restores_plugins_and_absence(self):
        self.fixture("core")
        config = self.root / "config.yaml.final"
        config.write_text(config.read_text() + f"delete-plugin: {self.root}/plugins/linux/amd64/qwen-cliproxyapi.so\nwrite-plugin: {self.root}/plugins/added.so\n")
        self.assertEqual(self.run_deploy(failure="health-new").returncode, 3)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_core_foreign_healthy_process_is_not_accepted(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy(failure="foreign").returncode, 3)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_core_foreign_401_responder_is_not_accepted(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy(failure="foreign401").returncode, 3)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_core_rollback_write_failure_has_clear_status(self):
        self.fixture("core")
        self.inject("cp", '[[ "${@: -1}" == *instance/.config-restore-* ]]')
        self.assertEqual(self.run_deploy(failure="health-new").returncode, 4)
        self.assertEqual(self.report["status"], "rollback_failed")
        self.assertIn({"check": "restored", "ok": False}, self.report["health"])

    def test_core_rollback_health_failure_has_clear_status(self):
        self.fixture("core")
        self.assertEqual(self.run_deploy(failure="health-all").returncode, 4)
        self.assertEqual(self.report["status"], "rollback_failed")
        self.assertEqual((self.root / "app/current").resolve(), self.root / "app/old")
        self.assertEqual((self.root / "config.yaml").read_text(), self.old_config)

    def test_core_inline_plugin_path_is_rejected_before_rehearsal(self):
        self.fixture("core")
        config = self.root / "config.yaml.final"
        config.write_text(f'port: {self.port}\nplugins: {{dir: "/unsafe/plugins"}}\n')
        before = snapshot(self.root)
        self.assertEqual(self.run_deploy("--dry-run").returncode, 2)
        self.assertEqual(snapshot(self.root), before)

    def test_core_no_restart_does_not_claim_runtime_health(self):
        self.fixture("core")
        count = (self.state / "restarts").read_text()
        self.assertEqual(self.run_deploy("-NoRestart").returncode, 0)
        self.assertEqual(self.report["status"], "installed_not_restarted")
        self.assertEqual((self.state / "restarts").read_text(), count)
        self.assertFalse(any(item["check"] == "deployed" for item in self.report["health"]))

    def test_manager_success_and_rollback_only_without_artifacts(self):
        self.fixture("manager")
        self.assertEqual(self.run_deploy().returncode, 0, self.run.stdout + self.run.stderr)
        self.assertEqual((self.root / "app/current").resolve().name, self.version)
        self.assertEqual((self.root / "panel/current").resolve().name, "vnew")
        self.assertEqual((self.root / "current_release.txt").read_text(), "new\n")
        shutil.rmtree(self.artifacts)
        (self.root / "deploy-backups/zzzz-incomplete").mkdir()
        self.assertEqual(self.run_deploy("--rollback-only").returncode, 0, self.run.stdout + self.run.stderr)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_manager_backup_failure_aborts_before_replace(self):
        self.fixture("manager")
        count = (self.state / "restarts").read_text()
        self.inject("cp", '[[ "${@: -1}" == *deploy-backups/*/app/ ]]')
        self.assertEqual(self.run_deploy().returncode, 2)
        self.assertEqual((self.state / "restarts").read_text(), count)
        self.assertFalse((self.root / "app" / self.version).exists())
        self.assert_restored()

    def test_manager_replace_failure_restores_and_reports_failure(self):
        self.fixture("manager")
        self.command("scp", '''dest="${@: -1}"; dest="${dest#*:}"
if [[ "$dest" == "$DEPLOY_ROOT/app/"* ]]; then printf synthetic-secret-canary >&2; exit 1; fi
exec /bin/cp "${@: -2:1}" "$dest"
''')
        self.assertEqual(self.run_deploy().returncode, 3, self.run.stdout + self.run.stderr)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_manager_partial_switch_restores_all_pointers(self):
        self.fixture("manager")
        self.inject("mv", '[[ "${@: -1}" == "$DEPLOY_ROOT/panel/current" ]]')
        self.assertEqual(self.run_deploy().returncode, 3, self.run.stdout + self.run.stderr)
        self.assert_restored()

    def test_manager_restart_failure_restores_health(self):
        self.fixture("manager")
        self.assertEqual(self.run_deploy(failure="restart-new").returncode, 3)
        self.assert_restored()

    def test_manager_failed_rollback_is_explicit(self):
        self.fixture("manager")
        self.assertEqual(self.run_deploy(failure="restart-old").returncode, 4)
        self.assertEqual(self.report["status"], "rollback_failed")
        self.assertEqual((self.root / "current_release.txt").read_text(), "old\n")

    def test_manager_rollback_preserves_absent_release_text(self):
        self.fixture("manager")
        (self.root / "current_release.txt").unlink()
        self.assertEqual(self.run_deploy(failure="health-new").returncode, 3)
        self.assertFalse((self.root / "current_release.txt").exists())
        self.assertEqual((self.root / "app/current").resolve(), self.root / "app/old")

    def test_manager_foreign_healthy_process_is_not_accepted(self):
        self.fixture("manager")
        self.assertEqual(self.run_deploy(failure="foreign").returncode, 3)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()

    def test_manager_result_serializes_supplied_strings_safely(self):
        self.fixture("manager")
        service = 'fixture"\\\\\n.service'
        self.assertEqual(self.run_deploy("--service", service).returncode, 0)
        self.assertEqual(self.report["serviceName"], service)

    def test_occupied_rehearsal_port_does_not_kill_existing_service(self):
        self.fixture("manager")
        self.rehearsal_port = self.port
        pid = (self.state / "pid").read_text()
        self.assertEqual(self.run_deploy().returncode, 2)
        self.assertEqual((self.state / "pid").read_text(), pid)
        self.assertFalse((self.state / "unsafe").exists())
        self.assert_restored()

    def test_manager_health_failure_restores_all_version_pointers(self):
        self.fixture("manager")
        self.assertNotEqual(self.run_deploy(failure="health-new").returncode, 0)
        self.assertEqual(self.report["status"], "rolled_back")
        self.assert_restored()


if __name__ == "__main__":
    unittest.main(verbosity=2)
