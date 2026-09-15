/* py2cy_int128.h — vendored helpers for py2cy's __int128 middle tier.
 *
 * Cython has no native 128-bit int, so generated .pyx code refers to the
 * fake 64-bit names below (enough for Cython to emit C); the REAL types
 * and these helpers come from this header at C-compile time. Keep this
 * file beside any .pyx that uses it (the parity harness copies it into
 * the temp build dir).
 *
 * Soundness notes: all conversions are exact (decimal strings and
 * byte arrays are lossless); printing goes through a fresh Python int
 * (never printf formats — there is no %d128). No GMP dependency.
 */
#ifndef PY2CY_INT128_H
#define PY2CY_INT128_H

typedef __int128_t py2cy_i128;
typedef __uint128_t py2cy_u128;

/* Decimal string (optional leading '-') to signed __int128. The caller
 * guarantees the value fits (py2cy proves magnitude before emitting). */
static py2cy_i128 py2cy_i128_from_str(const char *s) {
    int neg = 0;
    py2cy_i128 v = 0;
    if (*s == '-') { neg = 1; s++; }
    while (*s >= '0' && *s <= '9') {
        v = v * 10 + (*s - '0');
        s++;
    }
    return neg ? -v : v;
}

/* Decimal string (digits only) to unsigned __int128. Caller guarantees fit. */
static py2cy_u128 py2cy_u128_from_str(const char *s) {
    py2cy_u128 v = 0;
    while (*s >= '0' && *s <= '9') {
        v = v * 10 + (py2cy_u128)(*s - '0');
        s++;
    }
    return v;
}

/* Exact __int128 -> Python int via byte array (no precision loss, no
 * static buffers — safe for nested/multi-arg prints). Needs Python.h. */
static PyObject *py2cy_i128_to_py(py2cy_i128 v) {
    int one = 1;
    int little = *(char *)&one;
    return _PyLong_FromByteArray((unsigned char *)&v, 16, little, 1);
}

static PyObject *py2cy_u128_to_py(py2cy_u128 v) {
    int one = 1;
    int little = *(char *)&one;
    return _PyLong_FromByteArray((unsigned char *)&v, 16, little, 0);
}

#endif
