# cython: language_level=3
#
# The hand-written Cython equivalent of what py-sh-go emits automatically
# for bench/sum_squares.py: a native i64 array plus a C accumulator.
# A Cython user has to write this (and pick the types) to approach the
# transpiler's output; -O2 compiles both the same way.
from libc.stdlib cimport malloc, free

cdef long long N = 2000000

def main():
    cdef long long *xs = <long long*>malloc(N * sizeof(long long))
    cdef long long i
    cdef long long total = 0
    if not xs:
        raise MemoryError()
    for i in range(N):
        xs[i] = i * i
        total += xs[i]
    print(total)
    print(N)
    free(xs)

main()
