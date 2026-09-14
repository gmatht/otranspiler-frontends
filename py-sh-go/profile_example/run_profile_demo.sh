#!/usr/bin/env bash
# run_profile_demo.sh — profile-guided transpilation, end to end.
#
# Pipeline (all in this workspace):
#
#   app.py --py-sh-go--> A1 shIR --otranspilerl-cli --target c--> C
#
# Three C builds per workload shape:
#
#   baseline   plain render                     (no profiler env)
#   profiled   SH2_PROFILE=1 render             (probes + exit dump)
#   guided     SH2_PROFILE_IN=<json> render     (observed-max pre-sizing)
#
# The profile is *taken* by running the profiled binary against one
# workload, then *consumed* by re-rendering the SAME A1 with
# SH2_PROFILE_IN. Because only the Phase-2 consumption is implemented,
# the difference between baseline and guided C is the advisory
# `_sh_prealloc_*` block (docs/PROFILING.md §5.1): a workload whose
# shape grows a list to N gets an exactly pre-sized (still growable)
# vec. Different workload shapes therefore give different C.
#
# Correctness is checked at every step: profiled and guided stdout must
# equal the baseline stdout and the native python3 oracle. A guard test
# runs the guided binary on a workload that exceeds the profiled cap.
#
# Usage: ./run_profile_demo.sh [workload ...]
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
MERGE="$ROOT/harness/profile-merge.py"
APP="$SCRIPT_DIR/app.py"
BENCH_DIR="$SCRIPT_DIR/benchmarks"
OUT="${OUT:-$SCRIPT_DIR/out}"
CC="${CC:-cc}"

die() { echo "run_profile_demo: $*" >&2; exit 1; }

[ -x "$PYG" ] || die "missing frontend binary $PYG (run: cd frontends/py-sh-go && make build)"
[ -x "$CLI" ] || die "missing $CLI (run: cd otranspilerl && cargo build --bin otranspilerl-cli)"
[ -f "$APP" ] || die "missing $APP"
command -v "$CC" >/dev/null || die "no C compiler ($CC)"
command -v python3 >/dev/null && HAVE_PY=1 || HAVE_PY=0

mkdir -p "$OUT"

# ── A1 IR: emitted once, identical for every build ──────────────────
A1="$OUT/app.shir.json"
"$PYG" --shir "$APP" --raw > "$A1" || die "frontend emit failed"

render() { # render <c-out> [env...]
  local out="$1"; shift
  env "$@" "$CLI" - --target c < "$A1" > "$out" 2> "$out.render.log"
}
compile() { # compile <c> <bin>
  $CC -O1 -o "$2" "$1" 2>"$2.gcc.log" || { cat "$2.gcc.log" >&2; die "cc failed on $1"; }
}
sha() { sha256sum "$1" | cut -c1-16; }

# ── baseline: one build, no profiler env ────────────────────────────
BASE_C="$OUT/baseline.c"
BASE_BIN="$OUT/baseline"
render "$BASE_C"
compile "$BASE_C" "$BASE_BIN"

# ── collect the benchmark list ──────────────────────────────────────
if [ "$#" -gt 0 ]; then
  BENCHES=("$@")
else
  BENCHES=()
  while IFS= read -r f; do BENCHES+=("$f"); done < <(find "$BENCH_DIR" -maxdepth 1 -name '*.log' | sort)
fi
[ "${#BENCHES[@]}" -gt 0 ] || die "no workloads in $BENCH_DIR (run benchmarks/gen.sh)"

run_one() { # run_one <binary> <workload>
  "$1" < "$2"
}

printf 'app.py: %s\n' "$APP"
printf 'A1 shIR: %s (%s bytes)\n' "$A1" "$(wc -c < "$A1")"
printf 'baseline C: %s (%s bytes) sha=%s\n\n' "$BASE_C" "$(wc -c < "$BASE_C")" "$(sha "$BASE_C")"

printf '%-16s %6s %11s %9s %-34s %s\n' \
  "workload" "lines" "errors_max" "ok_max" "prealloc caps (guided)" "guided C"
printf '%-16s %6s %11s %9s %-34s %s\n' \
  "----------------" "------" "-----------" "---------" "------------------------------" "----------"

PROFILES=()
for bench in "${BENCHES[@]}"; do
  name="$(basename "$bench" .log)"
  prof="$OUT/$name.profile.json"
  pc="$OUT/$name.profiled.c"
  pbin="$OUT/$name.profiled"
  gc="$OUT/$name.guided.c"
  gbin="$OUT/$name.guided"

  # 1. profile: render with probes, run against this workload
  render "$pc" SH2_PROFILE=1 SH2_PROFILE_OUT="$prof"
  compile "$pc" "$pbin"
  run_one "$pbin" "$bench" > "$OUT/$name.profiled.out" || die "profiled run failed ($name)"
  [ -s "$prof" ] || die "no profile JSON written for $name"
  PROFILES+=("$prof")

  # 2. guided: re-render the SAME A1 with the observed lengths
  render "$gc" -u SH2_PROFILE SH2_PROFILE_IN="$prof"
  compile "$gc" "$gbin"

  # 3. correctness: guided == profiled == baseline == python3
  run_one "$gbin" "$bench" > "$OUT/$name.guided.out" || die "guided run failed ($name)"
  run_one "$BASE_BIN" "$bench" > "$OUT/$name.baseline.out"
  cmp -s "$OUT/$name.guided.out" "$OUT/$name.baseline.out" \
    || die "guided stdout != baseline stdout on $name"
  cmp -s "$OUT/$name.profiled.out" "$OUT/$name.baseline.out" \
    || die "profiled stdout != baseline stdout on $name"
  if [ "$HAVE_PY" = 1 ]; then
    python3 "$APP" < "$bench" > "$OUT/$name.python.out"
    cmp -s "$OUT/$name.guided.out" "$OUT/$name.python.out" \
      || die "guided stdout != python3 oracle on $name"
  fi

  # 4. report the consumed caps + the C diff (prealloc block only)
  caps="$(grep -oE '_sh_prealloc_[A-Za-z0-9_]+\(void\) \{ [A-Za-z0-9_]+_cap = \(size_t\)[0-9]+' "$gc" \
            | sed -E 's/_sh_prealloc_([A-Za-z0-9_]+)\(void\) \{ ([A-Za-z0-9_]+)_cap = \(size_t\)([0-9]+)/\1=\3/' \
            | paste -sd, -)"
  [ -n "$caps" ] || caps="(none)"
  emax="$(python3 -c "import json,sys;d=json.load(open('$prof'));print(next((s['len_max'] for s in d['lens'] if ':errors@' in s['name']),'-'))")"
  omax="$(python3 -c "import json,sys;d=json.load(open('$prof'));print(next((s['len_max'] for s in d['lens'] if ':ok@' in s['name']),'-'))")"
  printf '%-16s %6s %11s %9s %-34s %s\n' \
    "$name" "$(wc -l < "$bench")" "$emax" "$omax" "$caps" "$(sha "$gc")"

  # per-workload artifacts for inspection
  {
    echo "=== profile ($name) ==="
    cat "$prof"
    echo
    echo "=== baseline vs guided C diff ==="
    diff -u "$BASE_C" "$gc" || true
  } > "$OUT/$name.diff.txt"
done

# ── merged profile: safe union across shapes ────────────────────────
# A merged profile must never shrink a cap (max/union, §5.4): every
# workload's widest observed list survives, so the merged guided build
# covers all shapes with zero regrowth on any of them.
if [ "${#PROFILES[@]}" -ge 2 ]; then
  merged="$OUT/merged.profile.json"
  python3 "$MERGE" "${PROFILES[@]}" --out "$merged" >/dev/null \
    || die "profile-merge failed"
  mgc="$OUT/merged.guided.c"
  mgbin="$OUT/merged.guided"
  render "$mgc" -u SH2_PROFILE SH2_PROFILE_IN="$merged"
  compile "$mgc" "$mgbin"
  mcaps="$(grep -oE '_sh_prealloc_[A-Za-z0-9_]+\(void\) \{ [A-Za-z0-9_]+_cap = \(size_t\)[0-9]+' "$mgc" \
            | sed -E 's/_sh_prealloc_([A-Za-z0-9_]+)\(void\) \{ ([A-Za-z0-9_]+)_cap = \(size_t\)([0-9]+)/\1=\3/' \
            | paste -sd, -)"
  printf '\n%-16s %6s %11s %9s %-34s %s\n' \
    "MERGED(all ${#PROFILES[@]})" \
    "-" "-" "-" "${mcaps:-none}" "$(sha "$mgc")"
  # the merged (wider) build must reproduce EVERY workload exactly
  for bench in "${BENCHES[@]}"; do
    [ -f "$bench" ] || continue
    run_one "$mgbin" "$bench" > "$OUT/merged.$(basename "$bench" .log).out"
    run_one "$BASE_BIN" "$bench" > "$OUT/base.$(basename "$bench" .log).out"
    cmp -s "$OUT/merged.$(basename "$bench" .log).out" "$OUT/base.$(basename "$bench" .log).out" \
      || die "merged guided stdout != baseline on $(basename "$bench")"
  done
  echo "merged build reproduces every workload's baseline stdout"
fi

# ── guard test: a stale/narrow profile must not break correctness ────
# Build guided from the smallest NON-EMPTY profile, run the largest
# workload: the observed cap is far too small, so the growth check
# (which the pre-sizing never removes) must grow the vec and still
# print the right answer (§5.3). Prefer the deliberately tiny
# few_errors profile when it is present.
SMALLEST=""
for p in "${PROFILES[@]}"; do
  case "$(basename "$p")" in
    few_errors.profile.json) SMALLEST="$p"; break ;;
  esac
done
if [ -z "$SMALLEST" ]; then
  for p in "${PROFILES[@]}"; do
    # skip a profile that observed nothing (a 0/0 shape pre-sizes nothing)
    if python3 -c "import json,sys;d=json.load(open('$p'));sys.exit(0 if any(s['len_max'] for s in d['lens']) else 1)"; then
      SMALLEST="$p"; break
    fi
  done
fi
BIGGEST="$(find "$BENCH_DIR" -maxdepth 1 -name '*.log' -printf '%s %p\n' 2>/dev/null | sort -n | tail -1 | cut -d' ' -f2-)"
if [ -n "$SMALLEST" ] && [ -n "$BIGGEST" ]; then
  stale_c="$OUT/stale_guard.guided.c"
  stale_bin="$OUT/stale_guard.guided"
  render "$stale_c" -u SH2_PROFILE SH2_PROFILE_IN="$SMALLEST"
  compile "$stale_c" "$stale_bin"
  run_one "$stale_bin" "$BIGGEST" > "$OUT/stale_guard.out" || die "stale-guard run failed"
  run_one "$BASE_BIN" "$BIGGEST" > "$OUT/stale_guard.base.out"
  cmp -s "$OUT/stale_guard.out" "$OUT/stale_guard.base.out" \
    || die "stale/narrow profile changed the result on $BIGGEST"
  printf '\nguard: %s (profiled on %s) on %s -> %s (matches baseline)\n' \
    "$(basename "$stale_bin")" "$(basename "$SMALLEST" .profile.json)" \
    "$(basename "$BIGGEST")" "$(tr '\n' ' ' < "$OUT/stale_guard.out")"
fi

echo
echo "artifacts written under $OUT"
echo "  *.profile.json  the measured shapes"
echo "  *.guided.c      the transpiled C each profile produced"
echo "  *.diff.txt      profile + baseline-vs-guided diff per workload"
