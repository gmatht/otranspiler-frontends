// autocython_test.go — the annotation-pass gate. Every declaration is a
// proof: the pass must type the provable scalar shapes and refuse the rest.
package pylib

import (
	"strings"
	"testing"
)

func typed(out *CythonOutput, name string) bool {
	for _, n := range out.Typed {
		if n == name {
			return true
		}
	}
	return false
}

func TestAnnotateRollingHash(t *testing.T) {
	// `i` is a literal-bounded range counter -> proved. `h` is reassigned
	// with arithmetic (`h = (h*31+i) % M`), whose intermediate-fit needs a
	// range analysis, so it is refused (stays exact Python int).
	src := "h = 0\nfor i in range(2000000):\n    h = (h * 31 + i) % 1000000007\nprint(h)\n"
	out, err := AnnotateCython(src)
	if err != nil {
		t.Fatal(err)
	}
	if !typed(out, "i") {
		t.Fatalf("expected i typed; got %v", out.Typed)
	}
	if typed(out, "h") {
		t.Fatalf("h must be refused (unproved arithmetic); got %v", out.Typed)
	}
	if !strings.Contains(out.Source, "cython.declare(i=cython.longlong)") {
		t.Fatalf("declaration missing:\n%s", out.Source)
	}
	// the emitted file is runnable Python: the original source is intact.
	if !strings.HasSuffix(out.Source, src) {
		t.Fatalf("source not preserved:\n%s", out.Source)
	}
}

func TestAnnotateTypedShapes(t *testing.T) {
	out, err := AnnotateCython("a = 1\nb = -5\nc = 0\nfor i in range(0, 10, 2):\n    pass\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c", "i"} {
		if !typed(out, n) {
			t.Fatalf("expected %s typed; got %v (refused %v)", n, out.Typed, out.Refused)
		}
	}
}

func TestAnnotateRefusesUnproven(t *testing.T) {
	for _, src := range []string{
		"x = 1.5\n",                        // float literal
		"x = n\n",                          // unknown name
		"x = True\n",                       // bool
		"x = a < b\n",                      // comparison -> bool
		"x = 99999999999999999999999\n",    // int literal > i64
		"for i in range(n):\n    pass\n",   // non-literal range bound
		"for i in range(3.5):\n    pass\n", // non-int bound
		"x = y = 0\n",                      // multi-target
		"x = 0\nx += 1\n",                  // augmented assignment
		// t101: a growing accumulator must NOT be typed (it overflows i64).
		"s = 1\ni = 0\nwhile i < 5:\n    s = s + 4000000000000000000\n    i = i + 1\nprint(s)\n",
	} {
		out, err := AnnotateCython(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(out.Typed) != 0 {
			t.Errorf("unexpectedly typed %q -> %v", src, out.Typed)
		}
	}
}

func TestAnnotateRejectsSyntaxError(t *testing.T) {
	if _, err := AnnotateCython("def f(:\n    pass\n"); err == nil {
		t.Fatal("expected a syntax error")
	}
}
