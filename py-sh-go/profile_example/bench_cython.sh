#!/usr/bin/env bash
# bench_cython.sh — the fair yardstick: the SAME Python source compiled
# by Cython versus by py-sh-go, both to native C at -O2.
#
# Columns:
#   CPython            python3 app.py
#   Cython (pure)      cython --embed -3 app.py        (no annotations)
#   Cython (typed)     hand-written .pyx with C types  (the ceiling)
#   py-sh-go C         py-sh-go -> A1 -> C            (automatic)
#
# The point: Cython-pure usually does NOT beat CPython (ints are still
# boxed); Cython-typed only wins if the user rewrites the hot code with
# C types. py-sh-go produces C-typed code from the unmodified source.
#
# Requirements: cython, Python.h (python3-dev), python3-config --embed.
# Usage: ./bench_cython.sh
# Env: REPS=3 CC=cc CFLAGS=-O2
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
BENCH="$SCRIPT_DIR/bench"
OUT="${OUT:-$SCRIPT_DIR/out/cython}"
REPS="${REPS:-3}"
CC="${CC:-cc}"
CFLAGS="${CFLAGS:--O2}"
IO_LINES="${IO_LINES:-1000000}"
N_COMPUTE="${N_COMPUTE:-2000000}"   # i64-exact so typed Cython is comparable

die() { echo "bench_cython: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG"
[ -x "$CLI" ] || die "missing $CLI"
command -v cython >/dev/null || die "cython not installed"
PY_INC="$(python3-config --includes 2>/dev/null)" || die "python3-config missing"
PY_LD="$(python3-config --ldflags --embed 2>/dev/null)" || die "python3-config --embed missing"
command -v /usr/bin/time >/dev/null || die "/usr/bin/time needed"
mkdir -p "$OUT"

# high-resolution wall time for one run (stdin from the workload)
wall() { # wall <workload> <cmd...>
  local wl="$1"; shift
  python3 - "$wl" "$@" <<'PY'
import subprocess, sys, time
wl = sys.argv[1]; cmd = sys.argv[2:]
with open(wl, 'rb') as f:
    t0 = time.perf_counter()
    subprocess.run(cmd, stdin=f, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    print(f"{time.perf_counter() - t0:.6f}")
PY
}
measure() { # measure <workload> <cmd...> -> "secs rss_kb"
  local wl="$1"; shift
  local best="" t tf rss=""
  for _ in $(seq "$REPS"); do
    t="$(wall "$wl" "$@")"
    awk -v a="$t" -v b="$best" 'BEGIN{exit !(b=="" || a<b)}' && best="$t"
  done
  tf="$(mktemp)"; /usr/bin/time -f "%M" "$@" < "$wl" >/dev/null 2>"$tf" || true
  rss="$(tail -1 "$tf" | tr -dc '0-9')"; rm -f "$tf"
  echo "$best ${rss:-0}"
}

row() { # row <impl> <secs> <rss_kb> [base_secs_for_speedup]
  local sp=""
  [ -n "${4:-}" ] && [ -n "${4}" ] && [ "${4}" != "0" ] && \
    sp="$(awk -v p="$4" -v c="$2" 'BEGIN{ if (c+0>0) printf "%.1fx", p/c }')"
  printf '  %-16s %8.3f %9.1f %9s\n' "$1" "$2" "$(awk -v k="$3" 'BEGIN{printf "%.1f",k/1024}')" "$sp"
}

# cython_embed <src.py|pyx> <tag> -> binary path
cython_embed() {
  local src="$1" tag="$2"
  cython --embed -3 "$src" -o "$OUT/$tag.c" 2>"$OUT/$tag.cy.log" || { cat "$OUT/$tag.cy.log" >&2; die "cython $src"; }
  # shellcheck disable=SC2086
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" $PY_INC $PY_LD 2>"$OUT/$tag.cc.log" || { tail -5 "$OUT/$tag.cc.log" >&2; die "cc $tag"; }
  echo "$OUT/$tag"
}
pyg_c() { # pyg_c <src.py> <tag> -> binary path
  local src="$1" tag="$2"
  "$PYG" --shir "$src" --raw > "$OUT/$tag.shir.json" || die "emit $src"
  "$CLI" - --target c < "$OUT/$tag.shir.json" > "$OUT/$tag.c" 2>/dev/null || die "render $tag"
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" -lgmp 2>"$OUT/$tag.pygcc.log" || die "cc $tag"
  echo "$OUT/$tag"
}

printf 'cython: %s   python: %s   cc: %s\n' "$(cython --version 2>&1)" "$(python3 --version 2>&1)" "$($CC --version | head -1)"
printf 'reps per cell: %s   (Cython-typed needs N=%s so the i64 sum fits)\n\n' "$REPS" "$N_COMPUTE"

# ── compute: 10M app scaled to N_COMPUTE so typed Cython stays exact ─
sed "s/range(10000000)/range($N_COMPUTE)/" "$BENCH/sum_squares.py" > "$OUT/sum_compute.py"
PY="$OUT/sum_compute.py"
CY_PURE="$(cython_embed "$PY" cy_sum_pure)"
CY_TYPED="$(cython_embed "$BENCH/cython/sum_squares_typed.pyx" cy_sum_typed)"
PYG_C="$(pyg_c "$PY" pyg_sum)"
pyout="$(python3 "$PY")"
for b in "$CY_PURE" "$PYG_C"; do [ "$("$b")" = "$pyout" ] || die "parity $b"; done
[ "$("$CY_TYPED")" = "$pyout" ] || die "parity cython-typed"
read -r pts prss < <(measure /dev/null python3 "$PY")
printf 'sum_squares (%s)\n' "$N_COMPUTE"
row "CPython" "$pts" "$prss" "$pts"
read -r t r < <(measure /dev/null "$CY_PURE");  row "Cython (pure)"  "$t" "$r" "$pts"
read -r t r < <(measure /dev/null "$CY_TYPED"); row "Cython (typed)" "$t" "$r" "$pts"
read -r t r < <(measure /dev/null "$PYG_C");    row "py-sh-go C"     "$t" "$r" "$pts"
echo

# ── bigint: Cython cannot help (Python int either way) ─────────────
CY_BIG="$(cython_embed "$BENCH/bignum_mul.py" cy_big_pure)"
PYG_BIG="$(pyg_c "$BENCH/bignum_mul.py" pyg_big)"
pyout="$(python3 "$BENCH/bignum_mul.py")"
for b in "$CY_BIG" "$PYG_BIG"; do [ "$("$b")" = "$pyout" ] || die "parity $b"; done
read -r pts prss < <(measure /dev/null python3 "$BENCH/bignum_mul.py")
printf 'bignum_mul (100k x*=3 from 2**100)\n'
row "CPython" "$pts" "$prss" "$pts"
read -r t r < <(measure /dev/null "$CY_BIG"); row "Cython (pure)" "$t" "$r" "$pts"
printf '  %-16s %8s %9s %9s\n' "Cython (typed)" "-" "-" "n/a (no C bigint)"
read -r t r < <(measure /dev/null "$PYG_BIG"); row "py-sh-go C (GMP)" "$t" "$r" "$pts"
echo

# ── I/O ─────────────────────────────────────────────────────────────
IO_LOG="$OUT/io.log"
if [ ! -f "$IO_LOG" ] || [ "$(wc -l < "$IO_LOG" 2>/dev/null)" != "$IO_LINES" ]; then
  python3 - "$IO_LINES" > "$IO_LOG" <<'PY'
import sys
n = int(sys.argv[1])
for i in range(1, n + 1):
    sys.stdout.write(f"ERROR worker {i} failed\n" if i % 10 == 0 else f"request line {i} served\n")
PY
fi
CY_IO="$(cython_embed "$SCRIPT_DIR/app.py" cy_io_pure)"
PYG_IO="$(pyg_c "$SCRIPT_DIR/app.py" pyg_io)"
pyout="$(python3 "$SCRIPT_DIR/app.py" < "$IO_LOG")"
for b in "$CY_IO" "$PYG_IO"; do [ "$("$b" < "$IO_LOG")" = "$pyout" ] || die "parity $b"; done
read -r pts prss < <(measure "$IO_LOG" python3 "$SCRIPT_DIR/app.py")
printf 'app.py (io/%s lines)\n' "$IO_LINES"
row "CPython" "$pts" "$prss" "$pts"
read -r t r < <(measure "$IO_LOG" "$CY_IO");  row "Cython (pure)" "$t" "$r" "$pts"
read -r t r < <(measure "$IO_LOG" "$PYG_IO"); row "py-sh-go C"    "$t" "$r" "$pts"
echo
echo "artifacts under $OUT"
