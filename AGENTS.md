## Agent skills

### Issue tracker

Issues and specs live in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a multi-context repository. See `docs/agents/domain.md`.

### Local deployment

For every Manager or panel change, deploy only to the local Windows CPA stack. Build the candidate first, then run `ops/windows/deploy-cpamp.ps1` (or the full-stack wrapper when the stack is rebuilt) with automatic rollback and health verification. Treat a nonzero result as not deployed; preserve the receipt and investigate before retrying. The yosef-server/small-computer environment is outside this repository's deployment scope.
