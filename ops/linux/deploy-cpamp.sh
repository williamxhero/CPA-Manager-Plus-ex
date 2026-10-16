#!/usr/bin/env bash
# Agent-less Linux Manager deployment (#62).
# --artifact-dir DIR [--host SSH] [--root DIR] [--service NAME] [--version V]
# [--dry-run] [--rollback-only] [--result-path FILE] [--isolated-port PORT]
# [--health-url URL] [--health-timeout SECONDS]
# Exit: 0 deployed/dry-run/explicit rollback, 2 preflight/rehearsal failed,
#       3 failed deployment restored, 4 rollback failed, 5 result write failed.
set -Eeuo pipefail
umask 077
# ERR traps can run inside a silenced SSH function; keep sanitized outcomes
# on the original output stream, independently of command redirections.
exec 3>&1
HOST=yosef-server ROOT=/data/cpa/CPA-Manager SERVICE=cpa-manager.service
HEALTH_URL=http://127.0.0.1:18317/health
ARTIFACT_DIR="" VERSION="" RESULT_PATH=""
DRY_RUN=0 ROLLBACK_ONLY=0 ISOLATED_PORT=19317 TIMEOUT_SECONDS=90
while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="$2"; shift 2;;
    --root) ROOT="$2"; shift 2;;
    --service) SERVICE="$2"; shift 2;;
    --health-url) HEALTH_URL="$2"; shift 2;;
    --artifact-dir) ARTIFACT_DIR="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --isolated-port) ISOLATED_PORT="$2"; shift 2;;
    --health-timeout) TIMEOUT_SECONDS="$2"; shift 2;;
    --result-path) RESULT_PATH="$2"; shift 2;;
    --dry-run) DRY_RUN=1; shift;;
    --rollback-only) ROLLBACK_ONLY=1; shift;;
    -h|--help) sed -n '2,7p' "$0"; exit 0;;
    *) printf 'Unknown argument\n' >&2; exit 2;;
  esac
done
[[ "$ROOT" == /* && "$ISOLATED_PORT" =~ ^[0-9]+$ && "$TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || exit 2
[[ "$ISOLATED_PORT" -gt 1024 && "$ISOLATED_PORT" -lt 65536 && "$ISOLATED_PORT" != 8000 && "$ISOLATED_PORT" != 8080 && "$TIMEOUT_SECONDS" -gt 0 ]] || exit 2
VERSION=${VERSION:-$(date -u +%Y%m%d%H%M%S)}
[[ "$VERSION" =~ ^[A-Za-z0-9._-]+$ && "$HOST" != -* ]] || exit 2
RELEASE="cpa-manager-plus_v${VERSION}_linux_amd64"
PANEL_REL="v${VERSION}"
STAMP="$(date -u +%Y%m%d-%H%M%S-%N)-$$"
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ) STARTED_EPOCH=$(date +%s)
BACKUP_DIR="$ROOT/deploy-backups/$STAMP"
PHASE=preflight FAIL_PHASE="" MUTATED=0 BACKUP_READY=0 REHEARSAL_DIR="" LOCKED=0
CURRENT_APP="" CURRENT_PANEL="" BIN_SHA="" PANEL_SHA=""
STEPS=() HEALTH=()
q() { printf '%q' "$1"; }
rssh() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "bash -c $(q "set -euo pipefail; $1")"; }
sha() { sha256sum "$1" | cut -d' ' -f1; }
step() { PHASE="$1"; STEPS+=("$1"); printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$1" >&3; }
finish() {
  local status="$1" code="$2" after_app after_panel
  after_app=$(rssh "readlink -f $(q "$ROOT/app/current")" 2>/dev/null || true)
  after_panel=$(rssh "readlink -f $(q "$ROOT/panel/current")" 2>/dev/null || true)
  if [[ -n "$RESULT_PATH" ]]; then
    if ! python3 - "$RESULT_PATH" "$status" "$STARTED" "$STARTED_EPOCH" "$HOST" "$SERVICE" "$VERSION" "$DRY_RUN" "$BACKUP_DIR" "$CURRENT_APP" "$CURRENT_PANEL" "$after_app" "$after_panel" "$BIN_SHA" "$PANEL_SHA" "$RELEASE" "${STEPS[*]}" "${HEALTH[*]}" "$FAIL_PHASE" <<'PY'
import datetime, json, os, sys, time
(p, status, started, epoch, host, service, version, dry, backup, before_app, before_panel,
 after_app, after_panel, binary, panel, release, steps, health, failed) = sys.argv[1:]
result = dict(startedAt=started, finishedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
              durationSeconds=int(time.time())-int(epoch), target=host, serviceName=service,
              status=status, version=version, dryRun=dry=="1", backup=backup, failurePhase=failed,
              before=dict(app=before_app, panel=before_panel), after=dict(app=after_app, panel=after_panel),
              artifact=dict(exeSha256=binary, panelSha256=panel, release=release),
              steps=[dict(name=s, status="failed" if s==failed else "ok") for s in steps.split()],
              health=[dict(check=s, ok=True) for s in health.split()])
if failed == "health": result["health"].append(dict(check="deployed", ok=False))
if status == "rollback_failed": result["health"].append(dict(check="restored", ok=False))
try:
    with open(p+".tmp", "w") as f: json.dump(result, f); f.write("\n")
    os.replace(p+".tmp", p)
except OSError:
    sys.exit(1)
PY
    then printf 'RESULT: result_write_failed\n' >&3; exit 5; fi
  fi
  printf 'RESULT: %s\n' "$status" >&3
  exit "$code"
}
cleanup() {
  # Only paths created and owned by this run. Never kill by pattern or port.
  if [[ -n "$REHEARSAL_DIR" ]]; then rssh "rm -rf -- $(q "$REHEARSAL_DIR")" >/dev/null 2>&1 || true; fi
  if [[ "$LOCKED" == 1 ]]; then rssh "rmdir -- $(q "$ROOT/.deploy-lock")" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
restart_service() { rssh "export XDG_RUNTIME_DIR=/run/user/\$(id -u); systemctl --user restart $(q "$SERVICE")" >/dev/null 2>&1; }
remote_health() {
  local binary="$1" deadline=$((SECONDS + TIMEOUT_SECONDS))
  while (( SECONDS < deadline )); do
    if rssh "export XDG_RUNTIME_DIR=/run/user/\$(id -u); pid=\$(systemctl --user show $(q "$SERVICE") --property MainPID --value); systemctl --user is-active --quiet $(q "$SERVICE"); $CHECK_RUN health --pid \"\$pid\" --binary $(q "$binary") --url $(q "$HEALTH_URL") --marker cpa-manager-plus" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  return 1
}
restore_files() {
  rssh "test -f $(q "$BACKUP_DIR/complete");
    app=\$(cat $(q "$BACKUP_DIR/app_target.txt")); panel=\$(cat $(q "$BACKUP_DIR/panel_target.txt"));
    test -n \"\$app\"; test -n \"\$panel\";
    diff -qr $(q "$BACKUP_DIR/app") \"\$app\" >/dev/null;
    diff -qr $(q "$BACKUP_DIR/panel") \"\$panel\" >/dev/null;
    ln -sfn \"\$app\" $(q "$ROOT/app/.current-$STAMP"); mv -Tf $(q "$ROOT/app/.current-$STAMP") $(q "$ROOT/app/current");
    ln -sfn \"\$panel\" $(q "$ROOT/panel/.current-$STAMP"); mv -Tf $(q "$ROOT/panel/.current-$STAMP") $(q "$ROOT/panel/current");
    if test -f $(q "$BACKUP_DIR/release-present"); then
      cp -a $(q "$BACKUP_DIR/current_release.txt") $(q "$ROOT/.release-$STAMP");
      mv -Tf $(q "$ROOT/.release-$STAMP") $(q "$ROOT/current_release.txt");
      cmp -s $(q "$BACKUP_DIR/current_release.txt") $(q "$ROOT/current_release.txt");
    else rm -f -- $(q "$ROOT/current_release.txt"); fi;
    test \"\$(readlink -f $(q "$ROOT/app/current"))\" = \"\$app\";
    test \"\$(readlink -f $(q "$ROOT/panel/current"))\" = \"\$panel\"" >/dev/null 2>&1
}
rollback() {
  local old
  restore_files &&
    old=$(rssh "cat $(q "$BACKUP_DIR/app_target.txt")" 2>/dev/null) &&
    restart_service && remote_health "$old/cpa-manager-plus"
}
failed() {
  trap - ERR INT TERM
  FAIL_PHASE="$PHASE"
  printf 'FAILED: %s\n' "$PHASE" >&3
  if [[ "$ROLLBACK_ONLY" == 1 ]]; then finish rollback_failed 4; fi
  if [[ "$BACKUP_READY" == 1 && "$MUTATED" == 1 ]]; then
    step rollback
    if rollback; then HEALTH+=(restored); finish rolled_back 3; fi
    finish rollback_failed 4
  fi
  finish preflight_failed 2
}
trap failed ERR INT TERM

step preflight
CHECKS="$(dirname "$(readlink -f "$0")")/deploy-check.py"
CHECK_CODE=$(<"$CHECKS")
CHECK_RUN="python3 -c $(q "$CHECK_CODE")"
rssh "command -v python3 >/dev/null; test -d $(q "$ROOT/app"); test -d $(q "$ROOT/panel")" >/dev/null 2>&1
if [[ "$ROLLBACK_ONLY" != 1 ]]; then
  rssh "test -L $(q "$ROOT/app/current"); test -L $(q "$ROOT/panel/current")" >/dev/null 2>&1
fi
if [[ "$DRY_RUN" != 1 ]]; then
  rssh "mkdir $(q "$ROOT/.deploy-lock")" >/dev/null 2>&1
  LOCKED=1
fi
CURRENT_APP=$(rssh "readlink -f $(q "$ROOT/app/current")" 2>/dev/null || true)
CURRENT_PANEL=$(rssh "readlink -f $(q "$ROOT/panel/current")" 2>/dev/null || true)
if [[ "$ROLLBACK_ONLY" != 1 ]]; then
  rssh "test -x $(q "$CURRENT_APP/cpa-manager-plus"); test -f $(q "$CURRENT_PANEL/management.html")" >/dev/null 2>&1
fi
if [[ "$ROLLBACK_ONLY" == 1 ]]; then
  [[ "$DRY_RUN" != 1 ]]
  # Only completed backups are eligible; an interrupted backup is never latest.
  BACKUP_DIR=$(rssh "find $(q "$ROOT/deploy-backups") -mindepth 2 -maxdepth 2 -type f -name complete -printf '%h\\n' | sort | tail -1" 2>/dev/null)
  [[ -n "$BACKUP_DIR" ]]
  step rollback
  rollback
  HEALTH+=(restored)
  finish rolled_back 0
fi
[[ -n "$ARTIFACT_DIR" ]]
BIN="$ARTIFACT_DIR/cpa-manager-plus" PANEL="$ARTIFACT_DIR/management.html"
[[ -f "$BIN" && -f "$PANEL" ]]
BIN_SHA=$(sha "$BIN") PANEL_SHA=$(sha "$PANEL")
# Never overwrite an executable/panel directory that may belong to a running
# or recoverable release. Callers must use a fresh version label.
rssh "test ! -e $(q "$ROOT/app/$RELEASE"); test ! -e $(q "$ROOT/panel/$PANEL_REL"); test -w $(q "$ROOT")" >/dev/null 2>&1

step rehearsal
REHEARSAL_DIR=$(rssh 'mktemp -d /tmp/cpamp-rehearsal.XXXXXXXX' 2>/dev/null)
[[ "$REHEARSAL_DIR" == /tmp/cpamp-rehearsal.* && "$REHEARSAL_DIR" != *$'\n'* ]]
scp -q "$BIN" "$HOST:$REHEARSAL_DIR/cpa-manager-plus" >/dev/null 2>&1
rssh "chmod +x $(q "$REHEARSAL_DIR/cpa-manager-plus");
  cd $(q "$REHEARSAL_DIR");
  HTTP_ADDR=127.0.0.1:$ISOLATED_PORT USAGE_DATA_DIR=$(q "$REHEARSAL_DIR/data") ./cpa-manager-plus >out.log 2>&1 &
  pid=\$!;
  trap 'kill \"\$pid\" 2>/dev/null || true; wait \"\$pid\" 2>/dev/null || true' EXIT;
  deadline=\$((SECONDS + $TIMEOUT_SECONDS));
  while (( SECONDS < deadline )); do
    if $CHECK_RUN health --pid \"\$pid\" --binary $(q "$REHEARSAL_DIR/cpa-manager-plus") --url http://127.0.0.1:$ISOLATED_PORT/health --marker cpa-manager-plus; then exit 0; fi;
    kill -0 \"\$pid\" 2>/dev/null || exit 1;
    sleep 0.2;
  done; exit 1" >/dev/null 2>&1
HEALTH+=(rehearsal)
rssh "rm -rf -- $(q "$REHEARSAL_DIR")" >/dev/null 2>&1
REHEARSAL_DIR=""
if [[ "$DRY_RUN" == 1 ]]; then finish deployed 0; fi

step backup
rssh "umask 077; mkdir -p $(q "$BACKUP_DIR/app") $(q "$BACKUP_DIR/panel");
  cp -a $(q "$CURRENT_APP/.") $(q "$BACKUP_DIR/app/");
  cp -a $(q "$CURRENT_PANEL/.") $(q "$BACKUP_DIR/panel/");
  printf '%s\\n' $(q "$CURRENT_APP") > $(q "$BACKUP_DIR/app_target.txt");
  printf '%s\\n' $(q "$CURRENT_PANEL") > $(q "$BACKUP_DIR/panel_target.txt");
  if test -e $(q "$ROOT/current_release.txt"); then
    cp -a $(q "$ROOT/current_release.txt") $(q "$BACKUP_DIR/current_release.txt"); touch $(q "$BACKUP_DIR/release-present"); fi;
  diff -qr $(q "$CURRENT_APP") $(q "$BACKUP_DIR/app") >/dev/null;
  diff -qr $(q "$CURRENT_PANEL") $(q "$BACKUP_DIR/panel") >/dev/null;
  touch $(q "$BACKUP_DIR/complete")" >/dev/null 2>&1
BACKUP_READY=1

step replace
MUTATED=1
rssh "mkdir $(q "$ROOT/app/$RELEASE") $(q "$ROOT/panel/$PANEL_REL")" >/dev/null 2>&1
scp -q "$BIN" "$HOST:$ROOT/app/$RELEASE/cpa-manager-plus" >/dev/null 2>&1
scp -q "$PANEL" "$HOST:$ROOT/panel/$PANEL_REL/management.html" >/dev/null 2>&1
rssh "chmod +x $(q "$ROOT/app/$RELEASE/cpa-manager-plus");
  test \"\$(sha256sum $(q "$ROOT/app/$RELEASE/cpa-manager-plus") | cut -d' ' -f1)\" = $(q "$BIN_SHA");
  test \"\$(sha256sum $(q "$ROOT/panel/$PANEL_REL/management.html") | cut -d' ' -f1)\" = $(q "$PANEL_SHA")" >/dev/null 2>&1
step switch
rssh "ln -s $(q "$ROOT/app/$RELEASE") $(q "$ROOT/app/.current-$STAMP"); mv -Tf $(q "$ROOT/app/.current-$STAMP") $(q "$ROOT/app/current");
  ln -s $(q "$ROOT/panel/$PANEL_REL") $(q "$ROOT/panel/.current-$STAMP"); mv -Tf $(q "$ROOT/panel/.current-$STAMP") $(q "$ROOT/panel/current");
  printf '%s\\n' $(q "$VERSION") > $(q "$ROOT/.release-$STAMP"); mv -Tf $(q "$ROOT/.release-$STAMP") $(q "$ROOT/current_release.txt")" >/dev/null 2>&1
step restart
restart_service
step health
remote_health "$ROOT/app/$RELEASE/cpa-manager-plus"
HEALTH+=(deployed)
finish deployed 0
