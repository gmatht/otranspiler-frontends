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
//   - a `for x in <proved list>` loop runs exactly len steps with x bound to
//     the element interval (the trip count is the array length), so an
//     accumulator converges instead of widening to ⊤; anything that may
//     mutate or alias the list drops the fact (REFUSE > GUESS);
//   - any other loop body is iterated to a fixed point with union-widening
//     (monotone transfer), capped — if it has not stabilised it is widened
//     to ⊤;
//   - a `while` body runs an unknown number of times, so every variable it
//     assigns becomes ⊤;
//   - `if`/`elif`/`else` joins the branch states (⊤ if either is ⊤).
//
// t101 (`s = s + 4000000000000000000` in a `while`) is widened to ⊤ and therefore
// refused; rolling_hash's `h` reaches the fixed point [0, 1000000006].
package pylib

import (
	"math"
	"regexp"
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

// ── bounded `for x in <list>` iteration ───────────────────────────────────
//
// A `for` over an unknown iterable runs an unknown number of times, so the
// fixed-point path widens loop-carried values to ⊤ (an accumulator never
// stabilises under union-widening: [0,0], [1,9], [2,18], … never repeats).
// But a loop over a PROVED list runs exactly len steps — the trip count is
// the array length — so the body is run exactly len times in the abstract
// domain, rebinding the target each trip like the concrete loop. That is
// sound (it mirrors the concrete trip count) and precise: with
// `arr = [3,1,4,1,5,9,2,6]` the accumulator `total = total + v` yields
// [8,72], not ⊤.
//
// A list fact is sound only while the object is provably unshared and
// unmutated, so the analysis refuses (drops the fact) on anything that may
// share or mutate it — REFUSE > GUESS, as everywhere else here:
//
//   - fact creation only from a single-target `NAME = [e1, …, en]` display
//     whose every element has a proved i64 interval (flow-sensitive, so
//     `[k, k+1]` with k proved works); a multi-target `x = y = […]` shares
//     one object and earns no fact; an alias `b = arr` drops arr's fact;
//   - any `NAME.` / `NAME[` use kills the fact (`xs.append(..)` and
//     `xs[i] = ..`, but also reads like `xs[3]` — conservative, still sound);
//   - passing the list to anything but a whitelisted pure builtin
//     (`len`, `print`, `sum`, …) kills it — the callee may mutate the alias;
//   - `del` naming it kills it; reassigning it replaces the fact;
//   - a name touched as `NAME.` / `NAME[` / `global NAME` inside a nested
//     function/class/lambda never earns one (a closure can mutate the shared
//     object and the flow pass does not cross scope boundaries);
//   - the bounded loop itself bails to the fixed-point path when the body
//     may mutate the iterable or rebind it, when the target IS the iterable
//     (`for arr in arr`), or when len exceeds maxBoundedIter.
//
// Int and list facts are mutually exclusive per name (every assignment clears
// both first), and a rebound loop target clears its list fact: after
// `for arr in [3,4]` arr is a scalar, not a list.

// listFact is the proved value of a Python list: its exact length and the
// joined interval of its elements (elem.ok=false only for the empty list,
// whose loop body never runs).
type listFact struct {
	length int
	elem   iv
}

// maxBoundedIter caps exact unrolling of `for x in <known list>`. Beyond it
// the loop falls back to the fixed-point path (loop-carried values go ⊤).
// Nesting multiplies the cost, but realistic list displays are small.
const maxBoundedIter = 256

var reAttrSub = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(\.|\[)`)

// pureBuiltinCalls are calls that provably do not mutate their arguments, so
// passing a tracked list to them keeps the fact.
var pureBuiltinCalls = map[string]bool{
	"len": true, "sum": true, "min": true, "max": true, "sorted": true,
	"list": true, "tuple": true, "range": true, "print": true, "abs": true,
	"repr": true, "str": true, "int": true, "float": true, "bool": true,
	"enumerate": true, "reversed": true, "any": true, "all": true,
	"isinstance": true, "hash": true, "chr": true, "ord": true,
	"hex": true, "oct": true, "bin": true,
}

var reCallHead = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(`)

// treeText renders a subtree's source text ("" when it is not a parse tree).
func treeText(n antlr.Tree) string {
	if pt, ok := n.(antlr.ParseTree); ok {
		return pt.GetText()
	}
	return ""
}

// cloneLists copies the list facts (branch scopes must not leak into each other).
func cloneLists(lists map[string]listFact) map[string]listFact {
	m := make(map[string]listFact, len(lists))
	for k, v := range lists {
		m[k] = v
	}
	return m
}

// splitTopCommas splits s on every top-level comma (bracket depth 0). It is
// quote-agnostic: a comma inside a string literal yields pieces that still
// contain quote characters, which the int analyses reject — the error is
// always toward refusal, never toward a false proof.
func splitTopCommas(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// evalListLiteral proves a list DISPLAY `[e1, …, en]` (not a comprehension or
// starred form): every element must have a proved i64 interval. It returns
// the exact length and the joined element interval.
func evalListLiteral(text string, e env) (listFact, bool) {
	s := strings.TrimSpace(text)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return listFact{}, false
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return listFact{length: 0}, true
	}
	if strings.ContainsAny(inner, "[]") {
		// a nested display, slice, or comprehension — none modelled
		return listFact{}, false
	}
	parts := splitTopCommas(inner)
	elem := iv{}
	for i, p := range parts {
		v := evalText(p, e)
		if !v.ok {
			return listFact{}, false
		}
		if i == 0 {
			elem = v
		} else {
			elem = join(elem, v)
		}
	}
	return listFact{length: len(parts), elem: elem}, true
}

// iterListFact resolves a `for` iterable to a proved list: a list display,
// or a name holding a proved list fact.
func iterListFact(iter string, e env, lists map[string]listFact) (listFact, bool) {
	if f, ok := evalListLiteral(iter, e); ok {
		return f, true
	}
	if f, ok := lists[strings.TrimSpace(iter)]; ok {
		return f, true
	}
	return listFact{}, false
}

// closedOverNames collects the names that must never hold a list fact in this
// scope: any name used as `NAME.` / `NAME[` or declared `global NAME` inside
// a nested function/class/lambda scope.
func closedOverNames(root antlr.Tree) map[string]bool {
	out := map[string]bool{}
	var walk func(n antlr.Tree, nested bool)
	walk = func(n antlr.Tree, nested bool) {
		switch t := n.(type) {
		case gen.IFuncdefContext, gen.IClassdefContext, gen.ILambdefContext:
			nested = true
		case gen.IGlobal_stmtContext:
			if nested {
				for _, nm := range t.AllName() {
					out[nm.GetText()] = true
				}
			}
		case gen.IAtom_exprContext:
			if nested {
				for _, m := range reAttrSub.FindAllStringSubmatch(t.GetText(), -1) {
					out[m[1]] = true
				}
			}
		}
		for i := 0; i < n.GetChildCount(); i++ {
			walk(n.GetChild(i), nested)
		}
	}
	walk(root, false)
	return out
}

// killListUses deletes the list facts of every tracked name used as `NAME.`
// or `NAME[` in text (a method call or subscript use may mutate the object;
// reads match too — conservative, still sound).
// or `NAME[` in text (a method call or subscript use may mutate the object;
// reads match too — conservative, still sound).
func killListUses(text string, lists map[string]listFact) {
	if len(lists) == 0 {
		return
	}
	for _, m := range reAttrSub.FindAllStringSubmatch(text, -1) {
		delete(lists, m[1])
	}
}

// killListCallArgs deletes tracked lists passed to a possibly-mutating call:
// anything but a whitelisted pure builtin applied to a bare name. The name
// match is a substring over-approximation (`f(xs)` also kills a tracked `x`)
// — always toward refusal.
func killListCallArgs(atomNoSpace string, lists map[string]listFact) {
	if len(lists) == 0 {
		return
	}
	open := strings.Index(atomNoSpace, "(")
	if open < 0 {
		return
	}
	if m := reCallHead.FindStringSubmatch(atomNoSpace); m != nil && pureBuiltinCalls[m[1]] {
		return
	}
	args := atomNoSpace[open:]
	for nm := range lists {
		if strings.Contains(args, nm) {
			delete(lists, nm)
		}
	}
}

// killListMutations drops every list fact the subtree may invalidate: any
// `NAME.` / `NAME[` use and any possibly-mutating call argument anywhere
// under it (statements, iterables, and conditions alike). It is idempotent,
// so overlapping scans are harmless.
func killListMutations(n antlr.Tree, lists map[string]listFact) {
	if len(lists) == 0 {
		return
	}
	walkTree(n, func(t antlr.Tree) {
		if atom, ok := t.(gen.IAtom_exprContext); ok {
			text := strings.ReplaceAll(atom.GetText(), " ", "")
			killListUses(text, lists)
			killListCallArgs(text, lists)
		}
	})
}

// bodyMutatesName reports whether a loop body may mutate or rebind the named
// list (textual over-approximation — toward refusal). "" never matches.
func bodyMutatesName(body antlr.Tree, name string) bool {
	if name == "" {
		return false
	}
	for _, m := range reAttrSub.FindAllStringSubmatch(treeText(body), -1) {
		if m[1] == name {
			return true
		}
	}
	mutated := false
	walkTree(body, func(t antlr.Tree) {
		if es, ok := t.(gen.IExpr_stmtContext); ok {
			text := es.GetText()
			if es.Augassign() != nil || es.Annassign() != nil {
				if strings.Contains(text, name) {
					mutated = true
				}
				return
			}
			for _, tl := range es.AllTestlist_star_expr() {
				if strings.TrimSpace(tl.GetText()) == name {
					mutated = true
				}
			}
		}
		if fs, ok := t.(gen.IFor_stmtContext); ok {
			if forTargetName(fs) == name {
				mutated = true
			}
		}
	})
	return mutated
}

// proveRanges walks a parsed module and returns the proved intervals plus the
// set of everything assigned (so the caller can report the refusals).
func proveRanges(tree antlr.Tree) (env, map[string]bool) {
	return proveRangesSeeded(tree, nil)
}

// proveRangesSeeded is proveRanges starting from an ASSUMED entry environment.
// The entry-guarded dual arm (dualguard.go) proves a function body under a
// hypothesis about a parameter (`n ∈ [0, 2^31-1]`); the hypothesis is never a
// proof — it is discharged at run time by the matching guard — so this entry
// point exists to make the assumption explicit and reviewable, not to relax
// the lattice. A nil seed is the ordinary whole-program proof.
func proveRangesSeeded(tree antlr.Tree, seed env) (env, map[string]bool) {
	e := env{}
	for k, v := range seed {
		e[k] = v
	}
	lists := map[string]listFact{}
	assigned := map[string]bool{}
	runNode(tree, e, lists, closedOverNames(tree), assigned, 0)
	return e, assigned
}

func runNode(n antlr.Tree, e env, lists map[string]listFact, taint map[string]bool, assigned map[string]bool, depth int) {
	if depth > 24 {
		return
	}
	switch ctx := n.(type) {
	case gen.IFuncdefContext, gen.IClassdefContext, gen.ILambdefContext:
		// own scope: do not prove module-level ranges through them
		return
	case gen.IDel_stmtContext:
		// `del xs[i]` mutates the list; `del xs` unbinds the name.
		// (GetText() drops whitespace, so match on the exprlist: the
		// full text reads `delx` and never matches a real name.)
		if el := ctx.Exprlist(); el != nil {
			for _, id := range reIdentAll.FindAllString(el.GetText(), -1) {
				if isSimpleName(id) {
					assigned[id] = true
					delete(e, id)
					delete(lists, id)
				}
			}
		}
		return
	case gen.ITry_stmtContext, gen.IMatch_stmtContext, gen.IAsync_stmtContext:
		// conditional / unknown-iteration bodies: widen everything they assign
		markUnknownSubtree(ctx, e, lists, assigned)
		return
	case gen.IWith_stmtContext:
		// `with ... as name` rebinds to an unknown value; the body and
		// item expressions still run below via the child recursion.
		for _, item := range ctx.AllWith_item() {
			if item.AS() != nil && item.Expr() != nil {
				if nm := strings.TrimSpace(item.Expr().GetText()); isSimpleName(nm) {
					assigned[nm] = true
					delete(e, nm)
					delete(lists, nm)
				}
			}
		}
	case gen.INamedexpr_testContext:
		// `x := e` rebinds: mark assigned but drop the flow fact (a
		// while-condition walrus varies every iteration, so folding
		// here would be stale). Precise folds happen at the single-
		// eval sites (runExprStmt, runIf conditions). Children still
		// run below for nested walrus expressions.
		if len(ctx.AllTest()) == 2 {
			if nm := strings.TrimSpace(ctx.Test(0).GetText()); isSimpleName(nm) {
				assigned[nm] = true
				delete(e, nm)
				delete(lists, nm)
			}
		}
	case gen.IFor_stmtContext:
		runFor(ctx, e, lists, taint, assigned, depth)
		return
	case gen.IWhile_stmtContext:
		runWhile(ctx, e, lists, taint, assigned, depth)
		return
	case gen.IIf_stmtContext:
		runIf(ctx, e, lists, taint, assigned, depth)
		return
	case gen.IExpr_stmtContext:
		runExprStmt(ctx, e, lists, taint, assigned)
		return
	case gen.IAtom_exprContext:
		// a call may mutate a list passed to it (`f(xs)`); a `NAME.` /
		// `NAME[` use may mutate directly (`xs.append(1)`). Pure builtins
		// (`len`, `print`, `sum`, …) only read.
		text := strings.ReplaceAll(ctx.GetText(), " ", "")
		killListUses(text, lists)
		killListCallArgs(text, lists)
	}
	for i := 0; i < n.GetChildCount(); i++ {
		runNode(n.GetChild(i), e, lists, taint, assigned, depth)
	}
}

func runExprStmt(ctx gen.IExpr_stmtContext, e env, lists map[string]listFact, taint map[string]bool, assigned map[string]bool) {
	// Walrus bindings in statement position evaluate once, in order:
	// fold them precisely (pre-order: an outer walrus sees unknown for
	// its inner walrus, which then folds exactly).
	walkTree(ctx, func(w antlr.Tree) {
		if wc, ok := w.(gen.INamedexpr_testContext); ok && len(wc.AllTest()) == 2 {
			if nm := strings.TrimSpace(wc.Test(0).GetText()); isSimpleName(nm) {
				assigned[nm] = true
				e.set(nm, evalText(wc.Test(1).GetText(), e))
				delete(lists, nm)
			}
		}
	})
	mark := func(nm string) {
		if isSimpleName(nm) {
			assigned[nm] = true
			delete(e, nm)
			delete(lists, nm)
		}
	}
	// a `NAME.` / `NAME[` use in this statement may mutate a tracked list
	// (`xs.append(..)`, `xs[i] = ..`), as may any call argument (`f(xs)`);
	// reads match too — toward refusal.
	killListMutations(ctx, lists)
	if ctx.Annassign() != nil {
		// annotated assignment is not a proof
		for _, t := range ctx.AllTestlist_star_expr() {
			mark(strings.TrimSpace(t.GetText()))
		}
		return
	}
	if ctx.Augassign() != nil {
		// `x <op>= <rhs>` — fold the interval when both sides are proved
		if nm, op, rhs, ok := parseAug(ctx.GetText()); ok && isSimpleName(nm) {
			assigned[nm] = true
			lhs, lok := e[nm]
			r := evalText(rhs, bindListAggregates(rhs, e, lists))
			var out iv
			if lok {
				out = applyAug(op, lhs, r)
			}
			e.set(nm, out)
			delete(lists, nm)
			return
		}
		for _, t := range ctx.AllTestlist_star_expr() {
			mark(strings.TrimSpace(t.GetText()))
		}
		return
	}
	ts := ctx.AllTestlist_star_expr()
	if len(ts) < 2 {
		for _, t := range ts {
			mark(strings.TrimSpace(t.GetText()))
		}
		return
	}
	// `x = y = <expr>`: every target gets the same proved interval
	rhsText := ts[len(ts)-1].GetText()
	// Reductions over proved lists (`len/max/min/sum`) are bound into
	// the evaluation env (list_reduce.go): the evaluator has no list
	// facts, so a composite like `sum(xs) + 1` would otherwise be ⊤.
	rhs := evalText(rhsText, bindListAggregates(rhsText, e, lists))
	// `b = arr` shares the object: either name may mutate through the other,
	// so the fact is dropped (aliases are never propagated).
	if nm := strings.TrimSpace(rhsText); isSimpleName(nm) {
		delete(lists, nm)
	}
	// a single-target `NAME = [e1, …, en]` display with proved elements earns
	// a list fact; a multi-target `x = y = […]` shares one object and earns none
	fact, haveFact := evalListLiteral(rhsText, e)
	for _, t := range ts[:len(ts)-1] {
		mark(strings.TrimSpace(t.GetText()))
		if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
			e.set(nm, rhs)
			if haveFact && len(ts) == 2 && !taint[nm] {
				lists[nm] = fact
			}
		}
	}
}

var reAug = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(//=|\*\*=|<<=|>>=|\+=|-=|\*=|/=|%=|&=|\|=|\^=|@=)(.+)$`)

func parseAug(text string) (name, op, rhs string, ok bool) {
	m := reAug.FindStringSubmatch(text)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2][:len(m[2])-1], m[3], true
}

func applyAug(op string, lhs, r iv) iv {
	switch op {
	case "+":
		return addIV(lhs, r)
	case "-":
		return subIV(lhs, r)
	case "*":
		return mulIV(lhs, r)
	case "%":
		return applyBin("%", lhs, r)
	case "/": // `/=`: true division is float, unrepresentable: unknown
		return iv{}
	}
	return iv{}
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
func markUnknownSubtree(n antlr.Tree, e env, lists map[string]listFact, assigned map[string]bool) {
	mark := func(nm string) {
		if isSimpleName(nm) {
			assigned[nm] = true
			delete(e, nm)
			delete(lists, nm)
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
		case gen.IExcept_clauseContext:
			if c.AS() != nil && c.Name() != nil {
				mark(c.Name().GetText())
			}
		}
	})
	// the subtree may mutate a tracked list without assigning it
	// (`xs.append(..)` in a try body, `f(xs)` in a match scrutinee)
	killListMutations(n, lists)
}

// rangeCounterIV proves the values a `for NAME in range(...)` counter takes.
//
// An endpoint is either an int literal or an expression the env PROVES: the
// counter is bounded by the endpoints either way, so `n = 5; for i in
// range(n)` bounds i by [0,5] exactly as `range(5)` does — and the guarded
// parameter of a dual arm (dualguard.go) bounds it the same way. A proved
// endpoint stays an interval rather than a point, so the result is a sound
// over-approximation whenever the endpoint itself is not exact. The literal
// case is byte-identical to the original: only an endpoint that is not an int
// literal consults the env.
func rangeCounterIV(ctx gen.IFor_stmtContext, e env) (iv, bool) {
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
	bounds := make([]iv, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		if v, err := strconv.ParseInt(a, 10, 64); err == nil {
			bounds = append(bounds, known(v, v))
			continue
		}
		if v := evalText(a, e); v.ok {
			bounds = append(bounds, v)
			continue
		}
		return iv{}, false
	}
	var a, b int64
	switch len(bounds) {
	case 1:
		a, b = min64(0, bounds[0].lo), max64(0, bounds[0].hi)
	default: // 2 or 3 args: the start/stop hull; the step can only widen
		a = min64(bounds[0].lo, bounds[1].lo)
		b = max64(bounds[0].hi, bounds[1].hi)
	}
	if b < a {
		a, b = b, a
	}
	return known(a, b), true
}

func runFor(ctx gen.IFor_stmtContext, e env, lists map[string]listFact, taint map[string]bool, assigned map[string]bool, depth int) {
	name := forTargetName(ctx)
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		if name != "" {
			assigned[name] = true
			delete(e, name)
			delete(lists, name)
		}
		return
	}
	iter := ""
	if tl := ctx.Testlist(); tl != nil {
		iter = tl.GetText()
		// the iterable is evaluated before the loop and may mutate a
		// tracked list through an alias (`for v in foo(arr)`)
		killListMutations(tl, lists)
	}
	if name != "" {
		// bounded iteration: the trip count is the array length. The body
		// runs exactly len times with the target rebound to the element
		// interval each trip — sound and precise for accumulators, where
		// the fixed-point path below would widen to ⊤.
		if fact, ok := iterListFact(iter, e, lists); ok &&
			(fact.length == 0 || fact.elem.ok) &&
			fact.length <= maxBoundedIter {
			itName := strings.TrimSpace(iter)
			if !isSimpleName(itName) {
				itName = ""
			}
			if itName != name && !bodyMutatesName(blocks[0], itName) {
				assigned[name] = true
				delete(lists, name)
				for k := 0; k < fact.length; k++ {
					e.set(name, fact.elem)
					runNode(blocks[0], e, lists, taint, assigned, depth+1)
				}
				// len == 0 runs the body never: the target keeps its entry value
				if len(blocks) > 1 {
					runNode(blocks[1], e, lists, taint, assigned, depth+1)
				}
				return
			}
		}
		assigned[name] = true
		delete(lists, name)
		if c, ok := rangeCounterIV(ctx, e); ok {
			e.set(name, c)
		} else {
			delete(e, name)
		}
	}
	body := blocks[0]
	for i := 0; i < 6; i++ {
		before := e.clone()
		runNode(body, e, lists, taint, assigned, depth+1)
		if envStable(e, before) {
			break
		}
		if i == 5 {
			widenChanged(e, before)
		}
	}
	if len(blocks) > 1 {
		runNode(blocks[1], e, lists, taint, assigned, depth+1)
	}
}

func runWhile(ctx gen.IWhile_stmtContext, e env, lists map[string]listFact, taint map[string]bool, assigned map[string]bool, depth int) {
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return
	}
	// the condition is evaluated before the body and may mutate a tracked
	// list through an alias (`while foo(arr):`)
	if ne := ctx.Namedexpr_test(); ne != nil {
		killListMutations(ne, lists)
	}
	body := blocks[0]
	if name, bound, ok := whileCounterBound(ctx, e); ok {
		// A bounded counter (`while i < K: i = i + k`) is an invariant, so
		// keep it proved across the body's fixed point.
		assigned[name] = true
		e.set(name, bound)
		delete(lists, name)
		for i := 0; i < 6; i++ {
			before := e.clone()
			runNode(body, e, lists, taint, assigned, depth+1)
			e.set(name, bound)
			if envStable(e, before) {
				break
			}
			if i == 5 {
				widenChanged(e, before)
			}
		}
		e.set(name, bound)
	} else {
		// unknown trip count: run once, then widen everything the body assigns.
		bodyAssigned := map[string]bool{}
		be := e.clone()
		beLists := cloneLists(lists)
		runNode(body, be, beLists, taint, bodyAssigned, depth+1)
		for k := range bodyAssigned {
			assigned[k] = true
			delete(e, k)
			delete(lists, k)
		}
		// the body may also mutate a tracked list without assigning it
		killListUses(strings.ReplaceAll(treeText(body), " ", ""), lists)
	}
	// the loop condition may contain a walrus (`while (n := f()):`); its
	// target is left ⊤ (never proved), which is sound.
	if len(blocks) > 1 {
		runNode(blocks[1], e, lists, taint, assigned, depth+1)
	}
}

var reCounterCond = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(<=|>=|<|>)([+-]?[0-9]+)$`)

// whileCounterBound proves `while <name> <op> <int literal>:` where the body's
// only assignment to <name> is a constant step in the matching direction.
// The bound is widened by one step so it also covers the value the counter
// holds when the loop exits.
func whileCounterBound(ctx gen.IWhile_stmtContext, e env) (string, iv, bool) {
	ne := ctx.Namedexpr_test()
	if ne == nil {
		return "", iv{}, false
	}
	m := reCounterCond.FindStringSubmatch(ne.GetText())
	if m == nil {
		return "", iv{}, false
	}
	name, op, litText := m[1], m[2], m[3]
	lit, err := strconv.ParseInt(litText, 10, 64)
	if err != nil {
		return "", iv{}, false
	}
	init, ok := e[name]
	if !ok {
		return "", iv{}, false
	}
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return "", iv{}, false
	}
	delta, ok := counterUpdate(blocks[0], name)
	if !ok || delta == 0 {
		return "", iv{}, false
	}
	up := op == "<" || op == "<="
	if (up && delta < 0) || (!up && delta > 0) {
		return "", iv{}, false
	}
	if up {
		// The largest value the counter takes is the loop EXIT value: the
		// biggest in-range value plus one step. `while i < K` runs for
		// i <= K-1 and exits at K; `while i <= K` runs for i <= K and exits
		// at K+1. (The old `lit-1 + delta` treated both as `<`, so `<=`
		// under-reported the max by one — an UNSOUND range.)
		b := lit
		if op == "<" {
			var ok bool
			if b, ok = addOvf(lit, -1); !ok {
				return "", iv{}, false
			}
		}
		hi, ok := addOvf(b, delta)
		if !ok {
			return "", iv{}, false
		}
		return name, known(init.lo, hi), true
	}
	// Down-counter: the smallest value is the loop exit value. `while i > K`
	// runs for i >= K+1 and exits at K; `while i >= K` runs for i >= K and
	// exits at K-1. delta is negative, so bound + delta. (The old
	// `lit+1 + -delta` ADDED the step magnitude instead of subtracting it
	// and treated both as `>`, so it reported a lower bound ABOVE K —
	// excluding the exit value: `i=10; while i>0: i=i-1` said Int[2,10]
	// when i is 0 at the print.)
	b := lit
	if op == ">" {
		var ok bool
		if b, ok = addOvf(lit, 1); !ok {
			return "", iv{}, false
		}
	}
	lo, ok := addOvf(b, delta)
	if !ok {
		return "", iv{}, false
	}
	return name, known(lo, init.hi), true
}

// counterUpdate returns the constant step applied to name in the body, and
// ok=false if the body assigns name in any other way (or not at all).
func counterUpdate(body antlr.Tree, name string) (int64, bool) {
	assigns := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `(\+?=|-=)`)
	inc := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `=` + regexp.QuoteMeta(name) + `\+([0-9]+)$`)
	dec := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `=` + regexp.QuoteMeta(name) + `-([0-9]+)$`)
	plusFirst := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `=([0-9]+)\+` + regexp.QuoteMeta(name) + `$`)
	augInc := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\+=([0-9]+)$`)
	augDec := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `-=([0-9]+)$`)

	var delta int64
	found := false
	okAll := true
	walkTree(body, func(t antlr.Tree) {
		es, isExpr := t.(gen.IExpr_stmtContext)
		if !isExpr {
			return
		}
		text := es.GetText()
		if !assigns.MatchString(text) {
			return
		}
		if found { // more than one assignment to the counter
			okAll = false
			return
		}
		found = true
		switch {
		case inc.MatchString(text):
			delta, _ = strconv.ParseInt(inc.FindStringSubmatch(text)[1], 10, 64)
		case dec.MatchString(text):
			d, _ := strconv.ParseInt(dec.FindStringSubmatch(text)[1], 10, 64)
			delta = -d
		case plusFirst.MatchString(text):
			delta, _ = strconv.ParseInt(plusFirst.FindStringSubmatch(text)[1], 10, 64)
		case augInc.MatchString(text):
			delta, _ = strconv.ParseInt(augInc.FindStringSubmatch(text)[1], 10, 64)
		case augDec.MatchString(text):
			d, _ := strconv.ParseInt(augDec.FindStringSubmatch(text)[1], 10, 64)
			delta = -d
		default:
			okAll = false
		}
	})
	return delta, found && okAll
}

func runIf(ctx gen.IIf_stmtContext, e env, lists map[string]listFact, taint map[string]bool, assigned map[string]bool, depth int) {
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return
	}
	// every condition is evaluated and may mutate through an alias
	for _, ne := range ctx.AllNamedexpr_test() {
		killListMutations(ne, lists)
		// Single evaluation: fold walrus bindings precisely.
		walkTree(ne, func(w antlr.Tree) {
			if wc, ok := w.(gen.INamedexpr_testContext); ok && len(wc.AllTest()) == 2 {
				if nm := strings.TrimSpace(wc.Test(0).GetText()); isSimpleName(nm) {
					assigned[nm] = true
					e.set(nm, evalText(wc.Test(1).GetText(), e))
					delete(lists, nm)
				}
			}
		})
	}
	merged := e.clone()
	var mergedLists map[string]listFact
	for i, b := range blocks {
		be := e.clone()
		beLists := cloneLists(lists)
		runNode(b, be, beLists, taint, assigned, depth+1)
		if i == 0 {
			merged = be
			mergedLists = beLists
		} else {
			merged.joinFrom(be)
			// a list fact survives the join only when every branch agrees
			// on the length; the elements join.
			for k, v := range mergedLists {
				o, ok := beLists[k]
				if !ok || o.length != v.length {
					delete(mergedLists, k)
				} else {
					mergedLists[k] = listFact{length: v.length, elem: join(v.elem, o.elem)}
				}
			}
		}
	}
	for k := range e {
		delete(e, k)
	}
	for k, v := range merged {
		e[k] = v
	}
	for k := range lists {
		delete(lists, k)
	}
	for k, v := range mergedLists {
		lists[k] = v
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

var reFloatLit = regexp.MustCompile(`^([0-9]+\.[0-9]*|\.[0-9]+|[0-9]+)([eE][+-]?[0-9]+)?$`)

func isFloatLit(s string) bool {
	return reFloatLit.MatchString(s) && strings.ContainsAny(s, ".eE")
}

// floatDomainFixpoint classifies each name as a Python float: EVERY
// assignment must be float-domain (so `x = 1; x = 1.5` is neither).
// obscureRHS never proves in any domain (a quoted name): bindings whose
// values no domain may claim are emitted with it so every domain drops
// them. Covers `with`/`except` targets, `del`eted names, anything `import`
// binds, and match-capture names.
const obscureRHS = `"__obscure__"`

// obscureAssigns collects the bindings whose values no domain can prove
// (see obscureRHS). The kill is deliberately broad — e.g. value patterns
// (`case Color.RED`) mention names that are reads, not binds — losing a
// declaration there is sound and rare.
func obscureAssigns(tree antlr.Tree) []assignT {
	var out []assignT
	kill := func(nm string) {
		if isSimpleName(nm) {
			out = append(out, assignT{[]string{nm}, obscureRHS})
		}
	}
	walkTree(tree, func(n antlr.Tree) {
		switch t := n.(type) {
		case gen.IWith_itemContext:
			if t.AS() != nil && t.Expr() != nil {
				kill(strings.TrimSpace(t.Expr().GetText()))
			}
		case gen.IExcept_clauseContext:
			if t.AS() != nil && t.Name() != nil {
				kill(t.Name().GetText())
			}
		case gen.IDel_stmtContext:
			if el := t.Exprlist(); el != nil {
				for _, id := range reIdentAll.FindAllString(el.GetText(), -1) {
					kill(id)
				}
			}
		case gen.IImport_stmtContext:
			walkTree(t, func(x antlr.Tree) {
				if tn, ok := x.(antlr.TerminalNode); ok &&
					tn.GetSymbol().GetTokenType() == gen.Python3ParserNAME {
					kill(tn.GetText())
				}
			})
		case gen.ICase_blockContext:
			if ps := t.Patterns(); ps != nil {
				for _, id := range reIdentAll.FindAllString(ps.GetText(), -1) {
					kill(id)
				}
			}
		}
	})
	return out
}

// obscureSet is obscureAssigns as a name set (e.g. for the container guard).
func obscureSet(tree antlr.Tree) map[string]bool {
	out := map[string]bool{}
	for _, a := range obscureAssigns(tree) {
		for _, t := range a.targets {
			out[t] = true
		}
	}
	return out
}

// walrusAssigns treats `x := e` as `x = e` for the domain proofs: a walrus
// rebinds its target (possibly to a non-numeric) wherever it appears.
func walrusAssigns(tree antlr.Tree) []assignT {
	var out []assignT
	walkTree(tree, func(n antlr.Tree) {
		ctx, ok := n.(gen.INamedexpr_testContext)
		if !ok || len(ctx.AllTest()) != 2 {
			return
		}
		if nm := strings.TrimSpace(ctx.Test(0).GetText()); isSimpleName(nm) {
			out = append(out, assignT{[]string{nm}, ctx.Test(1).GetText()})
		}
	})
	return out
}

// augAssigns desugars `x <op>= e` to `x <op> (e)` for the domain proofs.
// Every operator desugars uniformly: unprovable results (shifts in both
// domains, `/` in the int domain) simply fail their domain check.
func augAssigns(tree antlr.Tree) []assignT {
	var out []assignT
	walkTree(tree, func(n antlr.Tree) {
		ctx, ok := n.(gen.IExpr_stmtContext)
		if !ok || ctx.Augassign() == nil {
			return
		}
		nm, op, rhs, ok := parseAug(ctx.GetText())
		if !ok || !isSimpleName(nm) {
			return
		}
		out = append(out, assignT{[]string{nm}, nm + op + "(" + rhs + ")"})
	})
	return out
}

// scopeAssigns splits bindings into the kill-set (every plain/augmented/
// walrus/annotated binding in the subtree, any depth — anything that can
// unbind a name) and the creation-set (those visible at the tree's own
// scope: bindings under a nested `def`/`class` belong to that scope, not
// here). Skipping nested scopes wholesale is conservative: a float bound
// only via a decorator, default or `global` there merely stays untyped.
func scopeAssigns(tree antlr.Tree) (all, visible []assignT) {
	emit := func(dst *[]assignT, a assignT) { *dst = append(*dst, a) }
	var walk func(n antlr.Tree, nested bool)
	walk = func(n antlr.Tree, nested bool) {
		switch t := n.(type) {
		case gen.IFuncdefContext, gen.IClassdefContext:
			nested = true
		case gen.IExpr_stmtContext:
			for _, a := range plainEntries(t) {
				emit(&all, a)
				if !nested {
					emit(&visible, a)
				}
			}
			if t.Augassign() == nil {
				break
			}
			if nm, op, rhs, ok := parseAug(t.GetText()); ok && isSimpleName(nm) {
				a := assignT{[]string{nm}, nm + op + "(" + rhs + ")"}
				emit(&all, a)
				if !nested {
					emit(&visible, a)
				}
			}
		case gen.INamedexpr_testContext:
			if len(t.AllTest()) != 2 {
				break
			}
			if nm := strings.TrimSpace(t.Test(0).GetText()); isSimpleName(nm) {
				a := assignT{[]string{nm}, t.Test(1).GetText()}
				emit(&all, a)
				if !nested {
					emit(&visible, a)
				}
			}
		}
		for i := 0; i < n.GetChildCount(); i++ {
			walk(n.GetChild(i), nested)
		}
	}
	walk(tree, false)
	return all, visible
}

// plainEntries mirrors collectAssigns for one statement: plain multi-target
// assigns plus annotated assigns WITH a value (`x: T = v` rebinds; a bare
// `x: T` does not).
func plainEntries(ctx gen.IExpr_stmtContext) []assignT {
	var out []assignT
	if ctx.Augassign() != nil {
		return nil
	}
	if an := ctx.Annassign(); an != nil {
		if an.ASSIGN() == nil || len(an.AllTest()) != 2 {
			return nil
		}
		ts := ctx.AllTestlist_star_expr()
		if len(ts) < 1 {
			return nil
		}
		val := an.Test(1).GetText()
		for _, t := range ts {
			if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
				out = append(out, assignT{[]string{nm}, val})
			}
		}
		return out
	}
	ts := ctx.AllTestlist_star_expr()
	if len(ts) < 2 {
		return nil
	}
	var tg []string
	for _, t := range ts[:len(ts)-1] {
		if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
			tg = append(tg, nm)
		}
	}
	return append(out, assignT{tg, ts[len(ts)-1].GetText()})
}

func floatDomainFixpoint(tree antlr.Tree, isInt func(string) bool) map[string]bool {
	// Creation needs scope-visible bindings only (a float bound solely
	// inside a nested `def`/`class` must not become a float here); the
	// kill-set sees every binding at any depth. Obscure bindings (with/
	// except/del/import/match) can never prove, only kill.
	killAssigns, visibleAssigns := scopeAssigns(tree)
	obscure := obscureAssigns(tree)
	killAssigns = append(killAssigns, obscure...)
	visibleAssigns = append(visibleAssigns, obscure...)
	dom := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, a := range visibleAssigns {
			if floatDomain(a.rhs, dom, isInt) {
				for _, t := range a.targets {
					if !dom[t] {
						dom[t] = true
						changed = true
					}
				}
			}
		}
	}
	all := map[string]bool{}
	for k := range dom {
		all[k] = true
	}
	for _, a := range killAssigns {
		if !floatDomain(a.rhs, dom, isInt) {
			for _, t := range a.targets {
				delete(all, t)
			}
		}
	}
	return all
}

// floatDomain: Python floats are IEEE doubles, so `+ - * / % // **` on C
// doubles match Python (given the default cdivision=False).
func floatDomain(s string, dom map[string]bool, isInt func(string) bool) bool {
	s = stripOuterParens(strings.TrimSpace(s))
	if s == "" || strings.ContainsAny(s, `"'`) {
		// a string literal is not a float, and an operator INSIDE it (e.g.
		// the `/` in `'\x2f'`) must not be read as arithmetic.
		return false
	}
	if isFloatLit(s) || dom[s] {
		return true
	}
	if strings.HasPrefix(s, "float(") && strings.HasSuffix(s, ")") {
		return true
	}
	// An operator yields a float only when BOTH operands are scalar numbers
	// (proved float or proved int) and — except for `/` — at least one of
	// them is a float. `T / 2` over an unknown T is tensor division, not a
	// float: typing it `double` made Cython reject indexing on real code.
	num := func(t string) bool { return floatDomain(t, dom, isInt) || isInt(t) }
	flt := func(t string) bool { return floatDomain(t, dom, isInt) }
	if l, r, ok := splitTopTwo(s, "**"); ok {
		return num(l) && num(r) && (flt(l) || flt(r))
	}
	if l, r, ok := splitTopTwo(s, "//"); ok {
		return num(l) && num(r) && (flt(l) || flt(r))
	}
	if l, _, r, ok := splitTop(s, "/"); ok {
		return num(l) && num(r)
	}
	if l, _, r, ok := splitTop(s, "+-"); ok {
		return num(l) && num(r) && (flt(l) || flt(r))
	}
	if l, _, r, ok := splitTop(s, "*%"); ok {
		return num(l) && num(r) && (flt(l) || flt(r))
	}
	if strings.HasPrefix(s, "-") {
		return floatDomain(s[1:], dom, isInt)
	}
	return false
}

// unsafeTypedNames scans the arithmetic expressions of ONE scope and returns
// the typed names that must NOT be declared: a typed variable makes its
// whole expression evaluate in C, so every such expression must be provably
// i64-safe. `a + b`, `a * b`, shifts and bitwise ops can wrap (C has no
// Python big-int fixup); `/`, `//`, `%` are safe because the emitted header
// leaves `cdivision` at its default (False), where Cython applies Python
// semantics.
//
// De-typing every typed name in an unsafe expression is sufficient: once one
// operand is a Python object the whole expression is evaluated by Python
// (exact), and a variable's proved interval is a property of its VALUE, so
// remaining declarations stay sound.
var reIdCall = regexp.MustCompile(`\bid\s*\(`)

func banNames(text string, typed, bad map[string]bool) {
	for _, id := range reIdentAll.FindAllString(text, -1) {
		if typed[id] {
			bad[id] = true
		}
	}
}

func unsafeTypedNames(n antlr.Tree, e env, intTyped, allTyped map[string]bool) map[string]bool {
	bad := map[string]bool{}
	var walk func(antlr.Tree, bool)
	walk = func(t antlr.Tree, root bool) {
		if _, isFn := t.(gen.IFuncdefContext); isFn && !root {
			return // own scope: its own env types it
		}
		switch ctx := t.(type) {
		case gen.IExprContext:
			text := ctx.GetText()
			if strings.ContainsAny(text, "+-*&|^<>") && !evalText(text, e).ok {
				banNames(text, intTyped, bad)
			}
		case gen.IComparisonContext:
			// `a is b` / `a is not b`: identity, which a C value cannot have
			// (Cython would compare values). Refuse the operands.
			for _, op := range ctx.AllComp_op() {
				if op.IS() != nil {
					for _, ex := range ctx.AllExpr() {
						banNames(ex.GetText(), allTyped, bad)
					}
					break
				}
			}
		case gen.IAtom_exprContext:
			// `id(x)` boxes a C value into a fresh object on every call.
			if reIdCall.MatchString(ctx.GetText()) {
				for k := range allTyped {
					bad[k] = true
				}
			}
		}
		for i := 0; i < t.GetChildCount(); i++ {
			walk(t.GetChild(i), false)
		}
	}
	walk(n, true)
	return bad
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
	if v, ok := evalCall(s, e, depth); ok {
		return v
	}
	return iv{}
}

// evalCall handles the pure-int builtins the interval lattice is closed
// under: int(x) (identity on an int interval), abs(x), min/max of two
// proved ints.
func evalCall(s string, e env, depth int) (iv, bool) {
	if !strings.HasSuffix(s, ")") {
		return iv{}, false
	}
	for _, name := range []string{"int", "abs", "min", "max"} {
		if !strings.HasPrefix(s, name+"(") {
			continue
		}
		inner := s[len(name)+1 : len(s)-1]
		switch name {
		case "int":
			return evalTextDepth(inner, e, depth+1), true
		case "abs":
			v := evalTextDepth(inner, e, depth+1)
			if !v.ok || v.lo == math.MinInt64 {
				return iv{}, true
			}
			lo, hi := v.lo, v.hi
			if lo < 0 {
				lo = -lo
			}
			if hi < 0 {
				hi = -hi
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			return known(lo, hi), true
		default: // min / max
			l, r, ok := splitTopComma(inner)
			if !ok {
				return iv{}, true
			}
			a := evalTextDepth(l, e, depth+1)
			b := evalTextDepth(r, e, depth+1)
			if !a.ok || !b.ok {
				return iv{}, true
			}
			if name == "min" {
				return known(min64(a.lo, b.lo), min64(a.hi, b.hi)), true
			}
			return known(max64(a.lo, b.lo), max64(a.hi, b.hi)), true
		}
	}
	return iv{}, false
}

func splitTopComma(s string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// splitTop splits s at a top-level operator from the given set, preferring
// the RIGHTMOST split (left-associative evaluation still yields a sound
// hull). Returns ok=false when no top-level operator exists.
//
// The left `*` of a `**` is not an operator: splitting there would hand the
// evaluator a malformed operand (`2**200` -> `*200`). Nothing in the lattice
// models power, so skipping it only turns a would-be ⊤ into a ⊤ by another
// route; treating it as multiplication is what would be wrong.
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
				strings.IndexByte("+-*/%", s[i-1]) < 0 &&
				!(s[i] == '*' && i+1 < len(s) && s[i+1] == '*') {
				return s[:i], string(s[i]), s[i+1:], true
			}
		}
	}
	return "", "", "", false
}

// splitTopTwo splits at a top-level two-character operator (`**`, `//`).
func splitTopTwo(s, op string) (string, string, bool) {
	depth := 0
	for i := len(s) - len(op); i >= 0; i-- {
		switch s[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
		}
		if depth == 0 && s[i:i+len(op)] == op {
			return s[:i], s[i+len(op):], true
		}
	}
	return "", "", false
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

func itoa(n int) string { return strconv.Itoa(n) }
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
