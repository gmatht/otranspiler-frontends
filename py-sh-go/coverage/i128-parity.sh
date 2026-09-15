#!/usr/bin/env bash
# i128 parity oracle — py2cy --i128 fixtures compile through Cython with the
# vendored py2cy_int128.h and run bit-exact vs CPython. Only straight-line
# programs qualify as fixtures (big-bound loops cannot execute to
# completion); loop shapes are covered by emission unit tests instead.
# The annotation pass may only ADD declarations, so a mismatch is a bug.
#
# Usage: coverage/i128-parity.sh   (runs coverage/i128-fixtures/*.py)
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
"$GO" build -o py2cy ./cmd/py2cy || exit 1

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cp py2cy_int128.h "$tmp/"

ok=0
fail=0
for f in coverage/i128-fixtures/*.py; do
  bn="$(basename "$f" .py)"
  if ! ./py2cy --i128 "$f" > "$tmp/$bn.pyx" 2>"$tmp/$bn.ann"; then
    echo "FAIL $bn (annotate)"; fail=$((fail+1)); continue
  fi
  if grep -q "falling back" "$tmp/$bn.ann"; then
    echo "FAIL $bn (declined)"; fail=$((fail+1)); continue
  fi
  if ! cython --embed -3 "$tmp/$bn.pyx" -o "$tmp/$bn.c" 2>"$tmp/$bn.cy"; then
    echo "FAIL $bn (cython)"; fail=$((fail+1)); continue
  fi
  if ! cc -O2 -o "$tmp/$bn" "$tmp/$bn.c" -I"$tmp" \
      $(python3-config --includes) $(python3-config --ldflags --embed) 2>/dev/null; then
    echo "FAIL $bn (cc)"; fail=$((fail+1)); continue
  fi
  got="$(PYTHONUNBUFFERED=1 timeout 20 "$tmp/$bn" 2>/dev/null)"
  want="$(PYTHONUNBUFFERED=1 timeout 20 python3 "$f" 2>/dev/null)"
  if [ "$got" = "$want" ]; then
    ok=$((ok+1))
  else
    echo "FAIL $bn (parity: [$got] vs [$want])"; fail=$((fail+1))
  fi
done
echo "i128 parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
