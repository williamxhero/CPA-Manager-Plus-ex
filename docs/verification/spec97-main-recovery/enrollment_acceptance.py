"""Offline ordinary-main enrollment acceptance; no browser/parser replacement.

Usage: python enrollment_acceptance.py OUTPUT [--artifact EXISTING_EXE]
Without --artifact, build the ordinary main from the current clean commit.
"""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import types
import unittest
import urllib.parse

spec = importlib.util.spec_from_file_location('main_acceptance', Path(__file__).with_name('acceptance.py'))
entry = importlib.util.module_from_spec(spec)
spec.loader.exec_module(entry)
OUT = Path(sys.argv[1]).resolve()


class EnrollmentAcceptance(unittest.TestCase):
    def setUp(self):
        self.folder = OUT / self._testMethodName
        self.folder.mkdir()
        self.dependencies = entry.Dependencies(self.folder)
        self.addCleanup(self.dependencies.close)

    def test_ordinary_registration_restart_withdrawal_and_identity_fence(self):
        d = self.dependencies
        state = self.folder / 'sqlite'
        first = entry.Manager(self, d, state, entry.PRODUCTION_ARTIFACT, 'first')
        entry.wait(lambda: (p := first.inventory().get('latestRound')) and p['state'] != 'running')
        web = dict(entry.WEB, parsingRule='unverified')
        source = entry.request(first.api + '/usage-service/official-balance',
                               dict(scope=d.scope, targets=[d.target], version=0,
                                    adapter='layso_billing_web_v1', web=web), 'PUT')
        binding = dict(scope=source['scope'], targets=source['targets'])
        registration = dict(**binding, version=source['version'], sourceAuthority=source['webSourceAuthority'],
                            registrationVersion=0, proofReference='controlled-review-reference-1')
        path = '/usage-service/official-balance/web-enrollment'
        saved = entry.request(first.api + path, registration, 'PUT')
        self.assertEqual(saved['state'], 'pending_verification')
        self.assertEqual(saved['registrationVersion'], 1)
        self.assertFalse(saved['collectionAuthorized'])
        self.assertFalse(saved['parserSupported'])
        first.stop()
        second = entry.Manager(self, d, state, entry.PRODUCTION_ARTIFACT, 'restarted')
        query = path + '?binding=' + urllib.parse.quote(json.dumps(binding))
        restored = entry.request(second.api + query)
        self.assertEqual(restored, saved)
        status = second.status(binding)
        self.assertFalse(status['managerDirectQuery'])
        self.assertNotIn('lastSuccess', status)
        entry.request(second.api + '/usage-service/official-balance', dict(**binding, version=1), 'POST', 400)
        entry.request(second.api + path, dict(registration, registrationVersion=1, parserVerified=True), 'PUT', 400)
        withdrawn = entry.request(second.api + path,
                                  dict(registration, registrationVersion=1, withdraw=True, proofReference=''), 'PUT')
        self.assertEqual(withdrawn['state'], 'not_registered')
        self.assertEqual(withdrawn['registrationVersion'], 2)
        self.assertNotIn('proofReference', withdrawn)
        entry.request(second.api + path, dict(registration, registrationVersion=1), 'PUT', 409)
        with d.mu:
            d.target['credential_version'] += 1
        entry.request(second.api + query, expected=409)
        self.assertEqual(d.calls, [])
        entry.dump(self.folder / 'api-readback.json', dict(saved=saved, restored=restored,
                   withdrawn=withdrawn, status=status, controlledExternalCommands=d.calls,
                   verifiedBrowser=False, observedParser=False))
        second.stop()


if __name__ == '__main__':
    OUT.mkdir(exist_ok=False)
    (OUT / 'temp').mkdir()
    (OUT / 'RUNNER.py').write_bytes(Path(__file__).read_bytes())
    entry.GO_INPUTS = types.SimpleNamespace(source_fingerprint=entry.source_fingerprint)
    entry.HEAD = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=entry.SOURCE, text=True).strip()
    entry.BUILD_ENV = os.environ.copy()
    entry.BUILD_ENV.update(GOROOT=str(entry.GO.parent.parent), GOTOOLCHAIN='local', GOWORK='off', GOENV='off',
                           GOFLAGS='', GOPROXY='off', GOSUMDB='off', GOTELEMETRY='off',
                           GOCACHE=str(Path(os.environ['CPAMP_ENTRY_GOCACHE']).resolve()),
                           TEMP=str(OUT / 'temp'), TMP=str(OUT / 'temp'))
    entry.FIXTURE_ARTIFACT = OUT / 'fixture-not-built.exe'
    before = entry.snapshot()
    builds = []
    if len(sys.argv) > 2:
        assert len(sys.argv) == 4 and sys.argv[2] == '--artifact'
        entry.PRODUCTION_ARTIFACT = Path(sys.argv[3]).resolve()
    else:
        assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=entry.SOURCE), 'Build from clean commit only'
        entry.PRODUCTION_ARTIFACT = OUT / 'cpa-manager-ordinary.exe'
        builds = [entry.build('', entry.PRODUCTION_ARTIFACT)]
    suite = unittest.TestLoader().loadTestsFromTestCase(EnrollmentAcceptance)
    with (OUT / 'tests.stderr.log').open('w', encoding='utf-8') as stream:
        result = unittest.TextTestRunner(stream=stream, verbosity=2, resultclass=entry.RecordedResult).run(suite)
    after = entry.snapshot()
    processes = [json.loads(p.read_text(encoding='utf-8')) for p in OUT.rglob('PROCESS.json')]
    entry.dump(OUT / 'RESULT.json', dict(sourceCommit=entry.HEAD, testsRun=result.testsRun,
               success=result.wasSuccessful(), failures=len(result.failures), errors=len(result.errors),
               skips=len(result.skipped), events=result.events, builds=builds, processes=processes,
               sourceBefore=before, sourceAfter=after, sourceUnchanged=before == after,
               ordinaryArtifact=True, realBrowser=False, externalNetwork=False, deployment=False))
    print(json.dumps(dict(success=result.wasSuccessful(), testsRun=result.testsRun,
                          failures=len(result.failures), errors=len(result.errors), processes=len(processes))))
    raise SystemExit(0 if result.wasSuccessful() and before == after else 1)
