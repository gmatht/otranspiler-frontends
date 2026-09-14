# bench/sum_squares.py — compute shape: build an int list and reduce it.
# CPython boxes every element (PyLong) and dispatches the bytecode loop;
# the transpiled C uses a native i64 vector + a maintained aggregate.
xs = []
for i in range(10000000):
    xs.append(i * i)
print(sum(xs))
print(len(xs))
