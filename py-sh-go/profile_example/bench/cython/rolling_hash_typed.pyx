# cython: language_level=3, boundscheck=False, wraparound=False, cdivision=True
# Hand-written typed Cython for bench/rolling_hash.py.
def main():
    cdef long long i
    cdef long long h = 0
    for i in range(2000000):
        h = (h * 31 + i) % 1000000007
    print(h)
main()
