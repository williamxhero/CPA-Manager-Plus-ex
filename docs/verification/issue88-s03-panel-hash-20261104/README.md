# #88 S03 panel hash prep (2026-11-04)

Compares on-disk `management.html` sources. Does **not** deploy or claim S03 pass.

## Hashes

| Tree | Path | Bytes | SHA-256 |
|---|---|---|---|
| worktree panel source | `apps/manager-server/internal/httpapi/web/management.html` | 6634330 | `087C71D3E31952C09ED5F3B43B2E899A9294E67BDCF28360999EE1073A8D69B0` |
| installed runtime (after #95 E03) | `runtime/CPA-Manager-Plus/panel/management.html` | 6634330 | **same** `087C71D3…` |
| `CPA-Manager-Plus-ex` checkout | same relative path | 530 | `6913BD11…` (placeholder only) |

## Meaning

- Installed panel hash now **matches** the worktree build used in E03 redeploy.
- `CPA-Manager-Plus-ex` tree placeholder is unrelated to the running service.
- S03 still needs live Qwen/OpenCode two-provider save/rollback on this process; hash gate for panel deploy is satisfied.

## Next

1. Live page save for Qwen + OpenCode (excluded-models) with snapshot restore.
2. Do not close #88 until those page actions pass.
