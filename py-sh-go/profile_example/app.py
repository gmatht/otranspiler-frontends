# profile_example/app.py — ONE application, profiled against MANY shaped
# workloads.
#
# A tiny log classifier: read stdin line by line and split each line
# into `errors` (the line mentions ERROR) or `ok`. The two growable
# lists have lengths that depend on the *shape* of the workload (how
# many lines, how many mention ERROR), which is exactly what the C
# backend's SH2_PROFILE instrumentation observes and what
# SH2_PROFILE_IN turns into pre-sized allocations.
#
# Kept inside the py-sh-go v1 subset on purpose: py-sh-go -> A1 shIR ->
# C (--target c), no int()/f-strings, `while line != "":` (the
# `for line in sys.stdin:` lowering has an unrelated off-by-one), and
# substring containment (`"ERROR" in s`) rather than len() arithmetic.
import sys

errors = []
ok = []
line = sys.stdin.readline()
while line != "":
    s = line.strip()
    if "ERROR" in s:
        errors.append(s)
    else:
        ok.append(s)
    line = sys.stdin.readline()

print("errors=" + str(len(errors)))
print("ok=" + str(len(ok)))
