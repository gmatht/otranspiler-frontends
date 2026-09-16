// dualguard_test.go — the entry-guarded dual arm's gate.
//
// The policy this file follows (AGENTS: structural assertions, not "no crash"):
// the emitted source is checked for the STRUCTURE that carries the soundness
// argument — the guard's bounds, the parameter's C type, and the verbatim body —
// because those are exactly the facts whose drift would be a miscompile. The
// shell oracles (coverage/py2cy-parity.sh, coverage/semantics-parity.sh) then
// compile the result with `cython --embed` and require CPython-identical stdout,
// including call sites that FAIL the guard (a float, and an int far outside it).
package pylib

import (
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// dualSrc is the canonical shape: the accumulator and the loop counter are
// unprovable ONLY because `n` is a parameter.
const dualSrc = "def scaled(n):\n    t = 0\n    for i in range(1000):\n        t = (t + i * n) % 1000000007\n    return t\nprint(scaled(7))\n"

func TestDualGuardEmitsGuardedTwin(t *testing.T) {
	out := annotate(t, dualSrc)
	if len(out.Dual) != 1 {
		t.Fatalf("want 1 dual arm, got %+v", out.Dual)
	}
	d := out.Dual[0]
	if d.Func != "scaled" || d.Param != "n" {
		t.Fatalf("dual arm identity: %+v", d)
	}
	// the locals the guard buys — the reason the arm exists at all
	if len(d.Locals) == 0 {
		t.Fatalf("dual arm with no typed local: %+v", d)
	}
	// the twin is a cfunc (Cython does not expose it to Python) with the
	// parameter typed at least as wide as the guard
	if !strings.Contains(out.Source, "@cython.cfunc\n@cython.locals(n=cython.longlong,") {
		t.Fatalf("twin decorators missing:\n%s", out.Source)
	}
	if !strings.Contains(out.Source, "def _py2cy_fast_scaled(n):") {
		t.Fatalf("twin definition missing:\n%s", out.Source)
	}
	// the guard is exactly the assumed interval, with an exact-type check
	guard := guardLine(t, out.Source)
	want := "if type(n) is int and 0 <= n <= 2147483647:"
	if guard != want {
		t.Fatalf("guard:\n got %q\nwant %q", guard, want)
	}
	if !strings.Contains(out.Source, "        return _py2cy_fast_scaled(n)") {
		t.Fatalf("guard does not call the twin:\n%s", out.Source)
	}
	// the exact arm is the source program, verbatim
	if !strings.Contains(out.Source, "\n    t = 0\n    for i in range(1000):\n        t = (t + i * n) % 1000000007\n    return t\n") {
		t.Fatalf("exact arm not preserved verbatim:\n%s", out.Source)
	}
	// `import cython` must be present even though nothing else needed it: the
	// twin's own decorators are the only reason it is there.
	if !strings.Contains(out.Source, "import cython\n") {
		t.Fatalf("missing `import cython` for the twin:\n%s", out.Source)
	}
	// body appears twice (fast twin + exact arm) and executes once: exactly one
	// call site, and both arms are separate definitions.
	if n := strings.Count(out.Source, "def scaled(n):"); n != 1 {
		t.Fatalf("dispatcher must be defined once, got %d:\n%s", n, out.Source)
	}
	if n := strings.Count(out.Source, "return _py2cy_fast_scaled(n)"); n != 1 {
		t.Fatalf("twin must have one call site, got %d:\n%s", n, out.Source)
	}
}

// guardLine returns the trimmed guard prologue line of the dispatcher.
func guardLine(t *testing.T, src string) string {
	t.Helper()
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "type(") && strings.HasSuffix(strings.TrimSpace(l), ":") {
			return strings.TrimSpace(l)
		}
	}
	t.Fatalf("no guard line in:\n%s", src)
	return ""
}

// The invariant that carries the soundness argument: the guard's bounds ARE the
// interval the twin was proved under. A guard wider than the proof is the
// miscompile; a narrower one is dead code. Both are excluded by construction,
// and this test pins the construction against the emitted text.
func TestDualGuardBoundsAreTheAssumedRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			// the intermediate `i * n` overflows i64 for a full-range
			// non-negative `n`, so the widest hypothesis fails and the i32 one
			// wins: the guard narrows with the proof
			"nonneg-i32", dualSrc,
			"if type(n) is int and 0 <= n <= 2147483647:",
		},
		{
			// a body that only needs `n` to be a non-negative int keeps the
			// WIDEST guard, so the fast arm covers the whole int domain
			"nonneg-i64", "def f(n):\n    t = n % 1000000007\n    return t\n",
			"if type(n) is int and 0 <= n <= 9223372036854775807:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := annotate(t, tc.src)
			if len(out.Dual) != 1 {
				t.Fatalf("%s: want 1 dual arm, got %+v", tc.name, out.Dual)
			}
			got := guardLine(t, out.Source)
			if got != tc.want {
				t.Fatalf("%s: guard\n got %q\nwant %q", tc.name, got, tc.want)
			}
			// and the assumed range the report exposes is the same one
			d := out.Dual[0]
			lo, hi := strconv.FormatInt(d.Lo, 10), strconv.FormatInt(d.Hi, 10)
			if !strings.Contains(got, lo+" <= "+d.Param+" <= "+hi) {
				t.Fatalf("%s: report %d..%d does not match the guard %q", tc.name, d.Lo, d.Hi, got)
			}
		})
	}
}

// The parameter is typed `cython.longlong` in the twin — never narrower than the
// guard, so a guard-accepted value always converts exactly. Locals may be
// narrower (their values are computed inside the function and bounded by the
// proof), which is why the two cases are asserted separately.
func TestDualParamNeverNarrowerThanGuard(t *testing.T) {
	out := annotate(t, dualSrc)
	m := regexp.MustCompile(`@cython\.locals\(([^)]*)\)`).FindStringSubmatch(out.Source)
	if m == nil {
		t.Fatalf("no @cython.locals in:\n%s", out.Source)
	}
	decls := map[string]string{}
	for _, part := range strings.Split(m[1], ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			decls[kv[0]] = kv[1]
		}
	}
	if decls["n"] != "cython.longlong" {
		t.Fatalf("guarded parameter must be longlong, got %q (%v)", decls["n"], decls)
	}
	// a local that earns `int` proves the widths are still per-name evidence
	if decls["i"] != "cython.int" {
		t.Fatalf("loop counter should stay int, got %q (%v)", decls["i"], decls)
	}
}

func TestDualGuardRefusals(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"module-scope only", "t = 0\nfor i in range(10):\n    t = (t + i) % 7\nprint(t)\n"},
		{"no parameter", "def f():\n    t = 0\n    for i in range(10):\n        t = (t + i * 3) % 7\n    return t\n"},
		{"only the param would be typed", "def f(a, b):\n    c = a % b\n    return c\n"},
		{"docstring first", "def f(n):\n    \"\"\"doc\"\"\"\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"decorated", "@deco\ndef f(n):\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"one-line body", "def f(n): return n + 1\n"},
		{"nested def", "def outer(n):\n    def inner():\n        return n\n    return inner()\n"},
		{"global statement", "g = 0\ndef f(n):\n    global g\n    g = n\n    return g\n"},
		{"star args", "def f(*args):\n    t = 0\n    for i in range(10):\n        t = (t + i) % 7\n    return t\n"},
		{"kwargs", "def f(**kw):\n    t = 0\n    for i in range(10):\n        t = (t + i) % 7\n    return t\n"},
		{"keyword-only", "def f(n, *, m):\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"async", "async def f(n):\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"method", "class C:\n    def f(self, n):\n        t = 0\n        for i in range(10):\n            t = (t + i * n) % 7\n        return t\n"},
		{"twin name taken", "_py2cy_fast_f = 1\ndef f(n):\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"param rebound to a str", "def f(n):\n    n = \"s\"\n    t = 0\n    for i in range(10):\n        t = (t + i) % 7\n    return t\n"},
		{"param used with is", "def f(n):\n    t = 0\n    for i in range(10):\n        t = (t + i) % 7\n    print(n is t)\n    return t\n"},
		{"param bitwise-shifted", "def f(n):\n    t = 1\n    for i in range(10):\n        t = (t + i) % 7\n    print(n << 1)\n    return t\n"},
		{"provably bigint literal", "def f(n):\n    x = 340282366920938463463374607431768211456\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
		{"provably bigint pow", "def f(n):\n    x = 2 ** 100\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"},
	} {
		out, err := AnnotateCython(tc.src, DefaultOptions())
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(out.Dual) != 0 {
			t.Errorf("%s: must not dual-guard; got %+v\n%s", tc.name, out.Dual, out.Source)
		}
		if strings.Contains(out.Source, "@cython.cfunc") {
			t.Errorf("%s: no twin must be emitted:\n%s", tc.name, out.Source)
		}
	}
}

// The extra condition, positively stated: bigint evidence vetoes even when the
// guard WOULD buy a typed local, because the module's exact answer there is the
// GMP/object tier — and the dual twin must not dress an arbitrary-precision
// program in a guarded i64 arm.
func TestDualGuardBigintVetoBeatsBenefit(t *testing.T) {
	// (the same body with a small literal IS guarded — the pair isolates the veto)
	small := "def f(n):\n    x = 7\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n"
	if out := annotate(t, small); len(out.Dual) != 1 {
		t.Fatalf("control case must be guarded, got %+v", out.Dual)
	}
	for _, big := range []string{
		"def f(n):\n    x = 2 ** 100\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n",
		"def f(n):\n    x = 340282366920938463463374607431768211456\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n",
		// a chain: the constant propagates, so the value is still proved > i64
		"def f(n):\n    x = 2 ** 100\n    y = x * 2\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n",
		// a modulo of a big power is small: NO veto (the fold is exact)
		"def f(n):\n    x = 2 ** 200 % 1000000007\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\n",
	} {
		out := annotate(t, big)
		want := 0
		if strings.Contains(big, "% 1000000007") {
			want = 1 // the exact fold keeps this one small -> not provably bigint
		}
		if len(out.Dual) != want {
			t.Errorf("bigint veto: got %d dual arms, want %d for:\n%s\n%s", len(out.Dual), want, big, out.Source)
		}
	}
}

// The transform is invisible at the levels that do not run the interval lattice,
// and declined (not half-applied) in .pyx mode.
func TestDualGuardLevelsAndModes(t *testing.T) {
	for _, lvl := range []Level{OptNone, OptSimple} {
		out, err := AnnotateCython(dualSrc, Options{Level: lvl})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Dual) != 0 || strings.Contains(out.Source, "@cython.cfunc") {
			t.Errorf("level %v must not dual-guard:\n%s", lvl, out.Source)
		}
	}
	// .pyx cannot spell the twin: the pass declines to pure-Python mode and the
	// fallback still carries the guard (documented, and what the gate compiles)
	out, err := AnnotateCython(dualSrc, Options{Level: OptFull, Mode: ModePyx})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Declined || !strings.Contains(out.DeclineReason, "dual arm") {
		t.Fatalf("pyx must decline with a reason: declined=%v reason=%q", out.Declined, out.DeclineReason)
	}
	if len(out.Dual) != 1 || !strings.Contains(out.Source, "@cython.cfunc") {
		t.Fatalf("declined output must be the pure-Python one:\n%s", out.Source)
	}
}

// Multiple functions: one guard each, each under its own definition, and the
// guard's parameter is the one that bought the locals.
func TestDualGuardMultipleFunctions(t *testing.T) {
	src := "def a(n):\n    t = 0\n    for i in range(10):\n        t = (t + i * n) % 7\n    return t\ndef b(m):\n    u = 0\n    for j in range(4):\n        u = (u + j * m) % 5\n    return u\nprint(a(1) + b(2))\n"
	out := annotate(t, src)
	if len(out.Dual) != 2 {
		t.Fatalf("want 2 dual arms, got %+v", out.Dual)
	}
	for _, d := range out.Dual {
		if !strings.Contains(out.Source, "def _py2cy_fast_"+d.Func+"(") {
			t.Errorf("no twin for %s:\n%s", d.Func, out.Source)
		}
		if !strings.Contains(out.Source, "if type("+d.Param+") is int and") {
			t.Errorf("no guard for %s:\n%s", d.Func, out.Source)
		}
	}
	// twins precede their dispatchers, so a module-level call cannot run before
	// the twin exists
	for _, d := range out.Dual {
		twin := strings.Index(out.Source, "def _py2cy_fast_"+d.Func+"(")
		disp := strings.Index(out.Source, "def "+d.Func+"(")
		if twin < 0 || disp < 0 || twin > disp {
			t.Errorf("%s: twin must precede its dispatcher (twin@%d disp@%d)", d.Func, twin, disp)
		}
	}
}

// The constant folder is the bigint veto's EVIDENCE, so its parse is
// soundness-relevant in one direction only: a value it reports as outside i64
// must really be outside i64. These rows pin both directions and the precedence
// bug this table caught (`**` split before `%`, reading `2**200 % m` as
// `2 ** (200 % m)` and vetoing a value that is in fact small).
func TestDualBigintFold(t *testing.T) {
	for _, tc := range []struct {
		expr string
		huge bool // provably wider than the folder represents
		ok   bool // is a literal-only constant at all
		fits bool // the exact value fits i64
	}{
		{"9223372036854775807", false, true, true},
		{"9223372036854775808", false, true, false},
		{"2**63 - 1", false, true, true},           // exact, fits: not a veto
		{"2**63", false, true, false},              // exact, too wide: veto
		{"2**100", false, true, false},             // the canonical veto
		{"2**200 % 1000000007", false, true, true}, // big intermediate, small result
		{"2**10000 % 7", false, true, true},        // the divisor bounds it, so small
		{"2**10000", true, true, false},
		{"2**100 * 0", false, true, true}, // a zero collapse
		{"2**200 // 1000000007", false, true, false},
		{"-9223372036854775807 - 1", false, true, true}, // i64 min
		{"-9223372036854775807 - 2", false, true, false},
		{"n + 1", false, false, false}, // not a constant
		{"1 / 2", false, false, false}, // true division is a float
		{"x % 0", false, false, false}, // a zero divisor proves nothing
	} {
		v, huge, ok := foldBigConst(tc.expr, map[string]*big.Int{})
		if ok != tc.ok || huge != tc.huge {
			t.Errorf("%q: got ok=%v huge=%v, want ok=%v huge=%v", tc.expr, ok, huge, tc.ok, tc.huge)
			continue
		}
		if ok && !huge {
			if fits := v.IsInt64(); fits != tc.fits {
				t.Errorf("%q: exact %s fits i64 = %v, want %v", tc.expr, v, fits, tc.fits)
			}
		}
	}
}

// splitTop must not split inside `**`: the left `*` of a power is not a
// multiplicative operator, and treating it as one hands the evaluator a
// malformed operand (`2**200` -> `*200`).
func TestSplitTopSkipsPower(t *testing.T) {
	if l, _, r, ok := splitTop("2**200", "*%"); ok {
		t.Errorf("split inside `**`: l=%q r=%q", l, r)
	}
	if l, op, r, ok := splitTop("a*b", "*%"); !ok || op != "*" || l != "a" || r != "b" {
		t.Errorf("plain multiplication must still split: %q %q %q %v", l, op, r, ok)
	}
	// and the interval evaluator stays conservative (⊤), never a wrong bound
	if iv := evalText("2**200", env{}); iv.ok {
		t.Errorf("power is not modelled by the interval lattice, got %+v", iv)
	}
}
