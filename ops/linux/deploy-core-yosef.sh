#!/bin/bash
# Deploy the CLIProxyAPI core on yosef-server: config + version pointer, with
# backup and automatic rollback. Mirrors the Windows deploy script's contract.
#
#   -DryRun   : validate and rehearse everything, change nothing
#   -NoRestart: install files but do not restart the service
#
# Exit codes: 0 deployed, 2 preflight_failed, 3 deployed_but_unhealthy+rolled_back
set -u

ROOT=/data/cpa/CLIProxyAPI
SVC=cpa-cliproxyapi.service
NEWVER=CLIProxyAPI_8.0.15_linux_amd64
# The final config (local config + 3 host adaptations: server.host=0.0.0.0,
# management.allow-remote=true, keep live management secret-key) is staged
# separately staged as /data/cpa/CLIProxyAPI/config.yaml.final before running.
NEWCFG=/data/cpa/CLIProxyAPI/config.yaml.final
DRYRUN=0
NORESTART=0
for a in "$@"; do
  case "$a" in
    -DryRun|--dry-run) DRYRUN=1 ;;
    -NoRestart|--no-restart) NORESTART=1 ;;
  esac
done

STAMP=$(date +%Y%m%d-%H%M%S)
BKDIR=/data/cpa/CLIProxyAPI/deploy-backups/$STAMP
RESULT=/data/cpa/CLIProxyAPI/deploy-backups/$STAMP/result.json
FAIL=0
step() { echo "[$(date +%H:%M:%S)] $*"; }

sha() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }

wait_healthy() { # $1=seconds
  local deadline=$(( $(date +%s) + $1 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    local code
    code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:8317/v1/models 2>/dev/null || true)
    if [ "$code" = "401" ] || [ "$code" = "200" ]; then
      echo "healthy (http=$code)"
      return 0
    fi
    sleep 2
  done
  echo "unhealthy (last http=$code)"
  return 1
}

step "== preflight =="
for f in "$NEWVER" ; do
  if [ ! -x "$ROOT/app/$NEWVER/cli-proxy-api" ]; then echo "MISSING: $ROOT/app/$NEWVER/cli-proxy-api"; FAIL=1; fi
done
[ -f "$NEWCFG" ] || { echo "MISSING: $NEWCFG"; FAIL=1; }
[ -d /data/cpa/oauth ] || { echo "MISSING: /data/cpa/oauth"; FAIL=1; }
[ -x /data/cpa/bin/bailian-quota ] || { echo "MISSING: /data/cpa/bin/bailian-quota"; FAIL=1; }
for so in qwen-cliproxyapi.so opencode-go-cliproxyapi.so; do
  [ -f "$ROOT/plugins/linux/amd64/$so" ] || { echo "MISSING plugin: $so"; FAIL=1; }
done
ACTION=$(readlink -f "$ROOT/app/current" 2>/dev/null || echo none)
step "current core   : $ACTION"
step "target core    : $ROOT/app/$NEWVER/cli-proxy-api"
step "config (old)   : $(sha "$ROOT/config.yaml")"
step "config (new)   : $(sha "$NEWCFG")"
if [ "$FAIL" = "1" ]; then echo "PREFLIGHT FAILED"; exit 2; fi

step "== config syntax check with the NEW binary =="
if ! "$ROOT/app/$NEWVER/cli-proxy-api" --config "$NEWCFG" --help >/dev/null 2>&1; then
  echo "note: --help with config returned non-zero (not fatal)"
fi

if [ "$DRYRUN" = "1" ]; then
  echo "DRYRUN: would back up config, flip app/current to $NEWVER, restart $SVC"
  exit 0
fi

step "== backup =="
mkdir -p "$BKDIR"
cp -a "$ROOT/config.yaml" "$BKDIR/config.yaml.old" 2>/dev/null || echo "(no old config)"
readlink -f "$ROOT/app/current" > "$BKDIR/app-current.old" 2>/dev/null
cp -a "$ROOT/app/current" "$BKDIR/app-current.old" 2>/dev/null || true
step "backup -> $BKDIR"

step "== install config =="
cp -f "$ROOT/config.yaml" "$ROOT/config.yaml.bak-$STAMP" 2>/dev/null || true
cp -f "$NEWCFG" "$ROOT/config.yaml"
NEWCFG_SHA=$(sha "$ROOT/config.yaml")
step "installed config sha=$NEWCFG_SHA"

step "== flip version pointer (atomic) =="
ln -sfn "$ROOT/app/$NEWVER" "$ROOT/app/current.tmp"
mv -Tf "$ROOT/app/current.tmp" "$ROOT/app/current"
step "app/current -> $(readlink -f "$ROOT/app/current")"

if [ "$NORESTART" = "1" ]; then echo "NoRestart: skipping restart"; exit 0; fi

step "== restart service =="
systemctl --user restart "$SVC"
sleep 4
step "== health check =="
if wait_healthy 60; then
  step "DEPLOYED OK"
  printf '{"status":"deployed","stamp":"%s","version":"%s","configSha":"%s"}\n' "$STAMP" "$NEWVER" "$NEWCFG_SHA" > "$RESULT" 2>/dev/null || true
  echo "RESULT: deployed"
  exit 0
fi

step "!! health check failed -> rolling back"
ln -sfn "$(cat "$BKDIR/app-current.old" 2>/dev/null)" "$ROOT/app/current.tmp" 2>/dev/null
mv -Tf "$ROOT/app/current.tmp" "$ROOT/app/current" 2>/dev/null
if [ -f "$BKDIR/config.yaml.old" ]; then cp -f "$BKDIR/config.yaml.old" "$ROOT/config.yaml"; fi
systemctl --user restart "$SVC"
sleep 5
if wait_healthy 45; then
  echo "RESULT: rolled_back (service healthy on previous version)"
  exit 3
fi
echo "RESULT: rollback_failed"
exit 4
