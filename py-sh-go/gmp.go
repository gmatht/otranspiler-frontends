// gmp.go — the bigint transform (docs/AUTO_CYTHON.md Stage 1b, `py2cy --gmp`).
//
// Cython has no native bigint, so a bigint variable cannot be typed with a
// declaration; it must be REWRITTEN to the GMP C API. This pass recognises a
// scalar integer program whose integer variables are either provably i64
// (declared `cdef long long`) or unbounded (declared `cdef mpz_t` and
// rewritten to GMP calls), and emits a `.pyx`:
//
//	cdef mpz_t x          mpz_init(x)          mpz_ui_pow_ui(x, 2, 100)
//	                      mpz_mul_ui(x, x, 3)  gmp_printf("%Zd\n", x)
//
// It is REFUSE > GUESS: any statement or expression it cannot rewrite
// exactly makes it decline (the caller then falls back to the exact
// pure-Python-mode output, where the bigint is a CPython int — correct but
// not faster).
package pylib

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// gmpBlock is the GMP FFI declaration block (the hand-written golden's
// `cdef extern from "gmp.h"`, plus gmp_printf for output).
const gmpBlock = `from libc.stdio cimport printf
cdef extern from "gmp.h":
    ctypedef struct __mpz_struct:
        int _mp_alloc
        int _mp_size
        void *_mp_d
    ctypedef __mpz_struct mpz_t[1]
    void mpz_init(mpz_t)
    void mpz_clear(mpz_t)
    void mpz_set_si(mpz_t, long)
    int mpz_set_str(mpz_t, const char*, int)
    void mpz_ui_pow_ui(mpz_t, unsigned long, unsigned long)
    void mpz_mul_ui(mpz_t, const mpz_t, unsigned long)
    void mpz_add_ui(mpz_t, const mpz_t, unsigned long)
    void mpz_sub_ui(mpz_t, const mpz_t, unsigned long)
    void mpz_mul(mpz_t, const mpz_t, const mpz_t)
    void mpz_add(mpz_t, const mpz_t, const mpz_t)
    void mpz_sub(mpz_t, const mpz_t, const mpz_t)
    void mpz_neg(mpz_t, const mpz_t)
    unsigned long mpz_fdiv_ui(const mpz_t, unsigned long)
    void mpz_fdiv_r_ui(mpz_t, const mpz_t, unsigned long)
    int gmp_printf(const char*, ...)`

// GMPOutput is a `pyx`-mode result.
type GMPOutput struct {
	Source string
	Big    []string // mpz_t variables
	Longs  []string // cdef long long variables
}

// AnnotateGMP parses src and, if every integer variable is either provably
// i64 or rewritable to GMP, returns the `.pyx`. ok=false means "decline"
// (no bigint, or a construct this pass does not rewrite exactly).
func AnnotateGMP(src string) (*GMPOutput, bool, error) {
	tree, errs := ParsePython(src)
	if len(errs) > 0 {
		return nil, false, gmpErr(errs[0])
	}
	// The transform targets `.pyx` exclusively, so a Cython-reserved
	// identifier anywhere in the file declines (a `cdef` for it would not
	// parse, and verbatim statements like `from x import include` would not
	// either). The caller falls back to the exact pure-Python output.
	if len(pyxBlockingNames(tree)) > 0 {
		return nil, false, nil
	}
	big, longs := classifyBigints(tree)
	if len(big) == 0 {
		return nil, false, nil
	}
	r := &pyxR{big: big, longs: longs, ok: true}
	r.render(tree)
	if !r.ok {
		return nil, false, nil
	}
	var b strings.Builder
	b.WriteString("# cython: language_level=3\n")
	b.WriteString(gmpBlock)
	b.WriteByte('\n')
	for _, n := range r.sortedLongs() {
		b.WriteString("cdef long long " + n + "\n")
	}
	for _, n := range r.sortedBig() {
		b.WriteString("cdef mpz_t " + n + "\n")
	}
	for _, n := range r.sortedBig() {
		b.WriteString("mpz_init(" + n + ")\n")
	}
	for _, line := range r.out {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, n := range r.sortedBig() {
		b.WriteString("mpz_clear(" + n + ")\n")
	}
	return &GMPOutput{Source: b.String(), Big: r.sortedBig(), Longs: r.sortedLongs()}, true, nil
}

type gmpError string

func (e gmpError) Error() string { return string(e) }
func gmpErr(msg string) error    { return gmpError("python2cython(gmp): " + msg) }

// classifyBigints returns the unbounded-int (mpz_t) and i64-proved vars.
func classifyBigints(tree antlr.Tree) (big, longs map[string]bool) {
	proved, assigned := proveRanges(tree)
	dom := intDomainFixpoint(tree)
	big, longs = map[string]bool{}, map[string]bool{}
	for n := range assigned {
		switch {
		case proved[n].ok:
			longs[n] = true
		case dom[n]:
			big[n] = true
		}
	}
	return big, longs
}

// assignT is one `a = b = <rhs>` statement's simple-name targets and RHS.
type assignT struct {
	targets []string
	rhs     string
}

// collectAssigns gathers every plain (non-augmented, non-annotated)
// assignment's simple-name targets and RHS text.
func collectAssigns(tree antlr.Tree) []assignT {
	var out []assignT
	walkTree(tree, func(n antlr.Tree) {
		ctx, ok := n.(gen.IExpr_stmtContext)
		if !ok || ctx.Annassign() != nil || ctx.Augassign() != nil {
			return
		}
		ts := ctx.AllTestlist_star_expr()
		if len(ts) < 2 {
			return
		}
		var tg []string
		for _, t := range ts[:len(ts)-1] {
			if nm := strings.TrimSpace(t.GetText()); isSimpleName(nm) {
				tg = append(tg, nm)
			}
		}
		out = append(out, assignT{tg, ts[len(ts)-1].GetText()})
	})
	return out
}

// intDomainFixpoint classifies each name as a Python int: EVERY assignment
// (and a literal-bounded range counter) must be int-domain. "Any assignment"
// would be wrong — `x = 1; x = 1.5` is not an int.
func intDomainFixpoint(tree antlr.Tree) map[string]bool {
	assigns := collectAssigns(tree)
	taint := closedOverNames(tree)
	// intDomainFixpoint has no flow sensitivity, so a name used as `NAME.` /
	// `NAME[` anywhere may have been mutated after its list assignment and
	// never earns an int-list fact (the flow pass kills such facts precisely,
	// in order; this is the conservative analogue).
	attrUsed := attrUsedNames(tree)
	seed := map[string]bool{}
	walkTree(tree, func(n antlr.Tree) {
		if ctx, ok := n.(gen.IFor_stmtContext); ok {
			if nm := forTargetName(ctx); nm != "" {
				if _, ok := rangeCounterIV(ctx); ok {
					seed[nm] = true
				}
			}
		}
	})
	dom := map[string]bool{}
	for k := range seed {
		dom[k] = true
	}
	// intLists mirrors the flow pass's list facts without flow sensitivity:
	// a single-target `NAME = [<int-domain elems>]` earns one; any other
	// assignment to the name, or an alias `b = arr`, kills it permanently
	// (deadLists keeps the fixpoint from oscillating).
	intLists := map[string]bool{}
	deadLists := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, a := range assigns {
			if len(a.targets) == 1 && isIntListRHS(a.rhs, dom) {
				if t := a.targets[0]; !taint[t] && !attrUsed[t] && !deadLists[t] && !intLists[t] {
					intLists[t] = true
					changed = true
				}
			} else {
				for _, t := range a.targets {
					if !deadLists[t] {
						deadLists[t] = true
						changed = true
					}
					delete(intLists, t)
				}
				// `b = arr` shares the object: the alias kills the fact
				if nm := strings.TrimSpace(a.rhs); isSimpleName(nm) && !deadLists[nm] {
					deadLists[nm] = true
					delete(intLists, nm)
					changed = true
				}
			}
			if intDomain(a.rhs, dom) {
				for _, t := range a.targets {
					if !dom[t] {
						dom[t] = true
						changed = true
					}
				}
			}
		}
		// `for v in <int list>`: the target takes int elements. (The trip
		// count is bounded separately by the flow pass; here only the domain
		// matters.) A rebound target is no longer a list.
		walkTree(tree, func(n antlr.Tree) {
			ctx, ok := n.(gen.IFor_stmtContext)
			if !ok {
				return
			}
			nm := forTargetName(ctx)
			if nm == "" || dom[nm] || ctx.Testlist() == nil {
				return
			}
			if isIntListExpr(ctx.Testlist().GetText(), dom, intLists) {
				dom[nm] = true
				delete(intLists, nm)
				changed = true
			}
		})
	}
	all := map[string]bool{}
	for k := range dom {
		all[k] = true
	}
	for _, a := range assigns {
		if !intDomain(a.rhs, dom) {
			for _, t := range a.targets {
				delete(all, t)
			}
		}
	}
	return all
}

// intDomain is the integer-domain classifier (a superset of the interval
// lattice: a value can be an int without a proved bound).
func intDomain(s string, dom map[string]bool) bool {
	s = stripOuterParens(strings.TrimSpace(s))
	if s == "" || strings.ContainsAny(s, `"'`) {
		return false
	}
	if l, r, ok := splitTopTwo(s, "**"); ok {
		return intDomain(l, dom) && intDomain(r, dom)
	}
	if l, r, ok := splitTopTwo(s, "//"); ok {
		return intDomain(l, dom) && intDomain(r, dom)
	}
	if l, _, r, ok := splitTop(s, "+-"); ok {
		return intDomain(l, dom) && intDomain(r, dom)
	}
	if l, _, r, ok := splitTop(s, "*%"); ok {
		return intDomain(l, dom) && intDomain(r, dom)
	}
	if strings.HasPrefix(s, "-") {
		return intDomain(s[1:], dom)
	}
	if reIntLit.MatchString(s) {
		return true
	}
	if dom[s] {
		return true
	}
	for _, fn := range []string{"int", "abs", "len"} {
		if strings.HasPrefix(s, fn+"(") && strings.HasSuffix(s, ")") {
			return intDomain(s[len(fn)+1:len(s)-1], dom)
		}
	}
	for _, fn := range []string{"min", "max"} {
		if strings.HasPrefix(s, fn+"(") && strings.HasSuffix(s, ")") {
			l, r, ok := splitTopComma(s[len(fn)+1 : len(s)-1])
			return ok && intDomain(l, dom) && intDomain(r, dom)
		}
	}
	return false
}

// attrUsedNames collects names used as `NAME.` / `NAME[` anywhere under tree.
func attrUsedNames(tree antlr.Tree) map[string]bool {
	out := map[string]bool{}
	walkTree(tree, func(n antlr.Tree) {
		switch t := n.(type) {
		case gen.IAtom_exprContext:
			for _, m := range reAttrSub.FindAllStringSubmatch(t.GetText(), -1) {
				out[m[1]] = true
			}
		case gen.IExpr_stmtContext:
			for _, m := range reAttrSub.FindAllStringSubmatch(t.GetText(), -1) {
				out[m[1]] = true
			}
		}
	})
	return out
}

// isIntListRHS reports whether rhs is an int-domain list DISPLAY. Aliases
// (`b = arr`) return false: without flow sensitivity an alias cannot prove
// the object was not mutated through the other name.
func isIntListRHS(rhs string, dom map[string]bool) bool {
	s := strings.TrimSpace(rhs)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return false
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return true
	}
	if strings.ContainsAny(inner, "[]") {
		return false
	}
	for _, p := range splitTopCommas(inner) {
		if !intDomain(p, dom) {
			return false
		}
	}
	return true
}

// isIntListExpr reports whether a `for` iterable is an int-domain list: a
// display, or a name holding an int-list fact.
func isIntListExpr(iter string, dom map[string]bool, intLists map[string]bool) bool {
	if isIntListRHS(iter, dom) {
		return true
	}
	return intLists[strings.TrimSpace(iter)]
}

// ── rendering ────────────────────────────────────────────────────────────

type pyxR struct {
	big        map[string]bool
	longs      map[string]bool
	out        []string
	ok         bool
	lastIndent int
}

func (r *pyxR) sortedBig() []string   { return sortedKeys(r.big) }
func (r *pyxR) sortedLongs() []string { return sortedKeys(r.longs) }

func (r *pyxR) emit(indent int, s string) {
	r.out = append(r.out, strings.Repeat("    ", indent)+s)
}

func (r *pyxR) render(tree antlr.Tree) {
	fi, ok := tree.(gen.IFile_inputContext)
	if !ok {
		r.ok = false
		return
	}
	r.stmts(fi.AllStmt(), 0)
}

func (r *pyxR) stmts(list []gen.IStmtContext, indent int) {
	for _, s := range list {
		if ss := s.Simple_stmts(); ss != nil {
			for _, sm := range ss.AllSimple_stmt() {
				r.simple(sm, indent)
			}
		} else if cs := s.Compound_stmt(); cs != nil {
			r.compound(cs, indent)
		} else {
			r.ok = false
		}
	}
}

func (r *pyxR) simple(sm gen.ISimple_stmtContext, indent int) {
	if e := sm.Expr_stmt(); e != nil {
		r.exprStmt(e, indent)
		return
	}
	if sm.Pass_stmt() != nil {
		r.emit(indent, "pass")
		return
	}
	r.ok = false
}

func (r *pyxR) exprStmt(e gen.IExpr_stmtContext, indent int) {
	text := e.GetText()
	if strings.HasPrefix(text, "print(") && strings.HasSuffix(text, ")") {
		r.lastIndent = indent
		r.print(text[len("print(") : len(text)-1])
		return
	}
	ts := e.AllTestlist_star_expr()
	if e.Annassign() != nil || e.Augassign() != nil || len(ts) != 2 {
		r.ok = false
		return
	}
	lhs := strings.TrimSpace(ts[0].GetText())
	if !isSimpleName(lhs) {
		r.ok = false
		return
	}
	rhs := ts[1].GetText()
	if r.big[lhs] {
		r.bigAssign(lhs, rhs, indent)
		return
	}
	if r.mentionsBig(rhs) {
		// assigning a bigint into a non-bigint is a narrowing we don't model
		r.ok = false
		return
	}
	r.emit(indent, lhs+" = "+rhs)
}

// print is called from exprStmt; the caller passes the argument text.
func (r *pyxR) print(arg string) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		r.emit(r.lastIndent, `printf("\n")`)
	case strings.HasPrefix(arg, `"`) || strings.HasPrefix(arg, `'`):
		r.emit(r.lastIndent, `printf("`+strings.Trim(arg, `"'`)+`\n")`)
	case r.big[arg]:
		r.emit(r.lastIndent, `gmp_printf("%Zd\n", `+arg+`)`)
	default:
		if l, rr, ok := splitTopTwo(arg, "%"); ok && r.big[l] && isSmallUint(rr) {
			r.emit(r.lastIndent, `printf("%lu\n", mpz_fdiv_ui(`+l+`, `+rr+`))`)
			return
		}
		if r.mentionsBig(arg) {
			r.ok = false
			return
		}
		r.emit(r.lastIndent, `printf("%lld\n", `+arg+`)`)
	}
}

// bigAssign rewrites `target = <rhs>` where target is an mpz_t.
func (r *pyxR) bigAssign(target, rhs string, indent int) {
	s := strings.ReplaceAll(rhs, " ", "")
	if reIntLit.MatchString(s) {
		r.emit(indent, `mpz_set_str(`+target+`, "`+s+`", 10)`)
		return
	}
	if s == target {
		return
	}
	if r.big[s] {
		r.emit(indent, "mpz_set("+target+", "+s+")")
		return
	}
	// a ** b with literal operands
	if l, rr, ok := splitTopTwo(s, "**"); ok {
		if isSmallUint(l) && isSmallUint(rr) {
			r.emit(indent, "mpz_ui_pow_ui("+target+", "+l+", "+rr+")")
			return
		}
	}
	// binary op with one mpz operand and one small literal (or two mpz)
	for _, op := range []string{"*", "+", "-", "%"} {
		l, _, rr, ok := splitTop(s, op)
		if !ok {
			continue
		}
		switch {
		case r.big[l] && isSmallUint(rr):
			r.emit(indent, gmpUI(op, target, l, rr))
			return
		case isSmallUint(l) && r.big[rr]:
			if op == "*" || op == "+" {
				r.emit(indent, gmpUI(op, target, rr, l))
				return
			}
		case r.big[l] && r.big[rr]:
			r.emit(indent, gmpFull(op, target, l, rr))
			return
		}
	}
	r.ok = false
}

func gmpUI(op, target, m, lit string) string {
	switch op {
	case "*":
		return "mpz_mul_ui(" + target + ", " + m + ", " + lit + ")"
	case "+":
		return "mpz_add_ui(" + target + ", " + m + ", " + lit + ")"
	case "-":
		return "mpz_sub_ui(" + target + ", " + m + ", " + lit + ")"
	case "%":
		return "mpz_fdiv_r_ui(" + target + ", " + m + ", " + lit + ")"
	}
	return ""
}

func gmpFull(op, target, a, b string) string {
	switch op {
	case "*":
		return "mpz_mul(" + target + ", " + a + ", " + b + ")"
	case "+":
		return "mpz_add(" + target + ", " + a + ", " + b + ")"
	case "-":
		return "mpz_sub(" + target + ", " + a + ", " + b + ")"
	}
	return ""
}

func (r *pyxR) compound(cs gen.ICompound_stmtContext, indent int) {
	switch {
	case cs.While_stmt() != nil:
		w := cs.While_stmt()
		cond := w.Namedexpr_test().GetText()
		if r.mentionsBig(cond) {
			r.ok = false
			return
		}
		r.emit(indent, "while "+cond+":")
		r.block(w.Block(0), indent+1)
	case cs.For_stmt() != nil:
		f := cs.For_stmt()
		target, iter := f.Exprlist().GetText(), f.Testlist().GetText()
		if r.mentionsBig(iter) || !strings.HasPrefix(iter, "range(") {
			r.ok = false
			return
		}
		if !r.longs[target] && !r.big[target] {
			r.ok = false
			return
		}
		r.emit(indent, "for "+target+" in "+iter+":")
		r.block(f.Block(0), indent+1)
	case cs.If_stmt() != nil:
		ifs := cs.If_stmt()
		if r.mentionsBig(ifs.Namedexpr_test(0).GetText()) {
			r.ok = false
			return
		}
		blocks := ifs.AllBlock()
		for i, b := range blocks {
			switch {
			case i == 0:
				r.emit(indent, "if "+ifs.Namedexpr_test(0).GetText()+":")
			case i == len(blocks)-1 && ifs.ELSE() != nil:
				r.emit(indent, "else:")
			default:
				r.emit(indent, "elif "+ifs.Namedexpr_test(i).GetText()+":")
			}
			r.block(b, indent+1)
		}
	default:
		r.ok = false
	}
}

func (r *pyxR) block(b gen.IBlockContext, indent int) {
	if b == nil {
		r.ok = false
		return
	}
	if len(b.AllStmt()) == 0 {
		r.emit(indent, "pass")
		return
	}
	r.stmts(b.AllStmt(), indent)
}

// mentionsBig reports whether any bigint name occurs as an identifier in a
// piece of code (conservative).
func (r *pyxR) mentionsBig(s string) bool {
	for _, id := range reIdentAll.FindAllString(s, -1) {
		if r.big[id] {
			return true
		}
	}
	return false
}

var reIdentAll = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func isSmallUint(s string) bool {
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(s) {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small maps: insertion sort keeps it dependency-free
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
