#!/usr/bin/env bash
# CLIProxyAPI core deployment (#62/#65). Defaults remain yosef-server's layout.
# -DryRun/--dry-run: isolated startup only; no persistent deployment changes.
# -NoRestart/--no-restart: install, explicitly without claiming runtime health.
# Isolation: --root DIR --service NAME --health-url URL --isolated-port PORT
#            --oauth-dir DIR --quota-command FILE --health-timeout SECONDS
#            --config FILE --version RELEASE --result-path FILE
# Exit: 0 deployed/installed, 2 preflight/rehearsal failed, 3 rolled_back,
#       4 rollback_failed, 5 result_write_failed. Requires bash, python3, flock.
set -Eeuo pipefail
umask 077
ROOT=/data/cpa/CLIProxyAPI
SVC=cpa-cliproxyapi.service
NEWVER=CLIProxyAPI_8.0.15_linux_amd64
NEWCFG="" OAUTH="" QUOTA="" RESULT=""
HEALTH_URL=http://127.0.0.1:8317/v1/models
PORT=19318 TIMEOUT=60 DRYRUN=0 NORESTART=0
CHECKS="$(dirname "$(readlink -f "$0")")/deploy-check.py"
while [[ $# -gt 0 ]]; do
  case "$1" in
    -DryRun|--dry-run) DRYRUN=1; shift;;
    -NoRestart|--no-restart) NORESTART=1; shift;;
    --root) ROOT="$2"; shift 2;;
    --service) SVC="$2"; shift 2;;
    --version) NEWVER="$2"; shift 2;;
    --config) NEWCFG="$2"; shift 2;;
    --oauth-dir) OAUTH="$2"; shift 2;;
    --quota-command) QUOTA="$2"; shift 2;;
    --result-path) RESULT="$2"; shift 2;;
    --health-url) HEALTH_URL="$2"; shift 2;;
    --isolated-port) PORT="$2"; shift 2;;
    --health-timeout) TIMEOUT="$2"; shift 2;;
    -h|--help) sed -n '2,10p' "$0"; exit 0;;
    *) printf 'Unknown argument\n' >&2; exit 2;;
  esac
done
[[ "$ROOT" == /* && "$NEWVER" =~ ^[A-Za-z0-9._-]+$ && "$PORT" =~ ^[0-9]+$ && "$TIMEOUT" =~ ^[0-9]+$ ]] || exit 2
[[ "$PORT" -gt 1024 && "$PORT" -lt 65536 && "$PORT" != 8000 && "$PORT" != 8080 && "$TIMEOUT" -gt 0 ]] || exit 2
NEWCFG=${NEWCFG:-$ROOT/config.yaml.final}
OAUTH=${OAUTH:-$(dirname "$ROOT")/oauth}
QUOTA=${QUOTA:-$(dirname "$ROOT")/bin/bailian-quota}
STAMP="$(date -u +%Y%m%d-%H%M%S)-$$"
BKDIR="$ROOT/deploy-backups/$STAMP"
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
START_EPOCH=$(date +%s)
PHASE=preflight FAIL_PHASE="" MUTATED=0 BACKUP_READY=0 SANDBOX="" CHILD="" CURRENT=""
STEPS=() HEALTH=()
step() { PHASE="$1"; STEPS+=("$1"); printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$1"; }
sha() { sha256sum "$1" | cut -d' ' -f1; }
finish() {
  local status="$1" code="$2"
  if [[ -n "$RESULT" ]]; then
    if ! python3 - "$RESULT" "$status" "$STARTED" "$START_EPOCH" "$SVC" "$NEWVER" "$DRYRUN" "$BKDIR" "$CURRENT" "$(readlink -f "$ROOT/app/current" 2>/dev/null || true)" "${STEPS[*]}" "${HEALTH[*]}" "$FAIL_PHASE" "$STAMP" "$(sha "$ROOT/config.yaml" 2>/dev/null || true)" <<'PY'
import datetime, json, os, sys, time
p, status, started, epoch, service, version, dry, backup, before, after, steps, health, failed, stamp, config_sha = sys.argv[1:]
result = dict(startedAt=started, finishedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
              durationSeconds=int(time.time())-int(epoch), target="core", serviceName=service,
              status=status, version=version, dryRun=dry=="1", backup=backup,
              failurePhase=failed, stamp=stamp, configSha=config_sha,
              before=dict(app=before), after=dict(app=after),
              steps=[dict(name=s, status="failed" if s==failed else "ok") for s in steps.split()],
              health=[dict(check=s, ok=True) for s in health.split()])
if failed == "health": result["health"].append(dict(check="deployed", ok=False))
if status == "rollback_failed": result["health"].append(dict(check="restored", ok=False))
try:
    with open(p + ".tmp", "w") as f: json.dump(result, f); f.write("\n")
    os.replace(p + ".tmp", p)
except OSError:
    sys.exit(1)
PY
    then printf 'RESULT: result_write_failed\n'; exit 5; fi
  fi
  printf 'RESULT: %s\n' "$status"
  exit "$code"
}
cleanup() {
  if [[ -n "$CHILD" ]]; then kill "$CHILD" 2>/dev/null || true; wait "$CHILD" 2>/dev/null || true; fi
  [[ -z "$SANDBOX" ]] || rm -rf -- "$SANDBOX"
}
trap cleanup EXIT
atomic_pointer() {
  ln -sfn "$1" "$ROOT/app/.current-$STAMP" && mv -Tf "$ROOT/app/.current-$STAMP" "$ROOT/app/current"
}
service_health() {
  local binary="$1" pid deadline=$((SECONDS + TIMEOUT))
  while (( SECONDS < deadline )); do
    pid=$(systemctl --user show "$SVC" --property MainPID --value 2>/dev/null) || pid=0
    if systemctl --user is-active --quiet "$SVC" 2>/dev/null &&
       python3 "$CHECKS" health --pid "${pid:-0}" --binary "$binary" --url "$HEALTH_URL" 2>/dev/null; then return 0; fi
    sleep 0.2
  done
  return 1
}
rollback() {
  local old
  old=$(<"$BKDIR/app-target.txt")
  [[ -n "$old" && -x "$old/cli-proxy-api" ]] &&
    diff -qr "$BKDIR/release" "$old" >/dev/null &&
    cp -a "$BKDIR/config.yaml.old" "$ROOT/.config-restore-$STAMP" &&
    mv -Tf "$ROOT/.config-restore-$STAMP" "$ROOT/config.yaml" &&
    cp -a "$BKDIR/plugins" "$ROOT/.plugins-restore-$STAMP" &&
    mv -T "$ROOT/plugins" "$ROOT/.plugins-failed-$STAMP" &&
    mv -T "$ROOT/.plugins-restore-$STAMP" "$ROOT/plugins" &&
    atomic_pointer "$old" &&
    cmp -s "$BKDIR/config.yaml.old" "$ROOT/config.yaml" &&
    diff -qr "$BKDIR/plugins" "$ROOT/plugins" >/dev/null &&
    [[ "$(readlink -f "$ROOT/app/current")" == "$old" ]] &&
    systemctl --user restart "$SVC" >/dev/null 2>&1 &&
    service_health "$old/cli-proxy-api"
}
failed() {
  trap - ERR INT TERM
  FAIL_PHASE="$PHASE"
  printf 'FAILED: %s\n' "$PHASE"
  if [[ "$BACKUP_READY" == 1 && "$MUTATED" == 1 ]]; then
    step rollback
    if rollback; then HEALTH+=(restored); finish rolled_back 3; fi
    finish rollback_failed 4
  fi
  finish preflight_failed 2
}
trap failed ERR INT TERM

# Command diagnostics may contain configuration fragments or credential paths.
# The phase and result JSON are the public, sanitized diagnostic interface.
exec 2>/dev/null
step preflight
[[ -x "$ROOT/app/$NEWVER/cli-proxy-api" && -f "$NEWCFG" && -f "$ROOT/config.yaml" && ! -L "$ROOT/config.yaml" ]]
[[ -L "$ROOT/app/current" && -d "$OAUTH" && -x "$QUOTA" && -f "$CHECKS" ]]
for so in qwen-cliproxyapi.so opencode-go-cliproxyapi.so; do [[ -f "$ROOT/plugins/linux/amd64/$so" ]]; done
# cp -a must produce independent bytes, never aliases to a live plugin tree.
[[ ! -L "$ROOT/plugins" && -z "$(find "$ROOT/plugins" -type l -print -quit)" ]]
[[ -w "$ROOT" ]]
# Lock before observing pointers; the backup must describe the locked state.
# The existing directory inode avoids a lockfile side effect during dry-run.
exec {LOCK_FD}<"$ROOT"
flock -n "$LOCK_FD"
CURRENT=$(readlink -f "$ROOT/app/current")
[[ -x "$CURRENT/cli-proxy-api" ]]

step rehearsal
SANDBOX=$(mktemp -d "${TMPDIR:-/tmp}/cpa-core-rehearsal.XXXXXXXX")
cp -a "$ROOT/plugins" "$SANDBOX/plugins"
python3 "$CHECKS" sandbox-config "$NEWCFG" "$SANDBOX/config.yaml" "$SANDBOX" "$ROOT" "$PORT"
(
  cd "$SANDBOX"
  exec "$ROOT/app/$NEWVER/cli-proxy-api" --config "$SANDBOX/config.yaml"
) >"$SANDBOX/startup.log" 2>&1 &
CHILD=$!
REHEARSED=0
deadline=$((SECONDS + TIMEOUT))
while (( SECONDS < deadline )); do
  if python3 "$CHECKS" health --pid "$CHILD" --binary "$ROOT/app/$NEWVER/cli-proxy-api" --url "http://127.0.0.1:$PORT/v1/models"; then REHEARSED=1; break; fi
  kill -0 "$CHILD" 2>/dev/null || break
  sleep 0.2
done
[[ "$REHEARSED" == 1 ]]
kill "$CHILD"; wait "$CHILD" 2>/dev/null || true
CHILD=""
HEALTH+=(rehearsal)
if [[ "$DRYRUN" == 1 ]]; then finish deployed 0; fi

step backup
mkdir -p "$BKDIR"
printf '%s\n' "$CURRENT" > "$BKDIR/app-target.txt"
cp -a "$CURRENT" "$BKDIR/release"
cp -a "$ROOT/config.yaml" "$BKDIR/config.yaml.old"
cp -a "$ROOT/plugins" "$BKDIR/plugins"
cmp -s "$ROOT/config.yaml" "$BKDIR/config.yaml.old"
diff -qr "$CURRENT" "$BKDIR/release" >/dev/null
diff -qr "$ROOT/plugins" "$BKDIR/plugins" >/dev/null
BACKUP_READY=1
[[ -n "$RESULT" ]] || RESULT="$BKDIR/result.json"

step install_config
MUTATED=1
cp -a "$NEWCFG" "$ROOT/.config-new-$STAMP"
mv -Tf "$ROOT/.config-new-$STAMP" "$ROOT/config.yaml"
cmp -s "$NEWCFG" "$ROOT/config.yaml"
step switch
atomic_pointer "$ROOT/app/$NEWVER"
[[ "$(readlink -f "$ROOT/app/current")" == "$ROOT/app/$NEWVER" ]]
if [[ "$NORESTART" == 1 ]]; then finish installed_not_restarted 0; fi
step restart
systemctl --user restart "$SVC" >/dev/null 2>&1
step health
service_health "$ROOT/app/$NEWVER/cli-proxy-api"
HEALTH+=(deployed)
finish deployed 0
