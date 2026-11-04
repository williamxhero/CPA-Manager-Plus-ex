# #88 S04 branch/worktree audit (in progress, 2026-11-04)

Do **not** close #88. No branch/worktree deleted in this pass.

## This worktree

- Path: `ops/needs-forever-all-specs-20261103/worktrees/manager-canonical82-closing`
- Branch: `spec92/conter-balance-method-map-20261104` @ `223ef607` (pushed to `origin`)

## Related worktrees (same manager family)

| Path | Branch | HEAD |
|---|---|---|
| `.../manager-combined-731b18-ef611899-01` | `spec92/combined-20261103` | `2008445a` |
| `.../worktrees/manager-canonical82-closing` | `spec92/conter-balance-method-map-20261104` | `223ef607` |
| `.../worktrees/manager-policy82` | `spec82/provider-policy-20261103` | `d77c3338` |

## Integration remote sample

- `origin/spec92/conter-balance-method-map-20261104` tracking current branch
- Local `spec/92-qwen-manager-policy` still present on integration checkout
- `opencode-upstream/main` remote exists (not merged here)

## `spec/92-qwen-manager-policy` vs current tip

- Merge-base: `f2ec3d11`
- Ahead/behind (`qwen...conter`): **4 / 58**
- Unique on qwen (not in conter tip):
  - `e6e08730` feat: accept configured Qwen CLI quota evidence
  - `6620d952` test: keep existing quota window fixture semantics
  - `a89c7c76` fix: enforce exact Qwen quota provenance tuple
  - `ff94e943` test: reject cross-protocol quota provenance

These look **quota-provenance** focused, not the excluded-models save path already verified on live Manager.

### Supersede check (2026-11-04 loop tick)

Tip already contains equivalent commits under different SHAs:

| qwen-only | tip equivalent |
|---|---|
| `e6e08730` accept configured Qwen CLI quota evidence | `18f98a99` |
| `a89c7c76` exact Qwen quota provenance tuple | `55b5e9a2` |
| `ff94e943` reject cross-protocol quota provenance | `4b20a7e7` |

`evidence.go` / provenance tests match tip; **no cherry-pick required**. Keep `spec/92-qwen-manager-policy` until an explicit cleanup pass deletes it (not this tick).

## Related product repos (inventory only)

**CPA-Qwen** (on `main`): local branches still present — `spec/92-qwen-*`, `codex/spec92-qwen-coding-lane`, `spec/92-integration` (worktree-linked). Not deleted.

**CPA-OpenCode-ex** (on `main`): `spec/92-opencode-gate`, `spec/92-opencode-quota`, `spec/92-integration`. Not deleted.

## Next S04 steps

1. Explicit operator cleanup list for superseded manager + Qwen/OpenCode branches (after confirming no unique commits).
2. Still no issue close; no worktree delete this tick.
