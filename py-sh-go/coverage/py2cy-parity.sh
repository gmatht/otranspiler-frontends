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
  oext="$EXT"
  if ! ./py2cy $FLAGS "$f" > "$tmp/$bn.$oext" 2>"$tmp/$bn.err"; then
    echo "FAIL $bn (annotate)"; fail=$((fail+1)); continue
  fi
  if [ "$oext" = pyx ] && grep -q "declined" "$tmp/$bn.err" 2>/dev/null; then
    # declined .pyx falls back to pure-Python content: compile as .py
    cp "$tmp/$bn.pyx" "$tmp/$bn.py"
    oext=py
  fi
  if ! cython --embed -3 "$tmp/$bn.$oext" -o "$tmp/$bn.c" 2>"$tmp/$bn.cy"; then
    echo "FAIL $bn (cython)"; fail=$((fail+1)); continue
  fi
  if ! cc -O2 -o "$tmp/$bn" "$tmp/$bn.c" \
      $(python3-config --includes) $(python3-config --ldflags --embed) 2>/dev/null; then
    echo "FAIL $bn (cc)"; fail=$((fail+1)); continue
  fi
  # BOTH sides read the SAME stdin, from the fixture's own input file when it
  # has one (testdata/<name>.stdin) and /dev/null otherwise. Two programs cannot
  # share one inherited stdin: the first consumes it and the second only sees
  # EOF, so a stdin-reading fixture (t36, t61) compared a real line against
  # nothing — a "parity" failure that said nothing about the emitted code.
  stdin=/dev/null
  if [ -f "testdata/$bn.stdin" ]; then stdin="testdata/$bn.stdin"; fi
  got="$(PYTHONUNBUFFERED=1 timeout 20 "$tmp/$bn" <"$stdin" 2>/dev/null)"
  want="$(PYTHONUNBUFFERED=1 timeout 20 python3 "$f" <"$stdin" 2>/dev/null)"
  # Pure-Python mode promises a file that is still CPython: `import cython`
  # and its decorators (cython.declare, and the @cython.cfunc/@cython.locals
  # the dual arm emits) must be no-ops there, so the annotated file has to run
  # under python3 with the same stdout as the source.
  if [ "$MODE" = py ]; then
    pygot="$(PYTHONUNBUFFERED=1 timeout 20 python3 "$tmp/$bn.py" <"$stdin" 2>/dev/null)"
    if [ "$pygot" != "$want" ]; then
      echo "FAIL $bn (not runnable CPython: [$pygot] vs [$want])"; fail=$((fail+1)); continue
    fi
  fi
  if [ "$got" = "$want" ]; then
    ok=$((ok+1))
  else
    echo "FAIL $bn (parity: [$got] vs [$want])"; fail=$((fail+1))
  fi
done
echo "py2cy[$MODE] parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
