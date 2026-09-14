# cython: language_level=3
# Hand-written typed Cython for bench/bignum_mul.py. Cython has NO native
# big-int, so the user must bind GMP by hand (what py-sh-go emits
# automatically for a `2 ** 100` literal).
from libc.stdio cimport FILE, stdout, printf
cdef extern from "gmp.h":
    ctypedef struct __mpz_struct:
        int _mp_alloc
        int _mp_size
        void *_mp_d
    ctypedef __mpz_struct mpz_t[1]
    void mpz_init(mpz_t)
    void mpz_clear(mpz_t)
    void mpz_ui_pow_ui(mpz_t, unsigned long, unsigned long)
    void mpz_mul_ui(mpz_t, const mpz_t, unsigned long)
    unsigned long mpz_fdiv_ui(const mpz_t, unsigned long)
def main():
    cdef mpz_t x
    cdef long long i
    mpz_init(x)
    mpz_ui_pow_ui(x, 2, 100)
    for i in range(100000):
        mpz_mul_ui(x, x, 3)
    printf("%lu\n", mpz_fdiv_ui(x, 1000000007))
    mpz_clear(x)
main()
