// pep695.go — PEP 695 (Python 3.12+) type-parameter lowering.
//
// `def f[T]`, `class C[T]` and `type X = ...` do not parse with the
// pre-12 grammar, so today any file using them fails outright. This pass
// rewrites them into pre-12 syntax with identical runtime behavior:
//
//	def f[T: int, *Ts, **P](x: T) -> T
//	  ->  T = typing.TypeVar("T", bound=int)
//	      Ts = typing.TypeVarTuple("Ts")
//	      P = typing.ParamSpec("P")
//	      def f(x: T) -> T
//
//	class C[T](Base)  ->  T = typing.TypeVar("T")
//	                      class C(Base, typing.Generic[T])
//
//	type X[T] = list[T]  ->  T = typing.TypeVar("T")
//	                         X = list[T]
//
// The hook in AnnotateCython only fires on files that already fail to
// parse, so the lowering cannot change the output for anything that
// parses today; when the lowering declines, the original parse error is
// returned unchanged.
//
// Soundness boundary (documented in docs/AUTO_CYTHON.md): the lowering
// preserves runtime behavior EXCEPT (a) `type X = ...` is eager where
// PEP 695 is lazy (a right-hand side the check cannot prove safe
// declines the file instead), and (b) `__type_params__` /
// `TypeAliasType` identity / `__orig_bases__` introspection, which
// pre-12 syntax cannot express. Anything doubtful declines.
package pylib

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// pepTok is one significant token (whitespace/comments are skipped by the
// lexer; strings survive as single opaque tokens).
type pepTok struct {
	typ         int
	text        string
	line        int
	start, stop int // rune offsets into the source; stop is inclusive
}

// pepLex lexes src and returns the significant tokens, or nil when lexing
// itself fails (the caller then keeps the original parse error).
func pepLex(src string) []pepTok {
	runes := []rune(src)
	ec := &errCollector{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lex := gen.NewPython3Lexer(antlr.NewInputStream(src))
	lex.RemoveErrorListeners()
	lex.AddErrorListener(ec)
	stream := antlr.NewCommonTokenStream(lex, antlr.TokenDefaultChannel)
	stream.Fill()
	if len(ec.msgs) > 0 {
		return nil
	}
	raw := stream.GetAllTokens()
	out := make([]pepTok, 0, len(raw))
	for _, t := range raw {
		if t.GetTokenType() == antlr.TokenEOF {
			continue
		}
		a, b := t.GetStart(), t.GetStop()
		tt := t.GetTokenType()
		if tt == gen.Python3LexerNEWLINE || tt == gen.Python3LexerINDENT ||
			tt == gen.Python3LexerDEDENT {
			// Synthetic tokens: only the type and line are meaningful;
			// offsets/text are fabricated by the lexer base. No splice
			// below ever uses their offsets (only real tokens').
			out = append(out, pepTok{typ: tt, text: t.GetText(),
				line: t.GetLine(), start: -1, stop: -1})
			continue
		}
		// Offsets are rune-based; self-check before trusting them, since
		// every splice below is computed from them.
		if a < 0 || b < a || b >= len(runes) || string(runes[a:b+1]) != t.GetText() {
			return nil
		}
		out = append(out, pepTok{typ: tt, text: t.GetText(),
			line: t.GetLine(), start: a, stop: b})
	}
	return out
}

// pepSpan is a [start, end) rune span covering a string literal or comment
// in the ORIGINAL source, computed by an independent quote-aware scan.
// Keywords falling inside a span are string/comment text even when the
// grammar lexer mis-splits them (3.12 nested same-quote f-strings), so
// they must never trigger a rewrite.
type pepSpan struct{ start, end int }

// pepSpans returns the string/comment spans of src. ok=false only when the
// scan itself gives up (unterminated literal); the caller then declines.
func pepSpans(src string) ([]pepSpan, bool) {
	r := []rune(src)
	var spans []pepSpan
	i := 0
	for i < len(r) {
		c := r[i]
		if c == '#' {
			j := i
			for j < len(r) && r[j] != '\n' {
				j++
			}
			spans = append(spans, pepSpan{i, j})
			i = j
			continue
		}
		// string prefix? scan [A-Za-z]* then a quote.
		j := i
		for j < len(r) && ((r[j] >= 'a' && r[j] <= 'z') || (r[j] >= 'A' && r[j] <= 'Z')) {
			j++
		}
		if j < len(r) && (r[j] == '\'' || r[j] == '"') && isQuotePrefix(string(r[i:j])) {
			end, ok := pepStringEnd(r, j)
			if !ok {
				return nil, false
			}
			spans = append(spans, pepSpan{i, end})
			i = end
			continue
		}
		i++
	}
	return spans, true
}

func isQuotePrefix(p string) bool {
	if p == "" {
		return true
	}
	switch strings.ToLower(p) {
	case "r", "b", "u", "f", "rb", "br", "fr", "rf":
		return true
	}
	return false
}

// pepStringEnd returns the offset just past the string literal opening at
// quote position q (r[q] is ' or "). For f-strings the scan is
// brace-aware: quotes inside {...} open nested strings, so a 3.12
// nested same-quote f-string yields one outer span.
func pepStringEnd(r []rune, q int) (int, bool) {
	qc := r[q]
	n := 1
	if q+2 < len(r) && r[q+1] == qc && r[q+2] == qc {
		n = 3
	}
	// find the prefix: walk back over letters to learn f-ness
	p := q - 1
	for p >= 0 && ((r[p] >= 'a' && r[p] <= 'z') || (r[p] >= 'A' && r[p] <= 'Z')) {
		p--
	}
	isF := strings.ContainsRune(strings.ToLower(string(r[p+1:q])), 'f')
	j := q + n
	depth := 0
	for j < len(r) {
		c := r[j]
		if c == '\\' {
			j += 2
			continue
		}
		if isF && n == 1 {
			if c == '{' {
				if j+1 < len(r) && r[j+1] == '{' {
					j += 2
					continue
				}
				depth++
				j++
				continue
			}
			if c == '}' {
				if j+1 < len(r) && r[j+1] == '}' {
					j += 2
					continue
				}
				if depth > 0 {
					depth--
				}
				j++
				continue
			}
		}
		if c == qc && depth == 0 {
			if n == 3 {
				if j+2 < len(r) && r[j+1] == qc && r[j+2] == qc {
					return j + 3, true
				}
				j++
				continue
			}
			return j + 1, true
		}
		if (c == '\'' || c == '"') && depth > 0 {
			// nested string inside an f-string expression
			end, ok := pepStringEnd(r, j)
			if !ok {
				return 0, false
			}
			j = end
			continue
		}
		j++
	}
	return 0, false
}

// inSpans reports whether off falls inside any span.
func inSpans(spans []pepSpan, off int) bool {
	for _, s := range spans {
		if off >= s.start && off < s.end {
			return true
		}
	}
	return false
}

// pepBuiltins are value-level names that always exist: safe inside a
// type-alias RHS or a bound without further proof.
var pepBuiltins = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`False None True __name__ abs all any ascii
		bin bool breakpoint bytearray bytes callable chr classmethod compile
		complex dict dir divmod enumerate filter float format frozenset getattr
		globals hasattr hash help hex id input int isinstance issubclass iter
		len list locals map max memoryview min next object oct open ord pow
		print property range repr reversed round set setattr slice sorted
		staticmethod str sum super tuple type vars zip
		BaseException Exception ArithmeticError AssertionError AttributeError
		EOFError ImportError IndexError KeyError KeyboardInterrupt LookupError
		MemoryError NameError NotImplementedError OSError OverflowError
		RecursionError RuntimeError StopAsyncIteration StopIteration
		SyntaxError SystemError SystemExit Warning DeprecationWarning
		FutureWarning UserWarning`) {
		pepBuiltins[n] = true
	}
}

type pepScope struct {
	collide  map[string]bool // every name bound here (over-approximate)
	certain  map[string]int  // module scope: unconditionally bound -> def line
	star     bool            // `from x import *` seen here
	isFunc   bool
	isClass  bool
	isLambda bool
}

func newPepScope(fn, cls, lam bool) *pepScope {
	return &pepScope{collide: map[string]bool{}, certain: map[string]int{},
		isFunc: fn, isClass: cls, isLambda: lam}
}

// pepParam is one parsed type parameter.
type pepParam struct {
	name       string
	star       int // 0 plain, 1 *Ts, 2 **P
	boundStart int // token index of bound start, or -1
	boundEnd   int // token index of bound end (inclusive), or -1
}

// pepGeneric records one lowered generic for the post-walk occurrence scan.
type pepGeneric struct {
	params           []string
	bparams          []pepParam        // parallel to params (bounds live here)
	fresh            map[string]string // param -> fresh variable
	scope            *pepScope         // scope the TypeVars bind in (enclosing the definition)
	self             *pepScope         // the definition's own scope (nil for aliases)
	isDef            bool
	spanFrom, spanTo int
	insLine          int
	baseKind         int
	baseAt           int
	baseEnd          int
}

// aliasSpan bounds one `type X[...] = RHS` statement for positional
// ownership (alias parameters live in no scope).
type aliasSpan struct {
	g        *pepGeneric
	from, to int
}

// aliasSpec records one `type X = ...` statement for the use scan.
type aliasSpec struct {
	g         *pepGeneric
	name      string
	declFrom  int
	assignIdx int
	rhsFrom   int
	rhsEnd    int
	generic   bool
}

// lowerPEP695 rewrites PEP 695 type-parameter syntax into pre-12 syntax.
// ok=false means "not lowered": the caller keeps the original error.
func lowerPEP695(src string) (string, bool) {
	toks := pepLex(src)
	if toks == nil {
		return "", false
	}
	spans, ok := pepSpans(src)
	if !ok {
		return "", false
	}
	p := &pepLower{src: src, runes: []rune(src), toks: toks, spans: spans,
		scopes:     []*pepScope{newPepScope(false, false, false)},
		tparams:    map[*pepScope]map[string]bool{},
		inserts:    map[int][]string{},
		chains:     map[int][]*pepScope{},
		scopeOwner: map[*pepScope]*pepGeneric{},
		usedNames:  map[string]bool{}}
	if !p.walk() {
		return "", false
	}
	if !p.needLower {
		return "", false
	}
	out, ok := p.apply()
	if !ok {
		return "", false
	}
	return out, true
}

type pepLower struct {
	src     string
	runes   []rune
	toks    []pepTok
	spans   []pepSpan
	scopes  []*pepScope
	tparams map[*pepScope]map[string]bool

	depth     int
	cond      int
	stmtStart bool
	lastHead  int       // token type of the last compound header keyword, or -1
	decoLine  int       // first line of a pending decorator block, or 0
	decoTok   int       // token index of the pending `@`, or -1
	skipScope bool      // the next DEF/CLASS colon must not push a scope (manual)
	inHeader  bool      // inside a generic header (defaults bind enclosing)
	hdrScope  *pepScope // manual function/class scope of that header
	frames    []pepFrame
	poisoned  bool // star-import at module level, shadowed `typing`, or malformed `type`

	needLower  bool
	needTyping bool
	edits      []pepEdit
	inserts    map[int][]string // line -> texts, in order

	generics     []*pepGeneric
	chains       map[int][]*pepScope // scope chain per token index
	decoSpans    [][2]int            // decorator ranges [at, def)
	scopeOwner   map[*pepScope]*pepGeneric
	usedNames    map[string]bool // every NAME in the file (fresh-name gen)
	pending      []string        // params to bind at the next DEF colon
	aliasSpans   []aliasSpan
	defParens    [][]int  // every def's `(...)` range (param vs default)
	baseSpans    [][2]int // every class's `(...)` base range
	aliasSpecs   map[string]*aliasSpec
	bracketSpans [][2]int // alias `[...]` ranges (positional handling)
	allDefaults  [][2]int // every def-header default span
	gid          int      // generic serial for fresh names
}

type pepFrame struct {
	isScope    bool
	isCond     bool
	sameLine   bool
	lambdaBase int // valid when isLambda: depth at the LAMBDA token
	isLambda   bool
}

type pepEdit struct {
	start, stop int // rune range to replace (stop exclusive)
	text        string
}

func (p *pepLower) cur() *pepScope { return p.scopes[len(p.scopes)-1] }

// visibleTParams are the type parameters usable in an expression here:
// those of enclosing functions, stopping at the first class boundary
// (class scopes are skipped by name resolution, so a method bound cannot
// rely on a class type parameter through this lowering).
func (p *pepLower) visibleTParams() map[string]bool {
	out := map[string]bool{}
	for i := len(p.scopes) - 1; i >= 0; i-- {
		s := p.scopes[i]
		if s.isClass {
			break
		}
		for n := range p.tparams[s] {
			out[n] = true
		}
	}
	return out
}

func (p *pepLower) walk() bool {
	p.stmtStart = true
	p.lastHead = -1
	p.decoTok = -1
	for i := 0; i < len(p.toks); i++ {
		t := p.toks[i]
		p.chains[i] = append([]*pepScope{}, p.scopes...)
		switch t.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			p.depth++
			p.stmtStart = false
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			p.depth--
			if p.depth < 0 {
				return false
			}
			p.popLambda(t)
			p.stmtStart = false
		case gen.Python3LexerNEWLINE:
			// A NEWLINE followed by INDENT opens a block suite: pending
			// same-line frames become block frames instead of popping.
			if i+1 < len(p.toks) && p.toks[i+1].typ == gen.Python3LexerINDENT {
				p.blockify()
			} else {
				p.endSameLine()
			}
			p.stmtStart = true
			p.lastHead = -1
			p.popLambda(t)
		case gen.Python3LexerINDENT:
			p.blockify()
			p.stmtStart = true
			p.lastHead = -1
		case gen.Python3LexerDEDENT:
			if !p.popBlock() {
				return false
			}
			p.stmtStart = true
			p.lastHead = -1
		case gen.Python3LexerSEMI_COLON:
			p.stmtStart = true
			p.lastHead = -1
		case gen.Python3LexerCOLON:
			if p.depth == 0 {
				p.onColon()
			} else {
				p.stmtStart = false
			}
		case gen.Python3LexerAT:
			if p.stmtStart && p.depth == 0 {
				if p.decoLine == 0 {
					p.decoLine = t.line
					p.decoTok = i
				}
			}
			// Inside a decorator block now: the decorator's own names
			// must not clear the pending block (clearDeco fires only
			// on new statements).
			p.stmtStart = false
		case gen.Python3LexerDEF, gen.Python3LexerCLASS:
			if p.stmtStart && p.depth == 0 && !inSpans(p.spans, t.start) {
				if p.decoTok >= 0 {
					p.decoSpans = append(p.decoSpans, [2]int{p.decoTok, i})
				}
				ni, ok := p.onDef(i)
				if !ok {
					return false
				}
				i = ni
				continue
			}
			p.stmtStart = false
		case gen.Python3LexerIMPORT:
			p.clearDeco()
			p.onImport(i)
		case gen.Python3LexerFROM:
			p.clearDeco()
			p.onFrom(i)
		case gen.Python3LexerFOR:
			p.clearDeco()
			p.lastHead = gen.Python3LexerFOR
			p.onFor(i)
		case gen.Python3LexerWITH:
			p.clearDeco()
			p.lastHead = gen.Python3LexerWITH
			p.onWith(i)
		case gen.Python3LexerEXCEPT:
			p.clearDeco()
			p.lastHead = gen.Python3LexerEXCEPT
			p.onExcept(i)
		case gen.Python3LexerDEL:
			p.clearDeco()
			p.onDel(i)
		case gen.Python3LexerLAMBDA:
			p.clearDeco()
			p.pushFrame(pepFrame{isLambda: true, sameLine: true, lambdaBase: p.depth})
			p.scopes = append(p.scopes, newPepScope(false, false, true))
			p.bindLambdaParams(i)
			p.stmtStart = false
		case gen.Python3LexerNAME, gen.Python3LexerUNDERSCORE:
			p.clearDeco()
			p.usedNames[t.text] = true
			p.onName(i)
		case gen.Python3LexerGLOBAL, gen.Python3LexerNONLOCAL:
			p.clearDeco()
			p.stmtStart = false
		default:
			if p.isHeaderKw(t.typ) && p.stmtStart && p.depth == 0 {
				p.lastHead = t.typ
			}
			// `async` precedes a header (`async def/for/with`): it is
			// not a statement start boundary.
			if t.typ != gen.Python3LexerASYNC {
				p.stmtStart = false
			}
		}
	}
	if p.needLower && p.poisoned {
		return false
	}
	if p.needLower && !p.postPass() {
		return false
	}
	return true
}

// clearDeco drops a pending decorator block when a non-def/class/async
// statement starts (decorators must precede def/class; blank lines
// between them are fine and do not clear). Only fires at statement
// starts, so decorator names and arguments never clear their own block.
func (p *pepLower) clearDeco() {
	if !p.stmtStart {
		return
	}
	p.decoLine = 0
	p.decoTok = -1
}

// --- frame / scope bookkeeping ---

func (p *pepLower) pushFrame(f pepFrame) { p.frames = append(p.frames, f) }

// onColon implements the header rule: a depth-0 COLON after a compound
// header opens a suite that is either a new scope (def/class) or
// conditional (everything else). lastHead is stale-safe: it is cleared
// at every NEWLINE/INDENT/DEDENT/`;`, and only header keywords set it.
func (p *pepLower) onColon() {
	p.inHeader = false
	p.hdrScope = nil
	defer func() {
		// Parameters bind in the suite scope just opened (or immediately
		// for the manual-push path, where it is already on top).
		for _, nm := range p.pending {
			p.cur().collide[nm] = true
		}
		p.pending = nil
	}()
	switch p.lastHead {
	case gen.Python3LexerDEF, gen.Python3LexerCLASS:
		p.pushFrame(pepFrame{isScope: true, sameLine: true})
		if p.skipScope {
			p.skipScope = false
		} else if p.lastHead == gen.Python3LexerDEF {
			p.scopes = append(p.scopes, newPepScope(true, false, false))
		} else {
			p.scopes = append(p.scopes, newPepScope(false, true, false))
		}
	case gen.Python3LexerIF, gen.Python3LexerELIF, gen.Python3LexerELSE,
		gen.Python3LexerTRY, gen.Python3LexerEXCEPT, gen.Python3LexerFINALLY,
		gen.Python3LexerFOR, gen.Python3LexerWHILE, gen.Python3LexerWITH,
		gen.Python3LexerMATCH, gen.Python3LexerCASE:
		p.pushFrame(pepFrame{isCond: true, sameLine: true})
		p.cond++
	}
	p.stmtStart = true
	p.lastHead = -1
}

// blockify converts trailing same-line suites into block suites.
func (p *pepLower) blockify() {
	for i := len(p.frames) - 1; i >= 0 && p.frames[i].sameLine; i-- {
		if p.frames[i].isLambda {
			continue
		}
		p.frames[i].sameLine = false
	}
}

// endSameLine pops suites that end at this NEWLINE.
func (p *pepLower) endSameLine() {
	for len(p.frames) > 0 && p.frames[len(p.frames)-1].sameLine {
		f := p.frames[len(p.frames)-1]
		p.frames = p.frames[:len(p.frames)-1]
		if f.isCond {
			p.cond--
		}
		if f.isScope || f.isLambda {
			if len(p.scopes) <= 1 {
				return
			}
			p.scopes = p.scopes[:len(p.scopes)-1]
		}
	}
}

// popBlock pops one block suite at DEDENT.
func (p *pepLower) popBlock() bool {
	if len(p.frames) == 0 {
		return false
	}
	f := p.frames[len(p.frames)-1]
	p.frames = p.frames[:len(p.frames)-1]
	if f.isCond {
		p.cond--
	}
	if f.isScope {
		if len(p.scopes) <= 1 {
			return false
		}
		p.scopes = p.scopes[:len(p.scopes)-1]
	}
	return true
}

func (p *pepLower) lambdaDepth() int {
	for i := len(p.frames) - 1; i >= 0; i-- {
		if p.frames[i].isLambda {
			return p.frames[i].lambdaBase
		}
	}
	return -1
}

// popLambda ends a lambda scope when its expression terminates: at its
// own depth on `,`, `:`, NEWLINE, or when the depth drops below it.
func (p *pepLower) popLambda(t pepTok) {
	for len(p.frames) > 0 {
		f := p.frames[len(p.frames)-1]
		if !f.isLambda {
			return
		}
		end := false
		switch t.typ {
		case gen.Python3LexerNEWLINE, gen.Python3LexerCOMMA, gen.Python3LexerCOLON:
			end = p.depth == f.lambdaBase
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			end = p.depth < f.lambdaBase
		}
		if !end {
			return
		}
		p.frames = p.frames[:len(p.frames)-1]
		p.scopes = p.scopes[:len(p.scopes)-1]
	}
}

func (p *pepLower) isHeaderKw(typ int) bool {
	switch typ {
	case gen.Python3LexerIF, gen.Python3LexerELIF, gen.Python3LexerELSE,
		gen.Python3LexerTRY, gen.Python3LexerEXCEPT, gen.Python3LexerFINALLY,
		gen.Python3LexerFOR, gen.Python3LexerWHILE, gen.Python3LexerWITH,
		gen.Python3LexerMATCH, gen.Python3LexerCASE, gen.Python3LexerASYNC:
		return true
	}
	return false
}

// --- bindings ---

// bindAt records name in the current scope; certain marks an unconditional
// module-level definition (usable for the eager-alias safety check),
// recorded with its line: only earlier bindings prove existence.
func (p *pepLower) bindAt(name string, line int, certain bool) {
	p.cur().collide[name] = true
	if certain && len(p.scopes) == 1 && p.cond == 0 {
		if _, ok := p.scopes[0].certain[name]; !ok {
			p.scopes[0].certain[name] = line
		}
	}
}

func (p *pepLower) bind(name string, certain bool) {
	p.cur().collide[name] = true
}

func (p *pepLower) bindTyping() {
	// plain `import typing` is exactly what the lowering needs; any other
	// binding of the name shadows it and poisons the file.
	p.bind("typing", true)
}

// onName handles a NAME/`_` token: the `type` statement trigger and
// assignment targets. Imports, for/with/except/del have keyword tokens
// and are handled in their own walk arms.
func (p *pepLower) onName(i int) {
	t := p.toks[i]
	// `type` statement trigger: statement start, followed by NAME.
	// Anything else (`type(x)`, `x.type`, `type = 5`, `a: type = ...`)
	// cannot be a type statement by position.
	if t.text == "type" && t.typ == gen.Python3LexerNAME &&
		p.stmtStart && p.depth == 0 && !inSpans(p.spans, t.start) {
		if i+1 < len(p.toks) && p.toks[i+1].typ == gen.Python3LexerNAME {
			if !p.onTypeStmt(i) {
				// Malformed or unsafe: decline (original error kept).
				p.poisoned = true
			}
			p.stmtStart = false
			return
		}
	}
	p.stmtStart = false
	if p.depth != 0 {
		return
	}
	nx := p.peekTyp(i)
	// Assignment target. `x.y = ` / `x[0] = ` do not bind x (only a bare
	// NAME here binds, and those have DOT/`[` next, unmatched below).
	// Tuple targets (`a, b = ...`) join the collide set only: certainty
	// would need order analysis, and over-approximating there only
	// declines.
	switch {
	case nx == gen.Python3LexerASSIGN || nx == gen.Python3LexerCOLONEQUAL || p.isAug(nx):
		if t.text == "typing" {
			p.poisoned = true
			return
		}
		// A walrus inside a lambda binds the enclosing scope (PEP 572),
		// and a walrus in a generic header's defaults/annotations
		// binds the scope enclosing the definition (they evaluate
		// there), never the definition's own scope.
		s := p.cur()
		if nx == gen.Python3LexerCOLONEQUAL {
			lim := len(p.scopes)
			if p.inHeader {
				for k, s := range p.scopes {
					if s == p.hdrScope {
						lim = k
						break
					}
				}
			}
			for k := lim - 1; k >= 0; k-- {
				if !p.scopes[k].isLambda {
					s = p.scopes[k]
					break
				}
			}
			// Walrus timing is uncertain (short-circuit, loops), so it
			// never counts as certain; it always collides.
			s.collide[t.text] = true
			return
		}
		p.bindAt(t.text, t.line, nx == gen.Python3LexerASSIGN)
	case nx == gen.Python3LexerCOMMA:
		// Possible tuple target: confirm an `=` follows at depth 0.
		if p.assignAhead(i) {
			p.cur().collide[t.text] = true
		}
	case nx == gen.Python3LexerCOLON:
		// Possible annotated assignment `x: T = v` (binds); bare `x: T`
		// does not bind. Lambda parameters (`lambda x: ...`) have no
		// value ahead and are correctly ignored.
		if p.valueAhead(i) {
			if t.text == "typing" {
				p.poisoned = true
				return
			}
			p.bindAt(t.text, t.line, true)
		}
	}
}

func (p *pepLower) peekTyp(i int) int {
	if i+1 < len(p.toks) {
		return p.toks[i+1].typ
	}
	return -1
}

func (p *pepLower) isAug(typ int) bool {
	switch typ {
	case gen.Python3LexerADD_ASSIGN, gen.Python3LexerSUB_ASSIGN,
		gen.Python3LexerMULT_ASSIGN, gen.Python3LexerAT_ASSIGN,
		gen.Python3LexerDIV_ASSIGN, gen.Python3LexerMOD_ASSIGN,
		gen.Python3LexerAND_ASSIGN, gen.Python3LexerOR_ASSIGN,
		gen.Python3LexerXOR_ASSIGN, gen.Python3LexerLEFT_SHIFT_ASSIGN,
		gen.Python3LexerRIGHT_SHIFT_ASSIGN, gen.Python3LexerPOWER_ASSIGN,
		gen.Python3LexerIDIV_ASSIGN:
		return true
	}
	return false
}

// assignAhead reports whether an ASSIGN follows at depth 0 before the
// statement ends (tuple-target confirmation).
func (p *pepLower) assignAhead(i int) bool {
	depth := 0
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
		case gen.Python3LexerASSIGN:
			if depth == 0 {
				return true
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			if depth == 0 {
				return false
			}
		case gen.Python3LexerCOLON:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

// valueAhead reports whether an annotated NAME has `= value`
// (`x: T = v` binds; bare `x: T` does not).
func (p *pepLower) valueAhead(i int) bool {
	depth := 0
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
		case gen.Python3LexerASSIGN:
			if depth == 0 {
				return true
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

// onImport records `import a[.b][ as c], ...` (binds a, or the alias).
func (p *pepLower) onImport(i int) {
	j := i + 1
	cur, curLine, seenDot := "", 0, false
	flush := func() {
		if cur != "" && cur != "as" {
			if cur == "typing" {
				p.bindTyping()
			} else {
				p.bindAt(cur, curLine, true)
			}
		}
		cur, curLine, seenDot = "", 0, false
	}
	for ; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerSEMI_COLON {
			break
		}
		switch u.typ {
		case gen.Python3LexerNAME:
			if u.text == "as" {
				if j+1 < len(p.toks) && p.toks[j+1].typ == gen.Python3LexerNAME {
					cur = p.toks[j+1].text
					curLine = p.toks[j+1].line
					seenDot = true // alias replaces; ignore the rest
					j++
				}
				continue
			}
			if cur == "" && !seenDot {
				cur = u.text
				curLine = u.line
			}
		case gen.Python3LexerDOT:
			seenDot = true
		case gen.Python3LexerCOMMA:
			flush()
		}
	}
	flush()
	p.stmtStart = false
}

// onFrom records `from m import a [as b], ...`; `*` poisons the scope.
func (p *pepLower) onFrom(i int) {
	j := i + 1
	for ; j < len(p.toks); j++ {
		if p.toks[j].typ == gen.Python3LexerIMPORT {
			break
		}
		if p.toks[j].typ == gen.Python3LexerNEWLINE || p.toks[j].typ == gen.Python3LexerSEMI_COLON {
			p.stmtStart = false
			return
		}
	}
	nameAt := func(k int) (string, bool) {
		if k >= 0 && k < len(p.toks) && p.toks[k].typ == gen.Python3LexerNAME {
			return p.toks[k].text, true
		}
		return "", false
	}
	for j++; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerSEMI_COLON {
			break
		}
		if u.typ == gen.Python3LexerSTAR {
			if len(p.scopes) == 1 {
				p.poisoned = true
			} else {
				p.cur().star = true
			}
			p.stmtStart = false
			return
		}
		if u.typ != gen.Python3LexerNAME || u.text == "as" {
			continue
		}
		if nx, _ := nameAt(j + 1); nx == "as" {
			continue // pre-alias name: binds nothing
		}
		// Plain name or alias: binds. (Pre-alias names are skipped
		// above, so anything reaching here binds.)
		if u.text == "typing" {
			p.poisoned = true
		} else {
			p.bindAt(u.text, u.line, true)
		}
	}
	p.stmtStart = false
}

// onFor binds loop targets to the collide set only (zero iterations).
func (p *pepLower) onFor(i int) {
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerCOLON {
			break
		}
		if (u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE) && u.text != "in" {
			// stop at the iterable: the NAME right before... `for a in x`:
			// bind a, but x is a use. Distinguish by the `in` keyword:
			// everything before it is target.
			p.cur().collide[u.text] = true
			continue
		}
		if u.typ == gen.Python3LexerNAME && u.text == "in" {
			break
		}
	}
	p.stmtStart = false
}

// onWith binds `as` targets (they exist when the suite runs).
func (p *pepLower) onWith(i int) {
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerCOLON {
			break
		}
		if u.typ == gen.Python3LexerNAME && u.text == "as" &&
			j+1 < len(p.toks) && (p.toks[j+1].typ == gen.Python3LexerNAME ||
			p.toks[j+1].typ == gen.Python3LexerUNDERSCORE) {
			nm := p.toks[j+1].text
			if nm == "typing" {
				p.poisoned = true
			} else {
				p.bindAt(nm, p.toks[j+1].line, true)
			}
		}
	}
	p.stmtStart = false
}

// onExcept binds `as` targets to the collide set only (deleted at end).
func (p *pepLower) onExcept(i int) {
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerCOLON {
			break
		}
		if u.typ == gen.Python3LexerNAME && u.text == "as" &&
			j+1 < len(p.toks) && (p.toks[j+1].typ == gen.Python3LexerNAME ||
			p.toks[j+1].typ == gen.Python3LexerUNDERSCORE) {
			p.cur().collide[p.toks[j+1].text] = true
		}
	}
	p.stmtStart = false
}

// unbind drops a `del`eted name from the certain set (it may no longer
// exist) but keeps it colliding: re-creating it still changes reads.
func (p *pepLower) unbind(name string) {
	if len(p.scopes) == 1 {
		delete(p.scopes[0].certain, name)
	}
}

// onDel drops names from the certain set (they may no longer exist;
// they stay colliding since re-creating them still changes reads).
func (p *pepLower) onDel(i int) {
	for j := i + 1; j < len(p.toks); j++ {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNEWLINE || u.typ == gen.Python3LexerSEMI_COLON {
			break
		}
		if u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE {
			p.unbind(u.text)
		}
	}
	p.stmtStart = false
}

// --- def/class ---

// onDef processes a DEF/CLASS token at statement start: binds the name
// and lowers a type-parameter list when present. The suite scope itself
// is pushed by the COLON rule. Returns the index to continue from.
func (p *pepLower) onDef(i int) (int, bool) {
	t := p.toks[i]
	isDef := t.typ == gen.Python3LexerDEF
	j := i + 1
	if isDef && j < len(p.toks) && p.toks[j].typ == gen.Python3LexerASYNC {
		j++
	}
	if j >= len(p.toks) || (p.toks[j].typ != gen.Python3LexerNAME &&
		p.toks[j].typ != gen.Python3LexerUNDERSCORE) {
		p.stmtStart = false
		return i, true
	}
	fname := p.toks[j]
	p.bindAt(fname.text, fname.line, true)
	p.lastHead = t.typ
	if !isDef {
		p.recordBaseSpan(j)
	}
	// Uniform manual scopes: the suite scope is pushed now (not at the
	// colon), so header occurrences already carry the right chain and
	// parameters bind into it at the colon via pending.
	ns := newPepScope(isDef, !isDef, false)
	encl := p.cur()
	p.scopes = append(p.scopes, ns)
	p.skipScope = true
	if isDef {
		p.pending = p.scanParams(j)
	}
	if j+1 < len(p.toks) && p.toks[j+1].typ == gen.Python3LexerOPEN_BRACK {
		return p.onGenericDef(i, j, isDef, ns, encl)
	}
	p.clearDeco()
	p.stmtStart = false
	return j, true
}

// scanParams collects the parameter names of the def at name index n
// (`(` ... `)` top level, annotation-aware): `x`, `y=1`, `*a`, `**k"
// bind; `x: T` binds x but not T; `-> R` binds nothing.
func (p *pepLower) scanParams(n int) []string {
	j := n + 1
	if j < len(p.toks) && p.toks[j].typ == gen.Python3LexerOPEN_BRACK {
		// generic brackets first: skip to the matching `]`
		depth := 0
		for ; j < len(p.toks); j++ {
			switch p.toks[j].typ {
			case gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACE:
				depth--
				if depth == 0 {
					break
				}
			}
			if depth == 0 {
				break
			}
		}
		j++
	}
	if j >= len(p.toks) || p.toks[j].typ != gen.Python3LexerOPEN_PAREN {
		return nil
	}
	open := j
	var out []string
	depth := 0
	for k := j; k < len(p.toks); k++ {
		u := p.toks[k]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth == 0 {
				p.defParens = append(p.defParens, []int{open, k})
				return out
			}
		case gen.Python3LexerNAME, gen.Python3LexerUNDERSCORE:
			if depth == 1 && p.isParamName(k) {
				out = append(out, u.text)
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			return out
		}
	}
	return out
}

// isParamName reports whether the NAME at k is a parameter being bound
// (not an annotation/default use): next is `:`, `=`, `,`, `)` and prev
// is not `:`, `=`, `.`, `->`, `lambda`.
func (p *pepLower) isParamName(k int) bool {
	u := p.toks[k]
	if u.typ != gen.Python3LexerNAME && u.typ != gen.Python3LexerUNDERSCORE {
		return false
	}
	var nx, pv int = -1, -1
	if k+1 < len(p.toks) {
		nx = p.toks[k+1].typ
	}
	if k > 0 {
		pv = p.toks[k-1].typ
	}
	isNext := nx == gen.Python3LexerCOLON || nx == gen.Python3LexerASSIGN ||
		nx == gen.Python3LexerCOMMA || nx == gen.Python3LexerCLOSE_PAREN
	if !isNext {
		return false
	}
	// NOTE: no LAMBDA exclusion: the only NAME directly after `lambda`
	// is the lambda's first parameter, which does bind.
	if pv == gen.Python3LexerCOLON || pv == gen.Python3LexerASSIGN ||
		pv == gen.Python3LexerDOT || pv == gen.Python3LexerARROW {
		return false
	}
	return true
}

// bindLambdaParams binds `lambda a, b=1: ...` parameters into the
// lambda scope just pushed (lookahead to the colon at lambda depth).
func (p *pepLower) bindLambdaParams(i int) {
	base := p.depth
	for k := i + 1; k < len(p.toks); k++ {
		u := p.toks[k]
		if u.typ == gen.Python3LexerCOLON && p.depthOf(k, i) == base {
			break
		}
		if (u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE) &&
			p.depthOf(k, i) == base && p.isParamName(k) {
			p.cur().collide[u.text] = true
		}
		if u.typ == gen.Python3LexerNEWLINE {
			break
		}
	}
}

// depthOf computes the bracket depth at token k relative to token from.
func (p *pepLower) depthOf(k, from int) int {
	d := 0
	for m := from + 1; m <= k && m < len(p.toks); m++ {
		switch p.toks[m].typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			d++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			d--
		}
	}
	return d
}

// parseParams parses `[ ... ]` at toks[i]. Returns the parameters, the
// bracket indices, the index after `]`, and ok.
func (p *pepLower) parseParams(i int) ([]pepParam, int, int, int, bool) {
	if i >= len(p.toks) || p.toks[i].typ != gen.Python3LexerOPEN_BRACK {
		return nil, 0, 0, i, false
	}
	var params []pepParam
	cur := pepParam{boundStart: -1}
	have := false
	finish := func() bool {
		if !have {
			return false
		}
		if cur.boundStart >= 0 && cur.boundEnd < cur.boundStart {
			return false
		}
		params = append(params, cur)
		cur = pepParam{boundStart: -1}
		have = false
		return true
	}
	depth := 0
	for j := i; j < len(p.toks); j++ {
		u := p.toks[j]
		switch u.typ {
		case gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth < 0 {
				return nil, 0, 0, i, false
			}
			if depth == 0 {
				if u.typ != gen.Python3LexerCLOSE_BRACK {
					return nil, 0, 0, i, false
				}
				if have {
					if cur.boundStart >= 0 {
						cur.boundEnd = j - 1
					}
					if !finish() {
						return nil, 0, 0, i, false
					}
				}
				if !have && len(params) == 0 {
					return nil, 0, 0, i, false // `[]`
				}
				return params, i, j, j + 1, true
			}
		case gen.Python3LexerCOMMA:
			if depth == 1 {
				if !have {
					return nil, 0, 0, i, false // empty item
				}
				cur.boundEnd = j - 1
				if !finish() {
					return nil, 0, 0, i, false
				}
			}
		case gen.Python3LexerCOLON:
			if depth == 1 {
				if !have || cur.boundStart >= 0 {
					return nil, 0, 0, i, false
				}
				cur.boundStart = j + 1
			}
		case gen.Python3LexerSTAR:
			if depth == 1 && !have && cur.boundStart < 0 {
				cur.star = 1
			}
		case gen.Python3LexerPOWER:
			if depth == 1 && !have && cur.boundStart < 0 {
				cur.star = 2
			}
		case gen.Python3LexerNAME:
			if depth == 1 && !have && cur.boundStart < 0 {
				cur.name = u.text
				have = true
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			return nil, 0, 0, i, false
		}
	}
	return nil, 0, 0, i, false
}

// onGenericDef lowers `def f[...]`, `class C[...]` at name index n
// (def/class keyword at i). Returns the index to continue the walk from
// (the header is still walked for bindings).
func (p *pepLower) onGenericDef(i, n int, isDef bool, ns, encl *pepScope) (int, bool) {
	params, openIdx, closeIdx, next, ok := p.parseParams(n + 1)
	if !ok || len(params) == 0 {
		return i, false
	}
	// Shape: `(` for def; `(` or `:` for class.
	k := next
	if k >= len(p.toks) {
		return i, false
	}
	kt := p.toks[k].typ
	if isDef {
		if kt != gen.Python3LexerOPEN_PAREN {
			return i, false
		}
	} else if kt != gen.Python3LexerOPEN_PAREN && kt != gen.Python3LexerCOLON {
		return i, false
	}
	// Header end: COLON at depth 0 from the name on.
	hdrEnd := -1
	depth := 0
	for m := n + 1; m < len(p.toks); m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth < 0 {
				return i, false
			}
		case gen.Python3LexerCOLON:
			if depth == 0 {
				hdrEnd = m
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			if depth == 0 {
				return i, false
			}
		}
		if hdrEnd >= 0 {
			break
		}
	}
	if hdrEnd < 0 {
		return i, false
	}
	// Body range: block suite (matched DEDENT) or simple suite (NEWLINE).
	bodyEnd := -1
	if hdrEnd+2 < len(p.toks) && p.toks[hdrEnd+1].typ == gen.Python3LexerNEWLINE &&
		p.toks[hdrEnd+2].typ == gen.Python3LexerINDENT {
		nest := 0
		for m := hdrEnd + 2; m < len(p.toks); m++ {
			if p.toks[m].typ == gen.Python3LexerINDENT {
				nest++
			} else if p.toks[m].typ == gen.Python3LexerDEDENT {
				nest--
				if nest == 0 {
					bodyEnd = m
					break
				}
			}
		}
	} else {
		for m := hdrEnd + 1; m < len(p.toks); m++ {
			if p.toks[m].typ == gen.Python3LexerNEWLINE {
				bodyEnd = m
				break
			}
			if p.toks[m].typ == gen.Python3LexerSEMI_COLON || p.toks[m].typ == gen.Python3LexerDEDENT {
				return i, false
			}
		}
	}
	if bodyEnd < 0 {
		return i, false
	}
	if p.cur().star {
		return i, false // star-import here: unknown names may collide
	}
	// Name + shadow guards. Cross-scope collisions (rebinding a live
	// Name guards only (`_`, `typing`, duplicates are unrepresentable).
	// Shadowing and sibling reuse are safe: every parameter gets a
	// unique cell (see pepGeneric). Cross-scope collisions are decided
	// post-walk by the ownership scan, which sees future bindings.
	seen := map[string]bool{}
	for _, prm := range params {
		if prm.name == "" || prm.name == "_" || prm.name == "typing" || seen[prm.name] {
			return i, false
		}
		seen[prm.name] = true
	}
	// Bounds (eager in both versions, so f-string interiors need no
	// analysis; still no lambdas/await/yield/walrus/comprehension fors).
	vis := p.visibleTParams()
	for _, prm := range params {
		if prm.star > 0 && prm.boundStart >= 0 {
			return i, false
		}
	}
	allp := map[string]bool{}
	for n := range vis {
		allp[n] = true
	}
	for _, prm := range params {
		allp[prm.name] = true
	}
	for _, prm := range params {
		if prm.boundStart >= 0 {
			if !p.checkNames(prm.boundStart, prm.boundEnd, allp, p.toks[i].line, false, false) {
				return i, false
			}
		}
	}
	// Emit the structural splice now (offsets only); TypeVar inserts and
	// the Generic base need fresh names, staged for postPass.
	insLine := p.toks[i].line
	if p.decoLine != 0 {
		insLine = p.decoLine
	}
	p.edits = append(p.edits, pepEdit{start: p.toks[openIdx].start, stop: p.toks[closeIdx].stop + 1})
	// Record the generic for postPass (fresh names, ownership scan and
	// insert emission all happen there, when global information is
	// complete). ns was pushed by onDef; encl is the scope inserts land in.
	g := &pepGeneric{scope: encl, self: ns, isDef: isDef, spanFrom: i, spanTo: bodyEnd, insLine: insLine}
	for _, prm := range params {
		g.params = append(g.params, prm.name)
	}
	g.bparams = params
	if !isDef {
		kind, at, end, ok := p.analyzeBase(params, k, hdrEnd)
		if !ok {
			return i, false
		}
		g.baseKind, g.baseAt, g.baseEnd = kind, at, end
	}
	p.scopeOwner[ns] = g
	p.tparams[ns] = seen
	p.generics = append(p.generics, g)
	p.inHeader = true
	p.hdrScope = ns
	p.needLower = true
	p.needTyping = true
	p.clearDeco()
	p.stmtStart = false
	return closeIdx, true
}

// anyEnclosingTparam reports whether name is a type parameter of any
// enclosing scope, class scopes included (shadowing one rebinds reads
// the outer parameter owns).
func (p *pepLower) anyEnclosingTparam(name string) bool {
	for _, s := range p.scopes {
		if p.tparams[s][name] {
			return true
		}
	}
	return false
}

// analyzeBase stages the `typing.Generic[...]` splice for postPass (the
// text needs fresh names): `class C[T]:` gains `(typing.Generic[T])`;
// `class C[T](B)` appends; with keyword bases it prepends (keywords must
// stay last). A base list that already mentions Generic declines.
// Returns kind (1 colon-insert, 2 replace `()`, 3 prepend, 4 append) and
// rune offsets. Callers pass fresh-mapped names at emission.
func (p *pepLower) analyzeBase(params []pepParam, k, hdrEnd int) (kind, at, end int, ok bool) {
	if p.toks[k].typ == gen.Python3LexerCOLON {
		return 1, p.toks[k].start, p.toks[k].start, true
	}
	depth := 0
	pbEnd := -1
	for m := k; m < len(p.toks); m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth == 0 {
				if u.typ != gen.Python3LexerCLOSE_PAREN {
					return 0, 0, 0, false
				}
				pbEnd = m
			}
		}
		if pbEnd >= 0 {
			break
		}
	}
	if pbEnd < 0 {
		return 0, 0, 0, false
	}
	if pbEnd == k+1 {
		return 2, p.toks[k].start, p.toks[pbEnd].stop + 1, true
	}
	relDepth := 0
	keywords := false
	for m := k + 1; m < pbEnd; m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			relDepth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			relDepth--
		case gen.Python3LexerASSIGN, gen.Python3LexerPOWER:
			if relDepth == 0 {
				keywords = true
			}
		case gen.Python3LexerNAME:
			if relDepth == 0 && u.text == "Generic" {
				return 0, 0, 0, false
			}
			if relDepth == 0 && u.text == "typing" && m+2 < len(p.toks) &&
				p.toks[m+1].typ == gen.Python3LexerDOT &&
				p.toks[m+2].typ == gen.Python3LexerNAME && p.toks[m+2].text == "Generic" {
				return 0, 0, 0, false
			}
		}
	}
	if keywords {
		return 3, p.toks[k].stop + 1, p.toks[k].stop + 1, true
	}
	return 4, p.toks[pbEnd].start, p.toks[pbEnd].start, true
}

// tvarLine renders one TypeVar/TypeVarTuple/ParamSpec assignment into
// the fresh cell (display name preserved, so reprs match). Bound text is
// pre-rewritten by boundRewrite; constraints strip one outer pair.
func (p *pepLower) tvarLine(fresh, display, bound string, star int, isConstr bool, inner string) string {
	q := `"` + display + `"`
	switch star {
	case 1:
		return fresh + ` = typing.TypeVarTuple(` + q + ")\n"
	case 2:
		return fresh + ` = typing.ParamSpec(` + q + ")\n"
	}
	if isConstr {
		return fresh + ` = typing.TypeVar(` + q + `, ` + inner + ")\n"
	}
	if bound == "" {
		return fresh + ` = typing.TypeVar(` + q + ")\n"
	}
	return fresh + ` = typing.TypeVar(` + q + `, bound=` + bound + ")\n"
}

// constraintInner strips one outer paren pair when it wraps a
// comma-separated list: `(A, B)` is constraints.
func (p *pepLower) constraintInner(a, b int) (string, bool) {
	ts := p.toks
	if b-a+1 < 3 || ts[a].typ != gen.Python3LexerOPEN_PAREN ||
		ts[b].typ != gen.Python3LexerCLOSE_PAREN {
		return "", false
	}
	depth := 0
	comma := false
	for k := a; k <= b; k++ {
		switch ts[k].typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth == 0 && k != b {
				return "", false
			}
		case gen.Python3LexerCOMMA:
			if depth == 1 {
				comma = true
			}
		}
	}
	if !comma {
		return "", false
	}
	return string(p.runes[ts[a+1].start : ts[b-1].stop+1]), true
}

// --- type statements ---

// onTypeStmt lowers `type X[T...] = RHS` at token i (`type`). True when
// lowered; false declines (the original error is kept).
func (p *pepLower) onTypeStmt(i int) bool {
	x := p.toks[i+1]
	var params []pepParam
	next := i + 2
	openIdx, closeIdx := -1, -1
	if next < len(p.toks) && p.toks[next].typ == gen.Python3LexerOPEN_BRACK {
		var ok bool
		params, openIdx, closeIdx, next, ok = p.parseParams(next)
		if !ok || len(params) == 0 {
			return false
		}
		_ = openIdx
		_ = closeIdx
	}
	if next >= len(p.toks) || p.toks[next].typ != gen.Python3LexerASSIGN {
		return false
	}
	assign := next
	rel := 0
	rhsEnd := -1
	for m := assign + 1; m < len(p.toks); m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			rel++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			rel--
			if rel < 0 {
				return false
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			if rel == 0 {
				rhsEnd = m - 1
			}
		}
		if rhsEnd >= 0 {
			break
		}
	}
	if rhsEnd < assign+1 {
		return false
	}
	if p.cur().star {
		return false
	}
	if x.text == "typing" {
		return false
	}
	seen := map[string]bool{}
	for _, prm := range params {
		if prm.name == "" || prm.name == "_" || prm.name == "typing" || seen[prm.name] {
			return false
		}
		seen[prm.name] = true
	}
	// The alias name itself must not be a live type parameter: after
	// lowering it names the value, while 3.12 binds nothing observable
	// there, so later reads would diverge.
	if seen[x.text] || p.anyEnclosingTparam(x.text) {
		return false
	}
	// The alias binds its name in both versions (eagerly here).
	p.bindAt(x.text, x.line, true)
	vis := p.visibleTParams()
	allp := map[string]bool{}
	for n := range vis {
		allp[n] = true
	}
	for _, prm := range params {
		allp[prm.name] = true
	}
	for _, prm := range params {
		if prm.star > 0 && prm.boundStart >= 0 {
			return false
		}
		if prm.boundStart >= 0 &&
			!p.checkNames(prm.boundStart, prm.boundEnd, allp, x.line, false, false) {
			return false
		}
	}
	// Eager alias: module-certain names only when the statement itself is
	// an unconditional module statement; otherwise builtins, parameters
	// and `typing` only. (A function called before a later module
	// binding executes, or an early conditional block, would NameError
	// eagerly where the lazy alias would not.)
	useCertain := len(p.scopes) == 1 && p.cond == 0
	if !p.checkNames(assign+1, rhsEnd, allp, x.line, useCertain, true) {
		return false
	}
	g := &pepGeneric{scope: p.cur(), isDef: false, spanFrom: i, spanTo: rhsEnd, insLine: p.toks[i].line}
	for _, prm := range params {
		g.params = append(g.params, prm.name)
	}
	g.bparams = params
	if openIdx >= 0 {
		p.bracketSpans = append(p.bracketSpans, [2]int{openIdx, closeIdx})
	}
	p.generics = append(p.generics, g)
	p.aliasSpans = append(p.aliasSpans, aliasSpan{g: g, from: i, to: rhsEnd})
	if p.aliasSpecs == nil {
		p.aliasSpecs = map[string]*aliasSpec{}
	}
	p.aliasSpecs[x.text] = &aliasSpec{g: g, name: x.text, declFrom: i, assignIdx: assign, rhsFrom: assign + 1, rhsEnd: rhsEnd, generic: len(params) > 0}
	p.edits = append(p.edits, pepEdit{start: p.toks[i].start, stop: p.toks[assign].stop + 1,
		text: x.text + " ="})
	p.needLower = true
	p.needTyping = true
	return true
}

// checkNames proves every free name in toks[a:b] exists when the lowered
// code evaluates it: builtins, `typing` (imported by the lowering),
// extra (visible type parameters), and — for eager aliases — module
// names bound unconditionally on earlier lines. deep descends into
// f-string interpolations (lazy alias RHS); bounds are eager in both
// versions so their interiors cannot diverge.
func (p *pepLower) checkNames(a, b int, extra map[string]bool, stmtLine int, useCertain, deep bool) bool {
	rel := 0
	for k := a; k <= b; k++ {
		u := p.toks[k]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			rel++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			rel--
		case gen.Python3LexerLAMBDA, gen.Python3LexerAWAIT, gen.Python3LexerYIELD,
			gen.Python3LexerCOLONEQUAL:
			return false
		}
	}
	_ = rel
	names, ok := exprNames(p.toks[a:b+1], deep)
	if !ok {
		return false
	}
	for n := range names {
		if pepBuiltins[n] || n == "typing" || extra[n] {
			continue
		}
		if useCertain {
			if ln, ok := p.scopes[0].certain[n]; ok && ln < stmtLine {
				continue
			}
		}
		return false
	}
	return true
}

// exprNames collects free variable names in an expression token slice:
// keywords (`f(x=)`), attribute parts (`.y`) and comprehension targets
// are not variables. Nested f-strings are descended when deep.
func exprNames(ts []pepTok, deep bool) (map[string]bool, bool) {
	out := map[string]bool{}
	type ex struct {
		name  string
		depth int
	}
	var exempts []ex
	depth := 0
	isOpen := func(t int) bool {
		return t == gen.Python3LexerOPEN_PAREN || t == gen.Python3LexerOPEN_BRACK ||
			t == gen.Python3LexerOPEN_BRACE
	}
	isClose := func(t int) bool {
		return t == gen.Python3LexerCLOSE_PAREN || t == gen.Python3LexerCLOSE_BRACK ||
			t == gen.Python3LexerCLOSE_BRACE
	}
	for k, u := range ts {
		switch {
		case isOpen(u.typ):
			depth++
		case isClose(u.typ):
			depth--
			if depth < 0 {
				return nil, false
			}
			kept := exempts[:0]
			for _, e := range exempts {
				if e.depth <= depth {
					kept = append(kept, e)
				}
			}
			exempts = kept
		case u.typ == gen.Python3LexerFOR:
			for m := k + 1; m < len(ts); m++ {
				v := ts[m]
				if v.typ == gen.Python3LexerIN {
					break
				}
				if v.typ == gen.Python3LexerNEWLINE || v.typ == gen.Python3LexerSEMI_COLON {
					return nil, false
				}
				if v.typ == gen.Python3LexerNAME || v.typ == gen.Python3LexerUNDERSCORE {
					exempts = append(exempts, ex{v.text, depth})
				}
			}
		case u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE:
			if k > 0 && ts[k-1].typ == gen.Python3LexerDOT {
				continue
			}
			if k+1 < len(ts) && ts[k+1].typ == gen.Python3LexerASSIGN {
				continue
			}
			skip := false
			for _, e := range exempts {
				if e.name == u.text && e.depth <= depth {
					skip = true
					break
				}
			}
			if !skip {
				out[u.text] = true
			}
		case u.typ == gen.Python3LexerSTRING_LITERAL:
			if deep && isFStringTok(u.text) {
				for _, ex := range fstringExprs(u.text) {
					sub, ok := pepNamesInExpr(ex)
					if !ok {
						return nil, false
					}
					for n := range sub {
						out[n] = true
					}
				}
			}
		}
	}
	return out, true
}

// pepNamesInExpr lexes an f-string interpolation fragment and collects
// its free names (ok=false declines).
func pepNamesInExpr(expr string) (map[string]bool, bool) {
	toks := pepLex(expr)
	if toks == nil {
		return nil, false
	}
	return exprNames(toks, true)
}

// --- emission ---

func (p *pepLower) slice(a, b int) string {
	return string(p.runes[p.toks[a].start : p.toks[b].stop+1])
}

// importLine finds where `import typing` goes: after the shebang,
// comments, module docstring and `__future__` imports, before the first
// real statement.
func (p *pepLower) importLine() int {
	prelude := true
	for k, u := range p.toks {
		switch u.typ {
		case gen.Python3LexerNEWLINE, gen.Python3LexerINDENT, gen.Python3LexerDEDENT,
			gen.Python3LexerSEMI_COLON:
			continue
		case gen.Python3LexerSTRING_LITERAL:
			if prelude {
				continue
			}
			return u.line
		case gen.Python3LexerFROM:
			if prelude && k+1 < len(p.toks) &&
				p.toks[k+1].typ == gen.Python3LexerNAME && p.toks[k+1].text == "__future__" {
				for k++; k < len(p.toks); k++ {
					if p.toks[k].typ == gen.Python3LexerNEWLINE {
						break
					}
				}
				continue
			}
			return u.line
		default:
			return u.line
		}
	}
	return 1
}

func (p *pepLower) lineStarts() []int {
	starts := []int{0}
	for k, r := range p.runes {
		if r == '\n' {
			starts = append(starts, k+1)
		}
	}
	return starts
}

func (p *pepLower) indentOfLine(starts []int, line int) string {
	if line < 1 || line > len(starts) {
		return ""
	}
	s := starts[line-1]
	e := s
	for e < len(p.runes) && (p.runes[e] == ' ' || p.runes[e] == '\t') {
		e++
	}
	return string(p.runes[s:e])
}

// apply splices insertions and replacements into the source.
func (p *pepLower) apply() (string, bool) {
	if p.needTyping {
		line := p.importLine()
		p.inserts[line] = append([]string{"import typing\n"}, p.inserts[line]...)
	}
	starts := p.lineStarts()
	type op struct {
		pos  int
		end  int // == pos for pure insertions
		text string
	}
	var ops []op
	for line, texts := range p.inserts {
		if line < 1 || line > len(starts)+1 {
			return "", false
		}
		pos := len(p.runes)
		if line <= len(starts) {
			pos = starts[line-1]
		}
		ind := p.indentOfLine(starts, line)
		for _, tx := range texts {
			ops = append(ops, op{pos: pos, end: pos, text: ind + tx})
		}
	}
	for _, e := range p.edits {
		if e.start < 0 || e.stop > len(p.runes) || e.start > e.stop {
			return "", false
		}
		ops = append(ops, op{pos: e.start, end: e.stop, text: e.text})
	}
	sort.SliceStable(ops, func(a, b int) bool { return ops[a].pos < ops[b].pos })
	last := 0
	var b strings.Builder
	for _, o := range ops {
		if o.end > o.pos {
			// Replacement: must not overlap anything consumed before.
			if o.pos < last {
				return "", false
			}
			b.WriteString(string(p.runes[last:o.pos]))
			b.WriteString(o.text)
			last = o.end
			continue
		}
		// Pure insertion: must not land inside a replaced region.
		if o.pos < last {
			return "", false
		}
		b.WriteString(string(p.runes[last:o.pos]))
		b.WriteString(o.text)
		last = o.pos
	}
	b.WriteString(string(p.runes[last:]))
	return b.String(), true
}

func (p *pepLower) inDeco(k int) bool {
	for _, s := range p.decoSpans {
		if k >= s[0] && k < s[1] {
			return true
		}
	}
	return false
}

// isBindOcc reports whether the NAME at k binds (as opposed to reads).
func (p *pepLower) isBindOcc(k int) bool {
	if k+1 < len(p.toks) {
		switch nt := p.toks[k+1].typ; {
		case nt == gen.Python3LexerASSIGN || nt == gen.Python3LexerCOLONEQUAL || p.isAug(nt):
			// `y: U = x`: U is an annotation (read), not a target.
			if k > 0 && p.toks[k-1].typ == gen.Python3LexerCOLON {
				break
			}
			return true
		}
	}
	if k > 0 {
		switch pt := p.toks[k-1].typ; pt {
		case gen.Python3LexerDEF, gen.Python3LexerCLASS, gen.Python3LexerFOR,
			gen.Python3LexerIMPORT, gen.Python3LexerAS, gen.Python3LexerLAMBDA:
			return true
		}
	}
	if k+1 < len(p.toks) && p.toks[k+1].typ == gen.Python3LexerCOLON && p.valueAhead(k) {
		return true // `P: T = v`
	}
	return false
}

// --- post-walk pass: fresh cells, ownership scan, emission ---

// postPass runs after the walk, when binding information is complete.
// Phase 0 mints a fresh cell per parameter; phase 1 rewrites bounds and
// emits inserts; phase 2 scans occurrences, rewriting owned reads to
// their cell and declining blind spots.
func (p *pepLower) postPass() bool {
	for _, g := range p.generics {
		g.fresh = map[string]string{}
		for _, P := range g.params {
			f := P + "_f"
			for n := 2; p.usedNames[f]; n++ {
				f = fmt.Sprintf("%s_f%d", P, n)
			}
			p.usedNames[f] = true
			g.fresh[P] = f
		}
	}
	for _, g := range p.generics {
		if !p.emitGeneric(g) {
			return false
		}
	}
	annos := p.annoSpansLite()
	p.allDefaults = nil
	for _, pr := range p.defParens {
		p.allDefaults = append(p.allDefaults, p.defaultSpans(pr[0], pr[1])...)
	}
	for _, g := range p.generics {
		if !p.scanGeneric(g, annos) {
			return false
		}
	}
	// Owned cells in default values escape via __defaults__ (and their
	// repr differs), so they decline. Globals and other names stay.
	for _, s := range p.allDefaults {
		for k := s[0]; k <= s[1]; k++ {
			u := p.toks[k]
			if u.typ != gen.Python3LexerNAME && u.typ != gen.Python3LexerUNDERSCORE {
				continue
			}
			if p.ownerOf(k, u.text) != nil {
				return false
			}
		}
	}
	if len(p.aliasSpecs) > 0 {
		if !p.scanAliases(annos, p.isinstanceSpans()) {
			return false
		}
	}
	return true
}

// ownerOf resolves name at token k to the generic whose cell it reads,
// or nil (a real global/local, equivalent untouched in both versions).
// Alias parameters live in no scope: the innermost containing alias
// wins. Otherwise the scope chain decides: source bindings stay local,
// type parameters resolve to their generic, class namespaces are
// skipped (invisible nested) while class type parameters stay visible.
func (p *pepLower) ownerOf(k int, name string) *pepGeneric {
	best, bestFrom := (*pepGeneric)(nil), -1
	for _, a := range p.aliasSpans {
		if k < a.from || k > a.to {
			continue
		}
		for _, pn := range a.g.params {
			if pn == name && a.from > bestFrom {
				best, bestFrom = a.g, a.from
			}
		}
	}
	if best != nil {
		return best
	}
	for d := len(p.chains[k]) - 1; d >= 0; d-- {
		s := p.chains[k][d]
		if s.isClass {
			if p.tparams[s][name] {
				return p.scopeOwner[s]
			}
			continue
		}
		if s.collide[name] {
			return nil
		}
		if p.tparams[s][name] {
			return p.scopeOwner[s]
		}
	}
	return nil
}

func (p *pepLower) scanGeneric(g *pepGeneric, annos [][2]int) bool {
	for _, P := range g.params {
		F := g.fresh[P]
		for k, u := range p.toks {
			if u.typ == gen.Python3LexerSTRING_LITERAL && isFStringTok(u.text) {
				if !p.scanFStr(g, P, k, u.text) {
					return false
				}
				continue
			}
			if (u.typ != gen.Python3LexerNAME && u.typ != gen.Python3LexerUNDERSCORE) || u.text != P {
				continue
			}
			if p.inAliasBrackets(k) {
				continue // declarations and bounds: positional handling
			}
			if k > 0 {
				switch pt := p.toks[k-1].typ; pt {
				case gen.Python3LexerGLOBAL, gen.Python3LexerNONLOCAL, gen.Python3LexerDEL:
					continue // real-namespace ops; identical both sides
				}
			}
			if p.inDeco(k) {
				continue // decorators resolve without annotation scopes
			}
			if p.isBindOcc(k) {
				continue // bindings bind identically; never rewritten
			}
			owner := p.ownerOf(k, P)
			if owner == nil || owner != g {
				continue
			}
			// Owned read: only type positions observe the cell
			// opaquely. Annotations, bases and the alias's own RHS
			// rewrite; anything else — bodies, defaults, call
			// arguments, returns — can leak the value to repr/str
			// (which differs: `T` vs `~T`), so it declines. Bounds
			// are positional in boundRewrite.
			if g.isDef || g.self != nil {
				if !p.inTypePos(g, annos, k) {
					return false
				}
			}
			p.edits = append(p.edits, pepEdit{start: u.start, stop: u.stop + 1, text: F})
		}
	}
	return true
}

// inTypePos reports whether token k sits in a type position:
// an annotation span or a class base list. Anything else observes
// values.
func (p *pepLower) inTypePos(g *pepGeneric, annos [][2]int, k int) bool {
	_ = g
	if inSpansIdx(annos, k) || p.inBase(k) {
		return true
	}
	return false
}

// scanFStr declines f-strings interpolating an owned cell (splicing
// inside literals cannot be proven; the repr differs anyway).
func (p *pepLower) scanFStr(g *pepGeneric, P string, k int, tok string) bool {
	for _, ex := range fstringExprs(tok) {
		if !identHas(ex, P) {
			continue
		}
		if p.ownerOf(k, P) == g {
			return false
		}
	}
	return true
}

// defaultSpans collects `= value` spans at depth 1 of the parameter
// parens [k..pbEnd] (own-header defaults evaluate without the scope).
func (p *pepLower) defaultSpans(k, pbEnd int) [][2]int {
	var out [][2]int
	rel := 1
	for m := k + 1; m < pbEnd; m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			rel++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			rel--
		case gen.Python3LexerASSIGN:
			if rel == 1 {
				d, rd := m+1, 1
				for ; d < pbEnd; d++ {
					v := p.toks[d]
					if v.typ == gen.Python3LexerOPEN_PAREN || v.typ == gen.Python3LexerOPEN_BRACK ||
						v.typ == gen.Python3LexerOPEN_BRACE {
						rd++
					} else if v.typ == gen.Python3LexerCLOSE_PAREN || v.typ == gen.Python3LexerCLOSE_BRACK ||
						v.typ == gen.Python3LexerCLOSE_BRACE {
						rd--
					} else if v.typ == gen.Python3LexerCOMMA && rd == 1 {
						break
					}
				}
				if d-1 >= m+1 {
					out = append(out, [2]int{m + 1, d - 1})
				}
			}
		}
	}
	return out
}

// identHas reports whether P occurs as an identifier in expr text.
func identHas(ex, P string) bool {
	for _, m := range rePyIdent.FindAllString(ex, -1) {
		if m == P {
			return true
		}
	}
	return false
}

// inAliasBrackets reports whether token k lies in any alias's `[...]`
// (declarations and bounds, handled positionally, never by the scan).
func (p *pepLower) inAliasBrackets(k int) bool {
	for _, s := range p.bracketSpans {
		if k >= s[0] && k <= s[1] {
			return true
		}
	}
	return false
}

// emitGeneric emits one generic's TypeVar bindings and class Generic
// splice, with bounds rewritten to fresh cells.
func (p *pepLower) emitGeneric(g *pepGeneric) bool {
	for pi, P := range g.params {
		bp := g.bparams[pi]
		var bound, inner string
		var isConstr bool
		if bp.boundStart >= 0 {
			rb, ic, inn, ok := p.boundRewrite(g, pi, bp)
			if !ok {
				return false
			}
			bound, isConstr, inner = rb, ic, inn
		}
		p.inserts[g.insLine] = append(p.inserts[g.insLine],
			p.tvarLine(g.fresh[P], P, bound, bp.star, isConstr, inner))
	}
	if !g.isDef && g.self != nil {
		names := make([]string, 0, len(g.params))
		for _, P := range g.params {
			names = append(names, g.fresh[P])
		}
		generic := "typing.Generic[" + strings.Join(names, ", ") + "]"
		switch g.baseKind {
		case 1:
			p.edits = append(p.edits, pepEdit{start: g.baseAt, stop: g.baseAt,
				text: "(" + generic + ")"})
		case 2:
			p.edits = append(p.edits, pepEdit{start: g.baseAt, stop: g.baseEnd,
				text: "(" + generic + ")"})
		case 3:
			p.edits = append(p.edits, pepEdit{start: g.baseAt, stop: g.baseAt,
				text: generic + ", "})
		case 4:
			p.edits = append(p.edits, pepEdit{start: g.baseAt, stop: g.baseAt,
				text: ", " + generic})
		default:
			return false
		}
	}
	return true
}

// boundRewrite rewrites a bound range to fresh cells, positionally (no
// scope chains exist inside the skipped brackets): earlier siblings and
// enclosing generics rewrite; self references and later siblings stay
// (NameError in both versions); f-strings decline.
func (p *pepLower) boundRewrite(g *pepGeneric, pi int, bp pepParam) (bound string, isConstr bool, inner string, ok bool) {
	a, b := bp.boundStart, bp.boundEnd
	for k := a; k <= b; k++ {
		u := p.toks[k]
		if u.typ == gen.Python3LexerSTRING_LITERAL && isFStringTok(u.text) {
			return "", false, "", false
		}
	}
	rewrite := func(from, to int) (string, bool) {
		// free names with comp-target exemption
		type ex struct {
			name  string
			depth int
		}
		var exempts []ex
		depth := 0
		reps := map[int]string{}
		for k := from; k <= to; k++ {
			u := p.toks[k]
			switch u.typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				depth--
			case gen.Python3LexerFOR:
				for m := k + 1; m <= to; m++ {
					v := p.toks[m]
					if v.typ == gen.Python3LexerIN {
						break
					}
					if v.typ == gen.Python3LexerNAME || v.typ == gen.Python3LexerUNDERSCORE {
						exempts = append(exempts, ex{v.text, depth})
					}
				}
			case gen.Python3LexerNAME, gen.Python3LexerUNDERSCORE:
				if k > from && p.toks[k-1].typ == gen.Python3LexerDOT {
					continue
				}
				if k+1 <= to && p.toks[k+1].typ == gen.Python3LexerASSIGN {
					continue
				}
				exempt := false
				for _, e := range exempts {
					if e.name == u.text && e.depth <= depth {
						exempt = true
						break
					}
				}
				if exempt {
					continue
				}
				if fr, ok := p.boundOwner(g, pi, u.text, a); ok {
					reps[k] = fr
				}
			}
		}
		// splice
		var sb strings.Builder
		pos := p.toks[from].start
		ids := make([]int, 0, len(reps))
		for k := range reps {
			ids = append(ids, k)
		}
		sortInts(ids)
		for _, k := range ids {
			u := p.toks[k]
			if u.start < pos {
				return "", false
			}
			sb.WriteString(string(p.runes[pos:u.start]))
			sb.WriteString(reps[k])
			pos = u.stop + 1
		}
		sb.WriteString(string(p.runes[pos : p.toks[to].stop+1]))
		return sb.String(), true
	}
	if _, ok := p.constraintInner(a, b); ok {
		ri, ok := rewrite(a+1, b-1)
		if !ok {
			return "", false, "", false
		}
		return "", true, ri, true
	}
	rb, ok := rewrite(a, b)
	if !ok {
		return "", false, "", false
	}
	return rb, false, "", true
}

// boundOwner resolves a bound name: earlier siblings and enclosing
// generics rewrite to their cells; self, later siblings and globals
// stay (identical NameErrors/values in both versions).
func (p *pepLower) boundOwner(g *pepGeneric, pi int, name string, at int) (string, bool) {
	for qi, qn := range g.params {
		if qn != name {
			continue
		}
		if qi < pi {
			return g.fresh[name], true
		}
		return "", false
	}
	best, bestFrom := (*pepGeneric)(nil), -1
	for _, h := range p.generics {
		if h == g {
			continue
		}
		if at < h.spanFrom || at > h.spanTo {
			continue
		}
		for _, pn := range h.params {
			if pn == name && h.spanFrom > bestFrom {
				best, bestFrom = h, h.spanFrom
			}
		}
	}
	if best != nil {
		return best.fresh[name], true
	}
	return "", false
}

// sortInts orders token indices for splicing.
func sortInts(ids []int) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// --- alias definition support ---

// recordBaseSpan records a class header's `(...)` range for the
// alias-use scan (bare alias names in bases decline; subscripted ones
// rewrite).
func (p *pepLower) recordBaseSpan(n int) {
	j := n + 1
	if j < len(p.toks) && p.toks[j].typ == gen.Python3LexerOPEN_BRACK {
		depth := 0
		for ; j < len(p.toks); j++ {
			switch p.toks[j].typ {
			case gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACE:
				depth--
				if depth == 0 {
					break
				}
			case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
				return
			}
			if depth == 0 {
				break
			}
		}
		j++
	}
	if j >= len(p.toks) || p.toks[j].typ != gen.Python3LexerOPEN_PAREN {
		return
	}
	depth := 0
	for k := j; k < len(p.toks); k++ {
		switch p.toks[k].typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth == 0 {
				if p.toks[k].typ != gen.Python3LexerCLOSE_PAREN {
					return
				}
				p.baseSpans = append(p.baseSpans, [2]int{j, k})
				return
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			if depth == 0 {
				return
			}
		}
	}
}

// annoSpansLite computes annotation spans post-walk: def parameter and
// return annotations (any def), plus variable annotations `x: T [= v]`
// (statement-start NAME directly followed by a depth-0 colon that is
// not a header, lambda or keyword colon).
func (p *pepLower) annoSpansLite() [][2]int {
	var spans [][2]int
	// def headers
	for i := 0; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.typ != gen.Python3LexerDEF || !p.atStmtStart(i) {
			continue
		}
		j := i + 1
		if j < len(p.toks) && p.toks[j].typ == gen.Python3LexerASYNC {
			j++
		}
		if j >= len(p.toks) || (p.toks[j].typ != gen.Python3LexerNAME &&
			p.toks[j].typ != gen.Python3LexerUNDERSCORE) {
			continue
		}
		j++
		if j < len(p.toks) && p.toks[j].typ == gen.Python3LexerOPEN_BRACK {
			depth := 0
			for ; j < len(p.toks); j++ {
				switch p.toks[j].typ {
				case gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACE:
					depth++
				case gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACE:
					depth--
					if depth == 0 {
						break
					}
				}
				if depth == 0 {
					break
				}
			}
			j++
		}
		if j >= len(p.toks) || p.toks[j].typ != gen.Python3LexerOPEN_PAREN {
			continue
		}
		depth := 0
		pbEnd := -1
		for k := j; k < len(p.toks); k++ {
			switch p.toks[k].typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				depth--
				if depth == 0 {
					pbEnd = k
				}
			}
			if pbEnd >= 0 {
				break
			}
		}
		if pbEnd < 0 {
			continue
		}
		rel, astart := 1, -1
		flush := func() {
			if astart >= 0 && astart <= pbEnd-1 {
				spans = append(spans, [2]int{astart, pbEnd - 1})
			}
			astart = -1
		}
		for m := j + 1; m < pbEnd; m++ {
			u := p.toks[m]
			switch u.typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				rel++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				rel--
			case gen.Python3LexerCOLON:
				if rel == 1 {
					astart = m + 1
				}
			case gen.Python3LexerCOMMA, gen.Python3LexerASSIGN:
				if rel == 1 && astart >= 0 {
					if astart <= m-1 {
						spans = append(spans, [2]int{astart, m - 1})
					}
					astart = -1
				}
			}
		}
		flush()
		// return annotation
		for m := pbEnd + 1; m < len(p.toks); m++ {
			u := p.toks[m]
			if u.typ == gen.Python3LexerARROW {
				// to the header colon
				d := 0
				for e := m + 1; e < len(p.toks); e++ {
					v := p.toks[e]
					switch v.typ {
					case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
						d++
					case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
						d--
					case gen.Python3LexerCOLON:
						if d == 0 {
							if m+1 <= e-1 {
								spans = append(spans, [2]int{m + 1, e - 1})
							}
							e = len(p.toks)
						}
					case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
						if d == 0 {
							e = len(p.toks)
						}
					}
					if e >= len(p.toks) {
						break
					}
				}
				break
			}
			if u.typ == gen.Python3LexerCOLON || u.typ == gen.Python3LexerNEWLINE {
				break
			}
		}
	}
	// variable annotations `x: T [= v]`
	for i := 0; i+1 < len(p.toks); i++ {
		t := p.toks[i]
		if (t.typ != gen.Python3LexerNAME && t.typ != gen.Python3LexerUNDERSCORE) || !p.atStmtStart(i) {
			continue
		}
		if p.toks[i+1].typ != gen.Python3LexerCOLON {
			continue
		}
		depth := 0
		for m := i + 2; m < len(p.toks); m++ {
			u := p.toks[m]
			switch u.typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				depth--
			case gen.Python3LexerASSIGN, gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
				if depth == 0 {
					if i+2 <= m-1 {
						spans = append(spans, [2]int{i + 2, m - 1})
					}
					m = len(p.toks)
				}
			}
			if m >= len(p.toks) {
				break
			}
		}
	}
	return spans
}

// atStmtStart reports whether token i starts a statement: beginning of
// input or preceded by a statement boundary at depth 0. Depth is
// recomputed (post-walk helper, no walk state).
func (p *pepLower) atStmtStart(i int) bool {
	if i <= 0 {
		return true
	}
	// `async` precedes its header transparently (`async def/for/with`).
	if p.toks[i-1].typ == gen.Python3LexerASYNC {
		return p.atStmtStart(i - 1)
	}
	depth := 0
	for m := 0; m < i; m++ {
		switch p.toks[m].typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
		}
	}
	if depth != 0 {
		return false
	}
	pt := p.toks[i-1].typ
	return pt == gen.Python3LexerNEWLINE || pt == gen.Python3LexerINDENT ||
		pt == gen.Python3LexerDEDENT || pt == gen.Python3LexerSEMI_COLON ||
		pt == gen.Python3LexerCOLON
}

// isinstanceSpans finds `isinstance(`/`issubclass(` second-argument
// spans (bare alias names there check identically in both versions).
func (p *pepLower) isinstanceSpans() [][2]int {
	var spans [][2]int
	for i := 0; i+1 < len(p.toks); i++ {
		u := p.toks[i]
		if u.typ != gen.Python3LexerNAME || (u.text != "isinstance" && u.text != "issubclass") {
			continue
		}
		if p.toks[i+1].typ != gen.Python3LexerOPEN_PAREN {
			continue
		}
		depth := 0
		comma := -1
		close := -1
		for m := i + 1; m < len(p.toks); m++ {
			v := p.toks[m]
			switch v.typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				depth++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				depth--
				if depth == 0 {
					close = m
				}
			case gen.Python3LexerCOMMA:
				if depth == 1 && comma < 0 {
					comma = m
				}
			case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
				m = len(p.toks)
			}
			if close >= 0 {
				break
			}
		}
		if comma > 0 && close > comma+1 {
			spans = append(spans, [2]int{comma + 1, close - 1})
		}
	}
	return spans
}

// inSpansIdx reports whether k lies in any span.
func inSpansIdx(spans [][2]int, k int) bool {
	for _, s := range spans {
		if k >= s[0] && k <= s[1] {
			return true
		}
	}
	return false
}

// --- alias-use scan ---

// scanAliases enforces the alias-use policy per `type X = ...`: the name
// may appear in bindings, deletions, annotations (bare or subscripted
// with rewrite), isinstance second arguments and its own declaration;
// calls, subscripts elsewhere, attributes, decorators, defaults and
// bare value reads observe TypeAliasType-vs-value and decline.
func (p *pepLower) scanAliases(annos, isinst [][2]int) bool {
	for _, spec := range p.aliasSpecs {
		X := spec.name
		for k, u := range p.toks {
			if u.typ == gen.Python3LexerSTRING_LITERAL && isFStringTok(u.text) {
				for _, ex := range fstringExprs(u.text) {
					if identHas(ex, X) {
						return false
					}
				}
				continue
			}
			if (u.typ != gen.Python3LexerNAME && u.typ != gen.Python3LexerUNDERSCORE) || u.text != X {
				continue
			}
			if k > 0 && p.toks[k-1].typ == gen.Python3LexerDOT {
				continue // attribute part, not a read
			}
			if k >= spec.declFrom && k <= spec.assignIdx {
				continue // its own declaration prefix
			}
			if p.isBindOcc(k) {
				continue
			}
			if k > 0 {
				switch pt := p.toks[k-1].typ; pt {
				case gen.Python3LexerGLOBAL, gen.Python3LexerNONLOCAL, gen.Python3LexerDEL:
					continue
				}
			}
			if p.inDeco(k) {
				return false
			}
			nx := -1
			if k+1 < len(p.toks) {
				nx = p.toks[k+1].typ
			}
			if nx == gen.Python3LexerOPEN_BRACK {
				// Subscript: rewrite generic aliases in annotation
				// and base positions; non-generic subscripts crash
				// identically in both and stay; isinstance subscripts
				// crash identically too; value positions decline.
				if inSpansIdx(annos, k) || p.inBase(k) {
					if !p.subscriptUse(spec, k) {
						return false
					}
					continue
				}
				if inSpansIdx(isinst, k) {
					continue // both-crash parity; leave tokens
				}
				return false
			}
			if inSpansIdx(annos, k) {
				continue // bare alias in annotations: no crash either way
			}
			if inSpansIdx(isinst, k) {
				// Bare alias as isinstance subject: native always
				// TypeErrors (TypeAliasType is not a type); the
				// lowering only matches when the value crashes too,
				// i.e. a subscript-shaped RHS.
				if !p.rhsIsSubscript(spec) {
					return false
				}
				continue
			}
			return false
		}
	}
	return true
}

// subscriptUse handles `X[args]`: generic aliases rewrite to the
// substituted RHS in annotation/isinstance/base positions and decline
// elsewhere; non-generic subscripts crash identically in both and stay.
func (p *pepLower) subscriptUse(spec *aliasSpec, k int) bool {
	// match brackets from k+1
	depth := 0
	close := -1
	for m := k + 1; m < len(p.toks); m++ {
		v := p.toks[m]
		switch v.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
			if depth == 0 {
				if v.typ != gen.Python3LexerCLOSE_BRACK {
					return false
				}
				close = m
			}
		case gen.Python3LexerNEWLINE, gen.Python3LexerSEMI_COLON:
			return false
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return false
	}
	if !spec.generic {
		return true // both-crash parity (`Y[int]` TypeErrors either way)
	}
	// Caller restricts to annotation/base positions.
	text, ok := p.substitute(spec, k+2, close-1)
	if !ok {
		return false
	}
	p.edits = append(p.edits, pepEdit{start: p.toks[k].start, stop: p.toks[close].stop + 1, text: text})
	return true
}

// inBase reports whether k lies in a class base list.
func (p *pepLower) inBase(k int) bool {
	return inSpansIdx(p.baseSpans, k)
}

// substitute rewrites `X[args]` to the alias RHS with formal parameters
// replaced positionally by the actual argument texts. Starred/keyword
// arguments, arity mismatch, nested alias subscripts and owned
// references inside arguments decline.
func (p *pepLower) substitute(spec *aliasSpec, a, b int) (string, bool) {
	if a > b {
		return "", false // `X[]`
	}
	g := spec.g
	// split top-level args
	var args [][2]int
	depth, start := 0, a
	for m := a; m <= b; m++ {
		u := p.toks[m]
		switch u.typ {
		case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
			depth++
		case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
			depth--
		case gen.Python3LexerCOMMA:
			if depth == 0 {
				args = append(args, [2]int{start, m - 1})
				start = m + 1
			}
		case gen.Python3LexerSTAR, gen.Python3LexerPOWER:
			if depth == 0 {
				return "", false
			}
		}
	}
	args = append(args, [2]int{start, b})
	if len(args) != len(g.params) {
		return "", false
	}
	for _, ar := range args {
		// keyword argument?
		d := 0
		for m := ar[0]; m <= ar[1]; m++ {
			u := p.toks[m]
			switch u.typ {
			case gen.Python3LexerOPEN_PAREN, gen.Python3LexerOPEN_BRACK, gen.Python3LexerOPEN_BRACE:
				d++
			case gen.Python3LexerCLOSE_PAREN, gen.Python3LexerCLOSE_BRACK, gen.Python3LexerCLOSE_BRACE:
				d--
			case gen.Python3LexerASSIGN:
				if d == 0 {
					return "", false
				}
			}
		}
		// owned references inside arguments would need their own cells;
		// decline (rare twice over).
		for m := ar[0]; m <= ar[1]; m++ {
			u := p.toks[m]
			if (u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE) &&
				p.ownerOf(m, u.text) != nil {
				return "", false
			}
			if u.typ == gen.Python3LexerSTRING_LITERAL && isFStringTok(u.text) {
				return "", false
			}
		}
	}
	// nested alias subscripts inside the RHS template: decline (rare).
	for m := spec.rhsFrom; m <= spec.rhsEnd; m++ {
		u := p.toks[m]
		if (u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE) &&
			m+1 <= spec.rhsEnd && p.toks[m+1].typ == gen.Python3LexerOPEN_BRACK {
			if _, ok := p.aliasSpecs[u.text]; ok {
				return "", false
			}
		}
	}
	// rebuild the RHS with formals replaced by actuals
	type rep struct {
		at, to int
		text   string
	}
	var reps []rep
	for m := spec.rhsFrom; m <= spec.rhsEnd; m++ {
		u := p.toks[m]
		if u.typ != gen.Python3LexerNAME && u.typ != gen.Python3LexerUNDERSCORE {
			continue
		}
		for pi, pn := range g.params {
			if u.text != pn {
				continue
			}
			// formals resolve to the alias cell here (checked at
			// lowering time); substitute positionally.
			ar := args[pi]
			reps = append(reps, rep{m, m, p.slice(ar[0], ar[1])})
		}
	}
	var sb strings.Builder
	pos := p.toks[spec.rhsFrom].start
	for _, r := range reps {
		u := p.toks[r.at]
		if u.start < pos {
			return "", false
		}
		sb.WriteString(string(p.runes[pos:u.start]))
		sb.WriteString(r.text)
		pos = u.stop + 1
	}
	sb.WriteString(string(p.runes[pos : p.toks[spec.rhsEnd].stop+1]))
	return sb.String(), true
}

// rhsIsSubscript reports whether the alias RHS is subscript-shaped at
// top level (`a.b[c]`): isinstance() against such a value crashes
// identically in both versions, matching native's TypeError.
func (p *pepLower) rhsIsSubscript(spec *aliasSpec) bool {
	j := spec.rhsFrom
	for j <= spec.rhsEnd {
		u := p.toks[j]
		if u.typ == gen.Python3LexerNAME || u.typ == gen.Python3LexerUNDERSCORE {
			j++
			continue
		}
		if u.typ == gen.Python3LexerDOT {
			j++
			continue
		}
		break
	}
	return j <= spec.rhsEnd && p.toks[j].typ == gen.Python3LexerOPEN_BRACK
}
