#!/usr/bin/env bash
# py2cy-bench.sh — measures the ANNOTATION PASS, not the toolchain.
#
#   CPython            python3 bench/<shape>.py
#   Cython (pure)      cython --embed -3 bench/<shape>.py       (no typing)
#   py2cy              py2cy bench/<shape>.py                   (pure-Python mode)
#   py2cy --gmp        py2cy --gmp bench/<shape>.py             (.pyx + -lgmp)
#   hand-typed .pyx    bench/cython/<shape>_typed.pyx           (the ceiling)
#
# Sizes are the bench files' own N (rolling_hash=2_000_000,
# sum_squares=2_000_000, bignum_mul=100_000), so the numbers are comparable
# run-to-run without calibration. Every implementation is checked against
# CPython BEFORE timing; the reported time is the best of REPS runs.
#
# Usage: coverage/py2cy-bench.sh
# Env:   REPS=5 GO=go CC=cc   (needs cython, python3-config, gmp.h for --gmp)
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
CC="${CC:-cc}"
REPS="${REPS:-3}"
BENCH="profile_example/bench"
CYX="$BENCH/cython"
OUT="${OUT:-$(mktemp -d)}"
trap 'rm -rf "$OUT"' EXIT

"$GO" build -o py2cy ./cmd/py2cy || exit 1
command -v cython >/dev/null || { echo "py2cy-bench: cython required" >&2; exit 1; }

timeit() { # <cmd...> -> best-of-REPS seconds
  python3 - "$REPS" "$@" <<'PY'
import subprocess, sys, time
reps = int(sys.argv[1]); cmd = sys.argv[2:]
best = None
for _ in range(reps):
    t0 = time.perf_counter()
    subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    d = time.perf_counter() - t0
    best = d if best is None or d < best else best
print(f"{best:.4f}")
PY
}

compile_cy() { # <src> <tag> [libs...] -> binary path, or empty on failure
  local src="$1" tag="$2"; shift 2
  if ! cython --embed -3 "$src" -o "$OUT/$tag.c" 2>"$OUT/$tag.cy"; then
    echo "  (cython failed for $tag: $(head -1 "$OUT/$tag.cy"))" >&2; return 1
  fi
  # shellcheck disable=SC2086
  if ! "$CC" -O2 -o "$OUT/$tag" "$OUT/$tag.c" \
      $(python3-config --includes) $(python3-config --ldflags --embed) "$@" \
      2>"$OUT/$tag.cc"; then
    echo "  (cc failed for $tag: $(head -1 "$OUT/$tag.cc"))" >&2; return 1
  fi
  printf '%s' "$OUT/$tag"
}

# check <binary> <reference.py>: stdout must match CPython before timing.
check() {
  local got want
  got="$("$1" 2>/dev/null)"; want="$(python3 "$2" 2>/dev/null)"
  if [ "$got" != "$want" ]; then
    echo "  !! PARITY FAIL $(basename "$1"): [$got] vs [$want]" >&2
    return 1
  fi
}

row() { # <impl> <time> <base>
  python3 -c "print(f'{'$1':<24}{float('$2'):9.4f}{float('$3')/float('$2'):8.2f}x')"
}

# one_shape <name> <py2cy-flags...>; the flags apply to the py2cy row.
one_shape() {
  local name="$1"; shift
  local py="$BENCH/$name.py"
  local n; n="$(grep -oE 'range\([0-9]+\)|i < [0-9]+' "$py" | head -1)"
  echo "── $name  ($n) ──"
  printf '%-24s%9s %9s\n' "impl" "time(s)" "vs CPython"
  local base; base="$(timeit python3 "$py")"
  row "CPython" "$base" "$base"

  local b
  if b="$(compile_cy "$py" "${name}_pure")" && check "$b" "$py"; then
    row "Cython (pure)" "$(timeit "$b")" "$base"
  fi

  ./py2cy "$@" "$py" > "$OUT/$name.auto.src"
  local ext=py libs=""
  if grep -q 'cdef extern from "gmp.h"' "$OUT/$name.auto.src"; then
    ext=pyx; libs="-lgmp"
  fi
  cp "$OUT/$name.auto.src" "$OUT/$name.auto.$ext"
  if b="$(compile_cy "$OUT/$name.auto.$ext" "${name}_auto" $libs)" && check "$b" "$py"; then
    row "py2cy ${*:-<default>}" "$(timeit "$b")" "$base"
  fi

  # the hand-written typed goldens have shape-specific names
  local gold="" glibs=""
  case "$name" in
    bignum_mul)   gold="$CYX/bignum_typed.pyx"; glibs="-lgmp" ;;
    rolling_hash) gold="$CYX/rolling_hash_typed.pyx" ;;
  esac
  if [ -n "$gold" ] && [ -f "$gold" ]; then
    if b="$(compile_cy "$gold" "${name}_gold" $glibs)" && check "$b" "$py"; then
      row "hand-typed .pyx" "$(timeit "$b")" "$base"
    fi
  fi
  echo
}

one_shape rolling_hash
one_shape sum_squares
one_shape bignum_mul --gmp
echo "artifacts under $OUT (kept until exit)"
