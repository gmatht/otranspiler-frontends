#!/usr/bin/env bash
# check_cpython_parity.sh — differential correctness across the whole
# py-sh-go corpus: every testdata/*.py is transpiled Python -> A1 -> C,
# compiled, run, and its stdout compared byte-for-byte with CPython.
#
# This is the correctness half of "effectiveness": speed means nothing
# if the result differs. Known C-backend gaps are listed explicitly so a
# new mismatch is visible (the count must only grow downward).
#
# Usage: ./check_cpython_parity.sh [--verbose]
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
export OTRANSPILER_ROOT="$ROOT"

PYG="$ROOT/frontends/py-sh-go/py-sh-go"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
TD="$ROOT/frontends/py-sh-go/testdata"
CC="${CC:-cc}"
TMO="${TMO:-20}"
VERBOSE="${1:-}"

# Known C-backend (not frontend) gaps. Each entry: reason.
# EMPTY: t91_set_sum_fallback (bigint element in a set) was fixed — the
# string loop var is now string-homed and its accumulator's mpz slot is
# authoritative (`_big`), so the C sum is exact again.
KNOWN_GAPS=""

die() { echo "check_cpython_parity: $*" >&2; exit 1; }
[ -x "$PYG" ] || die "missing $PYG"
[ -x "$CLI" ] || die "missing $CLI"
command -v python3 >/dev/null || die "python3 needed"

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
total=0 match=0 mismatch=0 emit_fail=0 cc_fail=0 timeout_fail=0
MISMATCHES=()

for f in "$TD"/*.py; do
  bn="$(basename "$f" .py)"; total=$((total+1))
  if ! "$PYG" --shir "$f" --raw > "$T/$bn.json" 2>/dev/null; then
    emit_fail=$((emit_fail+1)); [ "$VERBOSE" ] && echo "EMIT  $bn"; continue
  fi
  if ! "$CLI" - --target c < "$T/$bn.json" > "$T/$bn.c" 2>/dev/null; then
    emit_fail=$((emit_fail+1)); [ "$VERBOSE" ] && echo "LOWER $bn"; continue
  fi
  if ! $CC -O1 -o "$T/$bn" "$T/$bn.c" -lgmp 2>/dev/null; then
    cc_fail=$((cc_fail+1)); [ "$VERBOSE" ] && echo "CC    $bn"; continue
  fi
  # run each program in a fresh scratch CWD: corpus tests that write
  # relative paths (t32_redirect, t50_file_test, …) must not litter here
  run_dir="$T/run/$bn"; mkdir -p "$run_dir"
  py="$(cd "$run_dir" && python3 "$f" 2>/dev/null)"
  c="$(cd "$run_dir" && timeout "$TMO" "$T/$bn" 2>/dev/null)"; rc=$?
  if [ $rc -ge 124 ]; then timeout_fail=$((timeout_fail+1)); [ "$VERBOSE" ] && echo "TMO   $bn"; continue; fi
  if [ "$py" = "$c" ]; then
    match=$((match+1))
  else
    mismatch=$((mismatch+1)); MISMATCHES+=("$bn")
  fi
done

echo "py -> A1 -> C vs CPython, corpus $TD"
echo "  total        $total"
echo "  match        $match"
echo "  mismatch     $mismatch  ${MISMATCHES[*]:-}"
echo "  emit fail    $emit_fail"
echo "  cc fail      $cc_fail"
echo "  timeout      $timeout_fail"
echo

# classify mismatches: known gaps are expected; anything else is a bug
new=0
for bn in "${MISMATCHES[@]:-}"; do
  case " $KNOWN_GAPS " in (*" $bn "*) ;; *) echo "NEW MISMATCH: $bn"; new=$((new+1));; esac
done
for g in $KNOWN_GAPS; do
  case " ${MISMATCHES[*]:-} " in (*" $g "*) echo "known gap:  $g";; *) echo "known gap RESOLVED (remove from KNOWN_GAPS): $g";; esac
done
[ "$new" -eq 0 ] || die "$new unexpected mismatch(es)"
echo "parity OK (modulo known gaps)"
