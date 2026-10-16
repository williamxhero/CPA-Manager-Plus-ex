#!/usr/bin/env python3
"""Execute the remote command in-container, without a real SSH connection."""
import subprocess
import sys
import tempfile

# Files model an SSH channel boundary: an old script's orphaned background
# subshell must not retain the test runner's capture pipes indefinitely.
with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
    result = subprocess.run(["bash", "-c", sys.argv[-1]], stdout=out, stderr=err)
    out.seek(0)
    err.seek(0)
    sys.stdout.buffer.write(out.read())
    sys.stderr.buffer.write(err.read())
sys.exit(result.returncode)
