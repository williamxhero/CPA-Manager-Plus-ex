# DeepInfra / DeepSeek OfficialBalance live (2026-11-04)

## Manager PUT (PASS)

Used Core `openai-compatibility` complete groups (provider+member) and the
Core-stored inference API key as OfficialBalance `secret` (fingerprint only;
no secret material in this tree).

| Host | Adapter | Provider… | Credential… | Secret fp | PUT |
|---|---|---|---|---|---|
| `api.deepinfra.com` | `deepinfra` | `068a7783…` | `27c51563…` | `095ce95991d6` | `configured=true` v3 |
| `api.deepseek.com` | `deepseek` | `fa7cb5e3…` | `91d32a1d…` | `7205ca9e5dae` | `configured=true` v1 |

## Manager POST (FAIL — LocalService egress)

Jobs `bcacf44c…` / `66a99436…` → `queryStatus=network_error`,
`errorCode=UPSTREAM_UNAVAILABLE`.

## User-session upstream probe (same host, same keys)

- DeepInfra `GET https://api.deepinfra.com/payment/checklist` → **200**
- DeepSeek `GET https://api.deepseek.com/user/balance` → **200**,
  `balance_infos[0].total_balance=50.50` CNY

So credentials and endpoints are live; LocalService Manager process cannot
complete the HTTPS round-trip. Distinct from the BrowserSkill/LocalService gap
(already bridged by `:18717` bsk relay).

## Non-claims

- Tickets stay open.
- No secret values copied into git.
- Amounts above are user-session probes, not Manager OfficialBalance
  `lastSuccess` rows.
