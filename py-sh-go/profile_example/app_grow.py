# profile_example/app_grow.py — the integer-width companion to app.py.
#
# ONE application whose *integer magnitudes* depend on the workload
# shape: read N lines, then double an accumulator N times. The loop
# counter `i` is small for every workload; the accumulator `x` is
# 2**N, so a small workload keeps it in 32 bits and a large one needs
# more than 64. The C profiler records the magnitude bucket of both
# variables, and SH2_ASSUME_OBSERVED_WIDTHS turns those buckets into
# concrete C types:
#
#   small workload  -> x fits u32        -> uint32_t x (+ guard)
#   large workload  -> x hit the 64-bit  -> GMP mpz x (exact, "i128 or
#                     edge in the build       similar")
#   big counter     -> i needs 32 bits   -> uint32_t i
#
# The default (no-profile) build types x `long long` and is *wrong*
# past N=63; the wide profile is what makes it exact. That is the
# point: the profile does not just size allocations, it selects the
# integer tier, and it never removes the guard that keeps a stale
# profile from silently wrapping (SH2_ASSUME_OBSERVED_WIDTHS is the
# assertive tier of docs/DUAL_LOOPS.MD §4).
import sys

lines = []
line = sys.stdin.readline()
while line != "":
    lines.append(line)
    line = sys.stdin.readline()

n = len(lines)
x = 1
i = 0
while i < n:
    x = x * 2
    i = i + 1

print(x)
