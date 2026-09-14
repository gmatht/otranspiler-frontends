#!/usr/bin/env bash
# bench_lib.sh — shared measurement + duration-calibration helpers for
# the Python transpiler benchmarks (bench_vs_cpython.sh, bench_cython.sh).
#
# Source it after setting: OUT, REPS, TARGET_SECS, BENCH (app dir).
# It needs python3 and /usr/bin/time.
#
# Why calibrate: absolute wall times move ~2x run-to-run on a shared
# machine, so a fixed N makes a run either too short to measure or too
# long to wait. `bench_calibrate` scales a single constant until the
# CPython baseline lands in [0.8*TARGET, 1.25*TARGET] seconds — the
# 1..100s band the docs ask for — and every implementation then runs at
# the same N.

# best-of-REPS seconds for one run, high resolution (µs), stdin from file
bench_wall() {
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

# single-shot wall time (calibration probes; REPS makes those too slow)
bench_wall_once() {
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

# peak RSS in kB for one run
bench_rss() {
  local wl="$1"; shift
  local tf; tf="$(mktemp)"
  /usr/bin/time -f "%M" "$@" < "$wl" >/dev/null 2>"$tf" || true
  tail -1 "$tf" | tr -dc '0-9'
  rm -f "$tf"
}

bench_hdr() { # <title>
  printf '\n%s\n  %-18s %8s %9s %9s\n' "$1" "impl" "time(s)" "rss(MB)" "speedup"
}

bench_row() { # <impl> <secs> <rss_kb> [base_secs]
  local sp="-"
  if [ -n "${4:-}" ] && [ "${4:-0}" != "0" ]; then
    sp="$(awk -v p="$4" -v c="$2" 'BEGIN{ if (c+0>0) printf "%.1fx", p/c; else print "-" }')"
  fi
  printf '  %-18s %8.3f %9.1f %9s\n' "$1" "$2" "$(awk -v k="$3" 'BEGIN{printf "%.1f",k/1024}')" "$sp"
}

# bench_calibrate <seed> <cap> <gen-snippet> -> echoes the chosen N.
# <gen-snippet> is a shell fragment writing the app to stdout with
# $BENCH_N (the candidate) and $BENCH (app dir) in scope. The app is also
# left at $OUT/cal.py.
#
# Bisection, not fixed-point: bigint work is superlinear in N, so a
# fixed-point step (next = n*T/t) oscillates. Time is monotonic in N, so
# double to bracket, then halve the interval. Probes use a single run
# (the final table uses REPS).
bench_calibrate() {
  local seed="$1" cap="$2" gen="$3"
  local lo=1 hi="$seed" t mid i
  bench_cal_gen() { BENCH_N="$1" bash -c "$2" > "$OUT/cal.py"; }
  bench_cal_try() { bench_wall_once /dev/null python3 "$OUT/cal.py"; }
  bench_cal_in() { awk -v t="$1" -v T="$TARGET_SECS" 'BEGIN{exit !(t >= T*0.8 && t <= T*1.25)}'; }
  bench_cal_lt() { awk -v t="$1" -v T="$TARGET_SECS" 'BEGIN{exit !(t < T*0.8)}'; }
  bench_cal_gen "$hi" "$gen"; t="$(bench_cal_try)"
  i=0
  while bench_cal_lt "$t"; do
    i=$((i + 1)); [ "$i" -ge 12 ] && break
    lo="$hi"; hi=$((hi * 2)); [ "$hi" -gt "$cap" ] && hi="$cap"
    [ "$hi" = "$lo" ] && { echo "$hi"; return; }
    bench_cal_gen "$hi" "$gen"; t="$(bench_cal_try)"
  done
  if bench_cal_in "$t"; then echo "$hi"; return; fi
  for _ in $(seq 12); do
    mid=$(( (lo + hi) / 2 ))
    [ "$mid" = "$lo" ] || [ "$mid" = "$hi" ] && { echo "$mid"; return; }
    bench_cal_gen "$mid" "$gen"; t="$(bench_cal_try)"
    if bench_cal_in "$t"; then echo "$mid"; return; fi
    if bench_cal_lt "$t"; then lo="$mid"; else hi="$mid"; fi
  done
  echo "$lo"
}

# Clamp TARGET_SECS into the documented 1..100 band.
bench_clamp_target() {
  case "${TARGET_SECS:-}" in ''|*[!0-9]*) TARGET_SECS=5 ;; esac
  [ "$TARGET_SECS" -lt 1 ] && TARGET_SECS=1
  [ "$TARGET_SECS" -gt 100 ] && TARGET_SECS=100
}

# Write a deterministic mixed ERROR/ok log of <lines> lines to $OUT/io.log.
bench_gen_io() {
  python3 - "$1" > "$OUT/io.log" <<'PY'
import sys
n = int(sys.argv[1])
for i in range(1, n + 1):
    sys.stdout.write(f"ERROR worker {i} failed\n" if i % 10 == 0 else f"request line {i} served\n")
PY
}

# bench_calibrate_io <seed> <cap> <app.py> -> echoes the calibrated line
# count (bisection on the CPython runtime, same band as bench_calibrate).
bench_calibrate_io() {
  local seed="$1" cap="$2" app="$3"
  local lo=1 hi="$seed" t mid i
  bench_io_in() { awk -v t="$1" -v T="$TARGET_SECS" 'BEGIN{exit !(t >= T*0.8 && t <= T*1.25)}'; }
  bench_io_lt() { awk -v t="$1" -v T="$TARGET_SECS" 'BEGIN{exit !(t < T*0.8)}'; }
  bench_gen_io "$hi"; t="$(bench_wall_once "$OUT/io.log" python3 "$app")"
  i=0
  while bench_io_lt "$t"; do
    i=$((i + 1)); [ "$i" -ge 12 ] && break
    lo="$hi"; hi=$((hi * 2)); [ "$hi" -gt "$cap" ] && hi="$cap"
    [ "$hi" = "$lo" ] && { echo "$hi"; return; }
    bench_gen_io "$hi"; t="$(bench_wall_once "$OUT/io.log" python3 "$app")"
  done
  if bench_io_in "$t"; then echo "$hi"; return; fi
  for _ in $(seq 12); do
    mid=$(( (lo + hi) / 2 ))
    { [ "$mid" = "$lo" ] || [ "$mid" = "$hi" ]; } && { echo "$mid"; return; }
    bench_gen_io "$mid"; t="$(bench_wall_once "$OUT/io.log" python3 "$app")"
    if bench_io_in "$t"; then echo "$mid"; return; fi
    if bench_io_lt "$t"; then lo="$mid"; else hi="$mid"; fi
  done
  echo "$lo"
}
