# bench/rolling_hash.py — compute shape that scales to seconds without
# allocating (a list of 25M Python ints would eat ~1 GB). A non-foldable
# modulo recurrence: CPython dispatches every op, the C loop is a few
# instructions, and py-sh-go emits Python-correct modulo
# (((x % M) + M) % M) rather than C's truncating %.
h = 0
for i in range(2000000):
    h = (h * 31 + i) % 1000000007
print(h)
