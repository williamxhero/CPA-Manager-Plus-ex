# api-top browser-session unlock (2026-11-04 ~20:33)

## Why it looked stopped

`/loop` PID 28540 kept ticking; remaining gates were operator login. After login assist (`continued` / 已经登录), dashboard URL was live but OfficialBalance POST initially failed with `WEB_READ_FAILED` because newer BrowserSkill requires `cooperate sync` context+observation on `evaluate`.

## Fix

`adapter_browser_session.go`: after navigate, `cooperate wait` + `sync`, then `evaluate --context --observation`.

## Deploy

Elevated `deploy-cpamp.ps1` → `status=deployed`, exe SHA-256 `B26E39A8FE26BAA8B3E21B4D96EDCCAB0A02A854165B8E3DD444834D5A3E71A2`.

## Live POST

- Adapter `newapi-browser-session-v1`, endpoint `/api/user/self`, browser `5cfdda27`, userId `2648`
- Job `d41c45a5-cdc3-421a-b3f2-3729e8285f75` → **success**, native `newapi_quota` remaining **2280724**
- Evidence: `evidence/apitop-browser-session-job.json`

Tickets stay open.
