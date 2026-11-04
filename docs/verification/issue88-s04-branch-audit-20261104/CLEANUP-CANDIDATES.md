# S04 cleanup candidates (need explicit delete authorization)

Do **not** execute until operator says delete/cleanup is allowed.

## Manager (integration)

| Ref | Reason |
|---|---|
| branch `spec/92-qwen-manager-policy` | 4 unique commits superseded by tip equivalents `18f98a99` / `55b5e9a2` / `4b20a7e7` |

## CPA-Qwen

| Ref | Note |
|---|---|
| `spec/92-qwen-cli-*`, `spec/92-qwen-gate`, `spec/92-qwen-process-reaping`, `codex/spec92-qwen-coding-lane` | Still checked out / linked; audit unique commits before delete |

## CPA-OpenCode-ex

| Ref | Note |
|---|---|
| `spec/92-opencode-gate`, `spec/92-opencode-quota` | Inventory only |

## Worktrees

| Path | Branch |
|---|---|
| `manager-combined-731b18-ef611899-01` | `spec92/combined-20261103` |
| `manager-policy82` | `spec82/provider-policy-20261103` |

Keep `manager-canonical82-closing` / `spec92/conter-balance-method-map-20261104` — active delivery branch.
