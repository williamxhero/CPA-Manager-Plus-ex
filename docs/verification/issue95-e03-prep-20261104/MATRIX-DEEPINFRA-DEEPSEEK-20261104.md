# DeepInfra / DeepSeek OfficialBalance live (2026-11-04)

## Manager PUT (PASS)

Used Core `openai-compatibility` complete groups (provider+member) and the
Core-stored inference API key as OfficialBalance `secret` (fingerprint only;
no secret material in this tree).

| Host | Adapter | Provider… | Credential… | Secret fp | PUT |
|---|---|---|---|---|---|
| `api.deepinfra.com` | `deepinfra` | `068a7783…` | `27c51563…` | `095ce95991d6` | `configured=true` v3 |
| `api.deepseek.com` | `deepseek` | `fa7cb5e3…` | `91d32a1d…` | `7205ca9e5dae` | `configured=true` v1 |

## Manager POST (FAIL — fake-IP DNS vs secureClient SSRF)

Jobs `bcacf44c…` / `66a99436…` → `queryStatus=network_error`,
`errorCode=UPSTREAM_UNAVAILABLE`.

Root cause (not missing secrets):

- Host DNS (and even queries aimed at `1.1.1.1` / `8.8.8.8`) answers
  `api.deepseek.com` → `198.18.2.53`, `api.deepinfra.com` → `198.18.2.52`
  (Clash/TUN **fake-IP** range).
- OfficialBalance `secureClient` explicitly refuses `198.18.0.0/15`
  (`forbiddenIP` in `internal/balance/source.go`) before dial — intentional
  SSRF / metadata protection. `Proxy` is also forced `nil`.
- User-session `urllib` dials the fake-IP through TUN and succeeds; Manager
  never will while fake-IP answers remain.

Distinct from the BrowserSkill/LocalService gap (already bridged by `:18717`
bsk relay). Layso web adapter stays green because it uses the user browser.

## User-session upstream probe (same host, same keys)

- DeepInfra `GET https://api.deepinfra.com/payment/checklist` → **200**
- DeepSeek `GET https://api.deepseek.com/user/balance` → **200**,
  `balance_infos[0].total_balance=50.50` CNY

## Operator unblocker (no product SSRF weakening)

Exclude these hosts from Clash fake-IP (or give LocalService a resolver that
returns public A/AAAA), then re-POST. Do not add `198.18.*` to
`privateTargets`.

Prepared on this host (pending Clash Verge reload — `verge-mihomo` stop was
Access Denied without elevation):

- `profiles/Merge.yaml` and runtime `clash-verge.yaml` /
  `dns_config.yaml` now list `api.deepseek.com` / `+.deepseek.com` /
  `api.deepinfra.com` / `+.deepinfra.com` under `fake-ip-filter`
- After Verge applies Merge (or elevated mihomo restart), flush DNS and
  re-POST OfficialBalance; answers must leave `198.18.0.0/15`

## Non-claims

- Tickets stay open.
- No secret values copied into git.
- Amounts above are user-session probes, not Manager OfficialBalance
  `lastSuccess` rows.
