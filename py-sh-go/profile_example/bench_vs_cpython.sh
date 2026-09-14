#!/usr/bin/env bash
# bench_vs_cpython.sh — the direct effectiveness measure: run the SAME
# Python source and the SAME workload under CPython and as transpiled
# native C, and compare wall time + peak RSS.
#
#   python3 app.py        vs        ./app  (py-sh-go -> A1 -> C, -O2)
#
# Three shapes, because the win is not uniform:
#   compute  (sum_squares.py)  native i64 vector + maintained aggregate
#   bigint   (bignum_mul.py)   GMP vs CPython's own big-int
#   I/O      (app.py)          line read + substring classify
#
# Correctness is checked first: a run whose stdout differs from CPython
# is reported, never timed.
#
# Usage: ./bench_vs_cpython.sh
# Env:   REPS=3  CC=cc  CFLAGS=-O2  IO_LINES=1000000
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
APP_IO="$SCRIPT_DIR/app.py"
BENCH="$SCRIPT_DIR/bench"
OUT="${OUT:-$SCRIPT_DIR/out/bench}"
REPS="${REPS:-3}"
CC="${CC:-cc}"
CFLAGS="${CFLAGS:--O2}"
IO_LINES="${IO_LINES:-1000000}"

die() { echo "bench_vs_cpython: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG (cd frontends/py-sh-go && make build)"
[ -x "$CLI" ] || die "missing $CLI (cd otranspilerl && cargo build --bin otranspilerl-cli)"
command -v python3 >/dev/null || die "python3 needed"
command -v /usr/bin/time >/dev/null || die "/usr/bin/time needed (GNU time)"
mkdir -p "$OUT"

# 1M-line mixed log for the I/O shape (deterministic).
IO_LOG="$OUT/io.log"
if [ ! -f "$IO_LOG" ] || [ "$(wc -l < "$IO_LOG")" != "$IO_LINES" ]; then
  python3 - "$IO_LINES" > "$IO_LOG" <<'PY'
import sys
n = int(sys.argv[1])
for i in range(1, n + 1):
    sys.stdout.write(f"ERROR worker {i} failed\n" if i % 10 == 0 else f"request line {i} served\n")
PY
fi

build() { # build <src.py> <tag> -> echoes the binary path
  local src="$1" tag="$2"
  "$PYG" --shir "$src" --raw > "$OUT/$tag.shir.json" || die "emit $src"
  "$CLI" - --target c < "$OUT/$tag.shir.json" > "$OUT/$tag.c" 2>/dev/null || die "render $tag"
  $CC $CFLAGS -o "$OUT/$tag" "$OUT/$tag.c" -lgmp 2>"$OUT/$tag.cc.log" || die "cc $tag"
  echo "$OUT/$tag"
}

# high-resolution wall time for one run (μs; process startup included —
# the C compute binary is fast enough that `%e`'s 10ms resolution is
# useless). stdin comes from the workload file; stdout is discarded.
wall() { # wall <workload> <cmd...> -> seconds
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

# best-of-REPS wall seconds and max RSS (kB) for a command on a workload
measure() { # measure <workload> <cmd...> -> "secs rss_kb"
  local wl="$1"; shift
  local best="" t tf rssmax=0
  for _ in $(seq "$REPS"); do
    t="$(wall "$wl" "$@")"
    awk -v a="$t" -v b="$best" 'BEGIN{exit !(b=="" || a<b)}' && best="$t"
  done
  tf="$(mktemp)"
  /usr/bin/time -f "%M" "$@" < "$wl" > /dev/null 2>"$tf" || true
  rssmax="$(tail -1 "$tf" | tr -dc '0-9')"; rm -f "$tf"
  echo "$best ${rssmax:-0}"
}

row() { # row <app> <impl> <secs> <rss_kb>
  printf '%-18s %-8s %8.3f %9.1f\n' "$1" "$2" "$3" "$(awk -v k="$4" 'BEGIN{printf "%.1f", k/1024}')"
}

printf 'python3: %s\n' "$(python3 --version 2>&1)"
printf 'cc: %s   reps: %s\n\n' "$($CC --version | head -1)" "$REPS"
printf '%-18s %-8s %8s %9s %10s\n' "app / shape" "impl" "time(s)" "rss(MB)" "speedup"
printf '%-18s %-8s %8s %9s %10s\n' "------------------" "--------" "--------" "---------" "----------"

# ── compute + bigint shapes (no input) ──────────────────────────────
for spec in "sum_squares:compute" "bignum_mul:bigint"; do
  name="${spec%%:*}"; shape="${spec##*:}"
  bin="$(build "$BENCH/$name.py" "$name")"
  pyout="$(python3 "$BENCH/$name.py")"; cout="$("$bin")"
  [ "$pyout" = "$cout" ] || { echo "PARITY FAIL $name: py=[$pyout] c=[$cout]"; continue; }
  read -r pts prss < <(measure /dev/null python3 "$BENCH/$name.py")
  read -r cts crss < <(measure /dev/null "$bin")
  row "$name ($shape)" "python3" "$pts" "$prss"
  row "" "C" "$cts" "$crss"
  printf '%-18s %-8s %8s %9s %10s\n' "" "" "" "" "$(awk -v p="$pts" -v c="$cts" 'BEGIN{printf "%.1fx", p/c}')"
done

# ── I/O shape ───────────────────────────────────────────────────────
bin="$(build "$APP_IO" "app_io")"
pyout="$(python3 "$APP_IO" < "$IO_LOG")"; cout="$("$bin" < "$IO_LOG")"
[ "$pyout" = "$cout" ] || echo "PARITY FAIL app.py: py=[$pyout] c=[$cout]"
read -r pts prss < <(measure "$IO_LOG" python3 "$APP_IO")
read -r cts crss < <(measure "$IO_LOG" "$bin")
row "app.py (io/$IO_LINES)" "python3" "$pts" "$prss"
row "" "C" "$cts" "$crss"
printf '%-18s %-8s %8s %9s %10s\n' "" "" "" "" "$(awk -v p="$pts" -v c="$cts" 'BEGIN{printf "%.1fx", p/c}')"
echo

echo "artifacts under $OUT (the .c is exactly what ran)"
