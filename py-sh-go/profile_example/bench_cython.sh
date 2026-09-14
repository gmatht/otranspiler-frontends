#!/usr/bin/env bash
# bench_cython.sh — the Cython yardstick, all shapes, automatic vs typed.
#
# Cython also compiles Python source to C, so it is the direct
# comparison. Four columns per shape:
#   CPython            python3 app.py
#   Cython (pure)      cython --embed -3 app.py        (no annotations)
#   Cython (typed)     hand-written .pyx with C types  (the ceiling)
#   py-sh-go C         py-sh-go -> A1 -> C            (automatic)
#
# Two findings this is built to show:
#   1. Cython-pure is SLOWER than CPython (values stay boxed).
#   2. Cython --embed PAYS THE FULL CPython STARTUP (~57ms), so the typed
#      win only survives on long-running programs; py-sh-go C starts in
#      ~1ms. The startup-floor table is printed first for that reason.
#
# The typed columns are deliberately the *fair* ceiling: the I/O one does
# the same growable-list maintenance as app.py, and the bigint one binds
# GMP by hand (Cython has no native big-int).
#
# Requires: cython, Python.h, gmp.h, python3-config --embed, /usr/bin/time.
# Usage: ./bench_cython.sh
# Env: REPS=3 CC=cc CFLAGS=-O2 N_COMPUTE=2000000 IO_LINES=1000000
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
BENCH="$SCRIPT_DIR/bench"
CYX="$BENCH/cython"
OUT="${OUT:-$SCRIPT_DIR/out/cython}"
REPS="${REPS:-3}"
CC="${CC:-cc}"
CFLAGS="${CFLAGS:--O2}"
CFLAGS_CY="${CFLAGS_CY:-$CFLAGS}"
IO_LINES="${IO_LINES:-1000000}"
N_COMPUTE="${N_COMPUTE:-2000000}"

die() { echo "bench_cython: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG"
[ -x "$CLI" ] || die "missing $CLI"
command -v cython >/dev/null || die "cython not installed"
PY_INC="$(python3-config --includes 2>/dev/null)" || die "python3-config missing"
PY_LD="$(python3-config --ldflags --embed 2>/dev/null)" || die "python3-config --embed missing"
command -v /usr/bin/time >/dev/null || die "/usr/bin/time needed"
mkdir -p "$OUT"

wall() { # wall <workload> <cmd...> -> best-of-REPS seconds, high resolution
  local wl="$1"; shift
  python3 - "$wl" "$REPS" "$@" <<'PY'
import subprocess, sys, time
wl = sys.argv[1]; reps = int(sys.argv[2]); cmd = sys.argv[3:]
best = None
for _ in range(reps):
    with open(wl, 'rb') as f:
        t0 = time.perf_counter()
        subprocess.run(cmd, stdin=f, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        d = time.perf_counter() - t0
    best = d if best is None or d < best else best
print(f"{best:.6f}")
PY
}
rss() { # rss <workload> <cmd...> -> peak RSS kB
  local wl="$1"; shift; local tf; tf="$(mktemp)"
  /usr/bin/time -f "%M" "$@" < "$wl" >/dev/null 2>"$tf" || true
  tail -1 "$tf" | tr -dc '0-9'; rm -f "$tf"
}
row() { # row <impl> <secs> <rss_kb> [base]
  local sp="-"
  if [ -n "${4:-}" ]; then sp="$(awk -v p="$4" -v c="$2" 'BEGIN{ if (c+0>0) printf "%.1fx", p/c; else print "-" }')"; fi
  printf '  %-18s %8.3f %9.1f %9s\n' "$1" "$2" "$(awk -v k="$3" 'BEGIN{printf "%.1f",k/1024}')" "$sp"
}
hdr() { printf '\n%s\n  %-18s %8s %9s %9s\n' "$1" "impl" "time(s)" "rss(MB)" "speedup"; }

cython_embed() { # <src> <tag> [extra-cc-args...] -> binary
  local src="$1" tag="$2"; shift 2
  cython --embed -3 "$src" -o "$OUT/$tag.c" 2>"$OUT/$tag.cy.log" || { cat "$OUT/$tag.cy.log" >&2; die "cython $src"; }
  # shellcheck disable=SC2086
  $CC $CFLAGS_CY -o "$OUT/$tag" "$OUT/$tag.c" $PY_INC $PY_LD "$@" 2>"$OUT/$tag.cc.log" \
    || { tail -5 "$OUT/$tag.cc.log" >&2; die "cc $tag"; }
  echo "$OUT/$tag"
}
pyg_c() { # <src.py> <tag> -> binary
  local src="$1" tag="$2"
  "$PYG" --shir "$src" --raw > "$OUT/$tag.shir.json" || die "emit $src"
  "$CLI" - --target c < "$OUT/$tag.shir.json" > "$OUT/$tag.c" 2>/dev/null || die "render $tag"
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" -lgmp 2>"$OUT/$tag.pygcc.log" || die "cc $tag"
  echo "$OUT/$tag"
}
parity() { # parity <desc> <expected> <cmd...>
  local d="$1" exp="$2"; shift 2
  local got; got="$("$@" 2>/dev/null)"
  [ "$got" = "$exp" ] || die "parity $d: expected [$exp] got [$got]"
}

printf 'cython: %s   python: %s   cc: %s\n' "$(cython --version 2>&1)" "$(python3 --version 2>&1)" "$($CC --version | head -1)"

# ── startup floor: what Cython --embed costs even for an empty program ─
printf 'pass\n' > "$OUT/noop.py"
CY_NOOP="$(cython_embed "$CYX/noop.pyx" cy_noop)"
PYG_NOOP="$(pyg_c "$OUT/noop.py" pyg_noop)"
hdr "startup floor (empty program)"
row "CPython"         "$(wall /dev/null python3 -c pass)" 0 "$(wall /dev/null python3 -c pass)"
row "Cython (embedded)" "$(wall /dev/null "$CY_NOOP")" 0 "$(wall /dev/null python3 -c pass)"
row "py-sh-go C"      "$(wall /dev/null "$PYG_NOOP")" 0 "$(wall /dev/null python3 -c pass)"

# ── compute (scaled so typed i64 Cython stays exact) ────────────────
sed "s/range(10000000)/range($N_COMPUTE)/" "$BENCH/sum_squares.py" > "$OUT/sum_compute.py"
PY="$OUT/sum_compute.py"; pyout="$(python3 "$PY")"
CY_PURE="$(cython_embed "$PY" cy_sum_pure)"
CY_TYPED="$(cython_embed "$CYX/sum_squares_typed.pyx" cy_sum_typed)"
PYG_C="$(pyg_c "$PY" pyg_sum)"
parity "cython-pure"  "$pyout" "$CY_PURE"
parity "cython-typed" "$pyout" "$CY_TYPED"
parity "py-sh-go"     "$pyout" "$PYG_C"
hdr "sum_squares ($N_COMPUTE)"
base="$(wall /dev/null python3 "$PY")"
row "CPython"        "$base" "$(rss /dev/null python3 "$PY")" "$base"
row "Cython (pure)"  "$(wall /dev/null "$CY_PURE")"  "$(rss /dev/null "$CY_PURE")"  "$base"
row "Cython (typed)" "$(wall /dev/null "$CY_TYPED")" "$(rss /dev/null "$CY_TYPED")" "$base"
row "py-sh-go C"     "$(wall /dev/null "$PYG_C")"    "$(rss /dev/null "$PYG_C")"    "$base"

# ── I/O: same growable-list maintenance on both sides ───────────────
IO_LOG="$OUT/io.log"
if [ ! -f "$IO_LOG" ] || [ "$(wc -l < "$IO_LOG" 2>/dev/null)" != "$IO_LINES" ]; then
  python3 - "$IO_LINES" > "$IO_LOG" <<'PY'
import sys
n = int(sys.argv[1])
for i in range(1, n + 1):
    sys.stdout.write(f"ERROR worker {i} failed\n" if i % 10 == 0 else f"request line {i} served\n")
PY
fi
pyout="$(python3 "$SCRIPT_DIR/app.py" < "$IO_LOG")"
CY_IO="$(cython_embed "$SCRIPT_DIR/app.py" cy_io_pure)"
CY_IOT="$(cython_embed "$CYX/io_typed.pyx" cy_io_typed)"
PYG_IO="$(pyg_c "$SCRIPT_DIR/app.py" pyg_io)"
parity "cython-pure io"  "$pyout" bash -c "\"$CY_IO\" < \"$IO_LOG\""
parity "cython-typed io" "$pyout" bash -c "\"$CY_IOT\" < \"$IO_LOG\""
parity "py-sh-go io"     "$pyout" bash -c "\"$PYG_IO\" < \"$IO_LOG\""
hdr "app.py (io/$IO_LINES lines, storing both lists)"
base="$(wall "$IO_LOG" python3 "$SCRIPT_DIR/app.py")"
row "CPython"        "$base" "$(rss "$IO_LOG" python3 "$SCRIPT_DIR/app.py")" "$base"
row "Cython (pure)"  "$(wall "$IO_LOG" "$CY_IO")"  "$(rss "$IO_LOG" "$CY_IO")"  "$base"
row "Cython (typed)" "$(wall "$IO_LOG" "$CY_IOT")" "$(rss "$IO_LOG" "$CY_IOT")" "$base"
row "py-sh-go C"     "$(wall "$IO_LOG" "$PYG_IO")" "$(rss "$IO_LOG" "$PYG_IO")" "$base"

# ── bigint: Cython needn't a native big-int; typed binds GMP by hand ─
pyout="$(python3 "$BENCH/bignum_mul.py")"
CY_BIG="$(cython_embed "$BENCH/bignum_mul.py" cy_big_pure)"
CY_BIGT="$(cython_embed "$CYX/bignum_typed.pyx" cy_big_typed -lgmp)"
PYG_BIG="$(pyg_c "$BENCH/bignum_mul.py" pyg_big)"
parity "cython-pure big"  "$pyout" "$CY_BIG"
parity "cython-typed big" "$pyout" "$CY_BIGT"
parity "py-sh-go big"     "$pyout" "$PYG_BIG"
hdr "bignum_mul (100k x*=3 from 2**100)"
base="$(wall /dev/null python3 "$BENCH/bignum_mul.py")"
row "CPython"        "$base" "$(rss /dev/null python3 "$BENCH/bignum_mul.py")" "$base"
row "Cython (pure)"  "$(wall /dev/null "$CY_BIG")"  "$(rss /dev/null "$CY_BIG")"  "$base"
row "Cython (typed)" "$(wall /dev/null "$CY_BIGT")" "$(rss /dev/null "$CY_BIGT")" "$base"
row "py-sh-go C (GMP)" "$(wall /dev/null "$PYG_BIG")" "$(rss /dev/null "$PYG_BIG")" "$base"
echo
echo "artifacts under $OUT"
