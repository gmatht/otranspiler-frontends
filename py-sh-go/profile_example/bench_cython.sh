#!/usr/bin/env bash
# bench_cython.sh — the Cython yardstick (pure + typed), all shapes.
#
# Cython also compiles Python source to C, so it is the direct
# comparison. Columns:
#   CPython          python3 app.py
#   Cython (pure)    cython --embed -3 app.py        (no annotations)
#   Cython (typed)   hand-written .pyx with C types  (the fair ceiling)
#   py-sh-go C       py-sh-go -> A1 -> C            (automatic)
#
# Workload sizes are calibrated so CPython lands in
# [0.8*TARGET_SECS, 1.25*TARGET_SECS] (1..100s band, default 5). The
# typed columns are the *fair* ceiling: I/O does the same growable-list
# upkeep as app.py, bigint binds GMP by hand (Cython has no big-int).
#
# Requires: cython, Python.h, gmp.h, python3-config --embed, GNU time.
# Usage: ./bench_cython.sh
# Env: TARGET_SECS=5 REPS=3 CC=cc CFLAGS=-O2 CFLAGS_CY=-O2
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"
# shellcheck source=bench_lib.sh
. "$SCRIPT_DIR/bench_lib.sh"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
BENCH="$SCRIPT_DIR/bench"
export BENCH
CYX="$BENCH/cython"
export CYX
OUT="${OUT:-$SCRIPT_DIR/out/cython}"
REPS="${REPS:-3}"
TARGET_SECS="${TARGET_SECS:-5}"
CC="${CC:-cc}"
CFLAGS="${CFLAGS:--O2}"
CFLAGS_CY="${CFLAGS_CY:-$CFLAGS}"
LIST_CAP="${LIST_CAP:-10000000}"
IO_CAP="${IO_CAP:-20000000}"
bench_clamp_target

die() { echo "bench_cython: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG"
[ -x "$CLI" ] || die "missing $CLI"
command -v cython >/dev/null || die "cython not installed"
PY_INC="$(python3-config --includes 2>/dev/null)" || die "python3-config missing"
PY_LD="$(python3-config --ldflags --embed 2>/dev/null)" || die "python3-config --embed missing"
command -v /usr/bin/time >/dev/null || die "/usr/bin/time needed"
mkdir -p "$OUT"

cython_embed() { # <src> <tag> [extra-cc-args...] -> binary
  local src="$1" tag="$2"; shift 2
  cython --embed -3 "$src" -o "$OUT/$tag.c" 2>"$OUT/$tag.cy.log" || { cat "$OUT/$tag.cy.log" >&2; die "cython $src"; }
  # shellcheck disable=SC2086
  $CC $CFLAGS_CY -o "$OUT/$tag" "$OUT/$tag.c" $PY_INC $PY_LD "$@" 2>"$OUT/$tag.cc.log" \
    || { tail -5 "$OUT/$tag.cc.log" >&2; die "cc $tag"; }
  echo "$OUT/$tag"
}
pyg_c() { # <src.py> <tag> -> binary
  local src="$1" tag="$2" i
  "$PYG" --shir "$src" --raw > "$OUT/$tag.shir.json" || die "emit $src"
  # the shared otranspilerl-cli can be relinked by concurrent workers
  # mid-run; retry rather than fail a whole benchmark on a transient
  for i in 1 2 3 4; do
    if "$CLI" - --target c < "$OUT/$tag.shir.json" > "$OUT/$tag.c" 2>"$OUT/$tag.render.log"; then break; fi
    sleep 2
  done
  [ -s "$OUT/$tag.c" ] || { tail -3 "$OUT/$tag.render.log" >&2; die "render $tag"; }
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" -lgmp 2>"$OUT/$tag.pygcc.log" || die "cc $tag"
  echo "$OUT/$tag"
}
parity() { # <desc> <expected> <cmd...>
  local d="$1" exp="$2"; shift 2
  local got; got="$("$@" 2>/dev/null)"
  [ "$got" = "$exp" ] || die "parity $d: expected [$exp] got [$got]"
}
make_app() { BENCH_N="$2" bash -c "$1" > "$3"; }

# .py generators (for calibration) and .pyx generators (for the typed build)
GEN_ROLL='sed "s/range([0-9]*)/range($BENCH_N)/" "$BENCH/rolling_hash.py"'
GEN_SUM='sed "s/range([0-9]*)/range($BENCH_N)/" "$BENCH/sum_squares.py"'
GEN_BIG='sed "s/i < [0-9]*/i < $BENCH_N/" "$BENCH/bignum_mul.py"'
GENT_ROLL='sed "s/range([0-9]*)/range($BENCH_N)/" "$CYX/rolling_hash_typed.pyx"'
# (typed sum_squares removed: no __int128 in Cython)
# GENT_SUM='sed "s/N = [0-9]*/N = $BENCH_N/" "$CYX/sum_squares_typed.pyx"'
GENT_BIG='sed "s/range([0-9]*)/range($BENCH_N)/" "$CYX/bignum_typed.pyx"'

printf 'cython: %s   python: %s   cc: %s   reps: %s   target: %ss\n' \
  "$(cython --version 2>&1)" "$(python3 --version 2>&1)" "$($CC --version | head -1)" "$REPS" "$TARGET_SECS"
printf 'calibrating workloads (CPython baseline ~%ss each)...\n' "$TARGET_SECS"

N_ROLL="$(bench_calibrate 2000000 400000000 "$GEN_ROLL")"
N_SUM="$(bench_calibrate 2000000 "$LIST_CAP" "$GEN_SUM")"
N_BIG="$(bench_calibrate 100000 20000000 "$GEN_BIG")"
N_IO="$(bench_calibrate_io 1000000 "$IO_CAP" "$SCRIPT_DIR/app.py")"
printf 'sizes: rolling_hash=%s  sum_squares=%s  bignum_mul=%s  io=%s lines\n' \
  "$N_ROLL" "$N_SUM" "$N_BIG" "$N_IO"

# ── startup floor ───────────────────────────────────────────────────
printf 'pass\n' > "$OUT/noop.py"
CY_NOOP="$(cython_embed "$CYX/noop.pyx" cy_noop)"
PYG_NOOP="$(pyg_c "$OUT/noop.py" pyg_noop)"
cpython_start="$(bench_wall /dev/null python3 -c pass)"
bench_hdr "startup floor (empty program)"
bench_row "CPython"          "$cpython_start" 0 "$cpython_start"
bench_row "Cython (embedded)" "$(bench_wall /dev/null "$CY_NOOP")" 0 "$cpython_start"
bench_row "py-sh-go C"       "$(bench_wall /dev/null "$PYG_NOOP")" 0 "$cpython_start"

# ── rolling_hash ────────────────────────────────────────────────────
make_app "$GEN_ROLL" "$N_ROLL" "$OUT/rolling_hash.py"
make_app "$GENT_ROLL" "$N_ROLL" "$OUT/rolling_hash_typed.pyx"
pyout="$(python3 "$OUT/rolling_hash.py")"
CY_R="$(cython_embed "$OUT/rolling_hash.py" cy_roll_pure)"
CY_RT="$(cython_embed "$OUT/rolling_hash_typed.pyx" cy_roll_typed)"
PYG_R="$(pyg_c "$OUT/rolling_hash.py" pyg_roll)"
for b in "$CY_R" "$CY_RT" "$PYG_R"; do parity "rolling_hash $b" "$pyout" "$b"; done
bench_hdr "rolling_hash ($N_ROLL iterations, scalar recurrence)"
base="$(bench_wall /dev/null python3 "$OUT/rolling_hash.py")"
bench_row "CPython"        "$base" "$(bench_rss /dev/null python3 "$OUT/rolling_hash.py")" "$base"
bench_row "Cython (pure)"  "$(bench_wall /dev/null "$CY_R")"  "$(bench_rss /dev/null "$CY_R")"  "$base"
bench_row "Cython (typed)" "$(bench_wall /dev/null "$CY_RT")" "$(bench_rss /dev/null "$CY_RT")" "$base"
bench_row "py-sh-go C"     "$(bench_wall /dev/null "$PYG_R")" "$(bench_rss /dev/null "$PYG_R")" "$base"

# ── sum_squares ─────────────────────────────────────────────────────
# No typed column: the exact sum needs >64 bits (py-sh-go emits an
# __int128 aggregate); hand-typed Cython has no __int128, so a typed
# variant would either overflow or need the GMP FFI shown for bigint.
make_app "$GEN_SUM" "$N_SUM" "$OUT/sum_squares.py"
pyout="$(python3 "$OUT/sum_squares.py")"
CY_S="$(cython_embed "$OUT/sum_squares.py" cy_sum_pure)"
PYG_S="$(pyg_c "$OUT/sum_squares.py" pyg_sum)"
for b in "$CY_S" "$PYG_S"; do parity "sum_squares $b" "$pyout" "$b"; done
bench_hdr "sum_squares ($N_SUM, native vector + __int128 sum)"
base="$(bench_wall /dev/null python3 "$OUT/sum_squares.py")"
bench_row "CPython"        "$base" "$(bench_rss /dev/null python3 "$OUT/sum_squares.py")" "$base"
bench_row "Cython (pure)"  "$(bench_wall /dev/null "$CY_S")"  "$(bench_rss /dev/null "$CY_S")"  "$base"
printf '  %-18s %8s %9s %9s\n' "Cython (typed)" "-" "-" "n/a (no __int128)"
bench_row "py-sh-go C"     "$(bench_wall /dev/null "$PYG_S")" "$(bench_rss /dev/null "$PYG_S")" "$base"

# ── app.py I/O ──────────────────────────────────────────────────────
make_app 'cat "$CYX/io_typed.pyx"' 0 "$OUT/io_typed.pyx"
pyout="$(python3 "$SCRIPT_DIR/app.py" < "$OUT/io.log")"
CY_IO="$(cython_embed "$SCRIPT_DIR/app.py" cy_io_pure)"
CY_IOT="$(cython_embed "$OUT/io_typed.pyx" cy_io_typed)"
PYG_IO="$(pyg_c "$SCRIPT_DIR/app.py" pyg_io)"
parity "io pure"  "$pyout" bash -c "\"$CY_IO\" < \"$OUT/io.log\""
parity "io typed" "$pyout" bash -c "\"$CY_IOT\" < \"$OUT/io.log\""
parity "io pyg"   "$pyout" bash -c "\"$PYG_IO\" < \"$OUT/io.log\""
bench_hdr "app.py (io/$N_IO lines, storing both lists)"
base="$(bench_wall "$OUT/io.log" python3 "$SCRIPT_DIR/app.py")"
bench_row "CPython"        "$base" "$(bench_rss "$OUT/io.log" python3 "$SCRIPT_DIR/app.py")" "$base"
bench_row "Cython (pure)"  "$(bench_wall "$OUT/io.log" "$CY_IO")"  "$(bench_rss "$OUT/io.log" "$CY_IO")"  "$base"
bench_row "Cython (typed)" "$(bench_wall "$OUT/io.log" "$CY_IOT")" "$(bench_rss "$OUT/io.log" "$CY_IOT")" "$base"
bench_row "py-sh-go C"     "$(bench_wall "$OUT/io.log" "$PYG_IO")" "$(bench_rss "$OUT/io.log" "$PYG_IO")" "$base"

# ── bignum_mul ──────────────────────────────────────────────────────
make_app "$GEN_BIG" "$N_BIG" "$OUT/bignum_mul.py"
make_app "$GENT_BIG" "$N_BIG" "$OUT/bignum_typed.pyx"
pyout="$(python3 "$OUT/bignum_mul.py")"
CY_B="$(cython_embed "$OUT/bignum_mul.py" cy_big_pure)"
CY_BT="$(cython_embed "$OUT/bignum_typed.pyx" cy_big_typed -lgmp)"
PYG_B="$(pyg_c "$OUT/bignum_mul.py" pyg_big)"
for b in "$CY_B" "$CY_BT" "$PYG_B"; do parity "bignum_mul $b" "$pyout" "$b"; done
bench_hdr "bignum_mul ($N_BIG x*=3 from 2**100)"
base="$(bench_wall /dev/null python3 "$OUT/bignum_mul.py")"
bench_row "CPython"        "$base" "$(bench_rss /dev/null python3 "$OUT/bignum_mul.py")" "$base"
bench_row "Cython (pure)"  "$(bench_wall /dev/null "$CY_B")"  "$(bench_rss /dev/null "$CY_B")"  "$base"
bench_row "Cython (typed)" "$(bench_wall /dev/null "$CY_BT")" "$(bench_rss /dev/null "$CY_BT")" "$base"
bench_row "py-sh-go C (GMP)" "$(bench_wall /dev/null "$PYG_B")" "$(bench_rss /dev/null "$PYG_B")" "$base"
echo
echo "artifacts under $OUT"
