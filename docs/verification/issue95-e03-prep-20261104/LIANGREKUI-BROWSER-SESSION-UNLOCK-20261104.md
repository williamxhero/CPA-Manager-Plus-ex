# 凉热葵 browser-session unlock (2026-11-04 ~20:54)

## Gate cleared

`newapi-browser-session-v1` on `https://api.liangrekui.com` after operator login
(userId **920**) and adapter fix that wraps the in-page fetch as `eval(atob(...))`
so BrowserSkill argv/relay does not split the refresh→Bearer expression.

## Live POST

- Binding: Codex parent `090df0bf…` + `api.liangrekui.com` member `20198fa9…`
- Job `8547ebe1-d58d-4a7e-8710-f57249fa3120` → `state=success`
- Native quota `172900` (`newapi_quota`) → CNY `0.3458` (`moneyPerNativeQuota=0.000002`)
- Evidence: `evidence/liangrekui-browser-session-job-atob-20261104T205443.json`
- Deploy: elevated `deploy-cpamp` → runtime exe SHA256 `FCB41484D4A5FD787BB1B4597FC46B63A260F9CDD5FE1739B8C75A4D6E93883C`

## Prior failure

Pre-atob binary returned `WEB_READ_FAILED` on the same binding (job evidence
`liangrekui-browser-session-job.json`).
