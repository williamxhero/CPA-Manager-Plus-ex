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

## S03 save/restore (API via Manager → Core, 2026-11-04)

Against running `LocalCPAManagerPlus` after E03:

- Qwen + OpenCode: `PATCH /v0/management/auth-files/fields` add probe `excluded-models`, verify download, restore previous list.
- Result: **ALL_OK** — see `s03-save-evidence.json` (secrets redacted).
- Panel hash gate already matched installed `management.html`.

## S03 browser UI path (2026-11-04, later)

Against the same Running install + BrowserSkill `5cfdda27` (user session):

1. `bsk session start --browser 5cfdda27 --no-focus`
2. `navigate` → `http://127.0.0.1:18317/management.html` (session already authenticated; `登出` visible)
3. `cooperate sync --policy target` → click sidebar `@e13` “AI 提供商”
4. Observe banner **AI 提供商**; table includes `OpenAI Qwen-Compat` among provider rows

Compact evidence: `s03-browser-path-evidence.json`. Full VOM dumps stay local / untracked.

Still open for #88 close: in-UI excluded-models edit/save click-through (API path already ALL_OK); S04 cleanup awaits explicit delete auth.
