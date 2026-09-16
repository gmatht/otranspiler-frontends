// dualguard.go — the entry-guarded dual arm for a function whose integers are
// unprovable only because a PARAMETER is (docs/AUTO_CYTHON.md §11).
//
// A parameter can be any object at the call site, so the annotation pass never
// types one — which means an ordinary function like
//
//	def scaled(n):
//	    t = 0
//	    for i in range(1000):
//	        t = (t + i * n) % 1000000007
//	    return t
//
// gets NO declarations at all: `n` is ⊤, so `i * n` is ⊤, so `t` is ⊤. The
// whole function is exact-but-slow for every caller.
//
// This pass recovers the fast path WITHOUT guessing, by making the missing fact
// a GUARD the caller's value must pass (docs/AUTO_CYTHON.md §11.1, the
// "entry-guarded versioned" form — deliberately not the speculative-replay
// form: this one runs the body once, so it needs no purity gate):
//
//	@cython.cfunc
//	@cython.locals(n=cython.longlong, i=cython.int, t=cython.int)
//	def _py2cy_fast_scaled(n):
//	    <body, verbatim>
//
//	def scaled(n):
//	    if type(n) is int and 0 <= n <= 2147483647:
//	        return _py2cy_fast_scaled(n)
//	    <body, verbatim>
//
// The body is emitted TWICE but executes ONCE per call: the dispatcher picks
// exactly one arm, so side effects cannot double (that is the whole reason to
// prefer this form over a replay). The original body is preserved verbatim, so
// the exact arm is the source program unchanged.
//
// ## Soundness: the assumption IS the guard
//
// The fast arm is proved under a HYPOTHESIS — `param ∈ [lo, hi]` — and the
// hypothesis is only discharged at run time by the guard. Three invariants keep
// that honest, and the tests pin all three:
//
//  1. **The guard's bounds are the assumed interval**, literally
//     (`0 <= n <= 2147483647` above). A wider guard would let an unproved value
//     into typed arithmetic; a narrower one would be dead code. Both are the
//     same bug in opposite directions, so the bounds and the proof read from one
//     variable.
//  2. **`type(p) is int`, not `isinstance`.** A `bool` is an `int` subclass, and
//     an `int` subclass may carry an exotic `__index__`; an exact-type check
//     admits only genuine ints, whose C conversion is exactly the Python value.
//     `True`, floats and every other object take the exact arm. Refusing those
//     is cheap; guessing is not.
//  3. **The parameter's C type is `cython.longlong`, never narrower** than the
//     guard. A narrower C type would raise on conversion (or wrap under
//     `boundscheck=False`) for a guard-accepted value. Locals keep their own
//     narrower widths — they are computed INSIDE the function and bounded by the
//     proof, so they carry no such risk.
//
// Every name the twin types is proved by the ordinary pipeline run under the
// hypothesis (the same interval lattice, the same `unsafeTypedNames` veto), so
// the twin inherits the "an annotation is a proof" contract: nothing is typed
// that the analysis could not bound GIVEN the guard.
//
// ## Refusals (REFUSE > GUESS)
//
// A function is left completely alone when the rewrite could not be exact or
// honest:
//
//   - **provably bigint** (this pass's extra condition): if a value in the body
//     is PROVED outside i64 — a literal (`x = 340282366920938463463374607431768211456`)
//     or a folded constant (`x = 2 ** 100`, or a chain that folds to one) —
//     then arbitrary precision is the point of that code, and a guarded i64 arm
//     is dead weight or a lie. The `--gmp` tier answers those programs, not a
//     dual arm. Only EVIDENCE vetoes: a magnitude that cannot be folded simply
//     does not veto, and the twin stays sound for it because anything unproved
//     is left a Python object there.
//   - **no benefit**: the guard must buy at least one typed LOCAL. Typing only
//     the parameter (which no arithmetic needs) is not worth a branch on every
//     call, and refusing it keeps ordinary `def f(a, b): return a % b` sources
//     byte-identical to before.
//   - **not expressible**: a decorated / async / nested / method (`def` not at
//     column 0) / one-line-body / docstring-first function, a parameter list
//     with `*`, `**`, `/` or keyword-only markers, or a body with a nested
//     function, class, lambda, `global` or `nonlocal` (the twin would change
//     what those bind).
//   - **name collision**: the source already uses the twin's name.
//
// `--pyx` does not participate: the twin needs `@cython.cfunc` /
// `@cython.locals` and a `type(...) is int` guard, which are pure-Python-mode
// spellings. A `.pyx` run that finds a dual candidate declines to pure-Python
// mode (the caller reports it), exactly like the Cython-reserved-name decline.
package pylib

import (
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// dualRanges are the parameter hypotheses tried, WIDEST first. Every one is
// NON-NEGATIVE, and that is a consequence rather than a heuristic: proof
// strength is ANTITONE in the width of the assumption (every transfer function
// is monotone and ⊤ is top, so a wider entry interval only ever yields wider
// intermediates), so a signed range can never prove a body that the
// non-negative range of the same width does not. What a signed range *would* do
// is refuse the shapes this feature exists for: `%`/`//` need a proved
// non-negative dividend (`applyBin`), so the sign-sensitive bodies are exactly
// the ones a signed assumption cannot type. The two entries therefore differ
// only in how much of the caller's domain the guard admits, and the widest range
// that still buys a local wins.
var dualRanges = []struct{ lo, hi int64 }{
	{0, math.MaxInt64},
	{0, math.MaxInt32},
}

// twinPrefix namespaces the generated fast twin. `_py2cy_` keeps it private;
// the twin is a `cfunc`, so on the Cython side it is not reachable from Python
// at all — a typed arm cannot be entered without passing the guard.
const twinPrefix = "_py2cy_fast_"

// dualFunc is one entry-guarded function: the fast twin to insert before the
// definition and the guard to insert at the top of its body.
type dualFunc struct {
	name     string   // the user's function
	param    string   // the guarded parameter
	lo, hi   int64    // the assumed interval == the guard bounds
	defLine  int      // 1-based line of `def <name>`
	firstLn  int      // 1-based line of the body's first statement
	indent   string   // the body's indentation
	argNames []string // the pass-through arguments
	twin     string   // decorators + twin definition + verbatim body
	guard    string   // `if …: return <twin>(…)`, already indented
	locals   []string // typed locals in the twin (for the report and tests)
	decl     string   // the `@cython.locals(...)` argument list
}

// findDualFuncs returns the entry-guarded functions of a module. `level` gates
// the whole feature: the interval lattice is what an assumption unlocks, so only
// OptFull participates.
func findDualFuncs(tree antlr.Tree, lines []string, level Level) []dualFunc {
	if level != OptFull {
		return nil
	}
	if _, ok := tree.(gen.IFile_inputContext); !ok {
		return nil
	}
	source := strings.Join(lines, "\n")
	var out []dualFunc
	walkTree(tree, func(n antlr.Tree) {
		fn, ok := n.(gen.IFuncdefContext)
		if !ok {
			return
		}
		if df, ok := tryDualFunc(fn, lines, source); ok {
			out = append(out, df)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].defLine < out[j].defLine })
	return out
}

func tryDualFunc(fn gen.IFuncdefContext, lines []string, source string) (dualFunc, bool) {
	name := fn.Name().GetText()
	// A guard on every call must not collide with a name the source already
	// uses (checked once; the test is over-approximate, so it can only refuse).
	twinName := twinPrefix + name
	if twinInSource(source, twinName) {
		return dualFunc{}, false
	}
	// A decorated function's decorator applies to the DISPATCHER, which is the
	// definition the source keeps — a twin spliced at the `def` line would sit
	// under the decorator instead and both definitions would change meaning.
	if d, ok := fn.GetParent().(gen.IDecoratedContext); ok && d.Funcdef() != nil {
		return dualFunc{}, false
	}
	defLine := fn.GetStart().GetLine()
	if defLine < 1 || defLine > len(lines) {
		return dualFunc{}, false
	}
	// A nested `def` (method or closure) is indented; the twin would have to be
	// defined inside that scope rather than as a module-level cfunc.
	defText := lines[defLine-1]
	if leadingWS(defText) != "" || !strings.HasPrefix(strings.TrimSpace(defText), "def ") {
		return dualFunc{}, false // decorated / async / nested / method
	}
	body := fn.Block()
	if body == nil {
		return dualFunc{}, false
	}
	stmts := body.AllStmt()
	if len(stmts) == 0 {
		return dualFunc{}, false
	}
	// A docstring must stay the first statement: a guard inserted before it
	// would turn it into an ordinary expression and change `f.__doc__`.
	if isDocstring(stmts[0]) {
		return dualFunc{}, false
	}
	// The body must be a block on the following lines: a one-liner
	// (`def f(n): return n`) has no line to insert a guard at and no body span
	// to copy.
	if stmts[0].GetStart().GetLine() <= defLine {
		return dualFunc{}, false
	}
	if dualHasNestedScope(body) {
		return dualFunc{}, false
	}
	paramNames, paramsText, ok := dualPlainParams(fn)
	if !ok || len(paramNames) == 0 {
		return dualFunc{}, false
	}
	// The extra condition: provably-bigint code does not get a guarded i64 arm.
	if provablyNeedsBigint(body) {
		return dualFunc{}, false
	}
	firstLn := stmts[0].GetStart().GetLine()
	lastLn := stmts[len(stmts)-1].GetStop().GetLine()
	if lastLn < firstLn || lastLn > len(lines) {
		return dualFunc{}, false
	}
	bodyLines := lines[firstLn-1 : lastLn]
	indent := leadingWS(lines[firstLn-1])
	// What the body types WITHOUT the hypothesis: the guard must add at least one
	// local on top of this, or it is a per-call branch that buys nothing (a
	// literal-bounded counter like `for i in range(10)` is already typed either
	// way, so a candidate that only re-types it is refused).
	base, _ := proveDualTyped(body, "", nil)

	for _, param := range paramNames {
		for _, r := range dualRanges {
			seed := env{}
			seed.set(param, known(r.lo, r.hi))
			tw, ok := proveDualTyped(body, param, seed)
			if !ok || !addsLocal(tw.locals, base.locals) {
				continue
			}
			return dualFunc{
				name: name, param: param, lo: r.lo, hi: r.hi,
				defLine: defLine, firstLn: firstLn, indent: indent,
				argNames: paramNames,
				twin:     dualTwinText(twinName, paramsText, tw, bodyLines),
				guard:    dualGuardText(twinName, param, r.lo, r.hi, paramNames, indent),
				locals:   tw.locals,
				decl:     tw.decl,
			}, true
		}
	}
	return dualFunc{}, false
}

// twinInSource reports whether the twin name already occurs in the source.
func twinInSource(source, name string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(source)
}

// dualHasNestedScope reports whether a body contains a scope of its own, or a
// statement that rebinds across a scope boundary. The twin copies the body
// verbatim, so any of these would silently change meaning (a nested `def` would
// close over the twin's locals; `global g` would bind the module's).
func dualHasNestedScope(body antlr.Tree) bool {
	found := false
	walkTree(body, func(n antlr.Tree) {
		switch n.(type) {
		case gen.IFuncdefContext, gen.IClassdefContext, gen.ILambdefContext,
			gen.IGlobal_stmtContext, gen.INonlocal_stmtContext:
			found = true
		}
	})
	return found
}

// dualPlainParams returns the plain positional-or-keyword parameters: no `*`,
// `**`, `/` or keyword-only marker, since the dispatcher passes the twin's
// arguments positionally. It also returns the source text of the parameter list,
// so defaults and annotations survive into the twin verbatim.
func dualPlainParams(fn gen.IFuncdefContext) (names []string, paramsText string, ok bool) {
	prm := fn.Parameters()
	if prm == nil || prm.Typedargslist() == nil {
		return nil, "", false
	}
	paramsText = prm.GetText()
	tal := prm.Typedargslist()
	if tal.DIV() != nil || tal.STAR() != nil || tal.POWER() != nil {
		return nil, "", false
	}
	for _, td := range tal.AllTfpdef() {
		if td.Name() == nil {
			return nil, "", false
		}
		nm := td.Name().GetText()
		if !isSimpleName(nm) {
			return nil, "", false
		}
		names = append(names, nm)
	}
	if len(names) == 0 {
		return nil, "", false
	}
	return names, paramsText, true
}

// dualTwin is the typing of the fast arm: the typed locals and the
// `@cython.locals(...)` argument list that declares them (plus the guarded
// parameter, when there is one).
type dualTwin struct {
	locals []string // sorted typed locals (the parameter is not one of them)
	decl   string   // `n=cython.longlong, i=cython.int, t=cython.int`
}

// addsLocal reports whether the guarded attempt typed a local the unguarded body
// did not — the "the guard must buy something" rule.
func addsLocal(candidate, base []string) bool {
	if len(candidate) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, n := range base {
		have[n] = true
	}
	for _, n := range candidate {
		if !have[n] {
			return true
		}
	}
	return false
}

// proveDualTyped runs the ordinary pipeline over a function body, optionally
// under a parameter hypothesis, and returns the twin's declarations. `param` is
// empty for the baseline (no hypothesis) run.
//
// Soundness: every name returned is proved by the same lattice, the same
// domain classifier and the same `unsafeTypedNames` veto as any other
// declaration — the only difference is that the entry environment carries the
// hypothesis, which the emitted guard discharges at run time.
func proveDualTyped(body antlr.Tree, param string, seed env) (dualTwin, bool) {
	e, _ := proveRangesSeeded(body, seed)
	if param != "" {
		// The parameter must survive the body proved, or there is nothing to
		// guard: a body that rebinds it (`n = "s"`) leaves it ⊤.
		if v, ok := e[param]; !ok || !v.ok {
			return dualTwin{}, false
		}
	}
	// The domain classifier is seeded from the same env, so the hypothesis also
	// reaches arithmetic that MENTIONS the parameter (`i * n`), not just the
	// parameter itself.
	allInt := intDomainFixpoint(body, e)
	ints := map[string]bool{}
	for n, v := range e {
		if v.ok && allInt[n] {
			ints[n] = true
		}
	}
	floats := floatDomainFixpoint(body, intLeafPredicate(e, allInt))
	floats = subtract(floats, ints)
	// A guarded parameter is an int BY THE GUARD, so it joins the typed set the
	// usage scan reasons about: an `n is m` / `n << k` / `id(n)` use must veto it
	// (and with it the whole candidate).
	intTyped := map[string]bool{}
	allTyped := map[string]bool{}
	if param != "" {
		intTyped[param] = true
		allTyped[param] = true
	}
	for n := range ints {
		intTyped[n] = true
		allTyped[n] = true
	}
	for n := range floats {
		allTyped[n] = true
	}
	ban := unsafeTypedNames(body, e, intTyped, allTyped)
	if param != "" && ban[param] {
		return dualTwin{}, false
	}
	ints = subtract(ints, ban)
	floats = subtract(floats, ban)
	widths := intEvidence(body, e, ints)

	var decls, locals []string
	if param != "" {
		decls = append(decls, param+"="+WidthI64.cythonType())
	}
	for n := range ints {
		if n == param {
			continue
		}
		w := widths[n].Width
		if w != WidthI32 {
			w = WidthI64
		}
		decls = append(decls, n+"="+w.cythonType())
		locals = append(locals, n)
	}
	for n := range floats {
		if n == param {
			continue
		}
		decls = append(decls, n+"=cython.double")
		locals = append(locals, n)
	}
	sort.Strings(locals)
	if param == "" {
		// the baseline carries no declaration list
		return dualTwin{locals: locals}, true
	}
	if len(locals) == 0 {
		return dualTwin{}, false
	}
	rest := decls[1:] // the parameter is declared first, then locals sorted
	sort.Strings(rest)
	return dualTwin{locals: locals, decl: strings.Join(append([]string{decls[0]}, rest...), ", ")}, true
}

// dualTwinText renders the fast twin: the cfunc/locals decorators, a copy of the
// signature (so defaults and annotations survive), and the body verbatim.
func dualTwinText(twinName, paramsText string, tw dualTwin, bodyLines []string) string {
	var b strings.Builder
	// A cfunc is a C function: Cython does not expose it to Python, so no Python
	// caller can reach the typed arm without passing the guard. Under CPython
	// the shadow `cython` module makes the decorator a no-op, which is exactly
	// right — there the file has no C types at all.
	b.WriteString("@cython.cfunc\n")
	b.WriteString("@cython.locals(" + tw.decl + ")\n")
	b.WriteString("def " + twinName + paramsText + ":\n")
	for _, l := range bodyLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// dualGuardText renders the dispatcher prologue. The bounds come from the same
// interval the proof used: see the package comment, invariant 1.
func dualGuardText(twinName, param string, lo, hi int64, argNames []string, indent string) string {
	var b strings.Builder
	b.WriteString(indent + "if type(" + param + ") is int and " +
		strconv.FormatInt(lo, 10) + " <= " + param + " <= " + strconv.FormatInt(hi, 10) + ":\n")
	b.WriteString(indent + "    return " + twinName + "(" + strings.Join(argNames, ", ") + ")")
	return b.String()
}

// ── the extra condition: not provably bigint ─────────────────────────────
//
// A value is "provably bigint" when the analysis can PROVE it lies outside i64.
// Only evidence vetoes, and the test is deliberately conservative: a magnitude
// we cannot fold does not veto, and the guarded arm stays sound for it (anything
// unproved is left a Python object in the twin, so its arithmetic stays exact).

// maxExactBits bounds the constant folder: values are materialised exactly up to
// this width, and anything wider is reported as `huge` (provably larger than
// i64). It is far above 64 so that a big intermediate which a later `%` or `//`
// SHRINKS still folds exactly — `2 ** 200 % 1000000007` is a small value with a
// big intermediate, and vetoing it would refuse a dual arm for no reason.
const maxExactBits = 1 << 12

// provablyNeedsBigint reports whether a scope contains a value the analysis
// proves is outside i64: an integer literal that does not fit, or a constant
// expression / propagated constant that folds to one.
func provablyNeedsBigint(scope antlr.Tree) bool {
	// 1. Literals: an i64-incompatible literal is used by the code directly.
	bad := false
	walkTree(scope, func(n antlr.Tree) {
		tn, ok := n.(antlr.TerminalNode)
		if !ok {
			return
		}
		if v, ok := bigLitToken(tn.GetText()); ok && !v.IsInt64() {
			bad = true
		}
	})
	if bad {
		return true
	}
	// 2. Constants: `x = 2 ** 100`, or a chain of assignments that folds to one.
	// The pass is bounded and monotone (consts only ever gains names, and a name
	// with a non-constant assignment is dirtied once and never revisited).
	// Missing a constant merely misses the veto — it can never widen a proof.
	consts := map[string]*big.Int{}
	dirty := map[string]bool{}
	assigns := collectAssigns(scope)
	for pass := 0; pass < 4; pass++ {
		for _, a := range assigns {
			if len(a.targets) != 1 {
				for _, t := range a.targets {
					dirty[t] = true
				}
				continue
			}
			t := a.targets[0]
			if dirty[t] {
				continue
			}
			v, huge, ok := foldBigConst(a.rhs, consts)
			if huge || (ok && !v.IsInt64()) {
				// proven outside i64: `2 ** 100`, or a value the fold
				// materialised exactly and found too wide
				return true
			}
			if !ok {
				dirty[t] = true
				continue
			}
			consts[t] = v
		}
	}
	return false
}

var reUintToken = regexp.MustCompile(`^[0-9][0-9_]*$`)

// bigLitToken parses an unsigned integer literal token (underscore separators
// allowed, as CPython accepts in source).
func bigLitToken(text string) (*big.Int, bool) {
	t := strings.ReplaceAll(text, "_", "")
	if !reUintToken.MatchString(t) {
		return nil, false
	}
	v, ok := new(big.Int).SetString(t, 10)
	return v, ok
}

// foldBigConst folds a literal-only integer expression exactly. `huge` means the
// value is provably WIDER than can be represented exactly (so certainly outside
// i64); ok=false means the expression is not a constant at all. A value that is
// merely outside i64 but exactly representable comes back as an exact `v`, and
// the caller (provablyNeedsBigint) makes the i64 decision at the top of the
// expression — that is what keeps `2**63 - 1` from being a false veto.
//
// The branches are ordered by DECREASING precedence from the bottom up: the
// lowest-precedence top-level operator splits first, because that is the root of
// the expression's parse tree. (`**` splitting first would read
// `2**200 % 1000000007` as `2 ** (200 % 1000000007)` — a false veto of a value
// that is in fact small.) Unary signs are handled after the binary splits, so
// `-5+3` and `-(2**100)` fold by structure rather than by a prefix guess. Only
// the VETO direction matters, so an inexact detail that cannot hide a >i64 value
// (the rounding of `//`) is not modelled; the modulo is exact because
// `|a % b| < |b|` is precisely what keeps a huge intermediate small.
func foldBigConst(text string, consts map[string]*big.Int) (*big.Int, bool, bool) {
	s := stripOuterParens(strings.TrimSpace(text))
	if s == "" || strings.ContainsAny(s, `"'`) {
		return nil, false, false
	}
	// `+` / `-`: the lowest precedence
	if l, op, r, ok := splitTop(s, "+-"); ok {
		return foldBigConstBinop(l, r, consts, func(a, b *big.Int) *big.Int {
			if op == "-" {
				return new(big.Int).Sub(a, b)
			}
			return new(big.Int).Add(a, b)
		})
	}
	// the multiplicative level: only the integer-valued `//` and `%` are
	// modelled (a single `/` yields a float, not a constant)
	if l, r, ok := splitTopTwo(s, "//"); ok {
		return foldBigConstBinop(l, r, consts, func(a, b *big.Int) *big.Int {
			return new(big.Int).Quo(a, b) // rounding cannot hide a >i64 magnitude
		})
	}
	if l, op, r, ok := splitTop(s, "*%"); ok {
		if op == "%" {
			// The DIVISOR is folded first: |a % b| < |b| no matter how big `a`
			// is, so a huge dividend with a small divisor has a small result
			// (this is the `2 ** 200 % 1000000007` case). A huge divisor keeps
			// the result huge.
			rv, rhuge, rok := foldBigConst(r, consts)
			if !rok || rv.Sign() == 0 {
				return nil, false, false // an unknown or zero divisor proves nothing
			}
			if rhuge || !rv.IsInt64() {
				return nil, true, true
			}
			lv, lhuge, lok := foldBigConst(l, consts)
			if !lok {
				return nil, false, false
			}
			if lhuge {
				// the result's magnitude is bounded by the divisor (an
				// over-estimate of |a % b| — a sound magnitude for the veto,
				// which never claims the value itself)
				return new(big.Int).Abs(rv), false, true
			}
			return fitOrHuge(new(big.Int).Mod(lv, rv)) // Euclidean mod == Python's %
		}
		return foldBigConstBinop(l, r, consts, func(a, b *big.Int) *big.Int {
			return new(big.Int).Mul(a, b)
		})
	}
	// `**` binds tightest, so it splits LAST. A small non-negative exponent is
	// exact; a larger one is a veto by itself (2**k with k > maxExactBits does
	// not fit i64 for any |base| >= 2).
	if l, r, ok := splitTopTwo(s, "**"); ok {
		bv, bhuge, bok := foldBigConst(l, consts)
		ev, ehuge, eok := foldBigConst(r, consts)
		if !bok || !eok {
			return nil, false, false
		}
		if !ehuge && ev.IsInt64() && ev.Sign() == 0 {
			return big.NewInt(1), false, true // any base ** 0
		}
		if bhuge || ehuge || !ev.IsInt64() || ev.Sign() < 0 {
			return nil, true, true
		}
		e := ev.Int64()
		switch {
		case bv.Sign() == 0:
			return big.NewInt(0), false, true
		case bv.Cmp(big.NewInt(1)) == 0:
			return big.NewInt(1), false, true
		case bv.Cmp(big.NewInt(-1)) == 0:
			if e%2 == 1 {
				return big.NewInt(-1), false, true
			}
			return big.NewInt(1), false, true
		case e > maxExactBits:
			return nil, true, true
		}
		return fitOrHuge(new(big.Int).Exp(bv, big.NewInt(e), nil))
	}
	// unary signs, then the atoms
	if strings.HasPrefix(s, "-") {
		v, huge, ok := foldBigConst(s[1:], consts)
		if !ok || huge {
			return nil, huge, ok
		}
		return new(big.Int).Neg(v), false, true
	}
	if strings.HasPrefix(s, "+") {
		return foldBigConst(s[1:], consts)
	}
	if v, ok := consts[s]; ok {
		return v, false, true
	}
	if v, ok := bigLitToken(s); ok {
		// a literal is exactly representable however wide it is; whether it
		// fits i64 is the caller's decision at the top of the expression
		return v, false, true
	}
	return nil, false, false
}

// foldBigConstBinop applies op to two folded operands (a zero divisor is not a
// proof of anything). A huge operand keeps the result huge: `+`, `-` and `*`
// cannot shrink a magnitude, and a `*` by an exactly-known zero is handled
// because the exact operand is still available.
func foldBigConstBinop(l, r string, consts map[string]*big.Int, op func(a, b *big.Int) *big.Int) (*big.Int, bool, bool) {
	lv, lhuge, lok := foldBigConst(l, consts)
	rv, rhuge, rok := foldBigConst(r, consts)
	if !lok || !rok {
		return nil, false, false
	}
	if lhuge || rhuge {
		// x * 0 == 0 collapses a huge operand; everything else keeps it
		if !lhuge && lv.Sign() == 0 {
			return big.NewInt(0), false, true
		}
		if !rhuge && rv.Sign() == 0 {
			return big.NewInt(0), false, true
		}
		return nil, true, true
	}
	return fitOrHuge(op(lv, rv))
}

// fitOrHuge reports a folded value, flagging only what the folder cannot
// represent (wider than maxExactBits). The i64 decision belongs to the CALLER,
// at the top of an expression: flagging it here would make `2 ** 63 - 1` — the
// ordinary way to write MaxInt64, whose value fits — a false veto.
func fitOrHuge(v *big.Int) (*big.Int, bool, bool) {
	if v.BitLen() > maxExactBits {
		return nil, true, true
	}
	return v, false, true
}
