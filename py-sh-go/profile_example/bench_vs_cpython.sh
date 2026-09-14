#!/usr/bin/env bash
# bench_vs_cpython.sh — CPython vs the transpiled C on the same source.
#
#   python3 app.py        vs        ./app  (py-sh-go -> A1 -> C, -O2)
#
# Workload sizes are CALIBRATED so the CPython baseline lands in
# [0.8*TARGET_SECS, 1.25*TARGET_SECS] — set TARGET_SECS to anything in
# the documented 1..100s band (default 5). Every implementation then
# runs the same source. Correctness is checked before timing.
#
# Shapes: sum_squares (native vector), rolling_hash (scalar recurrence,
# memory-light), app.py (line I/O + substring), bignum_mul (GMP bigint).
#
# Usage: ./bench_vs_cpython.sh
# Env: TARGET_SECS=5 REPS=3 CC=cc CFLAGS=-O2
#      LIST_CAP=10000000 IO_CAP=20000000 (memory guards)
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
OUT="${OUT:-$SCRIPT_DIR/out/bench}"
REPS="${REPS:-3}"
TARGET_SECS="${TARGET_SECS:-5}"
CC="${CC:-cc}"
CFLAGS="${CFLAGS:--O2}"
LIST_CAP="${LIST_CAP:-10000000}"   # sum_squares: 10M boxed ints is already ~1GB
IO_CAP="${IO_CAP:-20000000}"
bench_clamp_target

die() { echo "bench_vs_cpython: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG (cd frontends/py-sh-go && make build)"
[ -x "$CLI" ] || die "missing $CLI (cd otranspilerl && cargo build --bin otranspilerl-cli)"
command -v python3 >/dev/null || die "python3 needed"
command -v /usr/bin/time >/dev/null || die "/usr/bin/time needed (GNU time)"
mkdir -p "$OUT"

pyg_c() { # <src.py> <tag> -> binary path
  local src="$1" tag="$2" i
  "$PYG" --shir "$src" --raw > "$OUT/$tag.shir.json" || die "emit $src"
  # the shared otranspilerl-cli can be relinked by concurrent workers
  # mid-run; retry rather than fail a whole benchmark on a transient
  for i in 1 2 3 4; do
    if "$CLI" - --target c < "$OUT/$tag.shir.json" > "$OUT/$tag.c" 2>"$OUT/$tag.render.log"; then break; fi
    sleep 2
  done
  [ -s "$OUT/$tag.c" ] || { tail -3 "$OUT/$tag.render.log" >&2; die "render $tag"; }
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" -lgmp 2>"$OUT/$tag.cc.log" || die "cc $tag"
  echo "$OUT/$tag"
}

# gen snippets (used by bench_calibrate; $BENCH_N in scope)
GEN_SUM='sed "s/range([0-9]*)/range($BENCH_N)/" "$BENCH/sum_squares.py"'
GEN_ROLL='sed "s/range([0-9]*)/range($BENCH_N)/" "$BENCH/rolling_hash.py"'
GEN_BIG='sed "s/i < [0-9]*/i < $BENCH_N/" "$BENCH/bignum_mul.py"'
make_app() { BENCH_N="$2" bash -c "$1" > "$3"; }

printf 'python3: %s   cc: %s   reps: %s   target: %ss\n' \
  "$(python3 --version 2>&1)" "$($CC --version | head -1)" "$REPS" "$TARGET_SECS"
printf 'calibrating workloads (CPython baseline ~%ss each)...\n' "$TARGET_SECS"

N_SUM="$(bench_calibrate 2000000 "$LIST_CAP" "$GEN_SUM")"
N_ROLL="$(bench_calibrate 2000000 400000000 "$GEN_ROLL")"
N_BIG="$(bench_calibrate 100000 20000000 "$GEN_BIG")"

# I/O needs a file generator, so it calibrates against a log we write.
N_IO="$(bench_calibrate_io 1000000 "$IO_CAP" "$SCRIPT_DIR/app.py")"

printf 'sizes: sum_squares=%s  rolling_hash=%s  bignum_mul=%s  io=%s lines\n\n' \
  "$N_SUM" "$N_ROLL" "$N_BIG" "$N_IO"

printf '%-18s %-8s %8s %9s %10s\n' "app / shape" "impl" "time(s)" "rss(MB)" "speedup"
printf '%-18s %-8s %8s %9s %10s\n' "------------------" "--------" "--------" "---------" "----------"

# ── rolling_hash: scalar recurrence ─────────────────────────────────
make_app "$GEN_ROLL" "$N_ROLL" "$OUT/rolling_hash.py"
BIN_ROLL="$(pyg_c "$OUT/rolling_hash.py" pyg_roll)"
pyout="$(python3 "$OUT/rolling_hash.py")"; cout="$("$BIN_ROLL")"
[ "$pyout" = "$cout" ] || die "parity rolling_hash: py=[$pyout] c=[$cout]"
printf '\nrolling_hash (%s)\n' "$N_ROLL"
base="$(bench_wall /dev/null python3 "$OUT/rolling_hash.py")"
bench_row "CPython"  "$base" "$(bench_rss /dev/null python3 "$OUT/rolling_hash.py")" "$base"
bench_row "C"        "$(bench_wall /dev/null "$BIN_ROLL")" "$(bench_rss /dev/null "$BIN_ROLL")" "$base"

# ── sum_squares: native vector + aggregate ──────────────────────────
make_app "$GEN_SUM" "$N_SUM" "$OUT/sum_squares.py"
BIN_SUM="$(pyg_c "$OUT/sum_squares.py" pyg_sum)"
pyout="$(python3 "$OUT/sum_squares.py")"; cout="$("$BIN_SUM")"
[ "$pyout" = "$cout" ] || die "parity sum_squares: py=[$pyout] c=[$cout]"
printf '\nsum_squares (%s)\n' "$N_SUM"
base="$(bench_wall /dev/null python3 "$OUT/sum_squares.py")"
bench_row "CPython"  "$base" "$(bench_rss /dev/null python3 "$OUT/sum_squares.py")" "$base"
bench_row "C"        "$(bench_wall /dev/null "$BIN_SUM")" "$(bench_rss /dev/null "$BIN_SUM")" "$base"

# ── bignum_mul: GMP vs CPython big-int ──────────────────────────────
make_app "$GEN_BIG" "$N_BIG" "$OUT/bignum_mul.py"
BIN_BIG="$(pyg_c "$OUT/bignum_mul.py" pyg_big)"
pyout="$(python3 "$OUT/bignum_mul.py")"; cout="$("$BIN_BIG")"
[ "$pyout" = "$cout" ] || die "parity bignum_mul: py=[$pyout] c=[$cout]"
printf '\nbignum_mul (%s x*=3 from 2**100)\n' "$N_BIG"
base="$(bench_wall /dev/null python3 "$OUT/bignum_mul.py")"
bench_row "CPython"      "$base" "$(bench_rss /dev/null python3 "$OUT/bignum_mul.py")" "$base"
bench_row "C (GMP)"      "$(bench_wall /dev/null "$BIN_BIG")" "$(bench_rss /dev/null "$BIN_BIG")" "$base"

# ── app.py: line I/O, storing both lists ────────────────────────────
BIN_IO="$(pyg_c "$SCRIPT_DIR/app.py" pyg_io)"
pyout="$(python3 "$SCRIPT_DIR/app.py" < "$OUT/io.log")"; cout="$("$BIN_IO" < "$OUT/io.log")"
[ "$pyout" = "$cout" ] || die "parity app.py: py=[$pyout] c=[$cout]"
printf '\napp.py (io/%s lines)\n' "$N_IO"
base="$(bench_wall "$OUT/io.log" python3 "$SCRIPT_DIR/app.py")"
bench_row "CPython"  "$base" "$(bench_rss "$OUT/io.log" python3 "$SCRIPT_DIR/app.py")" "$base"
bench_row "C"        "$(bench_wall "$OUT/io.log" "$BIN_IO")" "$(bench_rss "$OUT/io.log" "$BIN_IO")" "$base"

echo
echo "artifacts under $OUT (the .c is exactly what ran)"
