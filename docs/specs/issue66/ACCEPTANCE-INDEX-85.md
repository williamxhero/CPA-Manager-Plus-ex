# #85 acceptance index — parent #66 eighteen cases

Writer self-test handoff. **Does not claim product complete.** Hermes independent acceptance and live authorization remain blocked. No yosef-server. No production deploy/merge authority from this ticket.

Lineage tip for Manager work: `667e6143` (`spec74`/`#84`/`#73` chain). Brand verified on `97e92188`.

| # | Parent AC (abbrev) | Result | Evidence | Notes |
|---|---|---|---|---|
| 1 | A/B auto-pause routing isolation | PARTIAL | #82 Core money/routing + policy UI | DeepSeek first-source; full inventory live A/B blocked |
| 2 | Pause still queries; recover re-routes | PARTIAL | #82 recover UI + httpapi money/recover | Live window blocked |
| 3 | Manual+auto orthogonal | SAVED_PASS / PARTIAL | #77/#93 credential; #82 provider | Provider adapters beyond DeepSeek PARTIAL |
| 4–5 | Exact 0.1 threshold / no oscillation | PARTIAL | #82 money worker + Desktop | First-source verified; other sources PARTIAL |
| 6 | Failures retain success / no forged zero | SAVED_PASS / PARTIAL | balance adapters + #82/#73/#74 tests | Live PARTIAL |
| 7 | Inventory protocol fixtures | PARTIAL | #71/#73/#74 matrix + SCOPE docs | OpenRouter N/A; NewAPI needs operator confirm; live blocked |
| 8 | Shared wallet / multi-currency | PARTIAL | F06 SAVED_PASS; P03 PARTIAL | Provider whole-row still PARTIAL |
| 9–10 | Hourly full inventory / catch-up | PARTIAL | #76 full-refresh suite; F09/F10 SAVED_PASS | Pinned Core ActualCore often SKIP |
| 11 | Identity/version fences | SAVED_PASS | F01 / #70/#79 | |
| 12 | Gate failure observable / no false protect | PARTIAL | #82/#83 gate park + recover | Full production restore PARTIAL |
| 13 | Migration / ownership / rollback | PARTIAL | #83 migration/backup/park | Hermes deferred |
| 14 | Name-leading auto marker / UI consistency | PARTIAL | #84 consumers + markers + historic notes | Full visual/a11y PARTIAL |
| 15 | Brand EX geometry / consumers | PARTIAL | #72 generator check + 86 vitest | Hermes visual PARTIAL |
| 16 | Secret bait / SSRF / no leak | SAVED_PASS / PARTIAL | balance + official-balance tests | Browser subrequest PARTIAL (#97) |
| 17 | Live inventory per-source auth | **BLOCKED** | needs Hermes secure channel | No live this round |
| 18 | Local CPA authorized functional accept | **BLOCKED** | needs install auth + Hermes | No deploy from #85 |

## Explicit blockers retained

1. Hermes freeze of current local provider/credential/mode list + secure channel
2. Live authorized balance for each inventory host (esp. NewAPI instances, DeepInfra, Layso/#97)
3. Pinned ordinary Core binary for ActualCore full-refresh / credential-quota suites that currently SKIP
4. #97 local Enrollment+parser+live Credits 46.92 PASS (`c745df5a`); host attestation still must be installed on the authorized runtime before close. #95 E03 install deploy + #88 S03/S04 remain blocked without deploy authority
5. No yosef-server; no automatic production merge/restart/close of parent #66

## Closed child tickets (local evidence)

#72 brand · #73 DeepInfra · #74 NewAPI adapters · #75 credential quota path · #76 hourly refresh path · #82 monetary first-source · #83 migration · #84 status consumers

## Writer vs Hermes

This index is **writer self-test**. Hermes must independently re-verify; closing #85 does **not** close #66 or authorize deploy.
