# #97 LAYSO live observe notes (2026-11-04)

## BrowserSkill

- Connected instance: `5cfdda27` (Edge, extension 0.4.1, `version_skew=false`).
- Profile 1 mapping: **not proven**. `edge://version` is a restricted tab URL under BrowserSkill; cannot read Profile Path via observe. Instance ID alone is insufficient per BrowserSkill profile rules.
- Driver used: `session start --browser 5cfdda27 --no-focus` → `navigate https://layso.ai/billing` → `observe` → `session stop`. No evaluate / private API / storage.

## Observed page (logged-out)

After dismissing a “Message Center” dialog, the billing URL rendered a **public marketing + Sign in** surface, not an authenticated wallet:

- Root title: `LAYSO | One API for Leading AI Models at Lower Cost`
- Nav includes `Sign in / Sign up`
- Copy mentions USD credits / “One USD balance across models” as marketing text
- Visible login form: Username or Email + Password (+ email-code alternative)
- **No** verified accountId / tenantId / role / exact decimal wallet amount on this observation

## Parser gate (superseded same day)

- Initial logged-out `/billing` observation correctly blocked a parser.
- Later authenticated wallet surface is `/dashboard/models` (Account balance Credits), not `/billing`.
- Host Enrollment + live enrolled query evidence: `issue97-live-20261104/README.md` (Credits `46.92`, rule `layso-dashboard-models-account-balance-v1`).

## Follow-up (same day)

- Re-navigate `https://layso.ai/billing` again: **final_url redirected to `https://layso.ai/`** with Sign-in dialog (still signed out).
- Sync activity showed a human attempting Sign in and hitting **“human check is initializing…”** (captcha). Agent must not complete captcha/password.
- `bsk request-help --prompt …` on session `lbka` **timed_out** without a confirmed logged-in billing page.
- Local Edge disk has a directory named `Profile 1` (display name `Hinh`). This is **not** BrowserSkill Profile↔instance proof; `5cfdda27` remains unmapped until extension “Copy profile instructions”.

## Artifacts

Raw observe dumps were captured locally for operator review and are **not** committed (may contain transient UI chatter / form defaults). This note is the durable evidence.
