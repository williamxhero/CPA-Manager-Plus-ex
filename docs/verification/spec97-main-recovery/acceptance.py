"""Offline real-main artifact acceptance; loopback dependencies, real temp SQLite."""
from datetime import datetime, timezone
import base64
import copy
import ctypes
import hashlib
import shutil
import sqlite3
import types
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import threading
import time
import traceback
import unittest
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SOURCE = Path(__file__).resolve().parents[3]
OUT = Path(sys.argv[1]).resolve()
ROOT = OUT
if os.name != 'nt':
    raise SystemExit('This opt-in artifact acceptance runner requires Windows console signals.')
KEY = 'cpamp_test_key_0123456789abcdef'
GO = Path(os.environ.get('CPAMP_ENTRY_GO') or shutil.which('go') or 'go').resolve()
SCOPE = dict(instance_id='11111111-1111-4111-8111-111111111111', instance_incarnation='22222222-2222-4222-8222-222222222222', boot_epoch='33333333-3333-4333-8333-333333333333', snapshot_revision=7)
TARGET = dict(resource_id='44444444-4444-4444-8444-444444444444', incarnation='55555555-5555-4555-8555-555555555555', identity_version=2, credential_version=3, config_version=4, membership_version=0)
WEB = dict(site='layso', accountId='synthetic-account-a', tenantId='synthetic-tenant-a', role='billing-reader', executionUser='synthetic-operator', browser=dict(family='microsoft-edge', profile='Profile 1', instanceId='synthetic-edge-instance'), currency='USD', parsingRule='synthetic-observed-page-v1')


def dump(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_fingerprint():
    module_root = SOURCE / 'apps/manager-server'
    paths = [*module_root.rglob('*.go'), module_root / 'go.mod', module_root / 'go.sum']
    files = {p.relative_to(SOURCE).as_posix(): sha(p) for p in sorted(paths)}
    return dict(sha256=hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest(), files=files)


def snapshot():
    module_root = SOURCE / 'apps/manager-server'
    paths = [*module_root.rglob('*.go'), module_root / 'go.mod', module_root / 'go.sum',
             *(SOURCE / 'apps/web/src').rglob('*'),
             *[SOURCE / p for p in ['package.json', 'package-lock.json', 'apps/web/package.json',
               'apps/web/tsconfig.json', 'apps/web/tsconfig.node.json', 'apps/web/vite.config.ts']]]
    files = {p.relative_to(SOURCE).as_posix(): sha(p) for p in sorted(set(paths)) if p.is_file()}
    return dict(sha256=hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest(), files=files)


class RejectRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(url, body=None, method='GET', expected=200):
    assert urllib.parse.urlsplit(url).hostname == '127.0.0.1'
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={'Authorization': 'Bearer ' + KEY, 'Content-Type': 'application/json'})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), RejectRedirects())
    try:
        with opener.open(req, timeout=5) as res:
            status, raw = res.status, res.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    assert status == expected, (method, url, status, expected, raw)
    return json.loads(raw) if raw else None


def wait(predicate, timeout=7):
    until = time.monotonic() + timeout
    last = None
    while time.monotonic() < until:
        last = predicate()
        if last:
            return last
        time.sleep(.025)
    raise AssertionError('bounded condition did not become true; last=' + repr(last))


class Dependencies:
    def __init__(self, folder):
        self.folder = folder
        self.scope, self.target = copy.deepcopy(SCOPE), copy.deepcopy(TARGET)
        self.now, self.revision = int(time.time()*1000), 1
        self.stage, self.enrollment, self.page = '', {}, {}
        self.stop_delay = 0
        self.calls, self.core_requests = [], []
        self.blocked, self.release = threading.Event(), threading.Event()
        self.mu = threading.Lock()
        dependency = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def send(self, body, status=200):
                try:
                    self.send_response(status)
                    self.end_headers()
                    self.wfile.write(json.dumps(body).encode())
                except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                    pass

            def do_GET(self):
                if self.headers.get('Authorization') != 'Bearer synthetic-core-key':
                    self.send({'error': 'unauthorized'}, 401)
                    return
                dependency.core_requests.append(dict(path=self.path, method='GET'))
                with dependency.mu:
                    scope, target = copy.deepcopy(dependency.scope), copy.deepcopy(dependency.target)
                if self.path == '/v8/management/resource-capabilities':
                    self.send(dict(version=1, inventory=True, durable=True, gate=False, query=False, policy_locked=True, reason='available', scope=scope))
                elif self.path == '/v8/management/resources':
                    self.send(dict(version=1, **scope, complete=True, resources=[dict(**target, kind='credential', family='codex', manual=dict(disabled=True, excluded=False, zero_weight=False), identity='valid', catalog_readiness='ready', runtime_readiness='unknown', capabilities=dict(inventory=True, query=False, gate=False))]))
                elif self.path == '/v8/management/resource-bindings':
                    self.send(dict(bindings=[]))
                elif self.path.startswith('/v0/management/usage-queue'):
                    self.send(dict(items=[]))
                else:
                    self.send({}, 404)

            def do_POST(self):
                if self.headers.get('Authorization') != 'Bearer ' + KEY or not self.path.startswith('/fixture/'):
                    self.send({'error':'forbidden'}, 403)
                    return
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
                if self.path == '/fixture/clock':
                    with dependency.mu:
                        self.send(dict(NowMS=dependency.now, Revision=dependency.revision))
                elif self.path == '/fixture/enrollment':
                    self.send(dependency.enrollment)
                elif self.path == '/fixture/command':
                    args = body
                    dependency.calls.append(dict(at=time.time(), args=args))
                    output = None
                    if args[0] == 'browsers':
                        output = [] if dependency.stage == 'closed' else [dict(instance_id='synthetic-edge-instance', unresponsive=False)]
                    elif args[:2] == ['session', 'start']:
                        output = dict(session_id='synthetic-entry-owned-session', browser_instance_id='synthetic-edge-instance')
                    elif args[:2] == ['session', 'stop']:
                        assert args == ['session', 'stop', 'synthetic-entry-owned-session']
                        time.sleep(dependency.stop_delay)
                    elif args[0] == 'observe':
                        if dependency.stage == 'blocked':
                            dependency.blocked.set()
                            dependency.release.wait(8)
                        output = dependency.page
                    elif args[0] != 'navigate':
                        self.send({}, 400)
                        return
                    raw = b'' if output is None else json.dumps(output).encode()
                    self.send(dict(Output=base64.b64encode(raw).decode(), Error=''))
                else:
                    self.send({}, 404)

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)

    def advance(self, now):
        with self.mu:
            self.now, self.revision = now, self.revision+1

    def enroll(self, status):
        self.enrollment = dict(Authority=status['webSourceAuthority'], Source=status['web'], ProfileVerified=True, ExecutionUserVerified=True, ParserVerified=True, ProofReference='synthetic-entry-fixture', EvidenceKind='controlled_fixture')
        web = status['web']
        self.page = dict(URL='https://layso.ai/billing', AccountID=web['accountId'], TenantID=web['tenantId'], Role=web['role'], IdentityVerified=True, RoleVerified=True, Currency='USD', Amount='-12345678901234567890.00000000100')

    def close(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        dump(self.folder / 'dependency-calls.json', dict(commands=self.calls, core=self.core_requests))


class Manager:
    def __init__(self, owner, dependencies, state, artifact, label):
        self.dependencies, self.state = dependencies, state
        self.folder = owner.folder / label
        self.folder.mkdir()
        state.mkdir(exist_ok=True)
        config = self.folder / 'config.json'
        dump(config, dict(httpAddr='127.0.0.1:0', dataDir=str(state), dbPath=str(state/'usage.sqlite'), dataKeyPath=str(state/'data.key'), cpaUpstreamUrl=dependencies.url, managementKeyFile=str(state/'absent-management-key'), adminKeyFile=str(state/'absent-admin-key'), dataKeyFile=str(state/'absent-data-key')))
        env = {k:v for k,v in BUILD_ENV.items() if not (k.startswith('CPA_') or k.startswith('USAGE_') or k.startswith('CPAMP_'))}
        env.update(CPA_MANAGER_CONFIG=str(config), CPA_MANAGEMENT_KEY='synthetic-core-key', CPA_MANAGER_ADMIN_KEY=KEY,
            CPAMP_UPDATE_CHECK_ENABLED='false', CPAMP_ENTRY_FIXTURE_URL=dependencies.url, CPAMP_ENTRY_FIXTURE_TOKEN=KEY,
            HTTP_ADDR='127.0.0.1:0', HTTP_PROXY='http://127.0.0.1:1', HTTPS_PROXY='http://127.0.0.1:1', ALL_PROXY='http://127.0.0.1:1', NO_PROXY='127.0.0.1,localhost',
            USAGE_ACCOUNT_ACTIONS_ENABLED='false', USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE='false', USAGE_POLL_INTERVAL_MS='1000')
        self.stdout, self.stderr = (self.folder/'stdout.log').open('wb'), (self.folder/'stderr.log').open('wb')
        flags = subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NEW_CONSOLE
        startup = subprocess.STARTUPINFO()
        startup.dwFlags |= subprocess.STARTF_USESHOWWINDOW
        startup.wShowWindow = subprocess.SW_HIDE
        self.record = dict(command=[str(artifact)], artifactSHA256=sha(artifact), startedAt=datetime.now(timezone.utc).isoformat(), fullMain=True, testBinary=False, overlays=False, dependencyBuild=artifact==FIXTURE_ARTIFACT)
        # Console attach/detach invalidates inherited console stdin handles on
        # Windows. A private null stdin keeps later restarts independent.
        self.process = subprocess.Popen([str(artifact)], cwd=state, env=env, stdin=subprocess.DEVNULL, stdout=self.stdout, stderr=self.stderr, creationflags=flags, startupinfo=startup)
        self.record['pid'] = self.process.pid
        self.stopped = False
        owner.addCleanup(lambda: self.stop(crash=True) if not self.stopped else None)
        def ready():
            if self.process.poll() is not None:
                raise AssertionError('main exited before ready: '+(self.folder/'stderr.log').read_text(encoding='utf-8'))
            text = (self.folder/'stderr.log').read_text(encoding='utf-8')
            match = re.search(r'cpa-manager-plus listening on (127\.0\.0\.1:\d+)', text)
            return match.group(1) if match else None
        self.api = 'http://' + wait(ready, 12)
        self.record['api'] = self.api
        dump(self.folder/'PROCESS.json', self.record)

    def inventory(self):
        return request(self.api+'/usage-service/full-refresh')

    def status(self, binding):
        return request(self.api+'/usage-service/official-balance?binding='+urllib.parse.quote(json.dumps(binding)))

    def round(self, previous='', running=False):
        def ready():
            progress = self.inventory().get('latestRound')
            return progress if progress and progress['id'] != previous and (running or progress['state'] != 'running') else None
        return wait(ready)

    def tick(self, now):
        self.dependencies.advance(now)
        wait(lambda: self.inventory()['schedulerCheckedAtMs']==now)

    def stop(self, crash=False):
        if self.stopped:
            return
        self.stopped = True
        try:
            if self.process.poll() is None:
                if crash:
                    self.process.kill()
                else:
                    kernel = ctypes.windll.kernel32
                    kernel.FreeConsole()
                    if not kernel.AttachConsole(self.process.pid):
                        raise ctypes.WinError()
                    try:
                        if not kernel.GenerateConsoleCtrlEvent(1, self.process.pid):
                            raise ctypes.WinError()
                    finally:
                        kernel.FreeConsole()
            code = self.process.wait(timeout=15)
            self.record.update(exitCode=code, requestedStop='kill_owned_process' if crash else 'CTRL_BREAK_owned_process_group')
            if not crash:
                assert code==0, ('graceful exit', code)
        except BaseException as error:
            self.record['stopError'] = repr(error)
            if self.process.poll() is None:
                self.process.kill()
                self.record['forcedCleanupExitCode']=self.process.wait(timeout=5)
            raise
        finally:
            self.stdout.close()
            self.stderr.close()
            dump(self.folder/'PROCESS.json', self.record)


class EntryAcceptance(unittest.TestCase):
    def setUp(self):
        self.folder = OUT / self._testMethodName
        self.folder.mkdir()
        self.d = Dependencies(self.folder)
        self.addCleanup(self.d.close)
        self.state = self.folder/'sqlite'

    def manager(self, label, production=False):
        if self.state.name != 'sqlite':
            label = self.state.name + '-' + label
        return Manager(self, self.d, self.state, PRODUCTION_ARTIFACT if production else FIXTURE_ARTIFACT, label)

    def configure(self, manager, body=None):
        body = body or dict(scope=self.d.scope, targets=[self.d.target], version=0, adapter='layso_billing_web_v1', web=WEB)
        return request(manager.api+'/usage-service/official-balance', body, 'PUT')

    def seed(self):
        manager = self.manager('first')
        # Main schedules immediately on startup. Let that unconfigured roster
        # finish before enrolling a new source and advancing the next cycle.
        wait(lambda: (p:=manager.inventory().get('latestRound')) and p['state']!='running')
        st = self.configure(manager)
        self.d.enroll(st)
        # Enrollment makes this exact new source due on the next scheduler cycle.
        manager.tick(self.d.now+60_000)
        progress = wait(lambda: (p if (p:=manager.inventory().get('latestRound')) and p['state']=='complete' and p['succeeded']==1 else None))
        binding = dict(scope=st['scope'], targets=st['targets'])
        last = manager.status(binding)['lastSuccess']
        self.assertEqual(last['amountDecimal'], '-12345678901234567890.00000000100')
        return manager, st, binding, last, progress

    def test_graceful_stop_persists_cancelled_attempt(self):
        first, st, binding, last, initial = self.seed()
        self.d.stage, self.d.stop_delay = 'blocked', .7
        first.tick(first.inventory()['nextDueMs'])
        self.assertTrue(self.d.blocked.wait(4))
        running = wait(lambda: (p if (p:=first.inventory().get('latestRound')) and p['state']=='running' and p['items'][0].get('jobId') else None))
        first.stop()
        self.d.release.set()
        second = self.manager('restarted')
        status = second.status(binding)
        dump(self.folder/'restored-status.json',status)
        self.assertEqual(status['attempt'].get('errorCode'), 'WEB_CANCELLED')
        self.assertEqual(status['lastSuccess'], last)
        self.assertTrue(status['stale'])
        job=request(second.api+'/usage-service/official-balance?job='+running['items'][0]['jobId'])
        self.assertEqual(job['state'],'complete')
        second.stop()

    def test_hourly_restart_failure_and_recovery(self):
        first, st, binding, last, initial = self.seed()
        due=first.inventory()['nextDueMs']
        first.stop()
        self.d.advance(due-60_000)
        second=self.manager('restarted')
        wait(lambda:second.inventory()['schedulerCheckedAtMs']==due-60_000)
        self.assertEqual(second.inventory()['nextDueMs'],due)
        self.assertEqual(second.status(binding)['lastSuccess'],last)
        self.d.stage='closed'
        second.tick(due)
        failed=second.round(initial['id'])
        status=second.status(binding)
        self.assertEqual(failed['state'],'partial')
        self.assertEqual(failed['failed'],1)
        self.assertEqual(status['lastSuccess'],last)
        self.assertTrue(status['stale'])
        self.assertEqual(status['attempt']['errorCode'],'WEB_BROWSER_UNAVAILABLE')
        self.assertNotIn('observedAtMs',status['attempt'])
        self.assertNotIn('amountDecimal',status['attempt'])
        next_due=second.inventory()['nextDueMs']
        second.stop()
        third=self.manager('failed-restart')
        restored=third.status(binding)
        self.assertEqual(restored['lastSuccess'],last)
        self.assertTrue(restored['stale'])
        self.d.stage=''
        third.tick(next_due)
        recovered=third.round(failed['id'])
        self.assertEqual(recovered['succeeded'],1)
        self.assertFalse(third.status(binding)['stale'])
        dump(self.folder/'observations.json',dict(initial=initial,failed=failed,recovered=recovered))
        third.stop()

    def test_ordinary_build_rejects_fixture_environment(self):
        first, st, binding, last, initial = self.seed()
        due=first.inventory()['nextDueMs']
        first.stop()
        self.d.advance(due)
        calls=len(self.d.calls)
        normal=self.manager('ordinary-build',production=True)
        status=normal.status(binding)
        self.assertEqual(status['state'],'unavailable')
        self.assertFalse(status['managerDirectQuery'])
        self.assertEqual(status['lastSuccess'],last)
        self.assertTrue(status['stale'])
        # The ordinary binary ignores fixture URL/time/enrollment variables.
        self.assertEqual(len(self.d.calls),calls)
        body=dict(scope=self.d.scope,targets=[self.d.target],version=1,adapter='layso_billing_web_v1',web=WEB)
        request(normal.api+'/usage-service/official-balance',body,'PUT',400)
        normal.stop()

    def test_restart_rejects_old_identity_and_version(self):
        # Separate full-main processes and SQLite per parameter. These are
        # subcases of one unittest; report them separately, never as test passes.
        results=[]
        for kind in ['boot','identity_version','credential_version','config_version','membership_version','account','tenant','role']:
            with self.subTest(kind=kind):
                self.state=self.folder/('sqlite-'+kind)
                self.d.scope,self.d.target=copy.deepcopy(SCOPE),copy.deepcopy(TARGET)
                self.d.enrollment={}
                self.d.stage=''
                self.d.blocked.clear();self.d.release.clear()
                first,st,binding,last,initial=self.seed()
                self.d.stage='blocked'
                first.tick(first.inventory()['nextDueMs'])
                self.assertTrue(self.d.blocked.wait(4))
                running=wait(lambda:(p if (p:=first.inventory().get('latestRound')) and p['state']=='running' and p['items'][0].get('jobId') else None))
                old_id=running['items'][0]['jobId']
                first.stop(crash=True)
                self.d.release.set()
                body=dict(scope=copy.deepcopy(self.d.scope),targets=[copy.deepcopy(self.d.target)],version=1,adapter='layso_billing_web_v1',web=copy.deepcopy(WEB))
                if kind=='boot':
                    self.d.scope['boot_epoch']='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
                    body['scope']=copy.deepcopy(self.d.scope)
                elif kind.endswith('_version'):
                    self.d.target[kind]+=1
                    body['targets']=[copy.deepcopy(self.d.target)]
                else:
                    field={'account':'accountId','tenant':'tenantId','role':'role'}[kind]
                    body['web'][field]='synthetic-new-'+kind
                self.d.stage=''
                second=self.manager('restarted-'+kind)
                if kind=='boot' or kind.endswith('_version'):
                    request(second.api+'/usage-service/official-balance?job='+old_id,expected=409)
                    request(second.api+'/usage-service/official-balance',dict(**binding,version=1),'POST',409)
                current=self.configure(second,body)
                self.assertEqual(current['version'],2)
                self.assertFalse(current['managerDirectQuery'])
                self.assertNotIn('lastSuccess',current)
                request(second.api+'/usage-service/official-balance?job='+old_id,expected=409)
                new_binding=dict(scope=current['scope'],targets=current['targets'])
                request(second.api+'/usage-service/official-balance',dict(**new_binding,version=1),'POST',409)
                wait(lambda:(p:=second.inventory().get('latestRound')) and p['state']!='running')
                self.d.enroll(current)
                second.tick(self.d.now+60_000)
                fresh=wait(lambda:(x if (x:=second.status(new_binding)).get('lastSuccess') else None))
                self.assertNotEqual(fresh['lastSuccess']['queryId'],old_id)
                self.assertNotEqual(fresh['lastSuccess'],last)
                results.append(dict(kind=kind,oldJob=old_id,newQuery=fresh['lastSuccess']['queryId'],pass_=True))
                second.stop()
        dump(self.folder/'authority-subcases.json',results)


    def test_run_initialization_failure_still_drains_later_manual_host(self):
        first, st, binding, last, _ = self.seed()
        first.stop()
        # Input preparation in an owned temporary data Assertions still use
        # real main and its public API; no internal code or lifecycle is replaced.
        with sqlite3.connect(self.state / 'usage.sqlite') as db:
            db.execute('INSERT INTO full_refresh_runs(id,state,document) VALUES(?,?,?)',
                       ('controlled-malformed-recovery-input', 'running', '{invalid'))
        second = self.manager('initialization-failed')
        wait(lambda: '[full-refresh] background lifecycle unavailable:' in
                  (second.folder / 'stderr.log').read_text(encoding='utf-8'))
        with sqlite3.connect(self.state / 'usage.sqlite') as db:
            db.execute('DELETE FROM full_refresh_runs WHERE id=?', ('controlled-malformed-recovery-input',))
        self.d.stage, self.d.stop_delay = 'blocked', .7
        job = request(second.api + '/usage-service/official-balance',
                           dict(**binding, version=st['version']), 'POST', 202)
        self.assertTrue(self.d.blocked.wait(4))
        second.stop()
        self.d.release.set()
        third = self.manager('restarted')
        status = third.status(binding)
        dump(self.folder / 'restored-status.json', status)
        self.assertEqual(status['attempt'].get('errorCode'), 'WEB_CANCELLED')
        self.assertEqual(status['lastSuccess'], last)
        self.assertTrue(status['stale'])
        restored = request(third.api + '/usage-service/official-balance?job=' + job['id'])
        self.assertEqual(restored['state'], 'complete')
        third.stop()

    def test_slow_core_finalization_reports_bounded_shutdown(self):
        first, st, binding, last, _ = self.seed()
        self.d.stage = 'blocked'
        first.tick(first.inventory()['nextDueMs'])
        self.assertTrue(self.d.blocked.wait(4))
        running = wait(lambda: (p if (p := first.inventory().get('latestRound')) and
                                    p['state'] == 'running' and p['items'][0].get('jobId') else None))
        core_blocked, release_core = threading.Event(), threading.Event()
        self.addCleanup(release_core.set)
        original_handler = self.d.server.RequestHandlerClass

        class SlowCore(original_handler):
            def do_GET(handler):
                if handler.path == '/v8/management/resource-capabilities':
                    core_blocked.set()
                    release_core.wait(25)
                super().do_GET()

        self.d.server.RequestHandlerClass = SlowCore
        started = time.monotonic()
        try:
            first.stop()
            elapsed = time.monotonic() - started
            self.assertTrue(core_blocked.is_set())
            self.assertGreaterEqual(elapsed, 9)
            self.assertLess(elapsed, 14.5)
            log = (first.folder / 'stderr.log').read_text(encoding='utf-8')
            self.assertIn('shutdown full-refresh: context deadline exceeded', log)
            self.assertIn('continuing bounded process shutdown', log)
            dump(self.folder / 'shutdown-boundary.json', dict(seconds=elapsed,
                exitCode=first.record['exitCode'], coreFinalizationBlocked=True,
                allHostsJoined=False, terminalCancellationDurable=False,
                expectedDiagnostic='shutdown full-refresh: context deadline exceeded'))
        finally:
            self.d.server.RequestHandlerClass = original_handler
            release_core.set()
            self.d.release.set()
        self.d.stage = 'closed'
        second = self.manager('restarted')
        wait(lambda: (p := second.inventory().get('latestRound')) and p['state'] != 'running')
        status = second.status(binding)
        self.assertEqual(status['lastSuccess'], last)
        self.assertTrue(status['stale'])
        old = request(second.api + '/usage-service/official-balance?job=' + running['items'][0]['jobId'])
        self.assertEqual(old['state'], 'interrupted')
        dump(self.folder / 'restored-status.json', status)
        second.stop()


class RecordedResult(unittest.TextTestResult):
    def __init__(self,*args,**kwargs):
        super().__init__(*args,**kwargs)
        self.events=[]
    def addSuccess(self,test):
        super().addSuccess(test);self.events.append(dict(test=str(test),action='pass'))
    def addFailure(self,test,err):
        super().addFailure(test,err);self.events.append(dict(test=str(test),action='fail',error=self._exc_info_to_string(err,test)))
    def addError(self,test,err):
        super().addError(test,err);self.events.append(dict(test=str(test),action='error',error=self._exc_info_to_string(err,test)))
    def addSubTest(self,test,subtest,err):
        super().addSubTest(test,subtest,err)
        self.events.append(dict(test=str(test),subtest=str(subtest),action='pass_subcase' if err is None else 'fail_subcase',error=None if err is None else self._exc_info_to_string(err,test)))


def build(tag,output):
    command=[str(GO),'build','-mod=readonly','-trimpath','-buildvcs=false']
    if tag:command+=['-tags',tag]
    command+=['-ldflags','-X github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo.SourceCommit='+HEAD,'-o',str(output),'./cmd/cpa-manager-plus']
    before=GO_INPUTS.source_fingerprint()
    stamp=datetime.now(timezone.utc).isoformat();start=time.monotonic()
    with output.with_suffix('.build.stdout.log').open('wb') as stdout,output.with_suffix('.build.stderr.log').open('wb') as stderr:
        run=subprocess.run(command,cwd=SOURCE/'apps/manager-server',env=BUILD_ENV,stdout=stdout,stderr=stderr)
    after=GO_INPUTS.source_fingerprint()
    manifest=dict(startedAt=stamp,seconds=round(time.monotonic()-start,3),command=command,exitCode=run.returncode,sourceCommit=HEAD,dirty=bool(subprocess.check_output(['git','status','--porcelain'],cwd=SOURCE)),sourceBefore=before,sourceAfter=after,sourceUnchanged=before==after,buildTags=tag,overlays=False,testBinary=False,artifactSHA256=sha(output) if output.exists() else None)
    dump(output.with_suffix('.BUILD.json'),manifest)
    assert run.returncode==0 and before==after
    modules=subprocess.run([str(GO),'version','-m',str(output)],env=BUILD_ENV,capture_output=True)
    output.with_suffix('.modules.stdout.log').write_bytes(modules.stdout)
    output.with_suffix('.modules.stderr.log').write_bytes(modules.stderr)
    assert modules.returncode==0
    return manifest


if __name__=='__main__':
    OUT.mkdir(exist_ok=False)
    GO_INPUTS=types.SimpleNamespace(source_fingerprint=source_fingerprint)
    full_inputs=types.SimpleNamespace(snapshot=snapshot)
    (OUT/'RUNNER.py').write_bytes(Path(__file__).read_bytes())
    temp=OUT/'temp'
    temp.mkdir()
    HEAD=subprocess.check_output(['git','rev-parse','HEAD'],cwd=SOURCE,text=True).strip()
    BUILD_ENV=os.environ.copy()
    BUILD_ENV.update(GOROOT=str(GO.parent.parent),GOTOOLCHAIN='local',GOWORK='off',GOENV='off',GOFLAGS='',GOCACHE=str(Path(os.environ.get('CPAMP_ENTRY_GOCACHE') or OUT/'go-cache').resolve()),GOPROXY='off',GOSUMDB='off',GOTELEMETRY='off',TEMP=str(temp),TMP=str(temp))
    before=full_inputs.snapshot()
    FIXTURE_ARTIFACT=OUT/'cpa-manager-entry-fixture.exe'
    PRODUCTION_ARTIFACT=OUT/'cpa-manager-ordinary.exe'
    builds=[build('cpa_entry_fixture',FIXTURE_ARTIFACT),build('',PRODUCTION_ARTIFACT)]
    names=sys.argv[2:]
    suite=unittest.TestLoader().loadTestsFromNames(names,EntryAcceptance) if names else unittest.TestLoader().loadTestsFromTestCase(EntryAcceptance)
    with (OUT/'tests.stderr.log').open('w',encoding='utf-8') as stream:
        result=unittest.TextTestRunner(stream=stream,verbosity=2,resultclass=RecordedResult).run(suite)
    after=full_inputs.snapshot()
    processes=[json.loads(p.read_text(encoding='utf-8')) for p in OUT.rglob('PROCESS.json')]
    dump(OUT/'RESULT.json',dict(success=result.wasSuccessful(),testsRun=result.testsRun,failures=len(result.failures),errors=len(result.errors),skips=len(result.skipped),events=result.events,builds=builds,sourceBefore=before,sourceAfter=after,sourceUnchanged=before==after,processes=processes,fullMain=True,realBrowser=False,externalNetwork=False,realDeployment=False))
    print(json.dumps(dict(success=result.wasSuccessful(),testsRun=result.testsRun,failures=len(result.failures),errors=len(result.errors),events=[{k:v for k,v in e.items() if k!='error'} for e in result.events],processes=len(processes),sourceSHA256=after['sha256']),indent=2))
    sys.exit(0 if result.wasSuccessful() and before==after else 1)
