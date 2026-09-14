/* bench_i128_gmp.c — why the 128-bit tier is worth profiling for.
 *
 * Same work in two representations that are both exact past 64 bits:
 * native `unsigned __int128` vs GMP `mpz_t`. Run:
 *
 *   cc -O2 -o bench_i128_gmp bench_i128_gmp.c -lgmp && ./bench_i128_gmp
 *
 * This is the payoff of SH2_PROFILE_WIDE: the wide probe can tell that
 * a value needs 65..128 bits, and SH2_ASSUME_OBSERVED_WIDTHS can then
 * pick __int128 instead of GMP. Informational only (never a gate, per
 * the repo testing policy).
 */
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <gmp.h>

static double now(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec + 1e-9 * t.tv_nsec;
}

int main(void) {
    const int N = 120;      /* 2**120: fits 128 bits, not 64 */
    const int REPS = 1000000;
    volatile long long sink = 0;

    double t0 = now();
    for (int r = 0; r < REPS; r++) {
        unsigned __int128 x = 1;
        for (int i = 0; i < N; i++) x += x;
        sink ^= (long long)(x >> 64);
    }
    double t_i128 = now() - t0;

    t0 = now();
    for (int r = 0; r < REPS; r++) {
        mpz_t x;
        mpz_init_set_ui(x, 1);
        for (int i = 0; i < N; i++) mpz_mul_ui(x, x, 2);
        sink ^= mpz_get_si(x);
        mpz_clear(x);
    }
    double t_gmp = now() - t0;

    printf("doubling x %d times, %d reps\n", N, REPS);
    printf("  __int128 : %7.3f s  (%6.2f ns/op)\n", t_i128, 1e9 * t_i128 / ((double)REPS * N));
    printf("  mpz (GMP): %7.3f s  (%6.2f ns/op)\n", t_gmp, 1e9 * t_gmp / ((double)REPS * N));
    printf("  speedup  : %6.1fx\n", t_gmp / t_i128);
    printf("(sink=%lld)\n", (long long)sink);
    return 0;
}
