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
	Mode  Mode
	// GMP is the bigint transform (a .pyx-only rewrite to
	// cdef extern from "gmp.h"; docs/AUTO_CYTHON.md Stage 1b).
	GMP bool
}

// Mode is the output surface.
type Mode int

const (
	// ModePy is pure-Python mode: a .py that is valid CPython AND Cython,
	// with `cython.declare(...)` declarations (the default).
	ModePy Mode = iota
	// ModePyx is traditional Cython: a .pyx with `cdef` declarations. Not
	// runnable under CPython, but the native form (and what the hand-written
	// goldens use).
	ModePyx
)

// DefaultOptions is the full interval analysis, pure-Python mode, GMP off.
func DefaultOptions() Options { return Options{Level: OptFull, Mode: ModePy} }

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

	moduleTyped, moduleAssigned := map[string]bool{}, map[string]bool{}
	funcTyped := map[string]bool{}
	var ins []insertion
	switch opts.Level {
	case OptNone:
		// passthrough: no declarations at all
	default:
		var moduleEnv env
		moduleTyped, moduleAssigned, moduleEnv = proveNames(tree, opts.Level)
		if fi, ok := tree.(gen.IFile_inputContext); ok {
			for n := range unsafeTypedNames(fi, moduleEnv, moduleTyped) {
				delete(moduleTyped, n)
			}
		}
		var fnNames []string
		ins, fnNames = collectFuncDecls(tree, strings.Split(src, "\n"), opts.Level, opts.Mode)
		for _, n := range fnNames {
			funcTyped[n] = true
		}
	}

	typed := map[string]bool{}
	for n := range moduleTyped {
		typed[n] = true
	}
	for n := range funcTyped {
		typed[n] = true
	}
	assigned := map[string]bool{}
	for n := range moduleAssigned {
		assigned[n] = true
	}
	for n := range funcTyped {
		assigned[n] = true
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

	body := src
	if len(ins) > 0 {
		body = applyInsertions(src, ins)
	}
	var b strings.Builder
	b.WriteString("# cython: language_level=3\n")
	if opts.Mode == ModePy {
		b.WriteString("import cython\n")
	}
	if len(moduleTyped) > 0 {
		b.WriteString(declLine(sortedKeys(moduleTyped), opts.Mode))
	}
	b.WriteString(body)
	return &CythonOutput{Source: b.String(), Typed: names, Refused: refused}, nil
}

func declLine(names []string, mode Mode) string {
	if mode == ModePyx {
		return "cdef long long " + strings.Join(names, ", ") + "\n"
	}
	decls := make([]string, len(names))
	for i, n := range names {
		decls[i] = n + "=cython.longlong"
	}
	return "cython.declare(" + strings.Join(decls, ", ") + ")\n"
}

// proveNames runs the chosen analysis and returns the proved + assigned names
// plus the interval env (used by the usage-safety scan).
func proveNames(tree antlr.Tree, level Level) (map[string]bool, map[string]bool, env) {
	if level == OptSimple {
		e, _ := proveRanges(tree)
		typed, assigned := proveSimple(tree)
		return typed, assigned, e
	}
	e, assigned := proveRanges(tree)
	typed := map[string]bool{}
	for n, v := range e {
		if v.ok {
			typed[n] = true
		}
	}
	return typed, assigned, e
}

// insertion places text before a 1-based source line.
type insertion struct {
	line int
	text string
}

// collectFuncDecls proves each function's LOCAL ranges and returns the
// `cython.declare(...)` lines to insert at the top of each body (after a
// docstring). Parameters are never declared: a parameter can be any Python
// object, so forcing a C type would change behaviour for non-int callers.
func collectFuncDecls(tree antlr.Tree, lines []string, level Level, mode Mode) ([]insertion, []string) {
	var ins []insertion
	typed := map[string]bool{}
	walkTree(tree, func(n antlr.Tree) {
		fn, ok := n.(gen.IFuncdefContext)
		if !ok {
			return
		}
		body := fn.Block()
		if body == nil {
			return
		}
		localTyped, assigned, e := proveNames(body, level)
		for n := range unsafeTypedNames(body, e, localTyped) {
			delete(localTyped, n)
		}
		params := map[string]bool{}
		if p := fn.Parameters(); p != nil {
			for _, id := range reIdentAll.FindAllString(p.GetText(), -1) {
				params[id] = true
			}
		}
		var names []string
		for nm := range localTyped {
			if assigned[nm] && !params[nm] {
				names = append(names, nm)
				typed[nm] = true
			}
		}
		if len(names) == 0 {
			return
		}
		sort.Strings(names)
		stmts := body.AllStmt()
		if len(stmts) == 0 {
			return
		}
		line := stmts[0].GetStart().GetLine()
		if isDocstring(stmts[0]) {
			if len(stmts) > 1 {
				line = stmts[1].GetStart().GetLine()
			} else {
				line = stmts[0].GetStop().GetLine() + 1
			}
		}
		if line < 1 {
			line = 1
		}
		if line > len(lines) {
			line = len(lines)
		}
		indent := leadingWS(lines[line-1])
		ins = append(ins, insertion{line: line, text: indent + strings.TrimRight(declLine(names, mode), "\n")})
	})
	return ins, sortedKeys(typed)
}

func isDocstring(s gen.IStmtContext) bool {
	text := strings.TrimSpace(s.GetText())
	return strings.HasPrefix(text, `"`) || strings.HasPrefix(text, `'`)
}

func leadingWS(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

// applyInsertions inserts each text before its 1-based line, bottom-up so the
// line numbers stay valid.
func applyInsertions(src string, ins []insertion) string {
	lines := strings.Split(src, "\n")
	sort.Slice(ins, func(i, j int) bool { return ins[i].line > ins[j].line })
	for _, in := range ins {
		idx := in.line - 1
		if idx < 0 {
			idx = 0
		}
		if idx > len(lines) {
			idx = len(lines)
		}
		nl := make([]string, 0, len(lines)+1)
		nl = append(nl, lines[:idx]...)
		nl = append(nl, in.text)
		nl = append(nl, lines[idx:]...)
		lines = nl
	}
	return strings.Join(lines, "\n")
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
