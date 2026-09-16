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
	out, err := AnnotateCython(src, DefaultOptions())
	if err != nil {
		t.Fatalf("annotate %q: %v", src, err)
	}
	return out
}

// TestAnnotateLevels pins the three levels: none declares nothing, simple
// proves i64 literals + literal-bounded counters, full adds the interval
// analysis (here the accumulator h). All are behaviour-preserving.
func TestAnnotateLevels(t *testing.T) {
	src := "h = 0\nfor i in range(2000000):\n    h = (h * 31 + i) % 1000000007\nprint(h)\n"
	none, err := AnnotateCython(src, Options{Level: OptNone})
	if err != nil || len(none.Typed) != 0 {
		t.Fatalf("none: typed=%v err=%v", none.Typed, err)
	}
	simple, err := AnnotateCython(src, Options{Level: OptSimple})
	if err != nil {
		t.Fatal(err)
	}
	if !typed(simple, "i") || typed(simple, "h") {
		t.Fatalf("simple: want i only, got %v", simple.Typed)
	}
	full := annotate(t, src)
	if !typed(full, "h") || !typed(full, "i") {
		t.Fatalf("full: want h and i, got %v", full.Typed)
	}
}

func TestAnnotateRollingHash(t *testing.T) {
	// `i` is a literal-bounded range counter -> int; `h`'s VALUE reaches the
	// fixed point [0, 1000000006] (which fits int on its own), but the
	// assignment `h = (h*31 + i) % M` computes the intermediate `h*31` in h's
	// C type, and that reaches 3.1e10 -> h's STORAGE must be long long. This
	// is the value-range vs storage-width split: the proof is about the value,
	// the declaration is about the storage.
	src := "h = 0\nfor i in range(2000000):\n    h = (h * 31 + i) % 1000000007\nprint(h)\n"
	out := annotate(t, src)
	if !typed(out, "h") || !typed(out, "i") {
		t.Fatalf("expected h and i typed; got %v (refused %v)", out.Typed, out.Refused)
	}
	if !strings.Contains(out.Source, "cython.declare(h=cython.longlong, i=cython.int)") {
		t.Fatalf("declaration missing or wrong width:\n%s", out.Source)
	}
	// the evidence keeps the two facts separate and explains the width
	ev, ok := out.EvidenceFor("h")
	if !ok {
		t.Fatalf("no evidence for h: %+v", out.Evidence)
	}
	if ev.Lo != 0 || ev.Hi != 1000000006 {
		t.Fatalf("h value range: got [%d,%d]", ev.Lo, ev.Hi)
	}
	if ev.Width != WidthI64 || ev.WidthFrom != fromIntermediate || ev.Intermediate != "h*31" {
		t.Fatalf("h width evidence: %+v", ev)
	}
	if !strings.Contains(ev.Reason(), "h*31 intermediate needs long long") {
		t.Fatalf("h reason not honest about the intermediate: %q", ev.Reason())
	}
	if evi, _ := out.EvidenceFor("i"); evi.Width != WidthI32 {
		t.Fatalf("i must earn int, got %v", evi.Width)
	}
	// the emitted file is still runnable Python: the source is intact.
	if !strings.HasSuffix(out.Source, src) {
		t.Fatalf("source not preserved:\n%s", out.Source)
	}
}

// TestAnnotateWidthSmallRange pins the other half of the split: a variable
// whose value range AND whose every assigned intermediate fit 32 bits earns
// `int`, not the unconditional `long long` the pass used to emit.
func TestAnnotateWidthSmallRange(t *testing.T) {
	src := "h = 0\nfor i in range(10):\n    h = (h + i) % 7\nprint(h)\n"
	out := annotate(t, src)
	if !strings.Contains(out.Source, "cython.declare(h=cython.int, i=cython.int)") {
		t.Fatalf("small-range scalars must earn int:\n%s", out.Source)
	}
	for _, n := range []string{"h", "i"} {
		ev, ok := out.EvidenceFor(n)
		if !ok || ev.Width != WidthI32 {
			t.Fatalf("%s must be i32 with evidence; got %+v", n, ev)
		}
		if !strings.Contains(ev.Reason(), "fits int") {
			t.Fatalf("%s reason: %q", n, ev.Reason())
		}
	}
	// a value range that needs more than 32 bits is i64 for that reason alone
	big := annotate(t, "x = 5000000000\n")
	ev, ok := big.EvidenceFor("x")
	if !ok || ev.Width != WidthI64 || ev.WidthFrom != fromValue {
		t.Fatalf("x must be i64 from its value range; got %+v", ev)
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
		{"x = n\n", "x"},
		{"x = True\n", "x"},
		{"x = a < b\n", "x"},
		{"x = 99999999999999999999999\n", "x"},
		{"for i in range(n):\n    pass\n", "i"},
		{"for i in range(3.5):\n    pass\n", "i"},
		{"for x in xs:\n    pass\n", "x"},
		{"x = 0\nx += n\n", "x"},
		{"x = 9223372036854775807\nx += 1\n", "x"},
		{"x = 9223372036854775807\ny = x + 1\n", "y"},
		{"x = 4000000000\ny = x * x\n", "y"},
		{"x = -5\ny = x % 3\n", "y"},
		{"x = -5\ny = x // 3\n", "y"},
		{"x = 0\ntry:\n    x = 5\nexcept:\n    pass\n", "x"},
		{"x = 0\nmatch v:\n    case 1:\n        x = 1\n", "x"},
		{"i = 0\nwhile i < n:\n    i = i + 1\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i * 2\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i + j\n", "i"},
		{"i = 0\nwhile i < 5:\n    i = i - 1\n", "i"},            // wrong direction
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

func TestAnnotateScalarCoverage(t *testing.T) {
	for _, tc := range []struct{ src, name string }{
		{"x = y = 0\n", "y"},
		{"x = 0\nx += 5\n", "x"},
		{"x = 10\nx -= 3\n", "x"},
		{"x = 3\nx *= 4\n", "x"},
		{"x = -5\ny = abs(x)\n", "y"},
		{"x = 3\ny = min(x, 10)\n", "y"},
		{"x = 3\ny = max(x, 1)\n", "y"},
		{"x = 3\ny = int(x)\n", "y"},
	} {
		out := annotate(t, tc.src)
		if !typed(out, tc.name) {
			t.Errorf("%q: expected %s typed; got %v (refused %v)", tc.src, tc.name, out.Typed, out.Refused)
		}
	}
}

// TestAnnotateBoundedListLoop pins the array-length trip-count rule: a `for`
// over a proved list runs exactly len steps with the target bound to the
// element interval, so an accumulator converges instead of widening to ⊤.
func TestAnnotateBoundedListLoop(t *testing.T) {
	src := "arr = [3, 1, 4, 1, 5, 9, 2, 6]\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n"
	out := annotate(t, src)
	if !typed(out, "total") || !typed(out, "v") {
		t.Fatalf("expected total and v typed; got %v (refused %v)", out.Typed, out.Refused)
	}
	if typed(out, "arr") {
		t.Fatalf("arr is a Python list, not a scalar: %v", out.Typed)
	}
	if !strings.Contains(out.Source, "cython.declare(total=cython.int, v=cython.int)") {
		t.Fatalf("declaration missing or wrong width:\n%s", out.Source)
	}
	ev, ok := out.EvidenceFor("total")
	if !ok || ev.Lo != 8 || ev.Hi != 72 {
		t.Fatalf("total range: got %+v", ev)
	}
	if ev.Width != WidthI32 {
		t.Fatalf("total must earn int, got %v", ev.Width)
	}
	if evv, ok := out.EvidenceFor("v"); !ok || evv.Lo != 1 || evv.Hi != 9 {
		t.Fatalf("v range: got %+v", evv)
	}
	// simple level does not run the interval analysis: still refused there
	if simple, _ := AnnotateCython(src, Options{Level: OptSimple}); typed(simple, "total") || typed(simple, "v") {
		t.Fatalf("simple must refuse the accumulator: %v", simple.Typed)
	}
}

func TestAnnotateBoundedListShapes(t *testing.T) {
	// a literal iterable needs no name at all
	if out := annotate(t, "total = 0\nfor v in [3, 1, 4]:\n    total = total + v\nprint(total)\n"); !typed(out, "total") {
		t.Fatalf("literal iterable: total refused: %v", out.Refused)
	} else if ev, _ := out.EvidenceFor("total"); ev.Lo != 3 || ev.Hi != 12 {
		t.Fatalf("literal iterable: total range got [%d,%d], want [3,12]", ev.Lo, ev.Hi)
	}
	// an empty list runs the body never, so the target stays ⊤ (unbound) and
	// poisons the accumulator's only arithmetic use back to ⊤: refused, honestly
	if out := annotate(t, "total = 0\nfor v in []:\n    total = total + v\nprint(total)\n"); typed(out, "total") || typed(out, "v") {
		t.Fatalf("empty list: typed=%v refused=%v", out.Typed, out.Refused)
	}
	// nested bounded loops multiply trips exactly
	if out := annotate(t, "total = 0\nfor a in [1, 2]:\n    for b in [3, 4]:\n        total = total + a * b\nprint(total)\n"); !typed(out, "total") {
		t.Fatalf("nested: total refused: %v", out.Refused)
	} else if ev, _ := out.EvidenceFor("total"); ev.Lo != 12 || ev.Hi != 32 {
		t.Fatalf("nested: total range got [%d,%d], want [12,32]", ev.Lo, ev.Hi)
	}
	// an unknown-length iterable still widens to ⊤
	if out := annotate(t, "total = 0\nfor v in xs:\n    total = total + v\nprint(total)\n"); typed(out, "total") || typed(out, "v") {
		t.Fatalf("unknown iterable must be refused: %v", out.Typed)
	}
	// i64 overflow in the accumulation is ⊤, not a wrap
	if out := annotate(t, "total = 0\nfor v in [9223372036854775807, 9223372036854775807]:\n    total = total + v\nprint(total)\n"); typed(out, "total") {
		t.Fatalf("overflowing accumulator must be refused: %v", out.Typed)
	}
	// over the unroll cap a sum-shaped body is handled by the closed form
	// (length × element), not by unrolling — exact, O(1), and correctly typed
	big := "total = 0\nfor v in [" + strings.Repeat("7,", 299) + "7]:\n    total = total + v\nprint(total)\n"
	if out := annotate(t, big); !typed(out, "total") {
		t.Fatalf("over-cap sum loop must be typed via closed form: %v", out.Refused)
	} else if ev, _ := out.EvidenceFor("total"); ev.Lo != 2100 || ev.Hi != 2100 {
		t.Fatalf("over-cap sum loop range got [%d,%d], want [2100,2100]", ev.Lo, ev.Hi)
	}
	// a non-sum shape over the cap still falls back to the fixed point and is refused
	big2 := "total = 0\nfor v in [" + strings.Repeat("7,", 299) + "7]:\n    total = total + v\n    w = v+1\nprint(total)\n"
	if out := annotate(t, big2); typed(out, "total") {
		t.Fatalf("over-cap non-sum loop must be refused: %v", out.Typed)
	}
}

func TestAnnotateBoundedListRefusals(t *testing.T) {
	// anything that may share or mutate the list drops the fact, and the
	// accumulator goes back to ⊤
	for _, src := range []string{
		// method call in the body
		"arr = [1, 2]\ntotal = 0\nfor v in arr:\n    arr.append(v)\n    total = total + v\nprint(total)\n",
		// subscript store in the body
		"arr = [1, 2]\ntotal = 0\nfor v in arr:\n    arr[0] = v\n    total = total + v\nprint(total)\n",
		// rebinding the iterable in the body
		"arr = [1, 2]\ntotal = 0\nfor v in arr:\n    arr = [9]\n    total = total + v\nprint(total)\n",
		// the target IS the iterable
		"arr = [1, 2]\nfor arr in arr:\n    pass\n",
		// passed to an unknown callee before the loop
		"arr = [1, 2]\nfoo(arr)\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n",
		// aliased, then mutated through the alias
		"arr = [1, 2]\nb = arr\nb.append(3)\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n",
		// shared from birth by a multi-target assignment
		"x = y = [1, 2]\ntotal = 0\nfor v in x:\n    total = total + v\nprint(total)\n",
		// mutated through a closure the flow pass does not cross
		"arr = [1, 2]\ndef f():\n    arr.append(3)\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n",
		// a non-int element is no list at all
		"arr = [1, \"s\"]\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n",
	} {
		if out := annotate(t, src); typed(out, "total") || typed(out, "v") {
			t.Errorf("%q: must be refused; typed=%v", src, out.Typed)
		}
	}
	// reads through pure builtins keep the fact
	if out := annotate(t, "arr = [3, 1, 4]\nprint(len(arr))\ntotal = 0\nfor v in arr:\n    total = total + v\nprint(total)\n"); !typed(out, "total") {
		t.Fatalf("pure-builtin reads must keep the fact: refused=%v", out.Refused)
	}
}

func TestAnnotateRejectsSyntaxError(t *testing.T) {
	if _, err := AnnotateCython("def f(:\n    pass\n", DefaultOptions()); err == nil {
		t.Fatal("expected a syntax error")
	}
}

func TestAnnotateFunctionScope(t *testing.T) {
	src := "def f():\n    h = 0\n    for i in range(10):\n        h = (h + i) % 7\n    return h\n"
	out := annotate(t, src)
	if !typed(out, "h") || !typed(out, "i") {
		t.Fatalf("expected h and i typed; got %v (refused %v)", out.Typed, out.Refused)
	}
	if !strings.Contains(out.Source, "    cython.declare(h=cython.int, i=cython.int)") {
		t.Fatalf("function declaration missing:\n%s", out.Source)
	}
}

func TestAnnotateFunctionParamsNotTyped(t *testing.T) {
	// a parameter can be any object at the call site, so it is never declared
	out := annotate(t, "def f(x):\n    x = 0\n    return x\n")
	if typed(out, "x") {
		t.Fatalf("parameter x must not be declared: %v", out.Typed)
	}
}

func TestAnnotateFunctionDocstring(t *testing.T) {
	out := annotate(t, "def f():\n    \"\"\"d\"\"\"\n    n = 5\n    return n\n")
	i := strings.Index(out.Source, `"""d"""`)
	j := strings.Index(out.Source, "cython.declare(n=")
	if i < 0 || j < 0 || j < i {
		t.Fatalf("declaration must follow the docstring:\n%s", out.Source)
	}
}

func TestAnnotatePyxMode(t *testing.T) {
	opts := Options{Level: OptFull, Mode: ModePyx}
	out, err := AnnotateCython("h = 0\nfor i in range(10):\n    h = (h + i) % 7\nprint(h)\n", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Source, "cdef int h, i") {
		t.Fatalf("expected cdef declaration:\n%s", out.Source)
	}
	if strings.Contains(out.Source, "import cython") {
		t.Fatalf("pyx mode must not import cython:\n%s", out.Source)
	}
	// function-local cdef
	fn, err := AnnotateCython("def f():\n    h = 0\n    return h\n", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fn.Source, "    cdef int h") {
		t.Fatalf("expected function-local cdef:\n%s", fn.Source)
	}
	// pure-Python mode is unchanged
	py, _ := AnnotateCython("h = 0\n", DefaultOptions())
	if !strings.Contains(py.Source, "cython.declare(h=cython.int)") {
		t.Fatalf("py mode changed:\n%s", py.Source)
	}
	// a wider intermediate selects cdef long long, and the two widths are
	// emitted as separate declarations (the C loop shape).
	wide, _ := AnnotateCython("h = 0\nfor i in range(2000000):\n    h = (h * 31 + i) % 1000000007\nprint(h)\n", opts)
	if !strings.Contains(wide.Source, "cdef int i") || !strings.Contains(wide.Source, "cdef long long h") {
		t.Fatalf("width split not emitted in pyx mode:\n%s", wide.Source)
	}
}

func TestAnnotateOverflowOperandNotTyped(t *testing.T) {
	// typing x would make `x + 1` a C wrap: the OPERAND must be refused even
	// though only the RESULT (y) is unprovable.
	if out := annotate(t, "x = 9223372036854775807\ny = x + 1\nprint(y)\n"); typed(out, "x") {
		t.Fatalf("x must not be typed (x+1 overflows i64): %v", out.Typed)
	}
	// shifts / bitwise have C semantics that differ from Python for big ints
	if out := annotate(t, "a = 1\nb = 100\nc = a << b\nprint(c)\n"); typed(out, "a") || typed(out, "b") {
		t.Fatalf("shift operands must not be typed: %v", out.Typed)
	}
	if out := annotate(t, "a = 1\nb = 2\nc = a & b\nprint(c)\n"); typed(out, "a") {
		t.Fatalf("bitwise operand must not be typed: %v", out.Typed)
	}
	// but a provably-safe arithmetic expression keeps its operands typed
	if out := annotate(t, "h = 0\nfor i in range(10):\n    h = (h * 31 + i) % 7\nprint(h)\n"); !typed(out, "h") || !typed(out, "i") {
		t.Fatalf("safe arithmetic must stay typed: %v", out.Typed)
	}
}

func TestAnnotateIdentityNotTyped(t *testing.T) {
	// `a is b`: a C value has no Python identity, so both operands are refused
	if out := annotate(t, "a = 1000\nb = a + 0\nprint(a is b)\n"); typed(out, "a") || typed(out, "b") {
		t.Fatalf("is-operands must not be typed: %v", out.Typed)
	}
	// `id(x)` boxes a fresh object each call
	if out := annotate(t, "a = 5\nprint(id(a))\n"); typed(out, "a") {
		t.Fatalf("id() must disable typing: %v", out.Typed)
	}
}

func TestAnnotateFloat(t *testing.T) {
	out := annotate(t, "a = 1.5\nb = 2.0\nc = a * b + 1\nd = 1\ne = d / 2\n")
	for _, n := range []string{"a", "b", "c", "e"} {
		if !typed(out, n) {
			t.Fatalf("expected %s typed; got %v", n, out.Typed)
		}
	}
	if !strings.Contains(out.Source, "c=cython.double") || !strings.Contains(out.Source, "d=cython.int") {
		t.Fatalf("expected double c and int d:\n%s", out.Source)
	}
	// a single `/` (true division) is a float even for int operands
	if !strings.Contains(out.Source, "e=cython.double") {
		t.Fatalf("true division must be double:\n%s", out.Source)
	}
	// pyx mode groups by C type
	pyx, _ := AnnotateCython("a = 1.5\nd = 1\n", Options{Level: OptFull, Mode: ModePyx})
	if !strings.Contains(pyx.Source, "cdef double a") || !strings.Contains(pyx.Source, "cdef int d") {
		t.Fatalf("pyx grouping wrong:\n%s", pyx.Source)
	}
	// mixed int/float assignment is neither
	if out2 := annotate(t, "x = 1\nx = 1.5\n"); typed(out2, "x") {
		t.Fatalf("mixed x must not be typed: %v", out2.Typed)
	}
}

func TestAnnotateIntList(t *testing.T) {
	src := "xs = []\nfor i in range(10):\n    xs.append(i * i)\nprint(len(xs))\nprint(xs[3])\n"
	out, _ := AnnotateCython(src, Options{Level: OptFull, Mode: ModePyx})
	for _, want := range []string{
		"_sh_push_i64", "cdef long long *xs = NULL", "print(xs_len)",
		"xs = _sh_push_i64(xs, &xs_len, &xs_cap, i*i)",
	} {
		if !strings.Contains(out.Source, want) {
			t.Fatalf("missing %q:\n%s", want, out.Source)
		}
	}
	if strings.Contains(out.Source, "xs = []") {
		t.Fatalf("empty-list assignment not removed:\n%s", out.Source)
	}
	// pure-Python mode has no C pointer: leave the list alone
	if py, _ := AnnotateCython(src, DefaultOptions()); strings.Contains(py.Source, "_sh_push_i64") {
		t.Fatalf("container leaked into py mode:\n%s", py.Source)
	}
	// REFUSE > GUESS: unsupported uses stay a Python list
	for _, bad := range []string{
		"xs = []\nfor i in range(3):\n    xs.append(i)\nfor v in xs:\n    print(v)\n", // iteration
		"xs = []\nfor i in range(3):\n    xs.append(\"s\")\nprint(len(xs))\n",         // non-int
		"xs = []\nfor i in range(3):\n    xs.append(i)\nprint(sum(xs))\n",             // reduction
		"xs = []\nfor i in range(3):\n    xs.append(i)\nprint(xs)\n",                  // bare read
	} {
		if o, _ := AnnotateCython(bad, Options{Level: OptFull, Mode: ModePyx}); strings.Contains(o.Source, "_sh_push_i64") {
			t.Errorf("unexpected transform for %q:\n%s", bad, o.Source)
		}
	}
}

func TestAnnotateFunctionGlobalNotShadowed(t *testing.T) {
	// a function-local cdef `g` would shadow `global g`
	out := annotate(t, "g = 0\ndef f():\n    global g\n    g = 1\nf()\nprint(g)\n")
	for _, line := range strings.Split(out.Source, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "cython.declare(g=") && strings.HasPrefix(line, "    ") {
			t.Fatalf("function must not declare a global:\n%s", out.Source)
		}
	}
}

func TestAnnotateStringNotNumeric(t *testing.T) {
	// an operator INSIDE a string literal must not look like arithmetic
	for _, src := range []string{
		"s = \"a/b\"\n",
		"s = 'x+y'\n",
		"s = (\"a/b\" \"c\")\n",
		"d = \"mappings/VENDORS/MICSFT/MAC/LATIN2.TXT\"\n",
	} {
		if out := annotate(t, src); len(out.Typed) != 0 {
			t.Errorf("%q must not be typed: %v", src, out.Typed)
		}
	}
}

func TestAnnotateFutureImportOrder(t *testing.T) {
	out, _ := AnnotateCython("from __future__ import annotations\nx = 0\n", DefaultOptions())
	if !strings.Contains(out.Source, "from __future__ import annotations\nimport cython") {
		t.Fatalf("import cython must follow the future import:\n%s", out.Source)
	}
	// the module docstring stays the first statement
	out2, _ := AnnotateCython("\"\"\"d\"\"\"\nfrom __future__ import annotations\nx = 0\n", DefaultOptions())
	lines := strings.Split(out2.Source, "\n")
	if len(lines) < 2 || lines[1] != `"""d"""` {
		t.Fatalf("docstring must stay first:\n%s", out2.Source)
	}
}

func TestFloatRequiresNumericOperands(t *testing.T) {
	// An operator over an unknown operand is not a float: `T / 2` may be
	// tensor division, and `2.0 * T` may be anything.
	for _, src := range []string{
		"u = a / b\n",
		"x = 2.0 * a\n",
		"m = min(t1, t2)\n",
	} {
		if out := annotate(t, src); len(out.Typed) != 0 {
			t.Errorf("%q: expected no declarations, got %v", src, out.Typed)
		}
	}
	// Controls: int/int division and float*int stay double.
	if out := annotate(t, "a = 7\nx = a / 2\n"); !typed(out, "x") {
		t.Errorf("int/int division must stay double; typed=%v", out.Typed)
	}
	if out := annotate(t, "a = 1.5\nx = 2.0 * a\n"); !typed(out, "x") {
		t.Errorf("float*int must stay double; typed=%v", out.Typed)
	}
}

func TestPyxDeclinesReservedIdentifiers(t *testing.T) {
	pyx := Options{Level: OptFull, Mode: ModePyx}
	for name, src := range map[string]string{
		"from-import": "from x import include\ny = 2\nprint(y)\n",
		"attribute":   "print(x.include)\n",
		"fstring": `v = 1
print(f"{v} {include}")
`,
		"cdef-name": "include = 5\nprint(include)\n",
	} {
		out, err := AnnotateCython(src, pyx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !out.Declined {
			t.Errorf("%s: expected Declined=true", name)
		}
		if strings.Contains(out.Source, "cdef ") {
			t.Errorf("%s: fallback must not contain cdef:\n%s", name, out.Source)
		}
		// no declarations here, so no `import cython` either: the fallback
		// is the source verbatim (plus header), which is exactly what a
		// declined file must be.
	}
	// An innocent f-string with no reserved word still emits `.pyx`.
	out2, err := AnnotateCython("v = 1\nprint(f\"{v}\")\n", pyx)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Declined {
		t.Errorf("innocent f-string must not decline")
	}
	// ModePy is unaffected by the rule.
	out, err := AnnotateCython("from x import include\ny = 2\nprint(y)\n", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if out.Declined {
		t.Errorf("ModePy must never decline")
	}
}

func TestFloatAugAssignNeedsNumericRHS(t *testing.T) {
	// `x = 0.0; x += <unknown>` must not be double: `double += obj`
	// raises where Python returns a value (e.g. `0.0 + tensor`).
	for _, src := range []string{
		"x = 0.0\nx += f()\n",
		"x = 0.0\nx += t\n",
		"x = 1.5\nx <<= 1\n",
		"x = 1.5\nx /= t\n",
	} {
		if out := annotate(t, src); typed(out, "x") {
			t.Errorf("%q: x must not be typed; got %v", src, out.Typed)
		}
	}
	// Numeric aug operands keep the double.
	if out := annotate(t, "x = 0.0\nx += 2\n"); !typed(out, "x") {
		t.Errorf("numeric aug must keep double; typed=%v", out.Typed)
	}
	if out := annotate(t, "x = 1.5\ni = 0\nwhile i < 3:\n    x += i * 0.5\n    i = i + 1\n"); !typed(out, "x") {
		t.Errorf("int-expr aug must keep double; typed=%v", out.Typed)
	}
}

func TestScopeBindingsKillDomains(t *testing.T) {
	// Every non-plain binding form must drop a scalar: with/except/del/
	// import/match/walrus targets, annotated rebinds, and names bound only
	// inside functions (module scope must not claim them).
	for _, src := range []string{
		"fh = 0\nwith open(f) as fh:\n    print(fh.read())\n",
		"e = 0\ntry:\n    1/0\nexcept ZeroDivisionError as e:\n    print(e)\n",
		"x = 1\ndel x\nprint(\"done\")\n",
		"x = 1\nimport os as x\nprint(x)\n",
		"x = 0\nmatch [1, 2]:\n    case [x, y]:\n        print(x)\n",
		"x = 1\ny = [(x := str(i)) for i in range(3)]\n",
		"x = 1.5\nx: str = \"s\"\n",
		"x = 0\ndef f():\n    global x\n    x *= 1.5\n",
	} {
		if out := annotate(t, src); len(out.Typed) != 0 {
			t.Errorf("%q: expected no declarations, got %v", src, out.Typed)
		}
	}
	// A float bound only inside a function must not gain a MODULE
	// declaration (it printed 0.0 where CPython raises NameError); the
	// function-local declaration itself stays.
	out := annotate(t, "def f():\n    y = 1.5\n    return y\nprint(y)\n")
	if strings.Contains(out.Source, "\ncython.declare(y=") {
		t.Errorf("function-local float leaked to module scope:\n%s", out.Source)
	}
	if !strings.Contains(out.Source, "    cython.declare(y=") {
		t.Errorf("function-local float must stay declared:\n%s", out.Source)
	}
	// Controls that must STAY typed.
	for _, tc := range []struct{ src, name string }{
		{"x = 0\nx += 2\nprint(x)\n", "x"},
		{"y = (n := 5)\nprint(n)\n", "n"},
		{"if (n := 6):\n    print(n)\n", "n"},
		{"x = 5\ndel y\nprint(x)\n", "x"},
		{"x = 5\nwith open(f) as fh:\n    print(fh.read())\nprint(x)\n", "x"},
	} {
		if out := annotate(t, tc.src); !typed(out, tc.name) {
			t.Errorf("%q: expected %s typed; got %v", tc.src, tc.name, out.Typed)
		}
	}
}
