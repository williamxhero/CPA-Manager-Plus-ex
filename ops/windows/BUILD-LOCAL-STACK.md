# Immutable local CPA stack builds

`build-local-stack.py` builds the five owned local repositories without reading
the installed runtime, credentials or databases. It does not deploy or control
services. Use the reviewed deployment entrypoint separately after its acceptance
gates pass.

Pass an explicit UTF-8 source manifest; there is deliberately no default branch:

```json
{
  "schema": 1,
  "version": "spec92-20261102",
  "sources": {
    "CPA-Core-ex": {"repository": "D:/WILL/AGENT/CPA/CPA-Core-ex", "ref": "spec/92-integration", "head": "FULL_40_CHARACTER_COMMIT"},
    "CPA-Manager-Plus-ex": {"repository": "D:/WILL/AGENT/CPA/CPA-Manager-Plus-ex", "ref": "spec/92-integration", "head": "FULL_40_CHARACTER_COMMIT"},
    "CPA-Usage-Keeper-ex": {"repository": "D:/WILL/AGENT/CPA/CPA-Usage-Keeper-ex", "ref": "main", "head": "FULL_40_CHARACTER_COMMIT"},
    "CPA-OpenCode-ex": {"repository": "D:/WILL/AGENT/CPA/CPA-OpenCode-ex", "ref": "spec/92-integration", "head": "FULL_40_CHARACTER_COMMIT"},
    "CPA-Qwen": {"repository": "D:/WILL/AGENT/CPA/CPA-Qwen", "ref": "spec/92-integration", "head": "FULL_40_CHARACTER_COMMIT"}
  }
}
```

Replace every placeholder with the independently accepted release revision. The
script rejects a revision that differs from the named ref before creating any
output. `--preflight-only` emits the resolved identity and creates no output.

For the actual build, also pass `--node <node.exe> --npm-cli <npm-cli.js>` and,
if needed, `--cc <gcc.exe>`. `--output` must be a new directory outside every
source repository. Existing build evidence is never overwritten.

The script creates clean detached local Git clones. Each plugin uses an external
Go modfile with its SDK replacement fixed to the exact selected Core clone.
After all five clones exist, but before **any Go/npm compilation**, the pinned
independent CPA-Qwen verifier checks the Manager clone's complete
`providers/qwen/CPA_QWEN_SOURCE.json` and snapshot against that Qwen clone.
Repository, commit, tree and every file hash must match; missing/extra/changed
files, links/junctions/reparse points and stale/missing receipts fail closed.
No snapshot sync, receipt enrollment or provenance fabrication happens in a build.
`qwen-snapshot-verification.log` retains the verifier output. A successful gate
records the real receipt SHA-256 and canonical identity/files in `build-result.json`.
The preflight-only source-ref check does not execute this snapshot gate and is
not a build/snapshot acceptance result.

Qwen builds three products from the selected **independent CPA-Qwen pin**:
`qwen-cliproxyapi.dll`, `aliyun-bss-quota.dll` and the sole `bailian-quota.exe`.
All live under `artifacts/CPA-Qwen/`, outside the source snapshots. The compatibility
DLL is hashed under `candidate-manifest.json`'s `candidate_only` list with
`policy: candidate-only` and source/Core identities; it has **no `target`** and is
not added to the default deployable `files` list. Building it never enables or
deploys it. No old Aliyun checkout is read. Core must supply the compatible SPEC92
SDK; both DLLs and Qwen tests/CLI retain the existing external modfile binding to
the explicitly selected Core pin, rather than published v8.0.0.

Recorded Go build metadata must identify the selected source commit with
`vcs.modified=false`; DLL metadata must also name that Core replacement.
`bailian-quota.exe` is a standalone utility, not evidence of a linked Core SDK.

Before freezing the Manager source revision, regenerate and commit its three
tracked embedded panel assets using the same manifest `version`. The build
regenerates those assets and requires byte equality with the tracked copies,
instead of hiding generated modifications or misreporting a dirty source tree as
clean. Do not infer acceptance from a successful preflight or from an incomplete
build receipt. Only the final `accepted: true` receipt and candidate file hashes
identify a complete candidate; installed/running identity is a separate gate.

Source-ref gate tests run with Python's standard library:

```text
python -B tests/windows/test_build_local_stack.py
```

These tests use real local Git and the canonical snapshot verifier with synthetic
Go/Node boundaries. They do not establish real binary compilation or deployment.
