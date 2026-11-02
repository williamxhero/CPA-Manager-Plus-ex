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
python tests/windows/test_build_local_stack.py
```
