#!/usr/bin/env bash
# py2cy parity oracle — annotate Python with py2cy, compile the emitted
# pure-Python-mode Cython with `cython --embed`, and compare stdout to CPython.
# The annotation pass may only ADD declarations, so a mismatch is a real bug.
#
# Usage: coverage/py2cy-parity.sh ['glob']   (default t0*+t1* testdata slice)
# Env:   GO=go
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
"$GO" build -o py2cy ./cmd/py2cy || exit 1
MODE="${MODE:-py}"
FLAGS=""
EXT="py"
if [ "$MODE" = pyx ]; then FLAGS="--pyx"; EXT="pyx"; fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

ok=0
fail=0
for f in ${1:-testdata/t0*.py testdata/t1*.py}; do
  bn="$(basename "$f")"
  if ! ./py2cy $FLAGS "$f" > "$tmp/$bn.$EXT" 2>"$tmp/$bn.err"; then
    echo "FAIL $bn (annotate)"; fail=$((fail+1)); continue
  fi
  if ! cython --embed -3 "$tmp/$bn.$EXT" -o "$tmp/$bn.c" 2>"$tmp/$bn.cy"; then
    echo "FAIL $bn (cython)"; fail=$((fail+1)); continue
  fi
  if ! cc -O2 -o "$tmp/$bn" "$tmp/$bn.c" \
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
echo "py2cy[$MODE] parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
