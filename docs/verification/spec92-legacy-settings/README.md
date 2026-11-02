# SPEC92 legacy automation-settings preservation

Base: Manager `030e54324ce2c3dbd07d6b39c5526c68538ee03d`, fast-forwarded
onto `spec/92-legacy-policy-isolation` from a clean checkout. No source or index
changes were made in `manager-integration` by this repair.

## Contract and decision

SPEC92 requires a mapping and rollback evidence before historical global-policy
migration, preservation of valid intent, and no inferred enrollment of manually
disabled or paused credentials. Compatibility must not restore a hidden global
AND gate. This repair chooses **preserve only**, not an unproven migration from
a global boolean to per-subject authority.

The private `settings` row with key `automation_settings_v1` remains the source
document. Every save reads its latest value inside the SQLite write transaction,
overlays only the current model's three fields, and retains all other fields as
JSON raw values. Clearing a nil current override still removes that known key.
Large integers are not decoded through floating point. Invalid/non-object source
documents fail closed without replacement.

| Source field | Disposition | Runtime/per-subject interpretation |
| --- | --- | --- |
| `codexQuotaCooldownEnabled` | `preserved_unmapped_requires_confirmation`, same source key/value | None; no credential is enabled or enrolled |
| Other unknown/retired fields | `preserved_uninterpreted`, same source key/value | None |
| `authIssueQueueEnabled`, `authIssueAutoDisableEnabled`, `updatedAtMs` | Current typed model owns these fields | Existing account-policy semantics only |

No legacy field is accepted as a new PATCH input. Retained fields and the
rollback receipt are neither returned by the policy API nor logged. They may
contain private historical values and must remain subject to database access
controls and backup retention, not be copied into tracker/log output.

## Durable preservation receipt

On the first save of a document containing unknown/retired fields, the same
transaction inserts `settings/automation_settings_legacy_preservation_v1`:

- `version: 1`, `sourceKey: automation_settings_v1`, `strategy: preserve_only`;
- `perSubjectMapping: none` and a field-to-disposition map;
- the exact original JSON string (`originalValue`), original SQL row timestamp
  (`originalUpdatedAtMs`), and preservation timestamp (`preservedAtMs`).

Subsequent PATCHes never replace this first checkpoint. New or subsequently
changed unknown values are still retained in the current source document; the
receipt describes the first preservation checkpoint, not every later edit.
If either receipt persistence or the source write fails, neither is committed.
Known-only/new documents do not receive misleading legacy receipts. This is not
an assertion that unidentified per-subject policy migration has been completed.

## Offline recovery boundary (not performed on a runtime database)

Stop writers and take a compatible full database/WAL backup first. Inspect the
receipt privately and confirm its version, strategy, source key, and exact
pre-migration document. Decide explicitly whether reverting **all subsequent
edits, including later unknown fields,** is intended. This receipt is not a substitute for a full
backup and does not roll back credential policies, Core state, or other tables.

The tested restoration statement is:

```sql
BEGIN IMMEDIATE;
UPDATE settings
SET value = json_extract(
      (SELECT value FROM settings
       WHERE key = 'automation_settings_legacy_preservation_v1'),
      '$.originalValue'),
    updated_at_ms = json_extract(
      (SELECT value FROM settings
       WHERE key = 'automation_settings_legacy_preservation_v1'),
      '$.originalUpdatedAtMs')
WHERE key = 'automation_settings_v1';
SELECT changes(); -- must be 1; otherwise ROLLBACK, not COMMIT
COMMIT;
```

Use this only after the above checks; do not run it as an automatic startup
migration. The receipt is retained. A software downgrade can reintroduce old
global-policy semantics and therefore needs a separate runtime/compatibility
review; this repair does not authorize a downgrade, deployment, or any enable.

## Test evidence

All databases are temporary and all business traffic is synthetic. No running
CPA instance, official quota account, deployment script, Core source, UI, push,
or tracker state was changed. No descendant agent was used.

Staged whitespace checks rejected PowerShell's CRLF transcripts and indentation
on an empty diagnostic line. Only that empty-line indentation and line endings
(also the JSON receipt's line endings) were normalized. No nonblank output text,
event, result, count, timestamp, or test parameter was changed. `RESULT.json`
records both the original CRLF and tracked normalized LF transcript SHA256s.

- `01-red.log`: public HTTP PATCH against real temporary SQLite returned 200
  while deleting `codexQuotaCooldownEnabled` and another unknown field; exit 1.
- `02-green.log`: identical preservation test after repository merge; exit 0.
- `03-audit-red.log`: preservation worked but the durable migration receipt was
  absent; exit 1. This is a separate tracer-bullet failure, not a fake Core RED.
- `04-audit-green.log`: preservation plus immutable snapshot/disposition and
  byte-exact rollback (including the independent SQL timestamp); exit 0.
- `05-repository-service.log`: SQLite source/receipt failure atomicity, latest
  unknown-field retention, nil override clearing, non-object rejection,
  known-only behavior and existing service regressions; exit 0.
- `06-public-api-native-race.log`: retained failed environment attempt. The old
  Core `e978f72b` artifact failed the initial inventory GET with `identity_changed`
  **before the new legacy fixture was inserted**. Current consumer requires
  issued `sourceId`/`subjectId` that this old producer did not supply. This is not
  a migration product RED and its other passing tests are not a full PASS.
- `07-public-api-native-matched-race.log`: unchanged guards, current matching
  Core `2b825585fc04d5d1982b83151bb5bd7e5b6c0dca` test artifact and matching plugin
  SDK, `-race -count=1`. All six public/API native tests PASS with no skips.
  The existing CredentialUI migration test now seeds the old SQLite document,
  PATCHes an unrelated field, retains global ON as data, keeps A manually
  disabled/pending confirmation and B default OFF, and observes actual business
  sends `[A,B]=[0,1]`, quota queries `[0,0]`. It is not a test of Core `7b6448ba`.
- `08-settings-service-restart-race.log`: model/repository/service suites,
  including real store close/reopen, private-field non-disclosure and durable
  receipt retention; `-race -count=1`, 29 top-level tests PASS, no skips. Together
  with the six public/API native tests, the final scoped runs have 35 top-level
  passes and no failures, skips, or race reports (overlapping earlier runs are
  not added to that count).
- `09-build-vet.log`: affected-package `go vet`, Manager executable verification
  build, and `git diff --check` all exit 0. The uncommitted-source verification
  executable was hashed and removed, not installed or used as a release artifact.

The original independent Core environment was read from
`ops/implement-spec92-20261101/integration-evidence/core-sourceview-matched-merge.PARENT-ENV.json`;
the corrected one from
`ops/implement-spec92-20261102/full-refresh-budget-evidence/CURRENT-CORE-ENV.json`
in the parent workspace. Native fixture verification checks immutable artifact
SHA256 and VCS/SDK revisions, recorded in the raw log; none were rebuilt here.

This is scoped migration-repair evidence, not complete SPEC92 release,
browser-layout, authorization, build-provenance, or installed-runtime acceptance.
