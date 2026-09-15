// i128.go — the __int128 middle tier (py2cy --i128).
//
// Cython has no native 128-bit int (like bigint, it needs help), but C
// operators work transparently on __int128, so unlike GMP this tier needs
// no per-operation rewrites — only declarations, conversions at the
// boundary (big literals in, printing out), and a vendored header
// (py2cy_int128.h). Values needing 65..128 bits land here; beyond that
// the pass declines (GMP/object territory, never guessed).
//
// The proof is big-integer EXACT intervals (not bit caps): literals,
// int64-proved vars, big-bound counters, and straight-line arithmetic
// with exact Python semantics (floordiv/mod adjust from truncated).
// Anything unboundable (loop-carried growth except counter steps,
// unknown calls, absurd-size literals) refuses. Number-shaped text that
// is not a plain int literal (floats, hex, underscores) refuses too.
package pylib

import (
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// Caps keep const-eval honest: refuse absurd literals/exponents rather
// than eat memory proving them (fail-closed, documented).
const (
	maxIntLitDigits = 10000
	maxPowBits      = 10000000
	maxShiftCount   = 10000000
)

// bigIV is an exact integer interval (either bound may be huge).
type bigIV struct {
	lo, hi *big.Int
	ok     bool
}

func bigKnown(lo, hi *big.Int) bigIV {
	return bigIV{lo: new(big.Int).Set(lo), hi: new(big.Int).Set(hi), ok: true}
}

// bigLit parses a plain decimal int literal (optional leading '-').
func bigLit(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if s == "" || len(s) > maxIntLitDigits {
		return nil, false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, false
	}
	if neg {
		v.Neg(v)
	}
	return v, true
}

// magBits reports the bit-length class: bits = significant bits of |v|
// (0 for zero), for fit checks below.
func magBits(v *big.Int) int {
	if v.Sign() == 0 {
		return 0
	}
	return v.BitLen()
}

// fitsI128 reports whether [lo,hi] fits SIGNED __int128 ([-2^127,2^127-1]).
func fitsI128(lo, hi *big.Int) bool {
	two127 := new(big.Int).Lsh(big.NewInt(1), 127)
	negLim := new(big.Int).Neg(two127)
	posLim := new(big.Int).Sub(two127, big.NewInt(1))
	return lo.Cmp(negLim) >= 0 && hi.Cmp(posLim) <= 0
}

// fitsU128 reports whether [lo,hi] fits UNSIGNED __int128 ([0,2^128-1]).
func fitsU128(lo, hi *big.Int) bool {
	if lo.Sign() < 0 {
		return false
	}
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	return hi.Cmp(new(big.Int).Sub(two128, big.NewInt(1))) <= 0
}

// MagEvidence is the audit row for one i128/u128 variable: the EXACT
// proved interval (decimal strings — never a width bucket) plus storage.
type MagEvidence struct {
	Name   string
	Lo, Hi string // exact decimal bounds
	Bits   int    // max significant bits (for the reason line)
	Signed bool   // false => u128
	Why    string // one-line derivation
}

// ValueType renders an honest proved-type summary, e.g. "Int[0,126765...]".
// It always carries real bounds (unlike a bucket), so the reValueType
// interval-shape guard accepts it.
func (e MagEvidence) ValueType() string { return "Int[" + e.Lo + "," + e.Hi + "]" }

// WidthName is the C type the variable earns ("int128" / "uint128").
func (e MagEvidence) WidthName() string {
	if e.Signed {
		return "int128"
	}
	return "uint128"
}

// magEnv maps names to exact big intervals (straight-line flow only).
type magEnv map[string]bigIV

// magConst evaluates literal-only integer arithmetic exactly (big.Int).
// Supported: + - * / % ** <<, parentheses, unary minus, decimal literals.
// Anything else (names, calls, floats, hex, shifts by huge counts,
// division by zero, absurd sizes) refuses.
func magConst(s string) (*big.Int, bool) {
	p := &magParser{s: stripOuterParens(strings.TrimSpace(s))}
	v, ok := p.add()
	if !ok || p.rest() != "" {
		return nil, false
	}
	return v, true
}

type magParser struct {
	s string
}

func (p *magParser) rest() string { return strings.TrimSpace(p.s) }

func (p *magParser) num() (*big.Int, bool) {
	t := p.rest()
	i := 0
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		i++
	}
	j := i
	for j < len(t) && t[j] >= '0' && t[j] <= '9' {
		j++
	}
	if j == i {
		return nil, false
	}
	v, ok := bigLit(t[:j])
	if !ok {
		return nil, false
	}
	p.s = t[j:]
	return v, true
}

func (p *magParser) atom() (*big.Int, bool) {
	t := p.rest()
	if strings.HasPrefix(t, "(") {
		p.s = t[1:]
		v, ok := p.add()
		if !ok || !strings.HasPrefix(p.rest(), ")") {
			return nil, false
		}
		p.s = p.rest()[1:]
		return v, true
	}
	return p.num()
}

func (p *magParser) pow() (*big.Int, bool) {
	base, ok := p.atom()
	if !ok {
		return nil, false
	}
	t := p.rest()
	if !strings.HasPrefix(t, "**") {
		return base, true
	}
	p.s = t[2:]
	exp, ok := p.pow()
	if !ok || exp.Sign() < 0 {
		return nil, false
	}
	if !exp.IsInt64() || exp.Int64() > 100000 {
		return nil, false
	}
	if base.Sign() != 0 {
		bits := base.BitLen()
		e := exp.Int64()
		if bits > 0 && e > 0 && bits*int(e) > maxPowBits {
			return nil, false
		}
	}
	return new(big.Int).Exp(base, exp, nil), true
}

func (p *magParser) unary() (*big.Int, bool) {
	t := p.rest()
	if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "- ") {
		// careful: binary minus handled by add(); only a leading unary
		// minus reaches here via mul() entry... simplified: try unary
		p.s = t[1:]
		v, ok := p.unary()
		if !ok {
			return nil, false
		}
		return new(big.Int).Neg(v), true
	}
	if strings.HasPrefix(t, "+") {
		p.s = t[1:]
		return p.unary()
	}
	return p.pow()
}

func (p *magParser) mul() (*big.Int, bool) {
	l, ok := p.unary()
	if !ok {
		return nil, false
	}
	for {
		t := p.rest()
		var op string
		switch {
		case strings.HasPrefix(t, "<<"):
			op = "<<"
		case strings.HasPrefix(t, "//"):
			op = "//"
		case len(t) > 0 && (t[0] == '*' || t[0] == '/' || t[0] == '%'):
			op = t[:1]
		default:
			return l, true
		}
		p.s = t[len(op):]
		r, ok := p.unary()
		if !ok {
			return nil, false
		}
		switch op {
		case "*":
			l = new(big.Int).Mul(l, r)
		case "//":
			if r.Sign() == 0 {
				return nil, false
			}
			l = floordiv(l, r)
		case "/":
			// true division yields float in Python — not int-domain.
			return nil, false
		case "%":
			if r.Sign() == 0 {
				return nil, false
			}
			l = floormod(l, r)
		case "<<":
			if r.Sign() < 0 || !r.IsInt64() || r.Int64() > maxShiftCount {
				return nil, false
			}
			l = new(big.Int).Lsh(l, uint(r.Int64()))
		}
		if l.BitLen() > maxPowBits {
			return nil, false
		}
	}
}

func (p *magParser) add() (*big.Int, bool) {
	l, ok := p.mul()
	if !ok {
		return nil, false
	}
	for {
		t := p.rest()
		if len(t) == 0 || (t[0] != '+' && t[0] != '-') {
			return l, true
		}
		op := t[:1]
		p.s = t[1:]
		r, ok := p.mul()
		if !ok {
			return nil, false
		}
		if op == "+" {
			l = new(big.Int).Add(l, r)
		} else {
			l = new(big.Int).Sub(l, r)
		}
		if l.BitLen() > maxPowBits {
			return nil, false
		}
	}
}

// floordiv implements Python // (floor) via truncated Quo + adjustment.
func floordiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (b.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// floormod implements Python % (sign of divisor).
func floormod(a, b *big.Int) *big.Int {
	_, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (b.Sign() < 0) {
		r.Add(r, b)
	}
	return r
}

// magText proves an exact big interval for textual integer expressions:
// big literals (any size within caps), int64-proved names from env,
// previously-proven magnitude names from dm, and straight-line
// composition (+ - * // % ** <<, unary minus). Anything else refuses.
// Sign/magnitude rules are conservative over-approximations; division by
// zero, non-integer division (/), unknown names/calls, and absurd sizes
// all refuse (fail-closed).
func magText(s string, e env, dm map[string]bigIV) (bigIV, bool) {
	s = stripOuterParens(strings.TrimSpace(s))
	if s == "" {
		return bigIV{}, false
	}
	// Literal (any size within caps).
	if v, ok := bigLit(s); ok {
		return bigKnown(v, v), true
	}
	// Plain name: int64 env first (exact), then magnitude env.
	if reIdentAll.MatchString(s) && !strings.ContainsAny(s, " \t()[]{}+-*/%<>!=,.:") {
		if iv, ok := e[s]; ok {
			return bigKnown(big.NewInt(iv.lo), big.NewInt(iv.hi)), true
		}
		if m, ok := dm[s]; ok {
			return m, true
		}
		return bigIV{}, false
	}
	// Unary minus.
	if strings.HasPrefix(s, "-") {
		if m, ok := magText(s[1:], e, dm); ok {
			return bigKnown(new(big.Int).Neg(m.hi), new(big.Int).Neg(m.lo)), true
		}
		return bigIV{}, false
	}
	if strings.HasPrefix(s, "+") {
		return magText(s[1:], e, dm)
	}
	// Binary operators, low precedence first (+ -), then (* % //), <<,
	// then ** (right-assoc). splitTop handles parens/nesting.
	for _, ops := range []string{"+-", "*/%", "<<", "**"} {
		if l, op, r, ok := splitTop(s, ops); ok {
			lm, ok1 := magText(l, e, dm)
			rm, ok2 := magText(r, e, dm)
			if !ok1 || !ok2 {
				return bigIV{}, false
			}
			return magBinop(lm, rm, op)
		}
	}
	return bigIV{}, false
}

// magBinop combines exact big intervals. All four corners evaluated
// (monotone ops need only corners, but corners are cheap and exact for
// every op here). Division/modulo use Python floor semantics; division
// by a range containing zero refuses.
func magBinop(l, r bigIV, op string) (bigIV, bool) {
	// Zero-divisor check for % and //.
	if op == "%" || op == "//" {
		if r.lo.Sign() <= 0 && r.hi.Sign() >= 0 {
			return bigIV{}, false
		}
	}
	switch op {
	case "+", "-", "*", "%", "//":
		vals := []*big.Int{}
		for _, a := range []*big.Int{l.lo, l.hi} {
			for _, b := range []*big.Int{r.lo, r.hi} {
				var v *big.Int
				switch op {
				case "+":
					v = new(big.Int).Add(a, b)
				case "-":
					v = new(big.Int).Sub(a, b)
				case "*":
					v = new(big.Int).Mul(a, b)
				case "//":
					v = floordiv(a, b)
				case "%":
					v = floormod(a, b)
				}
				vals = append(vals, v)
			}
		}
		lo, hi := vals[0], vals[0]
		for _, v := range vals[1:] {
			if v.Cmp(lo) < 0 {
				lo = v
			}
			if v.Cmp(hi) > 0 {
				hi = v
			}
		}
		if lo.BitLen() > maxPowBits || hi.BitLen() > maxPowBits {
			return bigIV{}, false
		}
		return bigKnown(lo, hi), true
	case "**":
		// Exponent must be a single non-negative constant.
		if r.lo.Cmp(r.hi) != 0 || r.lo.Sign() < 0 {
			return bigIV{}, false
		}
		if !r.lo.IsInt64() || r.lo.Int64() > 100000 {
			return bigIV{}, false
		}
		exp := r.lo.Int64()
		// Base corners; negative bases with even/odd exponents handled
		// by evaluating all four corners exactly.
		vals := []*big.Int{}
		for _, a := range []*big.Int{l.lo, l.hi} {
			vals = append(vals, new(big.Int).Exp(a, big.NewInt(exp), nil))
		}
		lo, hi := vals[0], vals[0]
		for _, v := range vals[1:] {
			if v.Cmp(lo) < 0 {
				lo = v
			}
			if v.Cmp(hi) > 0 {
				hi = v
			}
		}
		if lo.BitLen() > maxPowBits || hi.BitLen() > maxPowBits {
			return bigIV{}, false
		}
		return bigKnown(lo, hi), true
	case "<<":
		if r.lo.Cmp(r.hi) != 0 || r.lo.Sign() < 0 || !r.lo.IsInt64() || r.lo.Int64() > maxShiftCount {
			return bigIV{}, false
		}
		sh := uint(r.lo.Int64())
		lo := new(big.Int).Lsh(l.lo, sh)
		hi := new(big.Int).Lsh(l.hi, sh)
		// Lsh of a negative is exact (arithmetic shift); order preserved
		// since shift is monotone.
		if lo.BitLen() > maxPowBits || hi.BitLen() > maxPowBits {
			return bigIV{}, false
		}
		return bigKnown(lo, hi), true
	}
	return bigIV{}, false
}

// assignNameRe matches any write to a name (plain, augmented, walrus).
// Unanchored (finds writes nested in branches too — conditional writes
// veto counter inits). String literals containing "i=" over-match (veto
// direction — safe).
var assignNameReTmpl = `(?:\+=|-=|\*=|/=|%=|//=|>>=|<<=|\*\*=?|:=|=[^=])`

// bigCounterInit finds `name = <int literal>` in earlier siblings within
// the innermost enclosing block. Any other write to name in between (or
// a conditional position, which the scan cannot prove dominates) vetoes.
func bigCounterInit(loop antlr.Tree, name string) (*big.Int, bool) {
	// Climb to the innermost enclosing statement list: top-level
	// statements live directly under File_input (no Block wrapper),
	// nested suites under Block. Siblings before the loop are scanned.
	stmtNode := loop
	for p := loop.GetParent(); p != nil; p = p.GetParent() {
		if _, ok := p.(gen.IBlockContext); ok {
			break
		}
		if _, ok := p.(gen.IFile_inputContext); ok {
			break
		}
		stmtNode = p
	}
	var sibs []gen.IStmtContext
	switch blk := stmtNode.GetParent().(type) {
	case gen.IBlockContext:
		sibs = blk.AllStmt()
	case gen.IFile_inputContext:
		sibs = blk.AllStmt()
	default:
		return nil, false
	}
	wre := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `=([+-]?[0-9]+)$`)
	var init *big.Int
	seen := false
	inner := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*` + assignNameReTmpl)
	for _, st := range sibs {
		if stmtEq(st, stmtNode) {
			break
		}
		text := stripSpaces(st.GetText())
		if m := wre.FindStringSubmatch(text); m != nil {
			v, ok := bigLit(m[1])
			if !ok {
				return nil, false
			}
			init, seen = v, true
			continue
		}
		if inner.MatchString(text) {
			return nil, false
		}
	}
	if !seen {
		return nil, false
	}
	return init, true
}

// stmtEq compares tree nodes by identity.
func stmtEq(a, b antlr.Tree) bool {
	return a == b
}

// stripSpaces removes all whitespace (counterUpdate scans GetText the
// same spaceless way).
func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
}

// bigCounterBound mirrors whileCounterBound for bounds beyond int64: same
// shape detection (reCounterCond + counterUpdate), but the bound parses
// via big.Int (never overflows) and the range is exact big arithmetic.
// Returns name, exact [lo,hi], nonneg, ok.
func bigCounterBound(ctx gen.IWhile_stmtContext, e env) (string, bigIV, bool, bool) {
	ne := ctx.Namedexpr_test()
	if ne == nil {
		return "", bigIV{}, false, false
	}
	m := reCounterCond.FindStringSubmatch(ne.GetText())
	if m == nil {
		return "", bigIV{}, false, false
	}
	name, op, litText := m[1], m[2], m[3]
	bound, ok := bigLit(litText)
	if !ok {
		return "", bigIV{}, false, false
	}
	// Init comes from a dominating `name = <int literal>` sibling in the
	// innermost enclosing block — never from the int64 env, which poisons
	// the entry (ok=false) precisely when the bound overflows (the case
	// this function exists for).
	init, ok := bigCounterInit(ctx, name)
	if !ok {
		return "", bigIV{}, false, false
	}
	blocks := ctx.AllBlock()
	if len(blocks) == 0 {
		return "", bigIV{}, false, false
	}
	delta, ok := counterUpdate(blocks[0], name)
	if !ok || delta == 0 {
		return "", bigIV{}, false, false
	}
	up := op == "<" || op == "<="
	if (up && delta < 0) || (!up && delta > 0) {
		return "", bigIV{}, false, false
	}
	bigInitLo := new(big.Int).Set(init)
	bigInitHi := new(big.Int).Set(init)
	d := big.NewInt(delta)
	var lo, hi *big.Int
	if up {
		// values in [init.lo, K] (for <) or [init.lo, K] (for <=, K
		// inclusive) plus the exit step: max taken is K-1+delta / K+delta.
		top := new(big.Int).Set(bound)
		if op == "<" {
			top.Sub(top, big.NewInt(1))
		}
		top.Add(top, d)
		lo, hi = bigInitLo, top
		if bigInitHi.Cmp(hi) > 0 {
			hi = bigInitHi
		}
	} else {
		bot := new(big.Int).Set(bound)
		if op == ">" {
			bot.Add(bot, big.NewInt(1))
		}
		bot.Add(bot, d)
		lo, hi = bot, bigInitHi
		if bigInitLo.Cmp(lo) < 0 {
			lo = bigInitLo
		}
	}
	if lo.Cmp(hi) > 0 {
		return "", bigIV{}, false, false
	}
	nonneg := lo.Sign() >= 0
	return name, bigKnown(lo, hi), nonneg, true
}

// tierOf decides the storage tier for an exact big interval: i64 is
// handled by the existing pipeline (not here); 65..128 bits earn i128
// (signed) or u128 (proven non-negative, required at exactly 128 bits);
// beyond refuses (GMP/object territory).
func tierOf(lo, hi *big.Int, nonneg bool) (signed bool, ok128 bool) {
	if fitsI64Bounds(lo, hi) {
		return false, false
	}
	// Non-negative values prefer unsigned (double headroom; defined
	// wraparound instead of signed-overflow UB near the top).
	if nonneg && fitsU128(lo, hi) {
		return false, true
	}
	if fitsI128(lo, hi) {
		return true, true
	}
	return false, false
}

func fitsI64Bounds(lo, hi *big.Int) bool {
	return lo.IsInt64() && hi.IsInt64()
}

// I128Output is a `.pyx`-mode result for the __int128 middle tier.
type I128Output struct {
	Source string
	I128   []string // cdef int128 (signed) variables
	U128   []string // cdef uint128 variables
	Longs  []string // cdef long long variables (mixed programs)
	Mag    []MagEvidence
}

// classifyI128 returns the i128/u128/long variable sets with magnitude
// evidence. Soundness shape (all fail-closed):
//   - counters: bigCounterBound on while loops with no loop/def
//     ancestors (nested-in-loop counters could suffer outer
//     interference; function bodies are own scopes).
//   - straight-line assigns: single program-order pass with HULL merge
//     (conditional assigns union soundly); assigns inside loop bodies or
//     function scopes skipped via ancestor check (loop-carried growth is
//     unbounded without widening; GMP/object covers those vars).
//   - int64-fit names stay canonical in the existing pipeline.
func classifyI128(tree antlr.Tree) (i128, u128, longs map[string]bool, mag []MagEvidence) {
	i128, u128, longs = map[string]bool{}, map[string]bool{}, map[string]bool{}
	proved, _ := proveRanges(tree)
	dm := magEnv{}
	// Counter bounds first (position-independent: the range always
	// contains the init, whether or not the loop runs).
	walkTree(tree, func(n antlr.Tree) {
		ctx, ok := n.(gen.IWhile_stmtContext)
		if !ok || scopedAncestor(n) {
			return
		}
		if name, m, _, ok := bigCounterBound(ctx, proved); ok {
			hullMerge(dm, name, m)
		}
	})
	// Straight-line assigns in collection order (document order).
	for _, a := range collectAssigns(tree) {
		if len(a.targets) != 1 {
			continue
		}
		// NOTE: collectAssigns loses positions; the ancestor check needs
		// the node. Handled below by re-walking with nodes (see
		// collectAssignNodes).
		_ = a
	}
	for _, na := range collectAssignNodes(tree) {
		if len(na.targets) != 1 {
			continue
		}
		if scopedAncestor(na.node) {
			continue
		}
		if m, ok := magText(na.rhs, proved, dm); ok {
			hullMerge(dm, na.targets[0], m)
		}
	}
	// Tier decision with evidence rows (deterministic order).
	names := make([]string, 0, len(dm))
	for n := range dm {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m := dm[n]
		signed, ok128 := tierOf(m.lo, m.hi, m.lo.Sign() >= 0)
		if !ok128 {
			continue
		}
		bits := m.lo.BitLen()
		if b := m.hi.BitLen(); b > bits {
			bits = b
		}
		why := "exact range [" + m.lo.String() + "," + m.hi.String() + "] needs " +
			map[bool]string{true: "int128", false: "uint128"}[signed]
		ev := MagEvidence{Name: n, Lo: m.lo.String(), Hi: m.hi.String(),
			Bits: bits, Signed: signed, Why: why}
		if signed {
			i128[n] = true
		} else {
			u128[n] = true
		}
		mag = append(mag, ev)
	}
	return i128, u128, longs, mag
}

// scopedAncestor reports whether n sits inside a loop body or a function
// scope (via ANTLR parent pointers). Counter/assign proofs must not cross
// these: loop-carried growth is unbounded, function bodies are own scopes.
func scopedAncestor(n antlr.Tree) bool {
	for p := n.GetParent(); p != nil; p = p.GetParent() {
		switch p.(type) {
		case gen.IWhile_stmtContext, gen.IFor_stmtContext,
			gen.IFuncdefContext, gen.IClassdefContext, gen.ILambdefContext:
			return true
		}
	}
	return false
}

// nodeAssign is collectAssigns with the source node retained for scoping.
type nodeAssign struct {
	node    antlr.Tree
	targets []string
	rhs     string
}

// collectAssignNodes mirrors collectAssigns (same shapes: plain,
// non-augmented, non-annotated, simple-name targets) keeping nodes.
func collectAssignNodes(tree antlr.Tree) []nodeAssign {
	var out []nodeAssign
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
		out = append(out, nodeAssign{n, tg, ts[len(ts)-1].GetText()})
	})
	return out
}

// hullMerge unions a new proof into the accumulated range (monotone:
// unions can only grow, so multi-assign and conditional-assign vars stay
// sound).
func hullMerge(dm magEnv, name string, m bigIV) {
	if old, ok := dm[name]; ok {
		lo, hi := old.lo, old.hi
		if m.lo.Cmp(lo) < 0 {
			lo = m.lo
		}
		if m.hi.Cmp(hi) > 0 {
			hi = m.hi
		}
		dm[name] = bigKnown(lo, hi)
		return
	}
	dm[name] = m
}
