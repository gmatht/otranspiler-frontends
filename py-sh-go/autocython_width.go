// autocython_width.go — the STORAGE-WIDTH model (AUTO_CYTHON.md §12a).
//
// The interval analysis (autocython_ranges.go) proves a variable's VALUE
// range. That is not by itself the C type it should get: a variable can hold a
// value that fits in 32 bits while an expression that COMPUTES its new value
// needs more than 32 bits to evaluate.
//
//	rolling_hash: h ∈ [0, 999999999]      -> the VALUE fits int
//	              h = (h*31 + i) % M      -> h*31 reaches 31e9 -> STORAGE long long
//
// The interval analysis rejects an expression whose intermediate overflows
// *i64* (mulIV returns ⊤), which is why the recurrence is provable at all: the
// value never exceeds i64. The storage width is the narrower question — the
// smallest C integer type in which every expression that PRODUCES the
// variable's value is evaluated without wrapping.
//
// ## Why the rule is per-assignment, not per-use
//
// C (and Cython) use the *usual arithmetic conversions*: in a binary op the
// narrower operand is promoted to the wider one's type. So typing a variable
// as `int` cannot make a neighbouring `long long` computation wrap —
//
//	h = (h*31 + i) % M      # h is long long; `i` is promoted, no i32 mul
//
// which is why only the variable the value is STORED INTO needs the wider
// type. The rule is therefore:
//
//	width(v) = max( width of the proved value range v can hold,
//	                width of the RHS expression assigned to v,
//	                width of the i32 context v is passed to )
//
// The third clause covers the uses that are NOT a plain assignment target and
// so are not protected by promotion: a comparison, an argument to a C `int`
// function, a value stored through an `int` array. `rolling_hash` has none of
// those, so `i` (a range counter in [0, 2000000]) stays `int` while `h` is
// `long long` for the `h*31` intermediate.
//
// ## Evidence
//
// The two parts are independent and the API reports both, so a UI can honestly
// show "proved type" next to "Cython type":
//
//	ValueRange   the proved set of values      (Int[0,999999999])
//	StorageWidth the C type that must hold them (long long)
//	Reason       why the width is what it is    (h*31 intermediate needs 64 bits)
//
// ## Soundness
//
//   - A context that is not a provably-i64-safe integer expression never
//     contributes a width: it either refuses its operands (unsafeTypedNames
//     removes them from the typed set) or is evaluated as a Python object, in
//     which case the value is exact and no C width is involved.
//   - An i32 width is chosen only when the value range, every assigned RHS and
//     every non-promoting i32 use all fit [-2^31, 2^31-1]. Cython's `int` is a
//     C `int`, which wraps at 32 bits, so anything else would be unsound.
package pylib

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// Width is the C storage class chosen for an int scalar.
type Width int

const (
	// WidthNone: not an int scalar (a float, an object, or never typed).
	WidthNone Width = iota
	// WidthI32: C `int` — the value and every expression that produces or
	// consumes it fit in 32 bits.
	WidthI32
	// WidthI64: C `long long` — needed because the value range does not fit
	// 32 bits, or because an assigned intermediate would wrap i32
	// (rolling_hash's `h*31`).
	WidthI64
)

func (w Width) String() string {
	switch w {
	case WidthI32:
		return "i32"
	case WidthI64:
		return "i64"
	}
	return "none"
}

// cType is the traditional-Cython declaration; cythonType is the pure-Python
// mode module attribute.
func (w Width) cType() string {
	if w == WidthI32 {
		return "int"
	}
	return "long long"
}

func (w Width) cythonType() string {
	if w == WidthI32 {
		return "cython.int"
	}
	return "cython.longlong"
}

// widthFrom names what forced a width, so the evidence can explain itself.
const (
	fromValue        = "value"        // the proved range alone needs it
	fromIntermediate = "intermediate" // an assigned RHS needs it
	fromUse          = "use"          // a non-promoting i32 use needs it
)

// IntEvidence is the honest evidence for one int scalar: the proved value range
// and the required C storage width, kept separate.
type IntEvidence struct {
	Name                           string
	Lo, Hi                         int64  // the proved value range
	Width                          Width  // the C storage width
	WidthFrom                      string // fromValue | fromIntermediate | fromUse
	Intermediate                   string // the expression that forced i64, if any
	IntermediateLo, IntermediateHi int64  // its i64-proved range (for the reason)
}

// WidthName is the Cython type the width earns ("int" / "long long").
func (e IntEvidence) WidthName() string { return widthName(e.Width) }

// CythonType is the full declaration type ("cython.int" / "cython.longlong"),
// what a pure-Python-mode UI shows in the "Cython" column.
func (e IntEvidence) CythonType() string { return e.Width.cythonType() }

// ValueType is the proved-type summary for the "proved type" column, e.g.
// "Int[0,999999999]".
func (e IntEvidence) ValueType() string { return "Int" + rangeText(e.Lo, e.Hi) }

// Reason states, in one line, why the storage width is what it is — the "why"
// column of the evidence table.
func (e IntEvidence) Reason() string {
	switch {
	case e.WidthFrom == fromIntermediate && e.Intermediate != "":
		return e.Intermediate + " intermediate needs " + widthName(e.Width) +
			" (" + rangeText(e.IntermediateLo, e.IntermediateHi) + ") while the value range " +
			rangeText(e.Lo, e.Hi) + " fits " + widthName(WidthI32)
	case e.WidthFrom == fromUse:
		return "used in a 32-bit-only context, so the value range " +
			rangeText(e.Lo, e.Hi) + " must be stored in " + widthName(WidthI64)
	case e.Width == WidthI32:
		return "proved value range " + rangeText(e.Lo, e.Hi) + " fits " + widthName(WidthI32)
	default:
		return "proved value range " + rangeText(e.Lo, e.Hi) + " needs " + widthName(WidthI64)
	}
}

func widthName(w Width) string {
	if w == WidthI32 {
		return "int"
	}
	return "long long"
}

func rangeText(lo, hi int64) string {
	if lo == hi {
		return strconv.FormatInt(lo, 10)
	}
	return "[" + strconv.FormatInt(lo, 10) + "," + strconv.FormatInt(hi, 10) + "]"
}

// fitsI32 reports whether [lo,hi] fits a C int.
func fitsI32(lo, hi int64) bool {
	return lo >= math.MinInt32 && hi <= math.MaxInt32
}

// intEvidence computes the storage width of every name in `names` (the proved
// int scalars of one scope), given the proved range env.
func intEvidence(n antlr.Tree, e env, names map[string]bool) map[string]IntEvidence {
	out := map[string]IntEvidence{}
	if len(names) == 0 {
		return out
	}

	// need64: names whose assigned RHS needs 64-bit storage. One pass is
	// enough: the rule reads each expression's own interval over the proved
	// env, and the env does not depend on the widths chosen.
	need64 := map[string]bool{}
	forcedBy := map[string]string{}
	forcedLo, forcedHi := map[string]int64{}, map[string]int64{}

	record := func(id, expr string, lo, hi int64) {
		if need64[id] {
			return
		}
		// keep the widest (largest |range|) expression as the explanation
		need64[id] = true
		forcedBy[id] = expr
		forcedLo[id], forcedHi[id] = lo, hi
	}

	walkTree(n, func(t antlr.Tree) {
		if _, isFn := t.(gen.IFuncdefContext); isFn {
			return // its own scope; proveAll runs there
		}
		switch ctx := t.(type) {
		case gen.IExpr_stmtContext:
			// `v = <rhs>`: v's STORAGE must be wide enough for every
			// intermediate of the expression that computes v's new value.
			// Operands of a wider sub-computation are NOT escalated: C's usual
			// arithmetic conversions promote them (rolling_hash's `i`), so
			// only the variables that actually FEED an over-wide intermediate
			// need the wider type (`h`, in `h*31`).
			target, rhs, ok := assignRHS(ctx)
			if !ok || !names[target] {
				return
			}
			if expr, lo, hi, ok := wideIntermediate(rhs, e, target); ok {
				record(target, expr, lo, hi)
			}
		}
	})

	// A use that promotion cannot rescue: a name passed to a context that
	// stores it into a narrower C type. Every current consumer of an i32 name
	// is either an assignment RHS, a comparison operand (promoted on both
	// sides), or a store into the `long long*` int-array vector (also
	// promoted), so nothing in the subset truncates. The `fromUse` category is
	// kept in IntEvidence so a truncating context has a place to land.

	for name := range names {
		v, ok := e[name]
		if !ok {
			continue
		}
		ev := IntEvidence{Name: name, Lo: v.lo, Hi: v.hi, Width: WidthI64}
		switch {
		case !fitsI32(v.lo, v.hi):
			ev.Width, ev.WidthFrom = WidthI64, fromValue
		case need64[name]:
			ev.Width, ev.WidthFrom = WidthI64, fromIntermediate
			ev.Intermediate = forcedBy[name]
			ev.IntermediateLo, ev.IntermediateHi = forcedLo[name], forcedHi[name]
		default:
			ev.Width, ev.WidthFrom = WidthI32, fromValue
		}
		out[name] = ev
	}
	return out
}

// containsName reports whether `text` uses the identifier `name`.
func containsName(text, name string) bool {
	for _, id := range reIdentAll.FindAllString(text, -1) {
		if id == name {
			return true
		}
	}
	return false
}

// wideIntermediate finds the sub-expression of `rhs` that makes the// assignment need 64-bit storage: the widest i32-overflowing arithmetic
// sub-expression that CONTAINS `name`. It returns the expression text and its
// proved i64 range, so the evidence can say "h*31 intermediate needs 64 bits".
//
// The sub-expressions are enumerated by re-parsing the RHS text (the interval
// evaluator is textual), which keeps the rule independent of the parse-tree
// shape. Only maximal overflowing sub-expressions are reported, so the reason
// names the computation a reader would point at (`h*31`), not an arbitrary
// parent.
func wideIntermediate(rhs string, e env, name string) (string, int64, int64, bool) {
	s := stripOuterParens(strings.TrimSpace(rhs))
	if s == "" || !containsName(s, name) {
		return "", 0, 0, false
	}
	if iv := evalText(s, e); iv.ok && !fitsI32(iv.lo, iv.hi) {
		// the whole RHS does not fit i32; descend to the widest part that
		// still contains the name, so the explanation is as small as possible
		if l, _, r, ok := splitTop(s, "+-"); ok {
			if expr, lo, hi, ok := wideIntermediate(l, e, name); ok {
				return expr, lo, hi, true
			}
			if expr, lo, hi, ok := wideIntermediate(r, e, name); ok {
				return expr, lo, hi, true
			}
		}
		if l, _, r, ok := splitTop(s, "*%"); ok {
			if expr, lo, hi, ok := wideIntermediate(l, e, name); ok {
				return expr, lo, hi, true
			}
			if expr, lo, hi, ok := wideIntermediate(r, e, name); ok {
				return expr, lo, hi, true
			}
		}
		return s, iv.lo, iv.hi, true
	}
	if iv := evalText(s, e); !iv.ok {
		return "", 0, 0, false
	}
	// this sub-expression fits i32, but a CHILD of it may not: `(h*31+i) % M`
	// fits, while `h*31` inside it does not.
	if l, _, r, ok := splitTop(s, "+-"); ok {
		if expr, lo, hi, ok := wideIntermediate(l, e, name); ok {
			return expr, lo, hi, true
		}
		if expr, lo, hi, ok := wideIntermediate(r, e, name); ok {
			return expr, lo, hi, true
		}
	}
	if l, _, r, ok := splitTop(s, "*%"); ok {
		if expr, lo, hi, ok := wideIntermediate(l, e, name); ok {
			return expr, lo, hi, true
		}
		if expr, lo, hi, ok := wideIntermediate(r, e, name); ok {
			return expr, lo, hi, true
		}
	}
	return "", 0, 0, false
}

// assignRHS returns the single simple-name target, the RHS text, and whether
// this is a plain `name = <expr>` (not `+=`, not a tuple, not `x = y = e`).
// Augmented assignment is handled by looking at the whole expression instead,
// since `x += e` stores the value of `x + e`.
func assignRHS(ctx gen.IExpr_stmtContext) (string, string, bool) {
	if ctx.Annassign() != nil {
		return "", "", false
	}
	if ctx.Augassign() != nil {
		// `x <op>= <rhs>` -> the stored value is `x <op> rhs`
		if nm, op, rhs, ok := parseAug(ctx.GetText()); ok && isSimpleName(nm) {
			return nm, nm + op + rhs, true
		}
		return "", "", false
	}
	ts := ctx.AllTestlist_star_expr()
	if len(ts) != 2 {
		return "", "", false
	}
	target := strings.TrimSpace(ts[0].GetText())
	if !isSimpleName(target) {
		return "", "", false
	}
	return target, ts[1].GetText(), true
}

// intEvidenceNames is the sorted name list of an evidence map (stable output).
func intEvidenceNames(m map[string]IntEvidence) []string {
	ns := make([]string, 0, len(m))
	for n := range m {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}
