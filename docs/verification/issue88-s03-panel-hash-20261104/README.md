# #88 S03 panel hash prep (2026-11-04)

Compares on-disk `management.html` sources. Does **not** deploy or claim S03 pass.

## Hashes

| Tree | Path | Bytes | SHA-256 (prefix) |
|---|---|---|---|
| worktree `manager-canonical82-closing` @ `a5904cd8`+ | `apps/manager-server/internal/httpapi/web/management.html` | 6634330 | `087C71D3E31952C0…` |
| `CPA-Manager-Plus-ex` checkout | same relative path | 530 | `6913BD11A06B0315…` |

Full SHA-256 recorded in `HASHES.txt` when regenerated.

## Meaning

- Worktree embeds a **built** management panel (~6.6 MiB).
- The `CPA-Manager-Plus-ex` tree currently has a **530-byte placeholder**, not the built panel.
- S03 “生产面板部署可回滚、读回哈希一致” still requires: authorized rebuild→install of a binary whose embedded panel hash matches the intended on-disk panel, then live Qwen/OpenCode save on that process.

## Next authorized step

1. Build panel into manager embed (existing web→embed pipeline).
2. Install with backup/rollback (#95 E03).
3. GET management HTML from the running process; match SHA-256 to build receipt.
4. Run two-provider save/rollback page checks; restore probe snapshot.
