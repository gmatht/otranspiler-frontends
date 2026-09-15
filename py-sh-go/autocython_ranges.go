// autocython_ranges.go — a small SOUND interval analysis over the ANTLR
// tree, so the annotation pass can type bounded accumulators (AUTO_CYTHON.md
// Stage 1): `h = (h*31+i) % 1000000007` is proved to stay in [0, m-1], not
// just literal-initialised scalars.
//
// Soundness is the whole point, so the lattice is conservative:
//
//   - a variable's abstract value is an i64 interval or ⊤ (unknown);
//   - arithmetic is interval arithmetic in i64, and ANY overflow yields ⊤;
//   - `a % m` with m provably > 0 is [0, m-1] (Python floor-mod);
//   - a `for x in range(<int literals>)` counter is bounded by the endpoints;
//   - a loop body is iterated to a fixed point with union-widening (monotone
//     transfer), capped — if it has not stabilised it is widened to ⊤;
//   - a `while` body runs an unknown number of times, so every variable it
//     assigns becomes ⊤;
//   - `if`/`elif`/`else` joins the branch states (⊤ if either is ⊤).
//
// t101 (`s = s + 4000000000000000000` in a `while`) is widened to ⊤ and therefore
// refused; rolling_hash's `h` reaches the fixed point [0, 1000000006].
package pylib

import (
	"math"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// iv is an i64 interval; ok=false is ⊤ (unknown).
type iv struct {
	lo, hi int64
	ok     bool
}

func known(lo, hi int64) iv { return iv{lo, hi, true} }

// join is the lattice join: ⊤ if either side is ⊤, else the convex hull.
func join(a, b iv) iv {
	if !a.ok || !b.ok {
		return iv{}
	}
	if b.lo < a.lo {
		a.lo = b.lo
	}
	if b.hi > a.hi {
		a.hi = b.hi
	}
	return a
}

func sameIV(a, b iv) bool {
	return a.ok == b.ok && (!a.ok || (a.lo == b.lo && a.hi == b.hi))
}

// env is the abstract state at a program point; absent = ⊤.
type env map[string]iv

func (e env) clone() env {
	m := make(env, len(e))
	for k, v := range e {
		m[k] = v
	}
	return m
}

func (e env) set(name string, v iv) {
	if v.ok {
		e[name] = v
	} else {
		delete(e, name)
	}
}

func (e env) joinFrom(o env) {
	for k, v := range e {
		if ov, ok := o[k]; ok {
			e[k] = join(v, ov)
		} else {
			delete(e, k)
		}
	}
}

// proveRanges walks a parsed module and returns the proved intervals plus the
// set of everything assigned (so the caller can report the refusals).
func proveRanges(tree antlr.Tree) (env, map[string]bool) {
	e := env{}
	assigned := map[string]bool{}
	runNode(tree, e, assigned, 0)
	return e, assigned
}

func runNode(n antlr.Tree, e env, assigned map[string]bool, depth int) {
	if depth > 24 {
		return
	}
	switch ctx := n.(type) {
	case gen.IFuncdefContext, gen.IClassdefContext, gen.ILambdefContext:
		// own scope: do not prove module-level ranges through them
		return
	case gen.ITry_stmtContext, gen.IMatch_stmtContext, gen.IAsync_stmtContext:
		// conditional / unknown-iteration bodies: widen everything they assign
		markUnknownSubtree(ctx, e, assigned)
		return
	case gen.IFor_stmtContext:
		runFor(ctx, e, assigned, depth)
		return
	case gen.IWhile_stmtContext:
		runWhile(ctx, e, assigned, depth)
		return
	case gen.IIf_stmtContext:
		runIf(ctx, e, assigned, depth)
		return
	case gen.IExpr_stmtContext:
		runExprStmt(ctx, e, assigned)
		return
	}
	for i := 0; i < n.GetChildCount(); i++ {
		runNode(n.GetChild(i), e, assigned, depth)
	}
}

func runExprStmt(ctx gen.IExpr_stmtContext, e env, assigned map[string]bool) {
	targets := func(ts []gen.ITestlist_star_exprContext) {
		for _, t := range ts {
			nm := strings.TrimSpace(t.GetText())
			if isSimpleName(nm) {
				assigned[nm] = true
				delete(e, nm)
			}
		}
	}
	if ctx.Annassign() != nil || ctx.Augassign() != nil {
		targets(ctx.AllTestlist_star_expr())
		return
	}
	ts := ctx.AllTestlist_star_expr()
	if len(ts) != 2 {
		targets(ts)
		return
	}
	lhs := strings.TrimSpace(ts[0].GetText())
	if !isSimpleName(lhs) {
		return
	}
	assigned[lhs] = true
	e.set(lhs, evalText(ts[1].GetText(), e))
}

func forTargetName(ctx gen.IFor_stmtContext) string {
	target := ctx.Exprlist()
	if target == nil {
		return ""
	}
	name := target.GetText()
	if !isSimpleName(name) {
		return ""
	}
	return name
}

// markUnknownSubtree widens every simple-name target assigned anywhere in a
// conditional / unknown-iteration subtree (try, match, async for/with).
func markUnknownSubtree(n antlr.Tree, e env, assigned map[string]bool) {
	mark := func(nm string) {
		if isSimpleName(nm) {
			assigned[nm] = true
			delete(e, nm)
		}
	}
	walkTree(n, func(t antlr.Tree) {
		switch c := t.(type) {
		case gen.IExpr_stmtContext:
			for _, ts := range c.AllTestlist_star_expr() {
				mark(strings.TrimSpace(ts.GetText()))
			}
		case gen.IFor_stmtContext:
			mark(forTargetName(c))
		}
	})
}

func rangeCounterIV(ctx gen.IFor_stmtContext) (iv, bool) {
	if forTargetName(ctx) == "" {
		return iv{}, false
	}
	iter := ctx.Testlist()
	if iter == nil {
		return iv{}, false
	}
	it := iter.GetText()
	if !strings.HasPrefix(it, "range(") || !strings.HasSuffix(it, ")") {
		return iv{}, false
	}
	args := strings.Split(it[len("range("):len(it)-1], ",")
	if len(args) < 1 || len(args) > 3 {
		return iv{}, false
	}
	vals := make([]int64, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		v, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			return iv{}, false
		}
		vals = append(vals, v)
	}
	var a, b int64
	switch len(vals) {
	case 1:
		a, b = 0, vals[0]
	case 2, 3:
		a, b = vals[0], vals[1]
	}
	if b < a {
		a, b = b, a
	}
	return known(a, b), true
}

func runFor(ctx gen.IFor_stmtContext, e env, assigned map[string]bool, depth int) {
	if name := forTargetName(ctx); name != "" {
		assigned[name] = true
		if c, ok := rangeCounterIV(ctx); ok {
			e.set(name, c)
		} else {
			delete(e, name)
		}
	}
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return
	}
	body := blocks[0]
	for i := 0; i < 6; i++ {
		before := e.clone()
		runNode(body, e, assigned, depth+1)
		if envStable(e, before) {
			break
		}
		if i == 5 {
			widenChanged(e, before)
		}
	}
	if len(blocks) > 1 {
		runNode(blocks[1], e, assigned, depth+1)
	}
}

func runWhile(ctx gen.IWhile_stmtContext, e env, assigned map[string]bool, depth int) {
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return
	}
	// unknown trip count: run once, then widen everything the body assigns.
	bodyAssigned := map[string]bool{}
	be := e.clone()
	runNode(blocks[0], be, bodyAssigned, depth+1)
	for k := range bodyAssigned {
		assigned[k] = true
		delete(e, k)
	}
	// the loop condition may contain a walrus (`while (n := f()):`); its
	// target is left ⊤ (never proved), which is sound.
	if len(blocks) > 1 {
		runNode(blocks[1], e, assigned, depth+1)
	}
}

func runIf(ctx gen.IIf_stmtContext, e env, assigned map[string]bool, depth int) {
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return
	}
	merged := e.clone()
	for i, b := range blocks {
		be := e.clone()
		runNode(b, be, assigned, depth+1)
		if i == 0 {
			merged = be
		} else {
			merged.joinFrom(be)
		}
	}
	for k := range e {
		delete(e, k)
	}
	for k, v := range merged {
		e[k] = v
	}
}

// collectWalrus is intentionally absent: a walrus target in a condition is
// never `set`, so it stays ⊤ and is refused — sound by construction.

func envStable(a, b env) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !sameIV(v, b[k]) {
			return false
		}
	}
	return true
}

func widenChanged(e, before env) {
	for k := range e {
		if !sameIV(e[k], before[k]) {
			delete(e, k)
		}
	}
}

// ── expression interval evaluation (textual: the subset is small) ────────

func evalText(text string, e env) iv { return evalTextDepth(text, e, 0) }

func evalTextDepth(text string, e env, depth int) iv {
	if depth > 16 {
		return iv{}
	}
	s := stripOuterParens(strings.TrimSpace(text))
	if s == "" {
		return iv{}
	}
	// binary operators, splitting at the lowest precedence first
	for _, ops := range []string{"+", "-"} {
		if l, op, r, ok := splitTop(s, ops); ok {
			return applyBin(op, evalTextDepth(l, e, depth+1), evalTextDepth(r, e, depth+1))
		}
	}
	if l, op, r, ok := splitTop(s, "*%"); ok {
		return applyBin(op, evalTextDepth(l, e, depth+1), evalTextDepth(r, e, depth+1))
	}
	if l, _, r, ok := splitTop(s, "/"); ok {
		// `//` only (a single `/` is true division -> float, unknown)
		if strings.Contains(l, "/") || !strings.HasSuffix(l, "/") {
			return iv{}
		}
		return floorDivIV(evalTextDepth(strings.TrimSuffix(l, "/"), e, depth+1), evalTextDepth(r, e, depth+1))
	}
	if strings.HasPrefix(s, "-") && len(s) > 1 {
		inner := evalTextDepth(s[1:], e, depth+1)
		if !inner.ok || inner.hi == math.MinInt64 {
			return iv{}
		}
		return known(-inner.hi, -inner.lo)
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return known(v, v)
	}
	if v, okk := e[s]; okk {
		return v
	}
	return iv{}
}

// splitTop splits s at a top-level operator from the given set, preferring
// the RIGHTMOST split (left-associative evaluation still yields a sound
// hull). Returns ok=false when no top-level operator exists.
func splitTop(s, ops string) (string, string, string, bool) {
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
		default:
			if depth == 0 && strings.IndexByte(ops, s[i]) >= 0 && i > 0 &&
				strings.IndexByte("+-*/%", s[i-1]) < 0 {
				return s[:i], string(s[i]), s[i+1:], true
			}
		}
	}
	return "", "", "", false
}

func applyBin(op string, a, b iv) iv {
	switch op {
	case "+":
		return addIV(a, b)
	case "-":
		return subIV(a, b)
	case "*":
		return mulIV(a, b)
	case "%":
		// Python floor-mod with a positive divisor d lies in [0, d-1]. C's
		// `%` truncates, which differs for a negative lhs, so require the
		// lhs (and divisor) to be provably non-negative.
		if a.ok && a.lo >= 0 && b.ok && b.lo > 0 {
			return known(0, b.hi-1)
		}
		return iv{}
	}
	return iv{}
}

func floorDivIV(a, b iv) iv {
	// Python floor division differs from C truncation for a negative lhs;
	// require a provably non-negative lhs and a positive divisor.
	if !a.ok || a.lo < 0 || !b.ok || b.lo <= 0 {
		return iv{}
	}
	return known(floorDiv(a.lo, b.hi), floorDiv(a.hi, b.lo))
}

func floorDiv(x, y int64) int64 {
	q := x / y
	if (x%y != 0) && ((x < 0) != (y < 0)) {
		q--
	}
	return q
}

func addIV(a, b iv) iv {
	if !a.ok || !b.ok {
		return iv{}
	}
	lo, ok1 := addOvf(a.lo, b.lo)
	hi, ok2 := addOvf(a.hi, b.hi)
	if !ok1 || !ok2 {
		return iv{}
	}
	return known(lo, hi)
}

func subIV(a, b iv) iv {
	if !a.ok || !b.ok || b.lo == math.MinInt64 || b.hi == math.MinInt64 {
		return iv{}
	}
	return addIV(a, known(-b.hi, -b.lo))
}

func mulIV(a, b iv) iv {
	if !a.ok || !b.ok {
		return iv{}
	}
	lo := int64(0)
	hi := int64(0)
	first := true
	for _, x := range []int64{a.lo, a.hi} {
		for _, y := range []int64{b.lo, b.hi} {
			p, ok := mulOvf(x, y)
			if !ok {
				return iv{}
			}
			if first || p < lo {
				lo = p
			}
			if first || p > hi {
				hi = p
			}
			first = false
		}
	}
	return known(lo, hi)
}

func addOvf(x, y int64) (int64, bool) {
	s := x + y
	if (y > 0 && s < x) || (y < 0 && s > x) {
		return 0, false
	}
	return s, true
}

func mulOvf(x, y int64) (int64, bool) {
	if x == 0 || y == 0 {
		return 0, true
	}
	if (x == -1 && y == math.MinInt64) || (y == -1 && x == math.MinInt64) {
		return 0, false
	}
	p := x * y
	if p/y != x {
		return 0, false
	}
	return p, true
}

func stripOuterParens(s string) string {
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' && balanced(s[1:len(s)-1]) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}
