#!/usr/bin/env bash
# run_width_demo.sh — profile-selected integer *widths* (u32 / wide).
#
# Companion to run_profile_demo.sh. That script consumes only the
# profile's `lens` sites (array pre-sizing). This one consumes `mags`
# (value magnitude buckets) under SH2_ASSUME_OBSERVED_WIDTHS, the
# assertive tier of docs/DUAL_LOOPS.MD §4:
#
#   bucket 1..7 -> seed the range to the observed bound, so the C
#                  backend homes the variable at u8/u16/u32/u64; every
#                  store carries a runtime range guard (assertive mode).
#   bucket >= 8 -> the profiled (i64) build hit the 8-byte edge, so the
#                  variable is homed in GMP (exact for any magnitude —
#                  the "i128 or similar" wide tier). No guard needed.
#
# The application is app_grow.py (read N lines, double x N times), so
# the workload shape decides the tiers:
#
#   profile a (10 lines)     x=2**10    -> uint32_t x, uint16_t i
#   profile b (130 lines)    x=2**130   -> mpz_t x (wide), uint16_t i
#   profile c (70000 lines)  x=2**70000 -> mpz_t x (wide), uint32_t i
#   merged a+b+c                        -> mpz_t x, uint32_t i  (both)
#
# Usage: ./run_width_demo.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
MERGE="$ROOT/harness/profile-merge.py"
APP="$SCRIPT_DIR/app_grow.py"
OUT="${OUT:-$SCRIPT_DIR/out/width}"
CC="${CC:-cc}"

die() { echo "run_width_demo: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG (cd frontends/py-sh-go && make build)"
[ -x "$CLI" ] || die "missing $CLI (cd otranspilerl && cargo build --bin otranspilerl-cli)"
command -v python3 >/dev/null || die "python3 needed as the oracle"
# 2**70000 has 21073 decimal digits; CPython 3.11+ caps int->str at 4300
# unless this is lifted (the C/GMP side has no such cap).
PYORACLE=(env PYTHONINTMAXSTRDIGITS=0 python3)
mkdir -p "$OUT"

A1="$OUT/app_grow.shir.json"
"$PYG" --shir "$APP" --raw > "$A1" || die "frontend emit failed"

# ── workloads (deterministic; sized to move the buckets) ────────────
seq 10    > "$OUT/a.lines"
seq 130   > "$OUT/b.lines"
seq 70000 > "$OUT/c.lines"

render() { # render <c-out> [env...]
  local out="$1"; shift
  env "$@" "$CLI" - --target c < "$A1" > "$out" 2> "$out.render.log"
}
compile() { $CC -O1 -o "$2" "$1" -lgmp 2>"$2.gcc.log" || { cat "$2.gcc.log" >&2; die "cc failed on $1"; }; }
sha() { sha256sum "$1" | cut -c1-16; }
types() { grep -oE '(mpz_t|uint(8|16|32|64)_t|long long|unsigned long long) (x|i)\b' "$1" | sort -u | paste -sd' ' -; }

# ── baseline: the default (no-profile) render ───────────────────────
BASE_C="$OUT/baseline.c"; BASE_BIN="$OUT/baseline"
render "$BASE_C"; compile "$BASE_C" "$BASE_BIN"
BASE_TYPES="$(types "$BASE_C")"
echo "app_grow.py -> default C types: ${BASE_TYPES:-?}"
echo "  (x is i64 by default: N=130 overflows, which is why the profile matters)"
echo

printf '%-10s %7s %8s %8s  %-34s %-17s %s\n' \
  "profile" "lines" "x-bucket" "i-bucket" "C types" "guided C" "correct?"
printf '%-10s %7s %8s %8s  %-34s %-17s %s\n' \
  "----------" "------" "--------" "--------" "----------------------------------" "-----------------" "--------"

PROFILES=()
for tag in a b c; do
  wl="$OUT/$tag.lines"
  prof="$OUT/$tag.profile.json"
  pc="$OUT/$tag.profiled.c"; pbin="$OUT/$tag.profiled"
  gc="$OUT/$tag.guided.c";  gbin="$OUT/$tag.guided"

  # 1. take the profile
  render "$pc" SH2_PROFILE=1 SH2_PROFILE_OUT="$prof"
  compile "$pc" "$pbin"
  "$pbin" < "$wl" > "$OUT/$tag.profiled.out" || die "profiled run $tag"
  PROFILES+=("$prof")

  # 2. guide the width off it
  render "$gc" -u SH2_PROFILE SH2_ASSUME_OBSERVED_WIDTHS=1 SH2_PROFILE_IN="$prof"
  compile "$gc" "$gbin"
  "$gbin" < "$wl" > "$OUT/$tag.guided.out" || die "guided run $tag"
  "${PYORACLE[@]}" "$APP" < "$wl" > "$OUT/$tag.python.out"
  ok="no"
  cmp -s "$OUT/$tag.guided.out" "$OUT/$tag.python.out" && ok="yes"

  xb="$(python3 -c "import json;d=json.load(open('$prof'));print(max(s['max_bucket'] for s in d['mags'] if ':mag:x@' in s['name']))")"
  ib="$(python3 -c "import json;d=json.load(open('$prof'));print(max(s['max_bucket'] for s in d['mags'] if ':mag:i@' in s['name']))")"
  printf '%-10s %7s %8s %8s  %-34s %-17s %s\n' \
    "$tag" "$(wc -l < "$wl")" "$xb" "$ib" "$(types "$gc")" "$(sha "$gc")" "$ok"
  [ "$ok" = "yes" ] || die "guided profile $tag output != python3 oracle"
done

# ── merged profile: the union of every observed shape ───────────────
merged="$OUT/merged.profile.json"
python3 "$MERGE" "${PROFILES[@]}" --out "$merged" >/dev/null || die "profile-merge failed"
mgc="$OUT/merged.guided.c"; mgbin="$OUT/merged.guided"
render "$mgc" -u SH2_PROFILE SH2_ASSUME_OBSERVED_WIDTHS=1 SH2_PROFILE_IN="$merged"
compile "$mgc" "$mgbin"
mok="yes"
for tag in a b c; do
  "$mgbin" < "$OUT/$tag.lines" > "$OUT/merged.$tag.out" || die "merged run $tag"
  cmp -s "$OUT/merged.$tag.out" "$OUT/$tag.python.out" || mok="no"
done
printf '%-10s %7s %8s %8s  %-34s %-17s %s\n' \
  "merged" "a+b+c" "-" "-" "$(types "$mgc")" "$(sha "$mgc")" "$mok"
[ "$mok" = "yes" ] || die "merged profile wrong on some workload"
echo

# ── guard: the narrow profile on a wider workload must stop, not wrap ─
ga_c="$OUT/guard_a_on_b.c"; ga_bin="$OUT/guard_a_on_b"
render "$ga_c" -u SH2_PROFILE SH2_ASSUME_OBSERVED_WIDTHS=1 SH2_PROFILE_IN="$OUT/a.profile.json"
compile "$ga_c" "$ga_bin"
"$ga_bin" < "$OUT/b.lines" > "$OUT/guard.out" 2> "$OUT/guard.err"
rc=$?
echo "guard: profile a (x<=65535) run on b.lines (x=2**130) -> exit $rc"
sed 's/^/  /' "$OUT/guard.err"
[ "$rc" -eq 127 ] || die "expected the assertive width guard to fire (exit 127)"
echo

echo "artifacts under $OUT"
echo "  <tag>.profile.json  a/b/c measurements   <tag>.guided.c  the C each produced"
