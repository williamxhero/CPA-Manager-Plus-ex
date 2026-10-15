"""Convert the live (Windows) core config.yaml into a yosef-server Linux config.

Path/OS-specific rewrites only; everything else is copied verbatim, including
api-keys / provider keys / management keys (user chose full overwrite).
The output therefore contains secrets: never print, log or commit it.

Rewrites
--------
- auth-dir      -> /data/cpa/oauth            (credential dir synced from the
                  local machine; on Windows this is "..\\oauth" next to cpa-core)
- quota command -> /data/cpa/bin/bailian-quota (Linux build of bailian-quota)
- cache-file    -> /data/cpa/qwen-quota/qwen-quota.json
  The qwen quota block is KEPT: plugin config validation requires
  quota-source == "command" AND a non-empty command, so dropping the block
  makes plugin.register fail and removes qwen entirely.
- plugins dir stays "plugins": the systemd unit sets
  WorkingDirectory=/data/cpa/CLIProxyAPI and the host searches
  <dir>/linux/amd64 then <dir>. (Rewritten to absolute to decouple from cwd.)

Usage: python convert_core_config_v2.py <local-config.yaml> <out-config.yaml>
"""

import re
import sys
from pathlib import Path

REWRITES = [
    (r"^(\s*auth-dir:\s*).*$", r"\g<1>/data/cpa/oauth"),
    (r'(^\s*"command":\s*").*(".*)$', r"\g<1>/data/cpa/bin/bailian-quota\g<2>"),
    (r"C:/ProgramData/cpa-qwen-quota/qwen-quota\.json", "/data/cpa/qwen-quota/qwen-quota.json"),
    (r"C:\\\\ProgramData\\\\cpa-qwen-quota\\\\qwen-quota\.json", "/data/cpa/qwen-quota/qwen-quota.json"),
    # Absolute plugins dir: the relative "plugins" value resolves against the
    # process cwd, which is only the CLIProxyAPI root when systemd's
    # WorkingDirectory applies. Absolute removes that coupling entirely.
    (r'^(\s*dir:\s*)"plugins"', r'\g<1>"/data/cpa/CLIProxyAPI/plugins"'),
]

src = Path(sys.argv[1]).read_text(encoding="utf-8")
out = Path(sys.argv[2])

result = []
for line in src.split("\n"):
    new = line
    for pat, rep in REWRITES:
        if re.search(pat, new):
            new = re.sub(pat, rep, new)
    result.append(new)

text = "\n".join(result)
out.write_text(text, encoding="utf-8")
print("wrote", out, "bytes:", len(text))

bad = [l for l in text.split("\n") if re.search(r"[A-Za-z]:[\\/]|ProgramData|\.exe\b|D:/WILL", l)]
print("remaining machine-specific lines:", len(bad))
for l in bad[:10]:
    print("  KEEP-CHECK:", l[:130])

print("auth-dir    :", [l.strip() for l in text.split("\n") if l.strip().startswith("auth-dir")][:2])
print("quota-source:", text.count('"quota-source"'))
print("command     :", [l.strip() for l in text.split("\n") if '"command"' in l][:2])
print("cache-file  :", [l.strip() for l in text.split("\n") if "cache-file" in l][:3])
print("plugins dir :", [l.strip() for l in text.split("\n") if l.strip().startswith("dir:")][:2])
