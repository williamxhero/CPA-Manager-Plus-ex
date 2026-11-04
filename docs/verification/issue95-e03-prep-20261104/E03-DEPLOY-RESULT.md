# #95 E03 local deploy result (2026-11-04)

Authorized: local backup + install + rollback; push branch; do **not** close issues.

## Target

- Service: `LocalCPAManagerPlus` (Running)
- AppDir: `D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\app`
- PanelPath: `D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\panel\management.html`
- Health: `http://127.0.0.1:18317/health` → `{"ok":true,"service":"cpa-manager-plus"}`

## Artifact

| File | SHA-256 |
|---|---|
| `artifacts/cpa-manager-plus.exe` | `46A85F911215EE5428A84DC26F367D98ECB63EA58261B9C84676127B1EC19639` |
| `artifacts/management.html` | `087C71D3E31952C09ED5F3B43B2E899A9294E67BDCF28360999EE1073A8D69B0` |

Source tip at build: branch `spec92/conter-balance-method-map-20261104` (includes `c745df5a` LAYSO enrollment).

## Evidence JSON (`evidence/`)

| Step | Result file | Status |
|---|---|---|
| Deploy | `evidence/deploy-20261104d.json` | `deployed` (backup→rehearsal→stop→replace→start→health) |
| Rollback drill | `evidence/rollback-20261104.json` | `rolled_back` |
| Redeploy | `evidence/redeploy-20261104.json` | `deployed` |

Installed exe SHA-256 after redeploy matches artifact (`46A85F91…`).

## Host attestation

- File: `D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\host-attestation.json`
- Service env: `CPAMP_WEB_BROWSER_ATTESTATION` in `LocalCPAManagerPlus.xml`

## Note

`ExpectMarker` must be a plain substring such as `ok`. Quoting `"ok":true` through nested PowerShell mangled the marker and caused false `rehearsal_failed`.
