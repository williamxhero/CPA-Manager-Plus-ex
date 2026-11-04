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

## Parser gate

- Default registry remains empty: no rule can be installed from marketing labels.
- A real `parsingRule` still requires an **authenticated** normal billing page observation that shows durable identity + finite USD amount.
- Closing #97 remains blocked until: Profile 1↔instance proof, logged-in billing page observe×2 match, and installed parser enrollment.

## Follow-up (same day)

- Re-navigate `https://layso.ai/billing` again: **final_url redirected to `https://layso.ai/`** with Sign-in dialog (still signed out).
- Sync activity showed a human attempting Sign in and hitting **“human check is initializing…”** (captcha). Agent must not complete captcha/password.
- `bsk request-help --prompt …` on session `lbka` **timed_out** without a confirmed logged-in billing page.
- Local Edge disk has a directory named `Profile 1` (display name `Hinh`). This is **not** BrowserSkill Profile↔instance proof; `5cfdda27` remains unmapped until extension “Copy profile instructions”.

## Artifacts

Raw observe dumps were captured locally for operator review and are **not** committed (may contain transient UI chatter / form defaults). This note is the durable evidence.
