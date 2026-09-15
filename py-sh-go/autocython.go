// autocython.go — the Python → typed-Cython annotation pass
// (docs/AUTO_CYTHON.md, Stage 0/1).
//
// It parses with the full ANTLR grammar (gen/) and emits pure-Python-mode
// Cython: integer scalars the analysis PROVES fit i64 get a
// `cython.declare(... = cython.longlong)`, and everything else is left as
// plain Python — exact but slow. An annotation is a proof, not a guess.
//
// The proof is the sound interval analysis in autocython_ranges.go:
// literals, literal-bounded `range`/`while` counters, and arithmetic/modulo
// whose i64 intermediate never overflows. A value whose range is unknown
// (an unbounded `while` accumulator, a non-literal divisor, a float, a
// call) stays a Python object, so the emitted file is semantics-preserving.
//
// `Options.Level` chooses how much cleverness is applied (all three levels
// are behaviour-preserving; see docs/AUTO_CYTHON.md §5):
//
//	OptNone    passthrough — no declarations at all (Stage 0)
//	OptSimple  only i64 literals and literal-bounded counters
//	OptFull    the full interval analysis (default)
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

// Level is the annotation strength.
type Level int

const (
	// OptNone emits the source unchanged (Stage 0 passthrough).
	OptNone Level = iota
	// OptSimple types only i64 literals and literal-bounded counters.
	OptSimple
	// OptFull runs the interval analysis (default).
	OptFull
)

// Options configures the annotation pass.
type Options struct {
	Level Level
	// GMP is reserved for the bigint transform (a .pyx-only rewrite to
	// cdef extern from "gmp.h"; docs/AUTO_CYTHON.md Stage 1b). Not yet
	// implemented: with it off (the default) bigints stay exact Python ints.
	GMP bool
}

// DefaultOptions is the full interval analysis, GMP off.
func DefaultOptions() Options { return Options{Level: OptFull} }

// CythonOutput is the result of the annotation pass.
type CythonOutput struct {
	Source  string   // pure-Python-mode Cython (also valid CPython)
	Typed   []string // names emitted as cdef long long
	Refused []string // names left as Python objects (informational)
}

var reSimple = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var reIntLit = regexp.MustCompile(`^[+-]?[0-9]+$`)

var pyKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

// AnnotateCython parses src and returns typed pure-Python-mode Cython plus a
// manifest. A syntax error is returned as an error (nothing is emitted).
func AnnotateCython(src string, opts Options) (*CythonOutput, error) {
	tree, errs := ParsePython(src)
	if len(errs) > 0 {
		return nil, fmt.Errorf("python2cython: %s", errs[0])
	}

	var typed, assigned map[string]bool
	switch opts.Level {
	case OptNone:
		typed, assigned = map[string]bool{}, map[string]bool{}
	case OptSimple:
		typed, assigned = proveSimple(tree)
	default:
		env, as := proveRanges(tree)
		typed, assigned = map[string]bool{}, as
		for n, v := range env {
			if v.ok {
				typed[n] = true
			}
		}
	}

	var names, refused []string
	for n := range assigned {
		if typed[n] {
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
		b.WriteString("cython.declare(" + strings.Join(decls, ", ") + ")")
		b.WriteByte('\n')
	}
	b.WriteString(src)
	return &CythonOutput{Source: b.String(), Typed: names, Refused: refused}, nil
}

// proveSimple is the OptSimple lattice: a scalar is typed iff every
// assignment to it is an i64-fitting integer literal, or it is a
// literal-bounded counter with only literal assignments besides.
func proveSimple(tree antlr.Tree) (map[string]bool, map[string]bool) {
	type info struct {
		rangeCounter bool
		assignments  int
		allLiterals  bool
	}
	infos := map[string]*info{}
	get := func(nm string) *info {
		v := infos[nm]
		if v == nil {
			v = &info{allLiterals: true}
			infos[nm] = v
		}
		return v
	}

	walkTree(tree, func(n antlr.Tree) {
		switch ctx := n.(type) {
		case gen.IFor_stmtContext:
			if nm := forTargetName(ctx); nm != "" {
				if _, ok := rangeCounterIV(ctx); ok {
					get(nm).rangeCounter = true
				}
			}
		case gen.IWhile_stmtContext:
			if nm, _, ok := whileCounterBound(ctx, env{}); ok {
				get(nm).rangeCounter = true
			}
		case gen.IExpr_stmtContext:
			ts := ctx.AllTestlist_star_expr()
			if ctx.Annassign() != nil || ctx.Augassign() != nil || len(ts) < 2 {
				for _, t := range ts {
					if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
						get(nm).allLiterals = false
					}
				}
				return
			}
			rhs := ts[len(ts)-1].GetText()
			lit := intLiteralFits64(rhs)
			for _, t := range ts[:len(ts)-1] {
				if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
					v := get(nm)
					v.assignments++
					if !lit {
						v.allLiterals = false
					}
				}
			}
		}
	})

	typed := map[string]bool{}
	assigned := map[string]bool{}
	for nm, v := range infos {
		assigned[nm] = true
		if v.allLiterals && (v.rangeCounter || v.assignments > 0) {
			typed[nm] = true
		}
	}
	return typed, assigned
}

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

func walkTree(n antlr.Tree, f func(antlr.Tree)) {
	f(n)
	for i := 0; i < n.GetChildCount(); i++ {
		walkTree(n.GetChild(i), f)
	}
}

func isSimpleName(s string) bool {
	return reSimple.MatchString(s) && !pyKeywords[s]
}
