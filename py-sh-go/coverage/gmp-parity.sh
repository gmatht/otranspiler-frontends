#!/usr/bin/env bash
# gmp parity oracle — `py2cy --gmp` on bigint programs -> .pyx (or the
# pure-Python fallback) -> cython --embed [-lgmp] -> stdout == CPython.
# The transform may only rewrite programs it can express exactly, so a
# mismatch is a real bug (declines fall back and must still match).
#
# Usage: coverage/gmp-parity.sh
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
"$GO" build -o py2cy ./cmd/py2cy || exit 1
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

ok=0
fail=0
run() { # <name> <source>
  local name="$1"
  printf '%s\n' "$2" > "$tmp/$name.py"
  if ! ./py2cy --gmp "$tmp/$name.py" > "$tmp/$name.out" 2>"$tmp/$name.err"; then
    echo "FAIL $name (py2cy)"; fail=$((fail+1)); return
  fi
  # .pyx if the GMP block was emitted, else the pure-Python fallback (.py)
  local ext=pyx libs=""
  if grep -q 'cdef extern from "gmp.h"' "$tmp/$name.out"; then
    libs="-lgmp"
  else
    ext=py
  fi
  cp "$tmp/$name.out" "$tmp/$name.$ext"
  if ! cython --embed -3 "$tmp/$name.$ext" -o "$tmp/$name.c" 2>"$tmp/$name.cy"; then
    echo "FAIL $name (cython)"; fail=$((fail+1)); return
  fi
  local c="$tmp/$name.c"
  # shellcheck disable=SC2086
  if ! cc -O2 -o "$tmp/$name" "$c" \
      $(python3-config --includes) $(python3-config --ldflags --embed) $libs 2>/dev/null; then
    echo "FAIL $name (cc)"; fail=$((fail+1)); return
  fi
  local got want
  got="$("$tmp/$name" 2>/dev/null)"
  want="$(python3 "$tmp/$name.py" 2>/dev/null)"
  if [ "$got" = "$want" ]; then
    ok=$((ok+1))
  else
    echo "FAIL $name (parity: [$got] vs [$want])"; fail=$((fail+1))
  fi
}

run bignum_mul "$(cat profile_example/bench/bignum_mul.py)"
run pow_mul 'x = 2 ** 100
x = x * x
print(x)'
run add_loop 'x = 2 ** 100
i = 0
while i < 50:
    x = x + 1
    i = i + 1
print(x)'
run mod_assign 'x = 2 ** 200
x = x % 1000000007
print(x)'
run fallback_unrewritable 'x = 2 ** 100
print(x + x)'
run reserved_decline 'include = 100000
x = 2 ** 100
print(x)
print(include)'
run fallback_walrus_bigint 'y = (x := 2 ** 100)
print(x)
print(y)'

echo "gmp parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
