# cython: language_level=3, boundscheck=False, wraparound=False, initializedcheck=False, cdivision=True
#
# The hand-written typed Cython equivalent of what py-sh-go emits
# automatically for bench/sum_squares.py: a native i64 array plus a C
# accumulator. A Cython user has to write this and pick the types.
# N=2e6 keeps the i64 sum exact; py-sh-go's __int128 aggregate is exact
# at any N.
from libc.stdlib cimport malloc, free
cdef long long N = 2000000
def main():
    cdef long long *xs = <long long*>malloc(N * sizeof(long long))
    cdef long long i, total = 0
    if not xs:
        raise MemoryError()
    for i in range(N):
        xs[i] = i * i
        total += xs[i]
    print(total)
    print(N)
    free(xs)
main()
