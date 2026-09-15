// autocython.go — the Python → typed-Cython annotation pass
// (docs/AUTO_CYTHON.md, Stage 0/1).
//
// It parses with the full ANTLR grammar (gen/) and emits pure-Python-mode
// Cython: integer scalars the analysis PROVES fit i64 get a
// `cython.declare(... = cython.longlong)`, and everything else is left as
// plain Python — exact but slow. An annotation is a proof, not a guess.
//
// The proof is the sound interval analysis in autocython_ranges.go:
// literals, literal-bounded `range` counters, and arithmetic/modulo whose
// i64 intermediate never overflows. A value whose range is unknown (an
// unbounded `while` accumulator, a non-literal divisor, a float, a call)
// stays a Python object, so the emitted file is semantics-preserving.
//
// Output is valid CPython too (the `cython` module is importable), so the
// typed and untyped files run identically — the oracle is `cython --embed`
// matching `python3` on the same source.
package pylib

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

)

// CythonOutput is the result of the annotation pass.
type CythonOutput struct {
	Source  string   // pure-Python-mode Cython (also valid CPython)
	Typed   []string // names emitted as cdef long long
	Refused []string // names left as Python objects (informational)
}

var reSimple = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
func AnnotateCython(src string) (*CythonOutput, error) {
	tree, errs := ParsePython(src)
	if len(errs) > 0 {
		return nil, fmt.Errorf("python2cython: %s", errs[0])
	}

	proved, assigned := proveRanges(tree)
	var names, refused []string
	for n := range assigned {
		if v, ok := proved[n]; ok && v.ok {
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

func walkTree(n antlr.Tree, f func(antlr.Tree)) {
	f(n)
	for i := 0; i < n.GetChildCount(); i++ {
		walkTree(n.GetChild(i), f)
	}
}

func isSimpleName(s string) bool {
	return reSimple.MatchString(s) && !pyKeywords[s]
}
