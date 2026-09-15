// autocython.go — the Python → typed-Cython annotation pass
// (docs/AUTO_CYTHON.md, Stage 0/1).
//
// It parses with the full ANTLR grammar (gen/) and emits pure-Python-mode
// Cython: integer scalars the simple local inference PROVES get a
// `cython.declare(... = cython.longlong)`, and everything else is left as
// plain Python — exact but slow. An annotation is a proof, not a guess.
//
// Soundness rule for this slice (deliberately narrow):
//
//	a scalar is typed iff every assignment to it is an i64-fitting integer
//	literal, or it is the target of a `for` loop over `range(<int
//	literals>)` (whose counter is bounded by the literal endpoints) and
//	every other assignment is an i64 literal.
//
// In particular the pass does NOT propagate through arithmetic: `h =
// (h*31+i) % M` leaves `h` a Python object, because proving the
// intermediate fits i64 needs a range analysis (the core's
// `analyze_var_ranges`, which the C backend already runs — Stage 1 proper).
// This is the difference between typing `i` and mis-typing the growing
// accumulator in t101 (`s = s + 4000000000000000000`), which silently
// wraps. Anything refused stays exact.
//
// Output is valid CPython too (the `cython` module is importable), so the
// typed and untyped files run identically — the oracle is `cython --embed`
// matching `python3` on the same source.
package pylib

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// CythonOutput is the result of the annotation pass.
type CythonOutput struct {
	Source  string   // pure-Python-mode Cython (also valid CPython)
	Typed   []string // names emitted as cdef long long
	Refused []string // names left as Python objects (informational)
}

var (
	reIntLit = regexp.MustCompile(`^[+-]?[0-9]+$`)
	reSimple = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

var pyKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

type varInfo struct {
	rangeCounter bool // `for x in range(<int literals>)`
	assignments  int  // simple `x = ...` assignments seen
	allLiterals  bool // every assignment RHS was an i64-fitting int literal
}

func (v varInfo) typed() bool {
	return v.allLiterals && (v.rangeCounter || v.assignments > 0)
}

// AnnotateCython parses src and returns typed pure-Python-mode Cython plus a
// manifest. A syntax error is returned as an error (nothing is emitted).
func AnnotateCython(src string) (*CythonOutput, error) {
	tree, errs := ParsePython(src)
	if len(errs) > 0 {
		return nil, fmt.Errorf("python2cython: %s", errs[0])
	}

	info := map[string]*varInfo{}
	get := func(name string) *varInfo {
		v := info[name]
		if v == nil {
			v = &varInfo{allLiterals: true}
			info[name] = v
		}
		return v
	}

	walkTree(tree, func(n antlr.Tree) {
		switch ctx := n.(type) {
		case gen.IFor_stmtContext:
			if name, ok := forRangeTarget(ctx); ok {
				get(name).rangeCounter = true
			}
		case gen.IExpr_stmtContext:
			// `<name> = <literal>` only; augmented/multi-target RHS are not
			// literal-provable.
			if ctx.Annassign() != nil || ctx.Augassign() != nil {
				for _, t := range ctx.AllTestlist_star_expr() {
					if nm := t.GetText(); isSimpleName(nm) {
						get(nm).allLiterals = false
					}
				}
				return
			}
			ts := ctx.AllTestlist_star_expr()
			if len(ts) != 2 {
				for _, t := range ts {
					if nm := t.GetText(); isSimpleName(nm) {
						get(nm).allLiterals = false
					}
				}
				return
			}
			lhs := strings.TrimSpace(ts[0].GetText())
			if !isSimpleName(lhs) {
				return
			}
			v := get(lhs)
			v.assignments++
			if !intLiteralFits64(ts[1].GetText()) {
				v.allLiterals = false
			}
		}
	})

	var names, refused []string
	for n, v := range info {
		if v.typed() {
			names = append(names, n)
		} else {
			refused = append(refused, n)
		}
	}
	sort.Strings(names)
	sort.Strings(refused)

	var b strings.Builder
	b.WriteString("# cython: language_level=3\nimport cython\n")
	if len(names) > 0 {
		decls := make([]string, len(names))
		for i, n := range names {
			decls[i] = n + "=cython.longlong"
		}
		b.WriteString("cython.declare(" + strings.Join(decls, ", ") + ")\n")
	}
	b.WriteString(src)
	return &CythonOutput{Source: b.String(), Typed: names, Refused: refused}, nil
}

func walkTree(n antlr.Tree, f func(antlr.Tree)) {
	f(n)
	for i := 0; i < n.GetChildCount(); i++ {
		walkTree(n.GetChild(i), f)
	}
}

// forRangeTarget recognises `for <name> in range(<int literals>)`. The
// counter is always in [start, stop), so literal endpoints that fit i64
// prove the counter does too.
func forRangeTarget(ctx gen.IFor_stmtContext) (string, bool) {
	target, iter := ctx.Exprlist(), ctx.Testlist()
	if target == nil || iter == nil {
		return "", false
	}
	name := target.GetText()
	if !isSimpleName(name) {
		return "", false
	}
	it := iter.GetText()
	if !strings.HasPrefix(it, "range(") || !strings.HasSuffix(it, ")") {
		return "", false
	}
	inner := it[len("range(") : len(it)-1]
	for _, a := range strings.Split(inner, ",") {
		a = strings.TrimSpace(a)
		if !reIntLit.MatchString(a) || !intLiteralFits64(a) {
			return "", false
		}
	}
	return name, true
}

func isSimpleName(s string) bool {
	return reSimple.MatchString(s) && !pyKeywords[s]
}

// intLiteralFits64 reports whether s is a plain integer literal whose value
// fits i64 (a positive literal up to 2^64-1 still bounds i64 values).
func intLiteralFits64(s string) bool {
	s = strings.TrimSpace(s)
	if !reIntLit.MatchString(s) {
		return false
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if !strings.HasPrefix(s, "-") {
		if _, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 10, 64); err == nil {
			return true
		}
	}
	return false
}
