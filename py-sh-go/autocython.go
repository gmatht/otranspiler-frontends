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
	Typed   []string // names emitted as C ints (any width)
	Refused []string // names left as Python objects (informational)
	// Evidence separates the proved VALUE range from the required C STORAGE
	// width (autocython_width.go): the same proof is honest for both the
	// annotation and the "why" column of the evidence table.
	Evidence []IntEvidence
}

// EvidenceFor returns the evidence for one name (ok=false if unproved).
func (o *CythonOutput) EvidenceFor(name string) (IntEvidence, bool) {
	for _, e := range o.Evidence {
		if e.Name == name {
			return e, true
		}
	}
	return IntEvidence{}, false
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

	moduleInts, moduleFloats := map[string]bool{}, map[string]bool{}
	moduleAssigned := map[string]bool{}
	funcInts, funcFloats := map[string]bool{}, map[string]bool{}
	moduleWidths := map[string]IntEvidence{}
	funcWidths := map[string]IntEvidence{}
	var ins []insertion
	var moduleEnv env
	if opts.Level != OptNone {
		p := proveAll(tree, opts.Level)
		moduleAssigned = p.assigned
		moduleEnv = p.env
		if fi, ok := tree.(gen.IFile_inputContext); ok {
			all := unionSets(p.ints, p.floats)
			bad := unsafeTypedNames(fi, p.env, p.ints, all)
			moduleInts = subtract(p.ints, bad)
			moduleFloats = subtract(p.floats, bad)
		}
		for n, ev := range p.widths {
			if moduleInts[n] {
				moduleWidths[n] = ev
			}
		}
		ins, funcInts, funcFloats, funcWidths = collectFuncDecls(tree, strings.Split(src, "\n"), opts.Level, opts.Mode)
	}

	// typed int lists -> a C `long long` vector (a .pyx-only transform)
	body, containerDecls := src, ""
	containerNames := map[string]bool{}
	if opts.Level != OptNone && opts.Mode == ModePyx {
		if nb, cd, cn, ok := rewriteContainers(tree, src, moduleEnv); ok {
			body, containerDecls = nb, cd
			for _, n := range cn {
				containerNames[n] = true
			}
		}
	}
	if len(ins) > 0 {
		body = applyInsertions(body, ins)
	}

	typed := map[string]bool{}
	for _, m := range []map[string]bool{moduleInts, moduleFloats, funcInts, funcFloats} {
		for n := range m {
			typed[n] = true
		}
	}
	for n := range containerNames {
		typed[n] = true
	}
	assigned := map[string]bool{}
	for n := range moduleAssigned {
		assigned[n] = true
	}
	for _, m := range []map[string]bool{funcInts, funcFloats} {
		for n := range m {
			assigned[n] = true
		}
	}

	var names, refused []string
	for n := range assigned {
		switch {
		case typed[n]:
			names = append(names, n)
		case containerNames[n]:
			// a C vector is not a scalar declaration
		default:
			refused = append(refused, n)
		}
	}
	sort.Strings(names)
	sort.Strings(refused)

	var b strings.Builder
	b.WriteString("# cython: language_level=3\n")
	if containerDecls != "" {
		b.WriteString(i64PushHelper)
	}
	if opts.Mode == ModePy {
		b.WriteString("import cython\n")
	}
	b.WriteString(declLines(sortedKeys(moduleInts), sortedKeys(moduleFloats), opts.Mode, moduleWidths))
	b.WriteString(containerDecls)
	b.WriteString(body)
	evidence := make([]IntEvidence, 0, len(moduleWidths)+len(funcWidths))
	for n, ev := range moduleWidths {
		ev.Name = n
		evidence = append(evidence, ev)
	}
	for n, ev := range funcWidths {
		ev.Name = n
		evidence = append(evidence, ev)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Name < evidence[j].Name })
	return &CythonOutput{Source: b.String(), Typed: names, Refused: refused, Evidence: evidence}, nil
}

// declLines groups the int scalars by their required C storage width
// (autocython_width.go): a value whose range AND every intermediate fit
// 32 bits earns `cdef int`, everything else `cdef long long`. A name with no
// evidence entry (only possible if the analysis changed under us) defaults to
// the conservative i64.
func declLines(ints, floats []string, mode Mode, widths map[string]IntEvidence) string {
	if len(ints) == 0 && len(floats) == 0 {
		return ""
	}
	byWidth := map[Width][]string{}
	for _, n := range ints {
		w := widths[n].Width
		if w != WidthI32 {
			w = WidthI64
		}
		byWidth[w] = append(byWidth[w], n)
	}
	for w := range byWidth {
		sort.Strings(byWidth[w])
	}
	if mode == ModePyx {
		var b strings.Builder
		for _, w := range []Width{WidthI32, WidthI64} {
			if len(byWidth[w]) > 0 {
				b.WriteString("cdef " + w.cType() + " " + strings.Join(byWidth[w], ", ") + "\n")
			}
		}
		if len(floats) > 0 {
			b.WriteString("cdef double " + strings.Join(floats, ", ") + "\n")
		}
		return b.String()
	}
	type decl struct{ name, ctype string }
	var ds []decl
	for _, w := range []Width{WidthI32, WidthI64} {
		for _, n := range byWidth[w] {
			ds = append(ds, decl{n, w.cythonType()})
		}
	}
	for _, n := range floats {
		ds = append(ds, decl{n, "cython.double"})
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].name < ds[j].name })
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.name + "=" + d.ctype
	}
	return "cython.declare(" + strings.Join(parts, ", ") + ")\n"
}

func indentLines(s, indent string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

// proof is the per-scope result: i64-typed names, double-typed names, every
// assigned name, and the int interval env (for the usage-safety scan).
type proof struct {
	ints, floats, assigned map[string]bool
	env                    env
	widths                 map[string]IntEvidence // C storage width per int scalar
}

// proveAll runs the chosen analysis for one scope.
func proveAll(tree antlr.Tree, level Level) proof {
	if level == OptSimple {
		e, assigned := proveRanges(tree)
		ints, _ := proveSimple(tree)
		return proof{ints: ints, floats: map[string]bool{}, assigned: assigned, env: e,
			widths: intEvidence(tree, e, ints)}
	}
	e, assigned := proveRanges(tree)
	allInt := intDomainFixpoint(tree)
	ints := map[string]bool{}
	for n, v := range e {
		if v.ok && allInt[n] {
			ints[n] = true
		}
	}
	floats := floatDomainFixpoint(tree)
	for n := range ints {
		delete(floats, n)
	}
	return proof{ints: ints, floats: floats, assigned: assigned, env: e,
		widths: intEvidence(tree, e, ints)}
}

func unionSets(a, b map[string]bool) map[string]bool {
	u := map[string]bool{}
	for k := range a {
		u[k] = true
	}
	for k := range b {
		u[k] = true
	}
	return u
}

func subtract(a, b map[string]bool) map[string]bool {
	o := map[string]bool{}
	for k := range a {
		if !b[k] {
			o[k] = true
		}
	}
	return o
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
func collectFuncDecls(tree antlr.Tree, lines []string, level Level, mode Mode) ([]insertion, map[string]bool, map[string]bool, map[string]IntEvidence) {
	var ins []insertion
	intsAll, floatsAll := map[string]bool{}, map[string]bool{}
	widthsAll := map[string]IntEvidence{}
	walkTree(tree, func(n antlr.Tree) {
		fn, ok := n.(gen.IFuncdefContext)
		if !ok {
			return
		}
		body := fn.Block()
		if body == nil {
			return
		}
		p := proveAll(body, level)
		all := unionSets(p.ints, p.floats)
		bad := unsafeTypedNames(body, p.env, p.ints, all)
		ints := subtract(p.ints, bad)
		floats := subtract(p.floats, bad)
		// never declare a parameter (any object at the call site) or a name
		// the function declares `global`/`nonlocal` (a local cdef would shadow
		// the outer binding).
		params := map[string]bool{}
		if prm := fn.Parameters(); prm != nil {
			for _, id := range reIdentAll.FindAllString(prm.GetText(), -1) {
				params[id] = true
			}
		}
		walkTree(body, func(t antlr.Tree) {
			switch g := t.(type) {
			case gen.IGlobal_stmtContext:
				for _, nm := range g.AllName() {
					params[nm.GetText()] = true
				}
			case gen.INonlocal_stmtContext:
				for _, nm := range g.AllName() {
					params[nm.GetText()] = true
				}
			}
		})
		var intNames, floatNames []string
		widths := map[string]IntEvidence{}
		for nm := range ints {
			if p.assigned[nm] && !params[nm] {
				intNames = append(intNames, nm)
				intsAll[nm] = true
				if ev, ok := p.widths[nm]; ok {
					widths[nm] = ev
					widthsAll[nm] = ev
				}
			}
		}
		for nm := range floats {
			if p.assigned[nm] && !params[nm] {
				floatNames = append(floatNames, nm)
				floatsAll[nm] = true
			}
		}
		if len(intNames) == 0 && len(floatNames) == 0 {
			return
		}
		sort.Strings(intNames)
		sort.Strings(floatNames)
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
		ins = append(ins, insertion{line: line, text: indentLines(declLines(intNames, floatNames, mode, widths), indent)})
	})
	return ins, intsAll, floatsAll, widthsAll
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
