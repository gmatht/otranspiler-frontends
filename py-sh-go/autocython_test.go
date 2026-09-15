// autocython_test.go — the annotation-pass gate. Every declaration is a
// proof: the interval analysis must type the provable scalar shapes and
// refuse anything it cannot bound (soundness beats coverage).
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

func annotate(t *testing.T, src string) *CythonOutput {
	t.Helper()
	out, err := AnnotateCython(src)
	if err != nil {
		t.Fatalf("annotate %q: %v", src, err)
	}
	return out
}

func TestAnnotateRollingHash(t *testing.T) {
	// `i` is a literal-bounded range counter; `h` reaches the fixed point
	// [0, 1000000006] via `% M` (with a provably non-negative lhs), and the
	// i64 intermediates never overflow -> both proved.
	src := "h = 0\nfor i in range(2000000):\n    h = (h * 31 + i) % 1000000007\nprint(h)\n"
	out := annotate(t, src)
	if !typed(out, "h") || !typed(out, "i") {
		t.Fatalf("expected h and i typed; got %v (refused %v)", out.Typed, out.Refused)
	}
	if !strings.Contains(out.Source, "cython.declare(h=cython.longlong, i=cython.longlong)") {
		t.Fatalf("declaration missing:\n%s", out.Source)
	}
	// the emitted file is still runnable Python: the source is intact.
	if !strings.HasSuffix(out.Source, src) {
		t.Fatalf("source not preserved:\n%s", out.Source)
	}
}

func TestAnnotateTypedShapes(t *testing.T) {
	out := annotate(t, "a = 1\nb = -5\nc = 0\nfor i in range(0, 10, 2):\n    pass\n")
	for _, n := range []string{"a", "b", "c", "i"} {
		if !typed(out, n) {
			t.Fatalf("expected %s typed; got %v (refused %v)", n, out.Typed, out.Refused)
		}
	}
}

func TestAnnotateWhileCounter(t *testing.T) {
	for _, tc := range []struct{ src, name string }{
		{"i = 0\nwhile i < 5:\n    i = i + 1\nprint(i)\n", "i"},
		{"i = 0\nwhile i < 5:\n    i += 2\nprint(i)\n", "i"},
		{"i = 10\nwhile i > 0:\n    i = i - 1\nprint(i)\n", "i"},
		{"i = 0\nwhile i <= 5:\n    i = i + 1\nprint(i)\n", "i"},
	} {
		out := annotate(t, tc.src)
		if !typed(out, tc.name) {
			t.Errorf("%q: expected %s typed; got %v (refused %v)", tc.src, tc.name, out.Typed, out.Refused)
		}
	}
}

func TestAnnotateRefusedShapes(t *testing.T) {
	for _, tc := range []struct{ src, refuse string }{
		{"x = 1.5\n", "x"},
		{"x = n\n", "x"},
		{"x = True\n", "x"},
		{"x = a < b\n", "x"},
		{"x = 99999999999999999999999\n", "x"},
		{"for i in range(n):\n    pass\n", "i"},
		{"for i in range(3.5):\n    pass\n", "i"},
		{"for x in xs:\n    pass\n", "x"},
		{"x = y = 0\n", "x"},
		{"x = 0\nx += 1\n", "x"},
		{"x = 9223372036854775807\ny = x + 1\n", "y"},
		{"x = 4000000000\ny = x * x\n", "y"},
		{"x = -5\ny = x % 3\n", "y"},
		{"x = -5\ny = x // 3\n", "y"},
		{"x = 0\ntry:\n    x = 5\nexcept:\n    pass\n", "x"},
		{"x = 0\nmatch v:\n    case 1:\n        x = 1\n", "x"},
		{"i = 0\nwhile i < n:\n    i = i + 1\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i * 2\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i + j\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i - 1\n", "i"},          // wrong direction
		{"i = 0\nwhile i < 5:\n    i = i + 1\n    i = 0\n", "i"}, // two updates
		// t101: a growing while accumulator must NOT be typed.
		{"s = 1\ni = 0\nwhile i < 5:\n    s = s + 4000000000000000000\n    i = i + 1\nprint(s)\n", "s"},
	} {
		out := annotate(t, tc.src)
		if typed(out, tc.refuse) {
			t.Errorf("%s: %q must be refused; typed=%v", tc.refuse, tc.src, out.Typed)
		}
	}
}

// The one non-refusal in the list above: a `%` with a non-negative lhs keeps
// the [0, m-1] bound.
func TestAnnotateNonNegativeMod(t *testing.T) {
	out := annotate(t, "x = 0\nfor i in range(10):\n    x = (x + i) % 7\n")
	if !typed(out, "x") {
		t.Fatalf("expected x typed; got %v", out.Typed)
	}
}

func TestAnnotateRejectsSyntaxError(t *testing.T) {
	if _, err := AnnotateCython("def f(:\n    pass\n"); err == nil {
		t.Fatal("expected a syntax error")
	}
}
