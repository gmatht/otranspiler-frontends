# bench/sum_squares.py — vector shape: build an int list and reduce it.
# CPython boxes every element (PyLong) and dispatches the bytecode loop.
# The `sum(xs)` consumer is what licenses the C backend to home `xs` as a
# native i64 vector with an exact __int128 aggregate (hand-typed Cython
# cannot express that aggregate without GMP; see docs/PY_BENCH.md).
xs = []
for i in range(2000000):
    xs.append(i * i)
print(sum(xs))
print(len(xs))
