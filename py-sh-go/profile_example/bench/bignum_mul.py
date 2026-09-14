# bench/bignum_mul.py — bigint shape: values far beyond 64 bits.
# Both sides are exact; the question is speed and memory.
x = 2 ** 100
i = 0
while i < 100000:
    x = x * 3
    i = i + 1
print(x % 1000000007)
