// gmp_test.go — the bigint-transform gate. The pass may only emit a .pyx it
// can rewrite EXACTLY; anything else must decline (falling back to the
// exact pure-Python output).
package pylib

import (
	"strings"
	"testing"
)

func TestGMPTransformBignum(t *testing.T) {
	src := "x = 2 ** 100\ni = 0\nwhile i < 100000:\n    x = x * 3\n    i = i + 1\nprint(x % 1000000007)\n"
	out, ok, err := AnnotateGMP(src)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a GMP transform")
	}
	for _, want := range []string{
		"cdef mpz_t x",
		"cdef long long i",
		"mpz_ui_pow_ui(x, 2, 100)",
		"mpz_mul_ui(x, x, 3)",
		"mpz_fdiv_ui(x, 1000000007)",
		"mpz_init(x)",
		"mpz_clear(x)",
	} {
		if !strings.Contains(out.Source, want) {
			t.Errorf("missing %q in:\n%s", want, out.Source)
		}
	}
}

func TestGMPTransformVariants(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x = 2 ** 100\nx = x * x\nprint(x)\n", "mpz_mul(x, x, x)"},
		{"x = 2 ** 100\ni = 0\nwhile i < 5:\n    x = x + 1\n    i = i + 1\nprint(x)\n", "mpz_add_ui(x, x, 1)"},
		{"x = 2 ** 200\nx = x % 1000000007\nprint(x)\n", "mpz_fdiv_r_ui(x, x, 1000000007)"},
		{"x = 2 ** 100\nprint(x)\n", `gmp_printf("%Zd\n", x)`},
	} {
		out, ok, err := AnnotateGMP(tc.src)
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		if !ok {
			t.Errorf("%q: expected a transform", tc.src)
			continue
		}
		if !strings.Contains(out.Source, tc.want) {
			t.Errorf("%q: missing %q in:\n%s", tc.src, tc.want, out.Source)
		}
	}
}

func TestGMPDeclines(t *testing.T) {
	for _, src := range []string{
		"x = 0\nprint(x)\n",                  // no bigint
		"x = 2 ** 100\nprint(x + x)\n",       // expression the printer can't rewrite
		"x = 2 ** 100\nif x > 0:\n    pass\n",// bigint in a condition
		"for x in xs:\n    pass\n",           // non-range iteration
		"x = 2 ** 100\ndef f():\n    return x\n", // function scope (out of subset)
		"include = 100000\nx = 2 ** 100\nprint(x)\nprint(include)\n", // Cython-reserved name: .pyx unparseable
	} {
		if _, ok, err := AnnotateGMP(src); err != nil || ok {
			t.Errorf("%q: expected decline, got ok=%v err=%v", src, ok, err)
		}
	}
}
