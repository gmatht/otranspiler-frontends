#!/bin/sh
# Regenerate the embedded SYNC_BUILTINS in go-sh.go from the
# A4 namespace JSON (sh2perl/data/sh2-builtins.json — the source of truth).
#
# The sh2perl lib test `a4_sync_builtins_matches_rust` asserts the JSON
# matches sh2.rs::SYNC_BUILTINS; this script makes go-sh derive from
# the same JSON so a drift anywhere fails everywhere.
set -e
JSON="../../sh2perl/data/sh2-builtins.json"
GO="go-sh.go"
if [ ! -f "$JSON" ]; then
    echo "missing $JSON" >&2; exit 1
fi
# Extract via python (jq may not be installed)
NAMES=$(python3 -c "import json; print(' '.join(json.load(open('$JSON'))['sync_builtins']))")
{
    echo "var SYNC_BUILTINS = map[string]bool{"
    # 5 per line, keep deterministic
    echo "$NAMES" | tr ' ' '\n' | sort | python3 -c "
import sys
names=[l.strip() for l in sys.stdin if l.strip()]
for i,n in enumerate(names):
    sep = ',' if i < len(names)-1 else ''
    print(f'\t{n!r}: true{sep}')
"
    echo "}"
} > /tmp/sync_builtins.go
# splice into go-sh.go between the existing SYNC_BUILTINS markers
python3 - <<PYEOF
import re
src = open("$GO").read()
new = open("/tmp/sync_builtins.go").read()
# replace from the opening marker line to the closing brace
pattern = re.compile(r"var SYNC_BUILTINS = map\[string\]bool\{[^}]*\}", re.S)
out, n = pattern.subn(new, src, count=1)
assert n == 1, "SYNC_BUILTINS block not found"
open("$GO","w").write(out)
print("synced SYNC_BUILTINS from $JSON")
PYEOF
