# #95 E03 + #88 S03 follow-up deploy (2026-11-04 night)

Authorized: local backup + install; do **not** close issues.

## Target

- Service: `LocalCPAManagerPlus` (Running after elevated deploy)
- Health: `http://127.0.0.1:18317/health` → ok

## Artifact (S03 qwen verifyRuntime fix)

| File | SHA-256 |
|---|---|
| `artifacts/cpa-manager-plus.exe` | `A89466285946D5DBCE60FB7058DFA4B22EB84C613D28FDD37CA0C67C74255987` |
| `artifacts/management.html` | `AF171E9BFBC045041043E71B7C589EE9BCA726C0A74FF55E504323A7162385DA` |

Evidence: `docs/verification/issue88-s03-panel-hash-20261104/deploy-s03-elevated-20261104T235703.json` status=`deployed`.

## S03 UI

Qwen excluded-models save via 凭证管理 → 配置 now persists (probe then restore). Root cause was `verifyRuntime` requiring Core model catalog that Qwen lacks.

## Capability readback (2026-11-05)

- `GET /usage-service/full-refresh` → 200, `resourceCount=51`
- OfficialBalance POST on this SHA: Layso **46.92**, Volc **122.35**, DeepInfra **0**, DeepSeek success, WawAPI **53.66854619** (after hosts re-pin)
- Evidence: `evidence/e03-s03-capability-readback-20261105.json`
- Matrix freeze: `MATRIX-LIVE-BLOCKERS.md`
- Rollback drill not re-run this tick (prior E03 `evidence/rollback-20261104.json` still on file); Aliyun still DEFERRED
