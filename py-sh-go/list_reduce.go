// list_reduce.go — reductions over proved int lists.
//
// The interval evaluator (`evalText`) has no access to the list facts, so
// `len(xs)`, `max(xs)`, `min(xs)` and `sum(xs)` used to be unprovable — and
// everything downstream of them (an accumulator fed by `sum`, a loop bound
// derived from `len`) was refused. Both facts are already proved:
// `listFact{length, elem}` knows the trip count exactly and the element
// interval, so:
//
//	len(xs)   = the exact length
//	max(xs)   = the element interval (hi bound),  min = the lo bound
//	sum(xs)   = length × element interval
//
// and a sum-shaped loop `for v in xs: acc = acc + E` needs no unrolling at
// all: its effect is `acc += length × E[v := elem]`. That matters beyond
// tidiness: exact unrolling is capped at maxBoundedIter (256), so a
// 300-element list previously lost the accumulator's bound entirely.
//
// Everything here is a bound, never a guess: an unknown element interval, an
// empty sequence for max/min (CPython raises — no value to prove), a
// non-`+` operator, any write to another name in the body, or any call that
// is not a whitelisted pure builtin refuses, leaving the existing
// fixed-point path in charge.
package pylib

import (
	"regexp"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// evalListAggregate proves `len/max/min/sum(<proved list>)`. `text` is the
// raw RHS text; `e` the int env; `lists` the list facts.
func evalListAggregate(text string, e env, lists map[string]listFact) (iv, bool) {
	s := strings.ReplaceAll(strings.TrimSpace(text), " ", "")
	for _, fn := range []string{"len", "max", "min", "sum"} {
		pre := fn + "("
		if !strings.HasPrefix(s, pre) || !strings.HasSuffix(s, ")") {
			continue
		}
		fact, ok := iterListFact(s[len(pre):len(s)-1], e, lists)
		if !ok {
			return iv{}, false
		}
		n := known(int64(fact.length), int64(fact.length))
		switch fn {
		case "len":
			return n, true
		case "sum":
			// sum([]) is exactly 0; otherwise length × element interval
			// (mulIV refuses an unknown element interval).
			if fact.length == 0 {
				return known(0, 0), true
			}
			sum := mulIV(n, fact.elem)
			return sum, sum.ok
		default: // max, min
			// an empty sequence raises: there is no value to prove
			if fact.length == 0 || !fact.elem.ok {
				return iv{}, false
			}
			return fact.elem, true
		}
	}
	return iv{}, false
}

// reListAgg finds `fn(<simple name>)` reductions in an expression text.
var reListAgg = regexp.MustCompile(`(len|max|min|sum)\(([A-Za-z_][A-Za-z0-9_]*)\)`)

// bindListAggregates evaluates every reduction sub-expression of `text` and
// registers it in a COPY of the env keyed by its source text, so the ordinary
// evaluator can solve composite right-hand sides that merely contain one
// (`sum(xs) + 1`). The keys are call-shaped, so they can never shadow a
// variable; the copy never leaks into the real environment.
func bindListAggregates(text string, e env, lists map[string]listFact) env {
	out := e.clone()
	for _, m := range reListAgg.FindAllStringSubmatch(strings.ReplaceAll(text, " ", ""), -1) {
		if v, ok := evalListAggregate(m[0], e, lists); ok {
			out[m[0]] = v
		}
	}
	return out
}

// additiveLoopAccumulator recognises the sum shape
//
//	for v in <proved list>:
//	    acc = acc + E          # or `acc += E`, or `acc = E + acc`
//
// where every other statement in the body writes nothing and calls only
// whitelisted pure builtins, E does not mention acc, and E is provable with
// the loop target bound to the element interval. It returns the accumulator
// and E's per-trip interval: the loop's whole effect is acc += length × delta
// (the caller pairs that with the known trip count).
func additiveLoopAccumulator(body antlr.Tree, target string, e env, elem iv) (string, iv, bool) {
	acc := ""
	var delta iv
	found := false
	ok := true
	walkTree(body, func(n antlr.Tree) {
		if !ok {
			return
		}
		switch n.(type) {
		case gen.ICompound_stmtContext,
			gen.IDel_stmtContext,
			gen.IImport_stmtContext,
			gen.IGlobal_stmtContext,
			gen.INonlocal_stmtContext:
			// a branch/loop/with/try (conditional or repeated execution),
			// or a binding we do not model: not this shape
			ok = false
			return
		}
		ctx, isStmt := n.(gen.IExpr_stmtContext)
		if !isStmt {
			return
		}
		name, d, isAcc, stmtOK := additiveStmt(ctx, target, e, elem)
		if !stmtOK {
			ok = false
			return
		}
		if !isAcc {
			return
		}
		if found {
			// a second accumulator statement: not the single-increment shape
			ok = false
			return
		}
		acc, delta, found = name, d, true
	})
	if !ok || !found || acc == "" {
		return "", iv{}, false
	}
	// The body can only write the accumulator (every other assignment
	// was rejected above); if that accumulator IS the loop target,`
	// E[v := elem]` no longer describes it — Python rebinds the target
	// at the top of each trip.
	if acc == target {
		return "", iv{}, false
	}
	return acc, delta, true
}

// additiveStmt classifies one body statement. It returns (accumulator, delta)
// with isAcc=true for the `acc = acc + E` / `acc += E` form; otherwise
// isAcc=false with stmtOK reporting that the statement is inert (no write,
// only pure builtin calls). E is evaluated with the loop target bound to the
// element interval — the value it has on each trip.
func additiveStmt(ctx gen.IExpr_stmtContext, target string, e env, elem iv) (string, iv, bool, bool) {
	text := strings.ReplaceAll(ctx.GetText(), " ", "")
	inner := e.clone()
	inner.set(target, elem)
	if ctx.Annassign() != nil {
		return "", iv{}, false, false
	}
	if ctx.Augassign() != nil {
		nm, op, rhs, parsed := parseAug(text)
		if !parsed || op != "+" || containsName(rhs, nm) || !callsArePure(rhs) {
			return "", iv{}, false, false
		}
		d := evalText(rhs, inner)
		if !d.ok {
			return "", iv{}, false, false
		}
		return nm, d, true, true
	}
	ts := ctx.AllTestlist_star_expr()
	if len(ts) < 2 {
		if strings.Contains(text, ":=") || !callsArePure(text) {
			return "", iv{}, false, false
		}
		return "", iv{}, false, true
	}
	if len(ts) != 2 {
		return "", iv{}, false, false
	}
	lhs := strings.TrimSpace(ts[0].GetText())
	if !isSimpleName(lhs) {
		return "", iv{}, false, false
	}
	rhs := strings.ReplaceAll(ts[1].GetText(), " ", "")
	if !callsArePure(rhs) {
		return "", iv{}, false, false
	}
	l, op, r, ok := splitTop(rhs, "+-")
	if !ok || op != "+" {
		return "", iv{}, false, false
	}
	var expr string
	switch {
	case l == lhs && !containsName(r, lhs):
		expr = r
	case r == lhs && !containsName(l, lhs):
		expr = l
	default:
		return "", iv{}, false, false
	}
	d := evalText(expr, inner)
	if !d.ok {
		return "", iv{}, false, false
	}
	return lhs, d, true, true
}

// callsArePure reports whether every call in `text` is a whitelisted pure
// builtin (a method call `x.f(...)` is not: it may mutate).
func callsArePure(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '(' {
			continue
		}
		j := i
		for j > 0 && isIdentChar(text[j-1]) {
			j--
		}
		if j == i {
			continue // grouping parens (or a call on a subscript)
		}
		if j > 0 && text[j-1] == '.' {
			return false
		}
		if !pureBuiltinCalls[text[j:i]] {
			return false
		}
	}
	return true
}
