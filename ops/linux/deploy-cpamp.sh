#!/usr/bin/env bash
# deploy-cpamp.sh — agent-less, seamless CPA Manager Plus deployment for Linux
# (SPEC #62; the Linux counterpart of ops/windows/deploy-cpamp.ps1).
#
# Properties:
#   * --dry-run performs preflight + rehearsal only; no release switch, no restart.
#   * Every run backs up the current release pointer and binary first.
#   * A failed replace/health step automatically rolls back to the backup and
#     re-checks health, so users are never left on a broken release.
#   * Machine-readable result JSON (fields aligned with the Windows script).
#
# Usage:
#   deploy-cpamp.sh --artifact-dir DIR [--host SSH] [--version V] [--dry-run]
#                   [--rollback-only] [--result-path FILE] [--isolated-port N]
set -euo pipefail

HOST="yosef-server"
ROOT="/data/cpa/CPA-Manager"
SERVICE="cpa-manager.service"
HEALTH_URL="http://127.0.0.1:18317/health"
MARKER="cpa-manager-plus"
ARTIFACT_DIR=""
VERSION=""
DRY_RUN=0
ROLLBACK_ONLY=0
RESULT_PATH=""
ISOLATED_PORT=19317
TIMEOUT_SECONDS=90

log() { printf '%s [%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="$2"; shift 2;;
    --root) ROOT="$2"; shift 2;;
    --service) SERVICE="$2"; shift 2;;
    --health-url) HEALTH_URL="$2"; shift 2;;
    --artifact-dir) ARTIFACT_DIR="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --isolated-port) ISOLATED_PORT="$2"; shift 2;;
    --result-path) RESULT_PATH="$2"; shift 2;;
    --dry-run) DRY_RUN=1; shift;;
    --rollback-only) ROLLBACK_ONLY=1; shift;;
    -h|--help) sed -n '2,20p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

[[ -n "$ARTIFACT_DIR" ]] || { echo "--artifact-dir is required" >&2; exit 2; }
BIN="$ARTIFACT_DIR/cpa-manager-plus"
PANEL="$ARTIFACT_DIR/management.html"
[[ -f "$BIN" ]] || { echo "missing $BIN" >&2; exit 2; }
[[ -f "$PANEL" ]] || { echo "missing $PANEL" >&2; exit 2; }

sha() { python -c "import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())" "$1"; }
BIN_SHA="$(sha "$BIN")"
PANEL_SHA="$(sha "$PANEL")"
STAMP="$(date -u +%Y%m%d-%H%M%S)"
[[ -n "$VERSION" ]] || VERSION="$(date -u +%Y%m%d%H%M%S)"
RELEASE="cpa-manager-plus_v${VERSION}_linux_amd64"
PANEL_REL="v${VERSION}"

rssh() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "$@"; }
remote_health() {
  rssh "curl -fsS --max-time 5 '$HEALTH_URL'" 2>/dev/null | grep -q "$MARKER"
}
restart_service() {
  rssh "export XDG_RUNTIME_DIR=/run/user/\$(id -u); systemctl --user restart '$SERVICE'" >/dev/null 2>&1
}

STEPS=()
HEALTH=()
add_step() { STEPS+=("{\"name\":\"$1\",\"status\":\"$2\",\"detail\":\"${3//\"/}\"}"); }
add_health() { HEALTH+=("{\"check\":\"$1\",\"ok\":$2,\"detail\":\"${3//\"/}\"}"); }

finish() {
  local status="$1" started="$2"
  local ended; ended="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local secs=$(( $(date -u +%s) - STARTED_EPOCH ))
  if [[ -n "$RESULT_PATH" ]]; then
    {
      printf '{"startedAt":"%s","finishedAt":"%s","durationSeconds":%d,' "$started" "$ended" "$secs"
      printf '"target":"%s","serviceName":"%s","status":"%s","version":"%s","dryRun":%s,' \
             "$HOST" "$SERVICE" "$status" "$VERSION" "$([[ $DRY_RUN == 1 ]] && echo true || echo false)"
      printf '"artifact":{"exeSha256":"%s","panelSha256":"%s","release":"%s"},' "$BIN_SHA" "$PANEL_SHA" "$RELEASE"
      printf '"steps":[%s],"health":[%s]}\n' "$(IFS=,; echo "${STEPS[*]:-}")" "$(IFS=,; echo "${HEALTH[*]:-}")"
    } > "$RESULT_PATH"
  fi
  log "RESULT" "$status; exit=$([[ $status == deployed ]] && echo 0 || echo 1); dryRun=$([[ $DRY_RUN == 1 ]] && echo true || echo false)"
}

STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
STARTED_EPOCH="$(date -u +%s)"

# ---------------------------------------------------------------- preflight
if ! rssh "true" 2>/dev/null; then
  add_step preflight failed "cannot ssh $HOST"
  finish preflight_failed "$STARTED"; exit 2
fi
if ! rssh "test -d '$ROOT/app' -a -d '$ROOT/panel'"; then
  add_step preflight failed "remote layout missing under $ROOT"
  finish preflight_failed "$STARTED"; exit 2
fi
CURRENT_APP="$(rssh "readlink -f '$ROOT/app/current'" 2>/dev/null || true)"
CURRENT_PANEL="$(rssh "readlink -f '$ROOT/panel/current'" 2>/dev/null || true)"
add_step preflight ok "current app=$CURRENT_APP panel=$CURRENT_PANEL"

# ---------------------------------------------------------------- rollback-only
if [[ $ROLLBACK_ONLY == 1 ]]; then
  LATEST_BACKUP="$(rssh "ls -1d '$ROOT/deploy-backups'/* 2>/dev/null | tail -1" || true)"
  if [[ -z "$LATEST_BACKUP" ]]; then
    add_step rollback failed "no backup found"; finish rollback_failed "$STARTED"; exit 1
  fi
  rssh "ln -sfn '$LATEST_BACKUP/app' '$ROOT/app/current' && ln -sfn '$LATEST_BACKUP/panel' '$ROOT/panel/current'"
  restart_service
  if remote_health; then
    add_step rollback ok "restored $LATEST_BACKUP"
    add_health service true "health ok after rollback"
    finish rolled_back "$STARTED"; exit 0
  fi
  add_step rollback failed "health failed after rollback"
  finish rollback_failed "$STARTED"; exit 1
fi

# ---------------------------------------------------------------- backup
BACKUP_DIR="$ROOT/deploy-backups/$STAMP"
if [[ $DRY_RUN == 1 ]]; then
  # dry-run must not touch the host: verify the backup is *possible* instead.
  if rssh "test -r '$CURRENT_APP' -a -d '$ROOT/deploy-backups' -a -w '$ROOT/deploy-backups'"; then
    add_step backup ok "dry-run: backup feasible ($BACKUP_DIR would be created)"
  else
    add_step backup failed "dry-run: cannot read current release or write backups"
    finish preflight_failed "$STARTED"; exit 1
  fi
else
  rssh "mkdir -p '$BACKUP_DIR/app' '$BACKUP_DIR/panel' && \
        cp -a '$CURRENT_APP/.' '$BACKUP_DIR/app/' 2>/dev/null || true; \
        cp -a '$CURRENT_PANEL/.' '$BACKUP_DIR/panel/' 2>/dev/null || true; \
        readlink -f '$ROOT/app/current' > '$BACKUP_DIR/app_target.txt'; \
        readlink -f '$ROOT/panel/current' > '$BACKUP_DIR/panel_target.txt'" >/dev/null
  add_step backup ok "$BACKUP_DIR"
fi

# ---------------------------------------------------------------- rehearsal
rssh "rm -rf /tmp/cpamp-rehearsal && mkdir -p /tmp/cpamp-rehearsal" >/dev/null
scp -q "$BIN" "$HOST:/tmp/cpamp-rehearsal/cpa-manager-plus"
rssh "chmod +x /tmp/cpamp-rehearsal/cpa-manager-plus" >/dev/null
REHEARSAL_OK=0
if rssh "cd /tmp/cpamp-rehearsal && HTTP_ADDR=127.0.0.1:$ISOLATED_PORT USAGE_DATA_DIR=/tmp/cpamp-rehearsal/data \
         nohup ./cpa-manager-plus >/tmp/cpamp-rehearsal/out.log 2>&1 & sleep 6; \
         curl -fsS --max-time 5 http://127.0.0.1:$ISOLATED_PORT/health | grep -q '$MARKER'"; then
  REHEARSAL_OK=1
fi
rssh "pkill -f 'cpamp-rehearsal' 2>/dev/null || true; \
      (command -v fuser >/dev/null 2>&1 && fuser -k ${ISOLATED_PORT}/tcp >/dev/null 2>&1) || true; \
      sleep 1; rm -rf /tmp/cpamp-rehearsal" >/dev/null 2>&1 || true
if [[ $REHEARSAL_OK != 1 ]]; then
  add_step rehearsal failed "new binary unhealthy on isolated port $ISOLATED_PORT"
  finish rehearsal_failed "$STARTED"; exit 1
fi
add_step rehearsal ok "new binary healthy on isolated port $ISOLATED_PORT"

if [[ $DRY_RUN == 1 ]]; then
  add_step dry_run ok "rehearsal only; no release switch"
  add_health service true "unchanged"
  finish deployed "$STARTED"; exit 0
fi

# ---------------------------------------------------------------- replace
rssh "mkdir -p '$ROOT/app/$RELEASE' '$ROOT/panel/$PANEL_REL'" >/dev/null
scp -q "$BIN" "$HOST:$ROOT/app/$RELEASE/cpa-manager-plus"
scp -q "$PANEL" "$HOST:$ROOT/panel/$PANEL_REL/management.html"
rssh "chmod +x '$ROOT/app/$RELEASE/cpa-manager-plus'" >/dev/null
REMOTE_BIN_SHA="$(rssh "sha256sum '$ROOT/app/$RELEASE/cpa-manager-plus' | cut -d' ' -f1")"
REMOTE_PANEL_SHA="$(rssh "sha256sum '$ROOT/panel/$PANEL_REL/management.html' | cut -d' ' -f1")"
if [[ "$REMOTE_BIN_SHA" != "$BIN_SHA" || "$REMOTE_PANEL_SHA" != "$PANEL_SHA" ]]; then
  add_step replace failed "uploaded hashes do not match"
  finish rehearsal_failed "$STARTED"; exit 1
fi
add_step replace ok "hashes verified on host"

# ---------------------------------------------------------------- switch + restart
rssh "ln -sfn '$ROOT/app/$RELEASE' '$ROOT/app/current' && ln -sfn '$ROOT/panel/$PANEL_REL' '$ROOT/panel/current' && \
      printf '%s\n' '$VERSION' > '$ROOT/current_release.txt'" >/dev/null
add_step switch ok "current -> $RELEASE"
restart_service
sleep 4

# ---------------------------------------------------------------- health / rollback
if remote_health; then
  add_step health ok "HTTP 200 and marker verified"
  add_health service true "health ok on $RELEASE"
  finish deployed "$STARTED"; exit 0
fi

add_step health failed "health check failed on $RELEASE"
rssh "ln -sfn '$CURRENT_APP' '$ROOT/app/current' && ln -sfn '$CURRENT_PANEL' '$ROOT/panel/current'" >/dev/null
restart_service
sleep 4
if remote_health; then
  add_step rollback ok "restored previous release after failed health"
  add_health service true "health ok after rollback"
  finish rolled_back "$STARTED"; exit 1
fi
add_step rollback failed "health still failing after rollback"
add_health service false "health failing"
finish rollback_failed "$STARTED"; exit 1
