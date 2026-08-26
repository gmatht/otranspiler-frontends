// go-sh: Go source -> shIR JSON (A1 contract), hand-rolled Go frontend.
//
// WORKER REWRITE (2026-08-06): the v1 line-scanner stub is replaced by a
// real tokenizer + recursive-descent parser for the v2 Go subset, with a
// lowering pass that emits the EXACT A1 node shapes the core frontend
// produces for the equivalent shell construct (verified against
// `debashc file --shir` on the paired posix-sh-go testdata, which shares
// the t01..t52 corpus). The A1 contract (sh2perl/src/shir_json.rs +
// shir_json_in.rs) is the source of truth; the deserializer's ingress
// gate is the Makefile test's acceptance criterion.
//
// Subset (v2, corpus-defined): package/import/func-main boilerplate,
// fmt.Println/Print/Printf, os.Getenv/Setenv/WriteFile/Stat,
// exec.Command(...).Output()/.Run(), bufio.NewReader(os.Stdin)+ReadString,
// bufio.NewScanner(os.Stdin)+for sc.Scan()/sc.Text() (while-read),
// := / = / += / ++ / multi-assign, indexed assign a[1]=x, append(a, ...),
// strings.ReplaceAll (${s//o/n}) + strings.Contains (grep idiom),
// string-concat + arithmetic exprs, len(), slicing, indexing, array
// literals + range-for, if/else if/else, for cond, for init;cond;post
// (numeric header → the core's ForInit C-style shape), switch/case/default,
// TYPE SWITCH
// (`switch [v :=] x.(type) { case T: ... default: ... }` — lowered to the
// Case node with discriminant Call{func:"typeof", args:[x]} (sh2.typeOf:
// store strings -> "string", lifted numbers -> "int"/"float", bools ->
// "bool", arrays -> "array"), type-name clause patterns ("*" = default),
// guard var v bound to getVar x in every arm; case labels must be plain
// type names — core request go-sh-20260813-154009), func literals
// (params -> $1.., fresh vars -> `local`), go-func background,
// raw-string heredocs, comments, shebang.
//
// Top-level func decls `func name[typeParams](params) [ret] { body }`
// lower to the same Function subs as func literals. Go GENERICS (grammar
// typeParameters / typeArgs, core request go-sh-typeargs): a generic
// call `id[int](x)` carries typeArgs on the Call — the A1 deserializer
// validates the string array and ERASES it at ingress (documented
// erasure contract; no runtime form), and only type-INDEPENDENT generic
// bodies lower (type-dependent bodies would need compile-time
// substitution and stay Refuse > guess territory).
//
// Refuse > guess: anything outside the subset is a hard error (the gate
// reports FAIL), never a silent mis-lowering.
package golib

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// ─────────────────────────────────────────────────────────────────────
// Tokenizer
// ─────────────────────────────────────────────────────────────────────

type tokKind int

const (
	tEOF tokKind = iota
	tNL
	tIdent
	tNum
	tStr    // "..."  (text = decoded, raw = verbatim between quotes)
	tChar   // '...'  (a Go rune literal — an INTEGER in Go; the A1
	        //         lowers it to its ASCII code in byte contexts)
	tRawStr // `...`  (text = verbatim content)
	tOp     // multi-char operator
	tPunct  // single char
)

type token struct {
	kind tokKind
	text string
	raw  string
	line int
}

var multiOps = []string{":=", "==", "!=", "<=", ">=", "&&", "||", "+=", "++"}

func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '\n':
			toks = append(toks, token{kind: tNL, line: line})
			line++
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i+1 >= len(src) {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += 2
		case c == '"':
			start := i
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' && i+1 < len(src) {
					i += 2
				} else {
					i++
				}
			}
			if i >= len(src) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: tStr, raw: src[start+1 : i], text: src[start+1 : i], line: line})
			i++
		case c == '\'':
			// single-quoted char literal (e.g. ReadString('\n')) — a Go
			// RUNE literal (an integer); tokenized as tChar so the parser
			// can lower it to its ASCII code in byte contexts.
			start := i
			i++
			for i < len(src) && src[i] != '\'' {
				if src[i] == '\\' && i+1 < len(src) {
					i += 2
				} else {
					i++
				}
			}
			if i >= len(src) {
				return nil, fmt.Errorf("unterminated char literal")
			}
			toks = append(toks, token{kind: tChar, raw: src[start+1 : i], text: src[start+1 : i], line: line})
			i++
		case c == '`':
			start := i
			i++
			for i < len(src) && src[i] != '`' {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i >= len(src) {
				return nil, fmt.Errorf("unterminated raw string literal")
			}
			toks = append(toks, token{kind: tRawStr, raw: src[start+1 : i], text: src[start+1 : i], line: line})
			i++
		case c >= '0' && c <= '9':
			start := i
			for i < len(src) {
				ch := src[i]
				if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') ||
					ch == 'o' || ch == 'x' || ch == 'X' || ch == '.' || ch == '_' {
					i++
				} else {
					break
				}
			}
			toks = append(toks, token{kind: tNum, text: src[start:i], line: line})
		case isIdentStart(c):
			start := i
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			toks = append(toks, token{kind: tIdent, text: src[start:i], line: line})
		default:
			matched := false
			for _, op := range multiOps {
				if strings.HasPrefix(src[i:], op) {
					toks = append(toks, token{kind: tOp, text: op, line: line})
					i += len(op)
					matched = true
					break
				}
			}
			if matched {
				break
			}
			toks = append(toks, token{kind: tPunct, text: string(c), line: line})
			i++
		}
	}
	toks = append(toks, token{kind: tEOF, line: line})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// decodeGoStr unescapes a double-quoted Go string body (\\ \" \n \t \r;
// other \X kept as-is — printf formats keep their raw backslashes).
func decodeGoStr(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' || i+1 >= len(raw) {
			b.WriteByte(c)
			continue
		}
		i++
		switch raw[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\', '"':
			b.WriteByte(raw[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(raw[i])
		}
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────
// A1 JSON builders (mirror shir_json.rs node shapes byte-for-byte)
// ─────────────────────────────────────────────────────────────────────

func strExpr(v string) map[string]any {
	return map[string]any{"type": "Str", "value": v, "style": "DoubleQuoted"}
}
func getVarExpr(name string) map[string]any {
	return map[string]any{
		"type": "Call", "func": "getVar",
		"args":   []any{strExpr(name)},
		"purity": "Emulable",
	}
}
func testCall(arg string) map[string]any {
	return map[string]any{
		"type": "Call", "func": "test",
		"args":   []any{strExpr(arg)},
		"purity": "Emulable",
	}
}
func interpLit(text string) map[string]any {
	return map[string]any{
		"type":  "Interpolate",
		"parts": []any{map[string]any{"kind": "lit", "text": text}},
	}
}
func interpParts(parts []any) map[string]any {
	return map[string]any{"type": "Interpolate", "parts": parts}
}
func partLit(text string) map[string]any { return map[string]any{"kind": "lit", "text": text} }
func partExpr(e map[string]any) map[string]any {
	return map[string]any{"kind": "expr", "expr": e}
}
func arithNum(n int) map[string]any { return map[string]any{"type": "Num", "value": n} }
func arithVar(name string) map[string]any {
	return map[string]any{"type": "Var", "name": name}
}
func arithBin(lhs map[string]any, op string, rhs map[string]any) map[string]any {
	// a BARE Bin node — the core's Arith ast tree has bare children
	// (`ast.Bin.lhs/rhs` are Num/Bin/Var nodes, not nested Arith
	// wrappers); the single top-level "Arith" wrapper is applied by
	// arithWrap at the expression boundary
	return map[string]any{"type": "Bin", "lhs": lhs, "op": op, "rhs": rhs}
}

// arithWrap: the one top-level wrapper the A1 contract expects
// (`{"type":"Arith","ast":<bare tree>}`) — deserializer rejects an
// "Arith" nested inside a Bin's lhs/rhs.
func arithWrap(ast map[string]any) map[string]any {
	return map[string]any{"type": "Arith", "ast": ast}
}
func paramCall(args ...string) map[string]any {
	a := make([]any, len(args))
	for i, s := range args {
		a[i] = strExpr(s)
	}
	return map[string]any{
		"type": "Call", "func": "param",
		"args":   a,
		"purity": "PureCpu",
	}
}
func joinCall(inner map[string]any) map[string]any {
	return map[string]any{
		"type": "Call", "func": "join",
		"args":   []any{inner},
		"purity": "PureCpu",
	}
}
func assignStmt(name string, expr map[string]any) map[string]any {
	return map[string]any{
		"type": "Assign",
		"targets": []any{map[string]any{
			"var": name, "sigil": nil, "indices": []any{},
		}},
		"expr": expr,
	}
}

// arithAssignStmt: the core's ForInit init shape (byte-identical to the
// `for ((i=N; ...))` lowering in shir.rs): Assign wrapping an Arith ast
// `Assign` node (`i = N`), NOT a plain Str assignment.
func arithAssignStmt(name string, n int) map[string]any {
	return map[string]any{
		"type": "Assign",
		"targets": []any{map[string]any{
			"var": name, "sigil": nil, "indices": []any{},
		}},
		"expr": map[string]any{
			"type": "Arith",
			"ast": map[string]any{
				"type": "Assign", "op": "=", "var": name, "rhs": arithNum(n),
			},
		},
	}
}

// arithIncDecStmt: the core's ForInit step shape (byte-identical to the
// `for ((...; i++))` lowering): Assign wrapping an Arith ast `IncDec`
// node (`i++`).
func arithIncDecStmt(name string) map[string]any {
	return map[string]any{
		"type": "Assign",
		"targets": []any{map[string]any{
			"var": name, "sigil": nil, "indices": []any{},
		}},
		"expr": map[string]any{
			"type": "Arith",
			"ast": map[string]any{
				"type": "IncDec", "var": name, "delta": 1, "prefix": false,
			},
		},
	}
}
func execStmt(cmd string, words []map[string]any, purity string) map[string]any {
	return execStmtTA(cmd, words, purity, nil)
}

// execStmtTA: execStmt with Go generic type arguments (grammar rule
// typeArgs, `Name[TypeList](args)`) attached to the Call. The A1
// erasure contract (core request go-sh-typeargs) validates them as a
// string array and drops them at ingress, so the runtime behavior is
// identical to execStmt — the type arguments are carried for fidelity,
// never executed.
func execStmtTA(cmd string, words []map[string]any, purity string, typeArgs []string) map[string]any {
	elems := make([]any, len(words))
	for i, w := range words {
		elems[i] = w
	}
	call := map[string]any{
		"type": "Call", "func": "exec",
		"args":   []any{strExpr(cmd), map[string]any{"type": "Array", "elements": elems}},
		"purity": purity,
	}
	if len(typeArgs) > 0 {
		ta := make([]any, len(typeArgs))
		for i, s := range typeArgs {
			ta[i] = s
		}
		call["typeArgs"] = ta
	}
	return map[string]any{
		"type": "Expr",
		"expr": call,
	}
}

// execCond builds a Call(fn=exec) usable as an If/While cond (the
// `while read x` shape).
func execCond(cmd string, words []map[string]any) map[string]any {
	elems := make([]any, len(words))
	for i, w := range words {
		elems[i] = w
	}
	return map[string]any{
		"type": "Call", "func": "exec",
		"args":   []any{strExpr(cmd), map[string]any{"type": "Array", "elements": elems}},
		"purity": "Emulable",
	}
}

// ─────────────────────────────────────────────────────────────────────
// Expressions
// ─────────────────────────────────────────────────────────────────────

// expr kinds: "str" "num" "rawstr" "var" "binop" (comparisons/logical)
// "not" "add" "mul" "neg" (arith-or-concat) "index" "slice" "strlen"
// "arrlen" "call" "func".
type expr struct {
	kind string
	// str/num
	text string
	raw  string // printf-format raw text
	// var
	name string
	// arith / concat / binop
	op  string
	lhs *expr
	rhs *expr
	// binop comparisons
	BOp     string // "==" "!=" "<" "<=" ">" ">=" "&&" "||"
	BOpKind string // "cmp" | "and" | "or"
	// index/slice/strlen/arrlen
	target *expr
	idx1   string // literal bound (plain number)
	idx2   string
	idx1e  *expr // expression bound (Go computed indices)
	idx2e  *expr
	// call
	callee   string
	args     []*expr
	typeArgs []string // generic instantiation `Name[TypeList](args)` (typeArgs)
	// struct composite literal (`token{kind: tNL, line: line}`): the
	// type name in `name`, the field VALUES in `args`, and the NAMED
	// field names in `fieldNames` (empty for positional — resolved from
	// the tracked struct type's fields).
	fieldNames []string
	// member access on a non-var base (`p.tok().line` — the method
	// call's result field): the base expr (the name is ".field").
	memberTarget *expr
	// func literal (lowered body, params in args)
	body   []map[string]any
	params []string
	// raw A1 cond JSON (if-init forms lower their cond directly,
	// bypassing condTestString — the assign-Capture shape has no
	// test-string form)
	rawJSON map[string]any
}

// ─────────────────────────────────────────────────────────────────────
// Parser
// ─────────────────────────────────────────────────────────────────────

type parser struct {
	toks []token
	pos  int
	// semantic side-state (the Go subset's shell-shaped meanings)
	varTypes   map[string]string // name -> "Int" | "Str" | "Array" | "Map"
	consts     map[string]int    // evaluated int const values (const refs)
	constStrs  map[string]string // evaluated string const values
	arrays     map[string]arrayInfo
	maps       map[string]bool    // m := map[K]V{...} — assoc-array name
	bufs       map[string]string  // b := bytes.Buffer — accumulated contents
	runtimeBufs map[string]bool   // buffers with runtime-dependent contents (WriteByte of a runtime byte)
	cmds       map[string][]*expr // cmd := exec.Command(...) -> args
	stdinRdr   map[string]bool    // r := bufio.NewReader(os.Stdin)
	fnNames    map[string]bool    // f := func(...){...} — callable subs
	outer      map[string]bool    // vars assigned at top level
	fnParams   map[string]bool    // inside a func literal
	fnParamOrd []string           // ordered param names -> $1..
	fnLocals   map[string]bool
	inFunc     bool
	// type-switch guard aliases: `switch v := x.(type)` binds v to x in
	// every arm (core request go-sh-20260813-154009) — reads of the guard
	// var resolve to the guarded var (getVar x), matching the contract's
	// "v binds to getVar(\"x\")" lowering.
	varAlias map[string]string
	// struct field names by type name (`type tok struct{ kind, text
	// string }` → ["kind", "text"]) — positional composite literals
	// `tok{"a", "b"}` lower by position (the go-sh self-hosting
	// contract: structs are JSON object strings).
	structFields map[string][]string
	// struct field ELEMENT types by type name (`toks []token` →
	// "token"; `pos int` → "int") — a struct-field index read
	// (`t := p.toks[p.pos]`) registers the target's type.
	structFieldTypes map[string]map[string]string
	// var → struct TYPE name (`p := &parser{...}` → "parser") — the
	// field-type lookup key for member/index reads.
	varStructTypes map[string]string
	// method receiver → struct TYPE name (`func (p *parser) ...` →
	// "parser").
	receiverTypes map[string]string
	// temp-var sequence for cond hoists (function-call conditions)
	tmpSeq int
}

type arrayInfo struct {
	elems []map[string]any
	typ   string // "Int" | "Str"
}

func (p *parser) tok() token { return p.toks[p.pos] }
func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}
func (p *parser) failf(f string, args ...any) {
	panic(fmt.Sprintf("line %d: %s", p.tok().line, fmt.Sprintf(f, args...)))
}

func (p *parser) skipNL() {
	for p.tok().kind == tNL {
		p.pos++
	}
}

func (p *parser) expect(k tokKind, text string) token {
	t := p.tok()


	if text != "" {
		if t.text != text {
			p.failf("expected %q, got %q", text, t.text)
		}
	} else if t.kind != k {
		p.failf("expected %s token, got %q", tokKindName(k), t.text)
	}
	return p.next()
}

func tokKindName(k tokKind) string {
	switch k {
	case tIdent:
		return "ident"
	case tNum:
		return "number"
	case tStr:
		return "string"
	case tRawStr:
		return "raw string"
	}
	return "token"
}

func (p *parser) atIdent(s string) bool {
	t := p.tok()
	return t.kind == tIdent && t.text == s
}
func (p *parser) atPunct(s string) bool {
	t := p.tok()
	return (t.kind == tPunct || t.kind == tOp) && t.text == s
}
func (p *parser) acceptPunct(s string) bool {
	if p.atPunct(s) {
		p.pos++
		return true
	}
	return false
}

// ── expression parsing (precedence climbing) ────────────────────────

var exprBoundary = map[string]bool{
	",": true, ")": true, "}": true, "]": true, ";": true, ":": true, "{": true,
}

func (p *parser) parseExpr() *expr {
	e := p.parseOr()
	t := p.tok()
	if t.kind == tNL || t.kind == tEOF || exprBoundary[t.text] {
		return e
	}
	p.failf("unexpected token %q after expression", t.text)
	return nil
}

func (p *parser) parseOr() *expr {
	l := p.parseAnd()
	for p.atPunct("||") {
		p.pos++
		p.skipNL() // a trailing `||` at line end continues the chain
		l = &expr{kind: "binop", BOp: "||", BOpKind: "or", lhs: l, rhs: p.parseAnd()}
	}
	return l
}

func (p *parser) parseAnd() *expr {
	l := p.parseCmp()
	for p.atPunct("&&") {
		p.pos++
		p.skipNL() // a trailing `&&` at line end continues the chain
		l = &expr{kind: "binop", BOp: "&&", BOpKind: "and", lhs: l, rhs: p.parseCmp()}
	}
	return l
}

func (p *parser) parseCmp() *expr {
	l := p.parseAdd()
	if t := p.tok(); (t.kind == tOp || t.kind == tPunct) &&
		(t.text == "==" || t.text == "!=" || t.text == "<" || t.text == "<=" || t.text == ">" || t.text == ">=") {
		p.pos++
		l = &expr{kind: "binop", BOp: t.text, BOpKind: "cmp", lhs: l, rhs: p.parseAdd()}
	}
	return l
}

func (p *parser) parseAdd() *expr {
	l := p.parseMul()
	for p.atPunct("+") || p.atPunct("-") {
		op := p.next().text
		l = &expr{kind: "add", op: op, lhs: l, rhs: p.parseMul()}
	}
	return l
}

func (p *parser) parseMul() *expr {
	l := p.parseUnary()
	for p.atPunct("*") || p.atPunct("/") {
		op := p.next().text
		l = &expr{kind: "mul", op: op, lhs: l, rhs: p.parseUnary()}
	}
	return l
}

func (p *parser) parseUnary() *expr {
	if p.atPunct("!") {
		p.pos++
		return &expr{kind: "not", lhs: p.parseUnary()}
	}
	if p.atPunct("-") {
		p.pos++
		return &expr{kind: "neg", lhs: p.parseUnary()}
	}
	if p.atPunct("&") {
		// `&parser{...}` — the address-of is erased (the A1 has no
		// pointers; the composite literal IS the value).
		p.pos++
		return p.parseUnary()
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() *expr {
	e := p.parsePrimary()
	for {
		switch {
		case p.atPunct("{"):
			// composite literal: `token{kind: tNL, line: line}` (named) or
			// `tok{"a", "b"}` (positional) — a struct VALUE. Lowered to
			// the jsonNew helper (the go-sh self-hosting contract: structs
			// are JSON object strings). Only fires for a KNOWN struct type
			// (a `3 {` for-body brace must not parse as a literal).
			if _, ok := p.structFields[callName(e)]; !ok {
				return e
			}
			p.pos++
			p.skipNL()
			var fields []*expr
			var names []string
			for {
				p.skipNL()
				if p.atPunct("}") {
					p.pos++
					break
				}
				if p.tok().kind == tIdent && p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == ":" {
					names = append(names, p.next().text)
					p.pos++ // :
					p.skipNL()
					fields = append(fields, p.parseExpr())
				} else {
					fields = append(fields, p.parseExpr())
				}
				if !p.acceptPunct(",") {
					p.skipNL()
					p.expect(tPunct, "}")
					break
				}
			}
			e = &expr{kind: "structlit", name: callName(e), args: fields, fieldNames: names}
		case p.atPunct("("):
			p.pos++
			args := p.parseArgs()
			e = &expr{kind: "call", callee: callName(e), args: args}
		case p.atPunct("["):
			// generic instantiation call `Name[TypeList](args)` (grammar rule
			// typeArgs): the type arguments ride on the Call (A1 erasure
			// contract, core request go-sh-typeargs — validated string array,
			// dropped at ingress). Only DEFINED functions are instantiable in
			// the subset, so a non-function base falls through to index/slice.
			if p.isTypeArgsBracket(callName(e)) {
				tas := p.parseTypeArgs()
				p.skipNL()
				p.expect(tPunct, "(")
				args := p.parseArgs()
				e = &expr{kind: "call", callee: callName(e), args: args, typeArgs: tas}
				break
			}
			p.pos++
			p.skipNL()
			lo, hi := "", ""
			var loE, hiE *expr
			if !p.atPunct(":") {
				if p.tok().kind == tNum {
					lo = p.next().text
				} else {
					loE = p.parseExpr()
				}
			}
			if p.atPunct(":") {
				p.pos++
				p.skipNL()
				if !p.atPunct("]") {
					if p.tok().kind == tNum {
						hi = p.next().text
					} else {
						hiE = p.parseExpr()
					}
				}
				p.expect(tPunct, "]")
				e = &expr{kind: "slice", target: e, idx1: lo, idx2: hi, idx1e: loE, idx2e: hiE}
			} else {
				p.expect(tPunct, "]")
				e = &expr{kind: "index", target: e, idx1: lo, idx1e: loE}
			}
		case p.atPunct("."):
			// a variadic spread `args...` — the `...` is erased (the A1
			// subs bind the remaining args positionally); the base is the
			// value.
			if p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." &&
				p.toks[p.pos+2].kind == tPunct && p.toks[p.pos+2].text == "." {
				p.pos += 3
				return e
			}
			p.pos++
			nm := p.expect(tIdent, "").text
			if cn := callName(e); cn != "" {
				e = &expr{kind: "member", name: cn + "." + nm}
			} else {
				// member on a non-var base (`p.tok().line` — the method
				// call's result field): keep the base for the jsonGet
				// lowering.
				e = &expr{kind: "member", name: "." + nm, memberTarget: e}
			}
		case p.atPunct("++"):
			p.pos++
			e = &expr{kind: "incr", target: e}
		default:
			return e
		}
	}
}

func callName(e *expr) string {
	if e.kind == "member" || e.kind == "var" {
		return e.name
	}
	return ""
}

// isTypeArgsBracket: the token at pos is `[` and the bracketed contents
// are a Go typeList (`IDENT (. IDENT)* (, ...)*`) immediately followed
// by `(` — the generic-instantiation call shape `Name[TypeList](args)`
// (grammar rule typeArgs) — AND the base name is a defined function
// (generic decls lower to subs; only defined generic functions are
// instantiable in the subset — an array/map index followed by a call
// stays a plain index and refuses as unsupported). Everything else
// falls through to the index/slice parser.
func (p *parser) isTypeArgsBracket(base string) bool {
	if !p.fnNames[base] {
		return false
	}
	if p.tok().kind != tPunct || p.tok().text != "[" {
		return false
	}
	i := p.pos + 1
	wantItem := true
	for {
		if i >= len(p.toks) {
			return false
		}
		t := p.toks[i]
		if t.kind == tEOF {
			return false
		}
		if wantItem {
			if t.kind != tIdent {
				return false
			}
			wantItem = false
			i++
			continue
		}
		switch t.text {
		case ".":
			i++ // qualified type name (pkg.T)
		case ",":
			i++
			wantItem = true
		case "]":
			i++
			return i < len(p.toks) && p.toks[i].kind == tPunct && p.toks[i].text == "("
		default:
			return false
		}
	}
}

// parseTypeArgs: `[TypeList]` — collects the type-argument strings
// (IDENT, possibly qualified pkg.T, comma-separated) and consumes
// through the closing `]`. The A1 erasure contract (core request
// go-sh-typeargs) validates them (string array) on the Call and drops
// them at ingress — the runtime behavior is the plain call.
func (p *parser) parseTypeArgs() []string {
	p.expect(tPunct, "[")
	var tas []string
	p.skipNL()
	item := ""
	for {
		item += p.expect(tIdent, "").text
		if p.acceptPunct(".") {
			item += "."
			continue
		}
		tas = append(tas, item)
		item = ""
		if p.acceptPunct(",") {
			p.skipNL()
			continue
		}
		break
	}
	p.skipNL()
	p.expect(tPunct, "]")
	return tas
}

func (p *parser) parseArgs() []*expr {
	var args []*expr
	p.skipNL()
	for !p.atPunct(")") {
		args = append(args, p.parseExpr())
		// a variadic spread `args...` — the `...` is erased (the A1
		// subs bind the remaining args positionally).
		if p.tok().kind == tPunct && p.tok().text == "." &&
			p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." &&
			p.toks[p.pos+2].kind == tPunct && p.toks[p.pos+2].text == "." {
			p.pos += 3
			p.skipNL()
		}
		if p.acceptPunct(",") {
			p.skipNL()
			continue
		}
		break
	}
	p.expect(tPunct, ")")
	return args
}

func (p *parser) parsePrimary() *expr {
	t := p.tok()
	switch t.kind {
	case tNum:
		p.pos++
		return &expr{kind: "num", text: t.text}
	case tStr:
		p.pos++
		return &expr{kind: "str", text: decodeGoStr(t.raw), raw: t.raw}
	case tChar:
		// a Go rune literal — an INTEGER (its ASCII code) in Go. The
		// expr keeps the decoded char; byte contexts lower it to the
		// code, string contexts to the char itself.
		p.pos++
		return &expr{kind: "char", text: decodeGoStr(t.raw), raw: t.raw}
	case tRawStr:
		p.pos++
		return &expr{kind: "rawstr", text: t.text}
	case tIdent:
		switch t.text {
		case "len":
			p.pos++
			p.expect(tPunct, "(")
			p.skipNL()
			arg := p.parseExpr()
			p.expect(tPunct, ")")
			if arg.kind == "var" && p.varTypes[arg.name] == "Array" {
				return &expr{kind: "arrlen", target: arg}
			}
			return &expr{kind: "strlen", target: arg}
		case "string":
			p.pos++
			p.expect(tPunct, "(")
			p.skipNL()
			arg := p.parseExpr()
			p.expect(tPunct, ")")
			return arg
		case "func":
			return p.parseFuncLit()
		case "map":
			// map[K]V{...} — a map literal in an EXPRESSION position (the
			// golib's A1 builders return map[string]any{...}). Lowered to
			// the jsonNew helper (the JSON object string) with the literal
			// keys as field names.
			p.pos++
			p.expect(tPunct, "[")
			for p.tok().kind == tIdent {
				p.next()
			}
			p.expect(tPunct, "]")
			for p.tok().kind == tIdent {
				p.next()
			}
			p.skipNL()
			p.expect(tPunct, "{")
			var fields []*expr
			var names []string
			for {
				p.skipNL()
				if p.atPunct("}") {
					p.pos++
					break
				}
				key := p.parseExpr()
				if key.kind != "str" && key.kind != "num" && key.kind != "rawstr" {
					p.failf("map keys must be literals (v2)")
				}
				names = append(names, key.text)
				p.expect(tPunct, ":")
				p.skipNL()
				fields = append(fields, p.parseExpr())
				if !p.acceptPunct(",") {
					p.skipNL()
					p.expect(tPunct, "}")
					break
				}
			}
			return &expr{kind: "structlit", name: "", args: fields, fieldNames: names}
		case "true", "false", "nil":
			p.pos++
			return &expr{kind: "var", name: t.text}
		}
		p.pos++
		return &expr{kind: "var", name: t.text}
	case tPunct:
		if t.text == "(" {
			p.pos++
			p.skipNL()
			e := p.parseExpr()
			p.skipNL()
			p.expect(tPunct, ")")
			return e
		}
		// []byte(x) conversion (grammar conversion): identity — the A1
		// is strings-only, so the byte-slice conversion is a no-op
		// (mirrors the `string(x)` arm above).
		if t.text == "[" && p.byteConvAhead() {
			p.pos += 4 // [ ] byte (
			p.skipNL()
			e := p.parseExpr()
			p.skipNL()
			p.expect(tPunct, ")")
			return e
		}
		// []T{...} — an array literal in an EXPRESSION position (the
		// golib's A1 builders' `[]any{...}`). Lowered to the jsonArrNew
		// helper (the JSON array string of the element fragments).
		if t.text == "[" && p.arrayLitAhead() {
			elems, _ := p.parseArrayLiteral()
			var args []any
			for _, el := range elems {
				args = append(args, el)
			}
			return &expr{kind: "arrlit", args: nil, fieldNames: nil, rawJSON: map[string]any{
				"type": "Call", "func": "jsonArrNew",
				"args":   args,
				"purity": "PureCpu",
			}}
		}
	}
	p.failf("unexpected token %q in expression", t.text)
	return nil
}

// ── func literals ───────────────────────────────────────────────────

// parseFuncLit parses `func(params) [ret] { body }` with the function
// scope active (params -> $1.., fresh vars -> local) and returns an expr
// carrying the lowered body.
func (p *parser) parseFuncLit() *expr {
	p.expect(tIdent, "func")
	p.expect(tPunct, "(")
	params := p.parseFuncParams()
	p.skipNL()
	// optional return type: ident | (a, b) | []T | *T
	for p.tok().kind == tIdent && p.tok().text != "{" {
		p.pos++
		p.skipNL()
	}
	for p.atPunct("(") || p.atPunct("[") || p.atPunct("*") {
		p.pos++
		p.skipNL()
		for p.tok().kind == tIdent {
			p.pos++
		}
		p.skipNL()
	}
	p.skipNL()
	// function scope
	saveParams, saveOrd, saveLocals, saveIn := p.fnParams, p.fnParamOrd, p.fnLocals, p.inFunc
	p.fnParams = map[string]bool{}
	p.fnParamOrd = nil
	p.fnLocals = map[string]bool{}
	p.inFunc = true
	for _, prm := range params {
		p.fnParams[prm] = true
		p.fnParamOrd = append(p.fnParamOrd, prm)
	}
	body := p.parseBlockStmts()
	p.fnParams, p.fnParamOrd, p.fnLocals, p.inFunc = saveParams, saveOrd, saveLocals, saveIn
	return &expr{kind: "func", params: params, body: body}
}

// skipType consumes a Go type expression as TOKENS (the type-position
// erasure contract — types carry no runtime shape): plain idents,
// pkg.Ident, []T / [N]T, *T, map[K]V, func(...) T, and (a, b) lists.
// The cpp frontend's signatures (`func lex(src string) ([]tok, error)`,
// `func translate(toks []tok) ([]tok, error)`) need the bracketed and
// parenthesized forms; the old sweeps only handled `[` followed
// directly by an ident, so `[]tok` broke at the empty bracket pair.
func (p *parser) skipType() {
	p.skipNL()
	for {
		t := p.tok()
		switch {
		case t.kind == tIdent:
			p.pos++
			p.skipNL()
			if p.atPunct(".") {
				p.pos++
				p.skipNL()
				p.expect(tIdent, "")
				p.skipNL()
			}
		case t.text == "[":
			p.pos++
			p.skipNL()
			for !p.atPunct("]") {
				if p.tok().kind == tEOF {
					p.failf("unterminated type (v2)")
				}
				p.pos++
			}
			p.pos++ // ]
			p.skipNL()
		case t.text == "*":
			p.pos++
			p.skipNL()
		case t.text == "(":
			// (a, b) return list or func(...) params — consume to the
			// matching close (nested parens counted)
			depth := 0
			for {
				if p.tok().kind == tEOF {
					p.failf("unterminated type (v2)")
				}
				if p.atPunct("(") {
					depth++
				}
				if p.atPunct(")") {
					depth--
					if depth == 0 {
						p.pos++
						break
					}
				}
				p.pos++
			}
			p.skipNL()
		default:
			return
		}
	}
}

// structTypeOf — the struct TYPE name of a var (a structlit-assigned
// var's type is the literal's type name; a method receiver's type is
// recorded at the decl).
func (p *parser) structTypeOf(name string) string {
	if t, ok := p.varStructTypes[name]; ok {
		return t
	}
	return p.receiverTypes[name]
}

func (p *parser) parseFuncParams() []string {
	var params []string
	p.skipNL()
	for !p.atPunct(")") {
		if p.atPunct("(") {
			p.pos++
			params = append(params, p.parseFuncParams()...)
			continue
		}
		nm := p.expect(tIdent, "").text
		// skip the type (erased — the A1 subs bind by position); a
		// `byte` param is a Go BYTE (an integer) — track it so char
		// literals in its comparisons lower to ASCII codes. A variadic
		// `args ...string` skips the `...` marker (the A1 subs bind the
		// remaining args positionally) and registers the param as an
		// ARRAY (the golib's `for i, s := range args`).
		isByte := p.tok().kind == tIdent && p.tok().text == "byte"
		isVariadic := false
		isSlice := p.tok().kind == tPunct && p.tok().text == "["
		if p.tok().kind == tPunct && p.tok().text == "." &&
			p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." &&
			p.toks[p.pos+2].kind == tPunct && p.toks[p.pos+2].text == "." {
			p.pos += 3 // ...
			p.skipNL()
			isVariadic = true
		}
		p.skipType()
		if isByte {
			p.varTypes[nm] = "Byte"
		}
		if isVariadic || isSlice {
			p.varTypes[nm] = "Array"
		}
		params = append(params, nm)
		if p.acceptPunct(",") {
			p.skipNL()
		}
	}
	p.expect(tPunct, ")")
	return params
}

// paramNumber maps a param name to its $N position.
func (p *parser) paramNumber(name string) (int, bool) {
	if !p.fnParams[name] {
		return 0, false
	}
	for i, prm := range p.fnParamOrd {
		if prm == name {
			return i + 1, true
		}
	}
	return 0, false
}

// ── statement parsing ───────────────────────────────────────────────

func (p *parser) parseTopLevel() []map[string]any {
	// PRE-SCAN: register every top-level function NAME before parsing
	// bodies, so a call to a later-defined function (the golib's
	// `tokKindName(k)` used in expect, defined after it) lowers as a
	// user-function call.
	for i, t := range p.toks {
		if t.kind == tIdent && t.text == "func" && i+1 < len(p.toks) {
			j := i + 1
			if j < len(p.toks) && p.toks[j].kind == tPunct && p.toks[j].text == "(" {
				// a method decl — the receiver parens; skip to the name
				for ; j < len(p.toks) && !(p.toks[j].kind == tPunct && p.toks[j].text == ")"); j++ {
				}
				j++
			}
			if j < len(p.toks) && p.toks[j].kind == tIdent {
				p.fnNames[p.toks[j].text] = true
			}
		}
	}
	var out []map[string]any
	for {
		p.skipNL()
		t := p.tok()
		if t.kind == tEOF {
			return out
		}
		switch {
		case p.atPunct("{") || p.atPunct("}"):
			p.pos++ // func main's braces / bare blocks
		case p.atIdent("package"):
			p.pos++
			p.skipToLineEnd()
		case p.atIdent("import"):
			// import "fmt" / import ( "fmt" \n "strings" ) — skip the
			// whole clause, parenthesized block included (t71).
			p.pos++
			p.skipNL()
			if p.atPunct("(") {
				depth := 0
				for {
					t := p.next()
					if t.kind == tEOF {
						break
					}
					if t.kind == tPunct && t.text == "(" {
						depth++
					}
					if t.kind == tPunct && t.text == ")" {
						depth--
						if depth == 0 {
							break
						}
					}
				}
			} else {
				p.skipToLineEnd()
			}
		case p.atIdent("func"):
			// top-level func decl `func name[typeParams](params) [ret] { body }`
			// — lowers to the same Function sub as the t22/t23 func
			// literals. Generic decls (grammar typeParameters, call-site
			// typeArgs — core request go-sh-typeargs): type parameters are
			// erased under the A1 erasure contract, which only lowers
			// type-INDEPENDENT bodies (erasure is faithful there). `main`
			// stays the entry: its body parses as top-level statements
			// below. Method decls `func (r T) m(...)` lower the receiver as
			// param 1 (the go-sh self-hosting contract: a method call
			// `p.m(...)` passes the receiver BY NAME, and the body's field
			// accesses resolve through the param).
			p.pos++
			p.skipNL()
			receiver := ""
			if p.atPunct("(") {
				// the receiver `(p *parser)` — the name (the type is erased)
				p.pos++
				p.skipNL()
				receiver = p.expect(tIdent, "").text
				p.varTypes[receiver] = "Struct" // the receiver is a struct
				// the receiver's TYPE name (`(p *parser)` → "parser") —
				// the field-type lookup key.
				if p.tok().kind == tPunct && p.tok().text == "*" {
					p.pos++
				}
				if p.tok().kind == tIdent {
					p.receiverTypes[receiver] = p.next().text
				}
				p.skipType()
				p.expect(tPunct, ")")
				p.skipNL()
			}
			nm := p.expect(tIdent, "").text
			if nm == "main" {
				p.skipToLineEnd()
				break
			}
			// type parameters [T any] — erased (no runtime form)
			if p.atPunct("[") {
				p.pos++ // [
				p.skipNL()
				for !p.atPunct("]") {
					if p.tok().kind == tEOF {
						p.failf("unterminated type parameter list (v2)")
					}
					p.pos++
				}
				p.expect(tPunct, "]")
				p.skipNL()
			}
			p.expect(tPunct, "(")
			params := p.parseFuncParams()
			p.skipNL()
			// optional return type: ident | (a, b) | []T | *T — erased
			// (skipType handles the bracketed/parenthesized forms the
			// cpp frontend's signatures use: `([]tok, error)` etc.)
			if !p.atPunct("{") {
				p.skipType()
			}
			p.skipNL()
			// function scope (mirrors parseFuncLit); a method's receiver
			// is param 1 (the go-sh self-hosting contract — the body's
			// field accesses resolve through the param to the receiver's
			// name).
			saveParams, saveOrd, saveLocals, saveIn := p.fnParams, p.fnParamOrd, p.fnLocals, p.inFunc
			p.fnParams = map[string]bool{}
			p.fnParamOrd = nil
			p.fnLocals = map[string]bool{}
			p.inFunc = true
			if receiver != "" {
				p.fnParams[receiver] = true
				p.fnParamOrd = append(p.fnParamOrd, receiver)
			}
			for _, prm := range params {
				p.fnParams[prm] = true
				p.fnParamOrd = append(p.fnParamOrd, prm)
			}
			body := p.parseBlockStmts()
			p.fnParams, p.fnParamOrd, p.fnLocals, p.inFunc = saveParams, saveOrd, saveLocals, saveIn
			p.fnNames[nm] = true
			if receiver != "" {
				params = append([]string{receiver}, params...)
			}
			out = append(out, map[string]any{"type": "Function", "name": nm, "params": params, "body": body})
		case p.atIdent("type"):
			// `type Name interface{}` — an EMPTY interface type decl:
			// compile-time only, erased under the type-position erasure
			// contract (t80/t82/t84/t85 — shell has no interface values;
			// the app's `type Expr interface{}` AST-node declarations).
			// A non-empty interface body (method dispatch) and composite
			// underlying types (structs — the core-requests
			// go-sh-dogfood-20260815 §3 boundary; map/[]/*/func/chan) stay
			// refused loudly.
			p.pos++
			p.skipNL()
			typeName := p.expect(tIdent, "").text // Name
			p.skipNL()
			if p.atIdent("interface") {
				p.pos++
				p.skipNL()
				p.expect(tPunct, "{")
				p.skipNL()
				if p.atPunct("}") {
					p.pos++
					break
				}
				p.failf("interface methods unsupported (v2) — method dispatch is a contract boundary")
			}
			// `type Name struct{...}` — a struct TYPE decl: compile-time
			// only, erased under the type-position erasure contract (the
			// golib's `type token struct{ kind, text string }`). The field
			// NAMES are tracked so positional composite literals
			// (`token{"a", "b"}`) lower by position; the struct VALUE
			// (composite literals, member access) lowers to the JSON
			// object-string helpers (jsonNew/jsonGet/jsonSet — the go-sh
			// self-hosting contract).
			if p.atIdent("struct") {
				p.pos++
				p.skipNL()
				p.expect(tPunct, "{")
				p.skipNL()
				var fields []string
				ft := map[string]string{}
				for !p.atPunct("}") {
					p.skipNL() // a field's line end — advance to the next field
					if p.atPunct("}") {
						break
					}
					if p.tok().kind == tEOF {
						p.failf("unterminated struct type (v2)")
					}
					// field decl: `name type` / `name, name type` — the
					// names and the ELEMENT type (a `[]token` field's
					// element type drives the index-read target type).
					if p.tok().kind == tIdent {
						var names []string
						names = append(names, p.next().text)
						for p.acceptPunct(",") {
							p.skipNL()
							names = append(names, p.expect(tIdent, "").text)
						}
						// the type: `[]T` → the element type T; `map[K]V` →
						// "map"; a plain ident → the type name. The REST of the
						// type is consumed to the LINE's end — struct field types
						// don't span lines, and skipType's skipNL would eat the
						// next field (`kind tokKind` + newline + `text string`).
						elt := ""
						switch {
						case p.atPunct("["):
							p.pos++
							p.expect(tPunct, "]")
							p.skipNL()
							if p.atPunct("*") {
								p.pos++
							}
							if p.tok().kind == tIdent {
								elt = p.next().text
							}
						case p.atIdent("map"):
							p.pos++
							elt = "map"
						case p.atPunct("*"):
							p.pos++
							if p.tok().kind == tIdent {
								elt = p.next().text
							}
						case p.tok().kind == tIdent:
							elt = p.next().text
						}
						// consume the rest of the type to the line end / `;` / `}`
						for p.tok().kind != tNL && p.tok().kind != tEOF &&
							!(p.tok().kind == tPunct && (p.tok().text == ";" || p.tok().text == "}")) {
							p.pos++
						}
						for _, n := range names {
							fields = append(fields, n)
							ft[n] = elt
						}
					}
					// a `;` field separator (NOT an unconditional advance — the
					// last field's `}` must stop the loop)
					if p.atPunct(";") {
						p.pos++
					}
				}
				p.pos++ // }
				p.structFields[typeName] = fields
				p.structFieldTypes[typeName] = ft
				break
			}
			// `type Name int` (string/bool/float64/…, or another named
			// type) — a named SCALAR type: compile-time only, erased under
			// the same type-position erasure contract as the empty
			// interface above. The A1 is dynamically typed, so a tokKind
			// VALUE is a plain int — no shape needed (go-sh.go's own
			// `type tokKind int`, fish-sh-go's tokKind, perl-sh-go's
			// `type tokKind string`). Type positions (params, struct
			// fields, var decls) already erase; a VALUE-position use
			// (conversion `tokKind(x)`) stays refused loudly by the
			// word-position call gate — no silent mis-lower.
			if p.tok().kind == tIdent && !p.atIdent("struct") &&
				!p.atIdent("func") && !p.atIdent("map") && !p.atIdent("chan") {
				p.pos++
				break
			}
			p.failf("type decls are outside the subset (v2) — struct/interface types have no A1 shape")
		case p.atIdent("const"):
			// `const x = expr` / `const ( specs )` — Go compile-time
			// constants (FRONTEND-GAP: the old path fell through to
			// parseStmt and died on `tEOF tokKind = iota` → "unexpected
			// token tokKind after expression"). Each name lowers to an
			// Assign of its evaluated value — see parseConstDecl.
			out = append(out, p.parseConstDecl()...)
		default:
			out = append(out, p.parseStmt()...)
		}
	}
}

func (p *parser) skipToLineEnd() {
	for p.tok().kind != tNL && p.tok().kind != tEOF {
		p.pos++
	}
}

func (p *parser) parseStmt() []map[string]any {
	p.skipNL()
	t := p.tok()
	if t.kind != tIdent {
		p.failf("unrecognized statement starting with %q", t.text)
	}
	switch t.text {
	case "if":
		return p.parseIf()
	case "for":
		return p.parseFor()
	case "switch":
		return p.parseSwitch()
	case "return":
		p.pos++
		// multi-value return: `return a, b` — the caller's multi-return
		// lowering captures the FIRST value (the error return is
		// dropped), so echo the first and skip the rest (the golib's
		// `return nil, fmt.Errorf(...)` error paths).
		first := p.parseExpr()
		for p.acceptPunct(",") {
			p.skipNL()
			p.parseExpr()
		}
		return []map[string]any{p.returnToStmt(first)}
	case "continue":
		// A1 Continue node (mirrors Command::Continue(None) in the core):
		// a bare `continue` inside a loop body. Labeled `continue L` is
		// refused (the A1 node has no label/level field).
		p.pos++
		if p.tok().kind == tIdent {
			p.failf("labeled continue unsupported (v2)")
		}
		return []map[string]any{{"type": "Continue"}}
	case "break":
		// A1 Break node (shir_json_in "Break" -> IrStmt::Break — the
		// core contract already has it; the estree/C renderers emit
		// `break;`). Labeled `break L` is refused, like continue.
		p.pos++
		if p.tok().kind == tIdent {
			p.failf("labeled break unsupported (v2)")
		}
		return []map[string]any{{"type": "Break"}}
	case "go":
		return p.parseGo()
	case "var":
		return p.parseVarDecl()
	case "const":
		return p.parseConstDecl()
	case "case", "default":
		p.failf("'%s' outside switch", t.text)
	}
	if p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." {
		return p.parseDottedStmt()
	}
	return p.parseAssignStmt()
}

// parseConstDecl: `const x = expr` / `const ( specs )` — Go compile-time
// constants. The A1 contract has no const/iota node, so each name lowers
// to an Assign of its EVALUATED value — the same shape as `var x =
// literal` (the A1 is dynamically typed; a tokKind value is a plain
// scalar). iota counts specs inside the ConstDecl starting at 0; a spec
// without an expression repeats the previous spec's expression with the
// current iota substituted (Go ConstSpec semantics). Supported RHS:
// iota, integer literals (with unary - and + - * / folding), and string
// literals — anything else refuses loudly (Refuse > guess).
func (p *parser) parseConstDecl() []map[string]any {
	p.expect(tIdent, "const")
	var out []map[string]any
	if p.atPunct("(") {
		p.pos++
		var prev *expr
		for iotaIdx := 0; ; iotaIdx++ {
			p.skipNL()
			if p.atPunct(")") {
				p.pos++
				return out
			}
			if p.tok().kind == tEOF {
				p.failf("unterminated const block")
			}
			prev = p.parseConstSpec(iotaIdx, prev, &out)
		}
	}
	// single spec: `const x = expr` (iota is 0; nothing to repeat)
	p.parseConstSpec(0, nil, &out)
	return out
}

// parseConstSpec: one `Name [Type] [= expr]` spec. The optional type
// position is erased (like every type position); the expression is
// evaluated via constWord and emitted as an Assign. Returns the spec's
// expression so a following `Name` (no `=`) can repeat it with iota.
func (p *parser) parseConstSpec(iotaIdx int, prev *expr, out *[]map[string]any) *expr {
	name := p.expect(tIdent, "").text
	if p.atPunct(",") {
		p.failf("const spec with multiple names (v2)")
	}
	// optional type: pkg.Ident | plain ident | []T | *T — the same sweep
	// as parseVarDecl (the erasure contract: a tokKind VALUE is untyped
	// in the A1, so the type position is dropped).
	for {
		t := p.tok()
		if t.kind == tIdent && p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." {
			p.next() // pkg
			p.next() // .
			p.expect(tIdent, "")
			continue
		}
		if t.kind == tIdent && (p.toks[p.pos+1].kind != tPunct || p.toks[p.pos+1].text == "=") {
			p.pos++ // plain type ident
			continue
		}
		if t.kind == tPunct && (t.text == "[" || t.text == "*") {
			p.pos++
			p.skipNL()
			for p.tok().kind == tIdent {
				p.next()
			}
			p.skipNL()
			continue
		}
		break
	}
	p.skipNL()
	if p.atPunct("=") {
		p.pos++
		p.skipNL()
		prev = p.parseExpr()
	} else if prev == nil {
		p.failf("const %q has no expression and nothing to repeat (v2)", name)
	}
	w, typ := p.constWord(prev, iotaIdx)
	p.registerVar(name, typ)
	// record the evaluated value so a later spec can reference the const
	// (`e = c + 1` — Go resolves consts in declaration order).
	if v := p.constValue(prev, iotaIdx); v.ok {
		if v.isStr {
			p.constStrs[name] = v.s
		} else {
			p.consts[name] = v.n
		}
	}
	*out = append(*out, assignStmt(name, w))
	return prev
}

// constWord: compile-time evaluation of a const RHS (Go consts are
// compile-time). `iota` → the spec's index; integer literals (with unary
// - and + - * / folding); string literals; and references to earlier
// consts in the same program. Anything else refuses loudly — an
// un-evaluated const would silently mis-lower (Refuse > guess).
func (p *parser) constWord(e *expr, iotaVal int) (map[string]any, string) {
	v := p.constValue(e, iotaVal)
	if !v.ok {
		p.failf("unsupported const expression (v2): %s", p.constExprDesc(e))
	}
	if v.isStr {
		return interpLit(v.s), "Str"
	}
	return strExpr(strconv.Itoa(v.n)), "Int"
}

// constVal: an evaluated const value — an int (n) or a string (s).
// ok=false means the expression is outside the const subset.
type constVal struct {
	n     int
	s     string
	isStr bool
	ok    bool
}

// constValue: fold a const RHS to its value.
func (p *parser) constValue(e *expr, iotaVal int) constVal {
	switch e.kind {
	case "var":
		if e.name == "iota" {
			return constVal{n: iotaVal, ok: true}
		}
		if n, ok := p.consts[e.name]; ok {
			return constVal{n: n, ok: true}
		}
		if s, ok := p.constStrs[e.name]; ok {
			return constVal{s: s, isStr: true, ok: true}
		}
	case "num":
		if n, err := strconv.ParseInt(e.text, 0, 64); err == nil {
			return constVal{n: int(n), ok: true}
		}
	case "str":
		return constVal{s: e.text, isStr: true, ok: true}
	case "rawstr":
		return constVal{s: e.text, isStr: true, ok: true}
	case "neg":
		if l := p.constValue(e.lhs, iotaVal); l.ok && !l.isStr {
			return constVal{n: -l.n, ok: true}
		}
	case "add", "mul":
		l := p.constValue(e.lhs, iotaVal)
		r := p.constValue(e.rhs, iotaVal)
		if !l.ok || !r.ok || l.isStr || r.isStr {
			return constVal{}
		}
		switch e.op {
		case "+":
			return constVal{n: l.n + r.n, ok: true}
		case "-":
			return constVal{n: l.n - r.n, ok: true}
		case "*":
			return constVal{n: l.n * r.n, ok: true}
		case "/":
			if r.n != 0 {
				return constVal{n: l.n / r.n, ok: true}
			}
		}
	}
	return constVal{}
}

// constExprDesc: a human-readable description of an expression, for
// refusal messages.
func (p *parser) constExprDesc(e *expr) string {
	if e == nil {
		return "<nil>"
	}
	switch e.kind {
	case "var":
		return e.name
	case "num", "str", "rawstr":
		return e.text
	case "neg":
		return "-" + p.constExprDesc(e.lhs)
	case "add", "mul":
		return p.constExprDesc(e.lhs) + " " + e.op + " " + p.constExprDesc(e.rhs)
	}
	return e.kind
}

// parseVarDecl: `var a, b [type] [= init]` — bare declarations register
// the names (""), bytes.Buffer registers a buffer accumulator (t56).
func (p *parser) parseVarDecl() []map[string]any {
	p.expect(tIdent, "var")
	var names []string
	for {
		p.skipNL()
		names = append(names, p.expect(tIdent, "").text)
		if !p.acceptPunct(",") {
			break
		}
	}
	// optional type: pkg.Ident (bytes.Buffer) | plain ident | []T | *T
	p.skipNL()
	isBuf := false
	if len(names) > 0 && names[0] == "f" {
	}
	for {
		t := p.tok()
		if t.kind == tIdent && p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "." {
			pkg := p.next().text
			p.next() // .
			typ := p.expect(tIdent, "").text
			if (pkg == "bytes" && typ == "Buffer") || (pkg == "strings" && typ == "Builder") {
				isBuf = true
			}
			continue
		}
		// plain type ident — `var x int = 5` / `var x any = "hi"`: a type
		// name may be followed by `=` (the initializer) or end the line.
		if t.kind == tIdent && (p.toks[p.pos+1].kind != tPunct || p.toks[p.pos+1].text == "=") {
			p.pos++ // plain type ident
			continue
		}
		// `interface{}` — the empty interface type (the erasure contract
		// t80/t82/t84/t85: shell has no interface values; the type is
		// erased and the value is only ever nil-checked or stored as its
		// underlying value). A non-empty body (methods) is method
		// dispatch — refused.
		if t.kind == tIdent && t.text == "interface" {
			p.pos++
			p.skipNL()
			p.expect(tPunct, "{")
			p.skipNL()
			if !p.atPunct("}") {
				p.failf("interface methods unsupported (v2) — method dispatch is a contract boundary")
			}
			p.pos++ // }
			p.skipNL()
			continue
		}
		if t.kind == tIdent && t.text == "func" {
			// func(...) Ret — a function type (t82/t84/t85: `var f
			// func(int) int = nil`); erased (skipType handles the
			// bracket/paren forms). The func params are consumed to the
			// matching paren; the return type to the line end / `=`.
			p.pos++
			if p.atPunct("(") {
				depth := 0
				for {
					if p.tok().kind == tEOF {
						p.failf("unterminated func type (v2)")
					}
					if p.atPunct("(") {
						depth++
					}
					if p.atPunct(")") {
						depth--
						if depth == 0 {
							p.pos++
							break
						}
					}
					p.pos++
				}
			}
			for p.tok().kind != tNL && p.tok().kind != tEOF &&
				!(p.tok().kind == tPunct && p.tok().text == "=") {
				p.pos++
			}
			continue
		}
		if t.kind == tPunct && (t.text == "[" || t.text == "*") {
			p.pos++
			p.skipNL()
			if t.text == "[" {
				// []T / [N]T — consume through the closing bracket, then
				// the element type (the cpp frontend's `var out []tok`;
				// the old sweep only handled `[` followed directly by an
				// ident, so `[]tok` broke at the empty bracket pair).
				for !p.atPunct("]") {
					if p.tok().kind == tEOF {
						p.failf("unterminated array type (v2)")
					}
					p.pos++
				}
				p.pos++ // ]
				p.skipNL()
			}
			for p.tok().kind == tIdent {
				p.next()
			}
			// NO skipNL here: the newline terminates the type — skipping
			// it would let the sweep's `continue` consume the NEXT line's
			// identifier as a plain type (`var toks []string` followed by
			// `line := 1` ate `line`).
			continue
		}
		break
	}
	var out []map[string]any
	for _, n := range names {
		p.registerVar(n, "Str")
		if isBuf {
			p.bufs[n] = ""
		}
		out = append(out, assignStmt(n, strExpr("")))
	}
	// optional initializer: var b = expr
	p.skipNL()
	if p.atPunct("=") {
		p.pos++
		p.skipNL()
		// var m = map[K]V{...} — the map literal (the cpp frontend's
		// whitelist tables); parseExpr would choke on `map[string]`
		// (the `string` type word hits the string(x) conversion path).
		if p.atIdent("map") {
			return p.parseMapLiteral(names)
		}
		// var a = []T{...} — the array literal (the golib's
		// `var multiOps = []string{...}`); parseExpr would choke on the
		// `[]` type word.
		if p.atPunct("[") {
			elems, _ := p.parseArrayLiteral()
			p.arrays[names[0]] = arrayInfo{elems: elems, typ: "Str"}
			p.registerVar(names[0], "Array")
			if len(names) > 1 {
				p.failf("array literal with multiple targets (v2)")
			}
			return []map[string]any{assignStmt(names[0],
				map[string]any{
					"type": "Call", "func": "setArray",
					"args":   []any{strExpr(names[0]), map[string]any{"type": "Array", "elements": elems}},
					"purity": "Emulable",
				})}
		}
		rhs := p.parseExpr()
		if len(names) != 1 {
			p.failf("var decl initializer with multiple targets (v2)")
		}
		w := p.exprToWord(rhs)
		p.registerVar(names[0], p.wordType(w))
		return []map[string]any{assignStmt(names[0], w)}
	}
	return out
}

func (p *parser) parseBlockStmts() []map[string]any {
	p.expect(tPunct, "{")
	out := []map[string]any{}
	for {
		p.skipNL()
		if p.atPunct("}") {
			p.pos++
			return out
		}
		if p.atPunct(";") {
			p.pos++ // a `;` statement separator (the golib's one-line bodies)
			continue
		}
		if p.tok().kind == tEOF {
			p.failf("unterminated block")
		}
		out = append(out, p.parseStmt()...)
	}
}

// ── dotted-callee statements ────────────────────────────────────────

func (p *parser) parseDottedStmt() []map[string]any {
	first := p.next().text
	p.expect(tPunct, ".")
	method := p.expect(tIdent, "").text
	// p.pos++ — a member INCREMENT (the golib's 91 `p.pos++`): the
	// jsonSet write-back, NOT a method call.
	if p.atPunct("++") {
		p.pos++
		p.registerVar(first, "Struct")
		return []map[string]any{assignStmt(first, map[string]any{
			"type": "Call", "func": "jsonSet",
			"args": []any{
				strExpr(p.resolveVar(first)), strExpr(method),
				arithWrap(arithBin(arithVar(first), "+", arithNum(1))),
			},
			"purity": "PureCpu",
		})}
	}
	// p.parseExpr() — a METHOD call on a struct (the go-sh self-hosting
	// contract): the receiver is passed BY NAME (the method body's field
	// accesses resolve through the param to the caller's var).
	if p.varTypes[p.resolveVar(first)] == "Struct" {
		p.expect(tPunct, "(")
		args := p.parseArgs()
		var words []map[string]any
		words = append(words, strExpr(p.resolveVar(first)))
		for _, a := range args {
			words = append(words, p.exprToWord(a))
		}
		return []map[string]any{execStmt(method, words, "Spawn")}
	}
	switch first + "." + method {
	case "fmt.Println", "fmt.Print":
		return p.printlnStmt()
	case "fmt.Fprintln":
		return p.fprintlnStmt()
	case "fmt.Printf":
		return p.printfStmt()
	case "os.WriteFile":
		return p.writeFileStmt()
	case "os.Setenv":
		return p.setenvStmt()
	case "os.Exit":
		return p.exitStmt()
	}
	// b.WriteString(...) on a bytes.Buffer → append to the accumulator
	// (the literal-only contract keeps the contents statically known).
	if _, ok := p.bufs[first]; ok {
		switch method {
		case "WriteString", "Write":
			p.expect(tPunct, "(")
			p.skipNL()
			a := p.parseExpr()
			p.skipNL()
			p.expect(tPunct, ")")
			if a.kind != "str" && a.kind != "num" && a.kind != "rawstr" {
				p.failf("bytes.Buffer.%s needs a literal (v2)", method)
			}
			p.bufs[first] += a.text
			return nil
		case "WriteByte":
			// b.WriteByte(c) — append ONE byte (the golib's decodeGoStr
			// escape decoder). The byte is a CODE (Go bytes are
			// integers); the buffer appends the char. A literal char
			// folds at emit time; a runtime byte (from `c := raw[i]`)
			// appends via the sh2.chr helper.
			p.expect(tPunct, "(")
			p.skipNL()
			a := p.parseExpr()
			p.skipNL()
			p.expect(tPunct, ")")
			if a.kind == "char" {
				p.bufs[first] += charCode(a)
				return nil
			}
			if a.kind == "str" || a.kind == "num" {
				p.bufs[first] += a.text
				return nil
			}
			// a runtime byte var — the buffer becomes runtime-dependent:
			// emit `b = <acc> + chr(c)` (the sh2.chr helper) and mark the
			// buffer so .String() reads the runtime value.
			chrCall := map[string]any{
				"type": "Call", "func": "chr",
				"args":   []any{p.exprToWord(a)},
				"purity": "PureCpu",
			}
			if p.runtimeBufs[first] {
				// already runtime — append to the runtime value
				return []map[string]any{assignStmt(first, interpParts([]any{
					partExpr(getVarExpr(first)), partExpr(chrCall),
				}))}
			}
			p.runtimeBufs[first] = true
			p.registerVar(first, "Str")
			var parts []any
			if p.bufs[first] != "" {
				parts = append(parts, partLit(p.bufs[first]))
			}
			parts = append(parts, partExpr(chrCall))
			return []map[string]any{assignStmt(first, interpParts(parts))}
		}
		p.failf("unsupported bytes.Buffer.%s (v2)", method)
	}
	// cmd := exec.Command(...) handles: Start → (…) & , Wait → wait
	// (t44: the deterministic background idiom, mirroring the posix
	// `(echo bg) & wait; echo main` shape node-for-node).
	if args, ok := p.cmds[first]; ok {
		switch method {
		case "Start":
			p.expect(tPunct, "(")
			p.skipNL()
			p.expect(tPunct, ")")
			return []map[string]any{{
				"type": "Background",
				"body": []any{map[string]any{
					"type": "Subshell",
					"body": []any{p.execFromArgs(args)},
				}},
			}}
		case "Wait":
			p.expect(tPunct, "(")
			p.skipNL()
			p.expect(tPunct, ")")
			return []map[string]any{execStmt("wait", []map[string]any{}, "Spawn")}
		case "Stdout":
			// cmd.Stdout = os.Stdout — Go children discard stdout unless
			// wired up; shell commands INHERIT stdout by default, so the
			// assignment is a no-op for the lowering (t44).
			p.skipNL()
			p.expect(tPunct, "=")
			p.skipNL()
			p.expect(tIdent, "os")
			p.expect(tPunct, ".")
			p.expect(tIdent, "Stdout")
			return nil
		}
	}
	// two-level dotted call: os.Stdout.Write(x) — the os.Stdout writer
	// methods (the CLI's raw stdout writes).
	if p.atPunct(".") {
		p.pos++
		m2 := p.expect(tIdent, "").text
		switch first + "." + method + "." + m2 {
		case "os.Stdout.Write":
			return p.stdoutWriteStmt()
		}
		p.failf("unsupported call %s.%s.%s (v2)", first, method, m2)
	}
	p.failf("unsupported call %s.%s (v2)", first, method)
	return nil
}

func (p *parser) printlnStmt() []map[string]any {
	p.expect(tPunct, "(")
	args := p.parseArgs()
	if len(args) == 0 {
		p.failf("Println/Print with no args (v2)")
	}
	// heredoc: Println(`...`) → Redirect(cat <<EOF ...)
	if len(args) == 1 && args[0].kind == "rawstr" {
		return []map[string]any{p.heredocStmt(args[0].text)}
	}
	// function call in Println: fmt.Println(greet(n)) → the call itself
	// (the function's echo writes stdout — the `greet "$n"` shape)
	if len(args) == 1 && args[0].kind == "call" && p.fnNames[args[0].callee] {
		var words []map[string]any
		for _, a := range args[0].args {
			words = append(words, p.exprToWord(a))
		}
		return []map[string]any{execStmtTA(args[0].callee, words, "Spawn", args[0].typeArgs)}
	}
	return []map[string]any{execStmt("echo", p.printlnWords(args), "Emulable")}
}

// printlnWords: Println/Print/Fprintln operand separation — Go separates
// operands with a space, which IS shell word separation — one word per
// operand (t40 fixes the old interpParts concat that printed "$i$j").
func (p *parser) printlnWords(args []*expr) []map[string]any {
	var words []map[string]any
	if len(args) > 1 {
		allStr := true
		for _, a := range args {
			if a.kind != "str" {
				allStr = false
			}
		}
		if allStr {
			// echo a b c — separate Str words (matches the core's shape)
			for _, a := range args {
				words = append(words, strExpr(a.text))
			}
		} else {
			for _, a := range args {
				words = append(words, p.exprToWord(a))
			}
		}
	} else {
		words = []map[string]any{p.exprToWord(args[0])}
	}
	return words
}

// fprintlnStmt: fmt.Fprintln(os.Stderr, args...) → the `echo … >&2` shape
// (the CLI's usage message, cmd/go-sh/main.go:24 — the dogfood frontier).
// Fprintln writes space-separated operands + "\n" to fd 2 — exactly
// `echo "$a" "$b" >&2`. The A1 redirect is the fd-dup form the core
// emits for `>&2` (Redirect{fd:1, mode:"w", target:"&2"}): the runtime
// dups fd 1 onto fd 2, so echo's stdout writes land on stderr. The first
// arg must be os.Stderr — any other writer refuses loudly (Refuse >
// guess); the trailing newline comes from echo itself.
func (p *parser) fprintlnStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	p.expect(tIdent, "os")
	p.expect(tPunct, ".")
	p.expect(tIdent, "Stderr")
	if !p.acceptPunct(",") {
		p.failf("Fprintln needs os.Stderr as the first arg (v2)")
	}
	p.skipNL()
	args := p.parseArgs()
	if len(args) == 0 {
		p.failf("Fprintln with no args (v2)")
	}
	echo := execStmt("echo", p.printlnWords(args), "Emulable")
	return []map[string]any{{
		"type":  "Redirect",
		"inner": []any{echo},
		"redirects": []any{map[string]any{
			"fd":          1,
			"mode":        "w",
			"interpolate": true,
			"target":      strExpr("&2"),
		}},
	}}
}

// printfStmt: one %s + trailing \n → the echo-interpolation shape
// (`echo "hi $NAME"`); otherwise printf with the RAW format text
// (`printf "%s-%s\n" a b`).
func (p *parser) printfStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	fmtTok := p.expect(tStr, "")
	var args []*expr
	if p.acceptPunct(",") {
		p.skipNL()
		args = p.parseArgs()
	}
	decoded := decodeGoStr(fmtTok.raw)
	if len(args) == 1 && strings.Count(decoded, "%s") == 1 && strings.HasSuffix(decoded, "\n") {
		before := strings.TrimSuffix(decoded, "\n")
		before = strings.Replace(before, "%s", "", 1)
		parts := []any{partLit(before), partExpr(p.exprToWord(args[0]))}
		return []map[string]any{execStmt("echo", []map[string]any{interpParts(parts)}, "Emulable")}
	}
	// one %d + trailing \n → echo-interpolation (the `count=${#a[@]}` shape)
	if len(args) == 1 && strings.Count(decoded, "%d") == 1 &&
		strings.Count(decoded, "%s") == 0 && strings.HasSuffix(decoded, "\n") {
		before := strings.TrimSuffix(decoded, "\n")
		before = strings.Replace(before, "%d", "", 1)
		parts := []any{partLit(before), partExpr(p.exprToWord(args[0]))}
		return []map[string]any{execStmt("echo", []map[string]any{interpParts(parts)}, "Emulable")}
	}
	var words []map[string]any
	words = append(words, interpLit(fmtTok.raw))
	for _, a := range args {
		words = append(words, p.exprToWord(a))
	}
	return []map[string]any{execStmt("printf", words, "Emulable")}
}

// writeFileStmt: os.WriteFile(path, []byte("data\n"), perm) → the
// Redirect shape of `echo data > path`.
func (p *parser) writeFileStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	pathTok := p.expect(tStr, "")
	p.expect(tPunct, ",")
	p.skipNL()
	p.expect(tPunct, "[")
	p.expect(tPunct, "]")
	p.expect(tIdent, "byte")
	p.expect(tPunct, "(")
	contentTok := p.expect(tStr, "")
	p.expect(tPunct, ")")
	p.expect(tPunct, ",")
	p.skipNL()
	p.parseExpr() // perm literal (0o644 etc.)
	p.expect(tPunct, ")")
	content := strings.TrimSuffix(decodeGoStr(contentTok.raw), "\n")
	echo := execStmt("echo", []map[string]any{strExpr(content)}, "Emulable")
	return []map[string]any{{
		"type":  "Redirect",
		"inner": []any{echo},
		"redirects": []any{map[string]any{
			"fd":          1,
			"mode":        "w",
			"interpolate": true,
			"target":      strExpr(decodeGoStr(pathTok.raw)),
		}},
	}}
}

// heredocStmt: a multi-line raw-string Println → the heredoc Redirect
// shape (`cat <<EOF ... EOF`).
func (p *parser) heredocStmt(content string) map[string]any {
	cat := execStmt("cat", []map[string]any{}, "Emulable")
	return map[string]any{
		"type":  "Redirect",
		"inner": []any{cat},
		"redirects": []any{map[string]any{
			"fd":          0,
			"mode":        "heredoc",
			"interpolate": true,
			"target":      strExpr(content + "\n"),
		}},
	}
}

// setenvStmt: os.Setenv("K", "v") → the export shape (`X=v` + `export X`).
// stdoutWriteStmt: os.Stdout.Write(x) → printf "%s" "$x" — the raw
// byte write WITHOUT the trailing newline echo adds (the A1 has no
// raw-write node; printf with a %s format is the faithful shape). The
// arg may be a []byte{...} byte-slice literal (the CLI's
// os.Stdout.Write([]byte{'\n'}) — the newline terminator), whose char
// elements decode to the literal bytes.
func (p *parser) stdoutWriteStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	var words []map[string]any
	if p.atPunct("[") && p.byteSliceLitAhead() {
		p.pos += 4 // [ ] byte {
		content := ""
		for {
			p.skipNL()
			if p.atPunct("}") {
				p.pos++
				break
			}
			t := p.tok()
			if t.kind != tStr && t.kind != tChar {
				p.failf("byte-slice elements must be char/string literals (v2)")
			}
			p.pos++
			content += decodeGoStr(t.raw)
			if !p.acceptPunct(",") {
				p.skipNL()
				p.expect(tPunct, "}")
				break
			}
		}
		words = append(words, interpLit(content))
	} else {
		arg := p.parseExpr()
		words = append(words, p.exprToWord(arg))
	}
	p.skipNL()
	p.expect(tPunct, ")")
	return []map[string]any{execStmt("printf", words, "Emulable")}
}

// exitStmt: os.Exit(N) → the A1 Exit statement ({"type":"Exit","value":N})
// — the `exit` builtin's shape, so all backends terminate with bash's
// exit code semantics (the CLI's `os.Exit(2)` on usage errors).
func (p *parser) exitStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	var value map[string]any
	switch p.tok().kind {
	case tNum:
		n, _ := strconv.Atoi(p.next().text)
		// the A1 expr type is "Int" ({"type":"Int","value":N} — the
		// core's IrExpr::Int), NOT the arith "Num" node: the Exit
		// value is a full expr, and "Num" is only a KNOWN_ARITH
		// sub-node (shir_json_in.rs rejects it at the expr boundary).
		value = map[string]any{"type": "Int", "value": n}
	case tIdent:
		name := p.next().text
		if n, ok := p.paramNumber(name); ok {
			value = map[string]any{"type": "Num", "value": n}
		} else {
			value = getVarExpr(name)
		}
	default:
		value = map[string]any{"type": "Num", "value": 0}
	}
	p.skipNL()
	p.expect(tPunct, ")")
	return []map[string]any{{"type": "Exit", "value": value}}
}

func (p *parser) setenvStmt() []map[string]any {
	p.expect(tPunct, "(")
	p.skipNL()
	k := p.expect(tStr, "")
	p.expect(tPunct, ",")
	p.skipNL()
	v := p.expect(tStr, "")
	p.expect(tPunct, ")")
	name := decodeGoStr(k.raw)
	p.registerVar(name, "Str")
	return []map[string]any{
		assignStmt(name, interpLit(decodeGoStr(v.raw))),
		execStmt("export", []map[string]any{strExpr(name)}, "Emulable"),
	}
}

// ── assignments and calls ───────────────────────────────────────────

func (p *parser) parseAssignStmt() []map[string]any {
	var targets []string
	targets = append(targets, p.next().text)
	for p.acceptPunct(",") {
		p.skipNL()
		if p.atIdent("_") {
			p.pos++
			targets = append(targets, "_")
		} else {
			targets = append(targets, p.expect(tIdent, "").text)
		}
	}
	// a type-switch guard var is bound to the guarded value — writing to
	// it would need a shadow (Refuse > guess; reads alias via resolveVar)
	if _, ok := p.varAlias[targets[0]]; ok {
		p.failf("cannot assign to type-switch guard %q (v2)", targets[0])
	}
	// indexed assign: a[1] = "X" → target var "a[1]" (the `arr[1]=X` shape)
	if p.atPunct("[") {
		if len(targets) != 1 {
			p.failf("indexed assign with multiple targets (v2)")
		}
		p.pos++
		idx := ""
		if p.tok().kind == tNum {
			idx = p.next().text
		} else if p.tok().kind == tStr {
			// a map-keyed assign: call["typeArgs"] = ta — the runtime's
			// setVar("call[typeArgs]") assoc-store write.
			idx = decodeGoStr(p.next().raw)
		} else {
			ie := p.parseExpr()
			if off := p.indexArithText(ie); off != "" {
				idx = off
			} else {
				p.failf("indexed assign key must be literal or arith (v2)")
			}
		}
		p.expect(tPunct, "]")
		targets[0] = targets[0] + "[" + idx + "]"
	}
	// member assign: p.pos = 5 → p = jsonSet("p", "pos", 5) (the go-sh
	// self-hosting contract: structs are JSON object strings; the write
	// lands on the resolved var).
	memberBase, memberField := "", ""
	memberIndex := "" // p.varTypes[name] — the map-field key
	if p.atPunct(".") {
		if len(targets) != 1 {
			p.failf("member assign with multiple targets (v2)")
		}
		p.pos++
		memberField = p.expect(tIdent, "").text
		memberBase = targets[0]
		targets[0] = memberBase + "." + memberField
		if p.atPunct("[") {
			p.pos++
			p.skipNL()
			if p.tok().kind == tNum {
				memberIndex = p.next().text
			} else {
				ie := p.parseExpr()
				if off := p.indexArithText(ie); off != "" {
					memberIndex = off
				} else {
					p.failf("struct field index must be literal or arith (v2)")
				}
			}
			p.expect(tPunct, "]")
			targets[0] = memberBase + "." + memberField + "[" + memberIndex + "]"
		}
	}
	// function call statement: f(args)
	if p.atPunct("(") {
		p.pos++
		args := p.parseArgs()
		var words []map[string]any
		for _, a := range args {
			words = append(words, p.exprToWord(a))
		}
		return []map[string]any{execStmt(targets[0], words, "Spawn")}
	}
	// x++
	if p.atPunct("++") {
		p.pos++
		if len(targets) != 1 {
			p.failf("++ needs one target")
		}
		if memberBase != "" {
			// p.pos++ → p = jsonSet("p", "pos", p.pos + 1) — the
			// go-sh self-hosting contract (the golib's 91 `p.pos++`).
			p.registerVar(memberBase, "Struct")
			return []map[string]any{assignStmt(memberBase, map[string]any{
				"type": "Call", "func": "jsonSet",
				"args": []any{
					strExpr(p.resolveVar(memberBase)), strExpr(memberField),
					arithWrap(arithBin(arithVar(memberBase), "+", arithNum(1))),
				},
				"purity": "PureCpu",
			})}
		}
		p.registerVar(targets[0], "Int")
		return []map[string]any{assignStmt(targets[0],
			arithWrap(arithBin(arithVar(targets[0]), "+", arithNum(1))))}
	}
	op := ""
	switch {
	case p.atPunct(":="):
		op = ":="
	case p.atPunct("="):
		op = "="
	case p.atPunct("+="):
		op = "+="
	default:
		p.failf("expected assignment operator, got %q", p.tok().text)
	}
	p.pos++
	p.skipNL()

	// func literal: name := func(...) { ... } → Function stmt
	if p.atIdent("func") {
		fn := p.parseFuncLit()
		p.fnNames[targets[0]] = true
		return []map[string]any{{
			"type": "Function",
			"name": targets[0],
			"body": fn.body,
		}}
	}
	// []byte(x) conversion → identity (strings ARE bytes in the A1 —
	// the `string(x)` twin already lowers as identity; the A1 has no
	// byte type, so the conversion is a parse-level no-op).
	if p.atPunct("[") && p.byteConvAhead() {
		if len(targets) > 1 {
			p.failf("[]byte conversion with multiple targets (v2)")
		}
		p.pos += 4 // [ ] byte (
		p.skipNL()
		arg := p.parseExpr()
		p.skipNL()
		p.expect(tPunct, ")")
		w := p.exprToWord(arg)
		p.registerVar(targets[0], p.wordType(w))
		return []map[string]any{assignStmt(targets[0], w)}
	}
	// []byte{...} byte-slice COMPOSITE literal: refused (Refuse >
	// guess) — an escaped char element ('\n') would silently lower as
	// its raw two-char text, and the shell-flavored A1 has no byte
	// array; the conversion form above is the supported shape.
	if p.atPunct("[") && p.byteSliceLitAhead() {
		p.failf("[]byte{...} byte-slice literal unsupported (v2) — use []byte(\"...\") conversion")
	}
	// array literal: name := []T{...}
	if p.atPunct("[") {
		elems, typ := p.parseArrayLiteral()
		p.arrays[targets[0]] = arrayInfo{elems: elems, typ: typ}
		p.registerVar(targets[0], "Array")
		if len(targets) > 1 {
			p.failf("array literal with multiple targets (v2)")
		}
		return []map[string]any{assignStmt(targets[0],
			map[string]any{
				"type": "Call", "func": "setArray",
				"args":   []any{strExpr(targets[0]), map[string]any{"type": "Array", "elements": elems}},
				"purity": "Emulable",
			})}
	}
	// map literal: name := map[K]V{ k: v, ... } — the runtime's
	// by-name associative-array store (t54) when the values are
	// literals; the JSON object string (jsonNew — the golib's A1
	// builders' map[string]any{...} fragments) when the values are
	// expressions.
	if p.atIdent("map") {
		me := p.parsePrimary() // the structlit expr (parsePrimary's map case)
		allLit := true
		for _, f := range me.args {
			if f.kind != "str" && f.kind != "num" && f.kind != "rawstr" &&
				!(f.kind == "var" && (f.name == "true" || f.name == "false")) {
				allLit = false
				break
			}
		}
		if allLit {
			// the assocSet lowering (t54) — reuse parseMapLiteral's body
			// by re-parsing: the tokens are already consumed, so emit the
			// assocSet calls directly.
			if len(targets) > 1 {
				p.failf("map literal with multiple targets (v2)")
			}
			p.maps[targets[0]] = true
			p.registerVar(targets[0], "Map")
			var body []map[string]any
			for i, f := range me.args {
				valText := f.text
				if f.kind == "var" {
					valText = f.name
				}
				body = append(body, map[string]any{
					"type": "Expr",
					"expr": map[string]any{
						"type": "Call", "func": "assocSet",
						"args":   []any{strExpr(targets[0]), strExpr(me.fieldNames[i]), strExpr(valText)},
						"purity": "Emulable",
					},
				})
			}
			return []map[string]any{{"type": "Block", "body": body}}
		}
		// expression values — the jsonNew lowering (the golib's A1
		// fragments).
		p.registerVar(targets[0], "Map")
		return []map[string]any{assignStmt(targets[0], p.exprToWord(me))}
	}
	// a := make([]T, n) — the make builtin: an empty array (the golib's
	// `a := make([]any, len(args))` + indexed-fill pattern; the runtime's
	// array store grows on the indexed assigns).
	if p.atIdent("make") {
		p.pos++
		p.expect(tPunct, "(")
		p.skipNL()
		p.expect(tPunct, "[")
		p.expect(tPunct, "]")
		for p.tok().kind == tIdent {
			p.next()
		}
		p.expect(tPunct, ",")
		p.skipNL()
		p.parseExpr() // the length
		p.expect(tPunct, ")")
		if len(targets) > 1 {
			p.failf("make with multiple targets (v2)")
		}
		p.arrays[targets[0]] = arrayInfo{elems: []map[string]any{}, typ: "Str"}
		p.registerVar(targets[0], "Array")
		return []map[string]any{assignStmt(targets[0],
			map[string]any{
				"type": "Call", "func": "setArray",
				"args":   []any{strExpr(targets[0]), map[string]any{"type": "Array", "elements": []any{}}},
				"purity": "Emulable",
			})}
	}
	// cmd := exec.Command(a, b) [.Output()|.Run()]  /  x, _ := ....Output()
	if p.atIdent("exec") {
		p.next()
		p.expect(tPunct, ".")
		p.expect(tIdent, "Command")
		p.expect(tPunct, "(")
		args := p.parseArgs()
		if !p.atPunct(".") {
			// bare Command — store for a later .Run()
			if len(targets) == 1 && targets[0] != "_" {
				p.cmds[targets[0]] = args
			}
			return nil
		}
		p.pos++
		m := p.expect(tIdent, "").text
		p.expect(tPunct, "(")
		p.skipNL()
		p.expect(tPunct, ")")
		if m == "Run" {
			return []map[string]any{p.execFromArgs(args)}
		}
		return []map[string]any{p.captureAssign(targets, args)}
	}
	// r := bufio.NewReader(os.Stdin)  /  sc := bufio.NewScanner(os.Stdin)
	if p.atIdent("bufio") {
		p.next()
		p.expect(tPunct, ".")
		m := p.expect(tIdent, "").text
		if m != "NewReader" && m != "NewScanner" {
			p.failf("unsupported bufio.%s (v2)", m)
		}
		p.expect(tPunct, "(")
		p.skipNL()
		p.expect(tIdent, "os")
		p.expect(tPunct, ".")
		p.expect(tIdent, "Stdin")
		p.expect(tPunct, ")")
		if len(targets) == 1 && targets[0] != "_" {
			p.stdinRdr[targets[0]] = true
		}
		return nil
	}
	// line, _ := reader.ReadString('\n')
	if p.tok().kind == tIdent && p.stdinRdr[p.tok().text] {
		p.next()
		p.expect(tPunct, ".")
		p.expect(tIdent, "ReadString")
		p.expect(tPunct, "(")
		p.skipNL()
		p.parseExpr() // delimiter literal — ignored
		p.expect(tPunct, ")")
		readVar := ""
		for _, tg := range targets {
			if tg != "_" {
				readVar = tg
				break
			}
		}
		return []map[string]any{execStmt("read",
			[]map[string]any{strExpr("-r"), strExpr(readVar)}, "Emulable")}
	}

	// n, _ := strconv.Atoi("42") → n = "42" — the error return is
	// dropped, and Atoi over a literal folds at emit time (t72).
	if p.atIdent("strconv") {
		p.next()
		p.expect(tPunct, ".")
		p.expect(tIdent, "Atoi")
		p.expect(tPunct, "(")
		p.skipNL()
		arg := p.parseExpr()
		p.skipNL()
		p.expect(tPunct, ")")
		if arg.kind != "str" && arg.kind != "num" {
			p.failf("strconv.Atoi needs a literal (v2)")
		}
		name := ""
		for _, tg := range targets {
			if tg == "_" || tg == "err" {
				continue
			}
			if name != "" {
				p.failf("strconv.Atoi returns (int, error) — one target (v2)")
			}
			name = tg
		}
		if name == "" {
			p.failf("strconv.Atoi needs a target var (v2)")
		}
		p.registerVar(name, "Int")
		return []map[string]any{assignStmt(name, strExpr(arg.text))}
	}

	// b, _ := os.ReadFile(path) → b = $(cat path) — the whole-file-read
	// cmdsub shape (t34's capture; cat is an Emulable sync builtin; the
	// []byte result is a string in the A1's strings-are-bytes model).
	// The error return is dropped like strconv.Atoi's. The if-init form
	// (`if b, err := os.ReadFile(p); err == nil {`) stays REFUSED: its
	// cond is a read-status test, and the A1 If cond is a `test` call,
	// not a command status (Refuse > guess). Other os.* RHS calls
	// (Getenv etc.) fall through to the generic expression RHS.
	if p.atIdent("os") && p.pos+2 < len(p.toks) &&
		p.toks[p.pos+1].text == "." && p.toks[p.pos+2].text == "ReadFile" {
		p.next()
		p.expect(tPunct, ".")
		p.expect(tIdent, "ReadFile")
		p.expect(tPunct, "(")
		p.skipNL()
		arg := p.parseExpr()
		p.skipNL()
		p.expect(tPunct, ")")
		name := ""
		for _, tg := range targets {
			if tg == "_" || tg == "err" {
				continue
			}
			if name != "" {
				p.failf("os.ReadFile returns ([]byte, error) — one target (v2)")
			}
			name = tg
		}
		if name == "" {
			p.failf("os.ReadFile needs a target var (v2)")
		}
		inner := execStmt("cat", []map[string]any{p.exprToWord(arg)}, "Emulable")
		capture := map[string]any{
			"type": "Call", "func": "capture",
			"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
			"purity": "Spawn",
		}
		p.registerVar(name, "Str")
		return []map[string]any{assignStmt(name, capture)}
	}

	// generic RHS
	rhs := p.parseExpr()

	// args := os.Args[1:] — argv with argv0 stripped, the shell's `"$@"`
	// (the array-valued positional slice `${@:off:len}`, which the core
	// lowers to the runtime's native positional list). Go's os.Args is
	// 0-based with argv0 at [0]; bash `${@:off}` is 1-BASED over the
	// positionals (`${@:1}` = all params; `${@:0}` = [argv0, ...params])
	// and is ARRAY-valued — so os.Args[i:] ↔ param("slice", "@", i, "")
	// and os.Args[i:j] ↔ param("slice", "@", i, j-i) (the exclusive-end
	// length, matching sliceWord). The runtime's setArray SPLICES the
	// param-slice array into the array store, so len(args)/args[i] read
	// off the store. Computed bounds stay refused; `range args` over the
	// result lowers to the `${arr[@]}` For-iter shape (ONE array-valued
	// param("slice", name, "@", "") element, flattened by forLoop — see
	// parseFor; templates/go/range_args.go).
	if rhs.kind == "slice" && rhs.target != nil && rhs.target.kind == "member" &&
		rhs.target.name == "os.Args" {
		if rhs.idx1e != nil || rhs.idx2e != nil {
			p.failf("os.Args slice bounds must be literal (v2)")
		}
		if len(targets) > 1 {
			p.failf("os.Args slice with multiple targets (v2)")
		}
		lo := rhs.idx1
		if lo == "" {
			lo = "0"
		}
		length := ""
		if rhs.idx2 != "" {
			loN, err1 := strconv.Atoi(lo)
			hiN, err2 := strconv.Atoi(rhs.idx2)
			if err1 != nil || err2 != nil || hiN < loN {
				p.failf("invalid os.Args slice bounds (v2)")
			}
			length = strconv.Itoa(hiN - loN)
		}
		p.registerVar(targets[0], "Array")
		return []map[string]any{assignStmt(targets[0], map[string]any{
			"type": "Call", "func": "setArray",
			"args": []any{strExpr(targets[0]), map[string]any{
				"type": "Array",
				"elements": []any{
					paramCall("slice", "@", lo, length),
				},
			}},
			"purity": "Emulable",
		})}
	}

	// a = append(a, "c", "d") → setArrayAppend (the `arr+=(c d)` shape)
	if rhs.kind == "call" && rhs.callee == "append" {
		if len(rhs.args) < 2 || rhs.args[0].kind != "var" {
			p.failf("append needs (array, elems...) (v2)")
		}
		var elems []any
		for _, a := range rhs.args[1:] {
			switch a.kind {
			case "str", "num", "rawstr", "char":
				elems = append(elems, p.exprToWord(a))
			case "var":
				// s = append(s, v) with a VARIABLE element — the dogfood
				// CLI's argv filter (`filtered = append(filtered, a)`) and
				// the golib's `tas = append(tas, item)`. The A1
				// setArrayAppend element contract is an EXPRESSION (shell
				// `arr+=(x)` lowers to split(getVar(x)) in shir.rs) — the
				// literal-only refusal was self-imposed. Lower through
				// exprToWord (getVar after resolve): one element per
				// appended VALUE, no field-splitting (Go semantics).
				elems = append(elems, p.exprToWord(a))
			case "structlit":
				// append(toks, token{...}) — a struct VALUE element (the
				// golib's lexer). The jsonNew result is the element.
				elems = append(elems, p.exprToWord(a))
			default:
				p.failf("append elements must be literals or vars (v2)")
			}
		}
		if memberBase != "" {
			// p.toks = append(p.toks, t) → p = jsonSet("p", "toks",
			// jsonArrAppend(jsonGet("p", "toks"), t)) — the go-sh
			// self-hosting contract (the golib's `p.fnParamOrd = append(...)`).
			p.registerVar(memberBase, "Struct")
			inner := map[string]any{
				"type": "Call", "func": "jsonGet",
				"args":   []any{strExpr(p.resolveVar(memberBase)), strExpr(memberField)},
				"purity": "PureCpu",
			}
			var appArgs []any
			appArgs = append(appArgs, inner)
			for _, el := range elems {
				appArgs = append(appArgs, el)
			}
			return []map[string]any{assignStmt(memberBase, map[string]any{
				"type": "Call", "func": "jsonSet",
				"args": []any{
					strExpr(p.resolveVar(memberBase)), strExpr(memberField),
					map[string]any{"type": "Call", "func": "jsonArrAppend", "args": appArgs, "purity": "PureCpu"},
				},
				"purity": "PureCpu",
			})}
		}
		p.registerVar(rhs.args[0].name, "Array")
		return []map[string]any{assignStmt(rhs.args[0].name, map[string]any{
			"type": "Call", "func": "setArrayAppend",
			"args":   []any{strExpr(rhs.args[0].name), map[string]any{"type": "Array", "elements": elems}},
			"purity": "Emulable",
		})}
	}

	// err := cmd.Run() — exec the stored command
	if rhs.kind == "call" && strings.HasSuffix(rhs.callee, ".Run") {
		base := strings.TrimSuffix(rhs.callee, ".Run")
		if args, ok := p.cmds[base]; ok {
			return []map[string]any{p.execFromArgs(args)}
		}
		p.failf("unknown command %q (v2)", base)
	}

	// out, err := golib.Shir(src) — a multi-target assign from a SINGLE
	// call returning (value, error): the value target captures the
	// call's stdout (the function-call shape — the A1 has no
	// cross-package calls, so a package-qualified callee lowers to a
	// shell sub of its last name component), the error target is
	// dropped (mirroring strconv.Atoi/os.ReadFile). A comma-separated
	// value list (`a, b := x, y`) falls through to the generic
	// multi-assign below.
	if len(targets) > 1 && rhs.kind == "call" && !p.atPunct(",") {
		name := ""
		for _, tg := range targets {
			if tg == "_" || tg == "err" {
				continue
			}
			if name != "" {
				p.failf("call returns (value, error) — one value target (v2)")
			}
			name = tg
		}
		if name == "" {
			p.failf("call needs a value target (v2)")
		}
		fn := rhs.callee
		if i := strings.LastIndex(fn, "."); i >= 0 {
			fn = fn[i+1:]
		}
		// shiremit.Emit(prog) — the A1 emitter (the golib's Shir): a
		// DIRECT call to the sh2.a1Emit helper (the sorted-key JSON
		// marshal), not a shell-command capture — the emitter is a
		// runtime function, not a command.
		if rhs.callee == "shiremit.Emit" {
			var words []map[string]any
			for _, a := range rhs.args {
				words = append(words, p.exprToWord(a))
			}
			p.registerVar(name, "Str")
			return []map[string]any{assignStmt(name, map[string]any{
				"type": "Call", "func": "a1Emit",
				"args":   words,
				"purity": "PureCpu",
			})}
		}
		var words []map[string]any
		for _, a := range rhs.args {
			words = append(words, p.exprToWord(a))
		}
		inner := execStmt(fn, words, "Spawn")
		capture := map[string]any{
			"type": "Call", "func": "capture",
			"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
			"purity": "Spawn",
		}
		p.registerVar(name, "Str")
		return []map[string]any{assignStmt(name, capture)}
	}

	// multi-assign: a, b := x, y → Block of Assigns (A=x B=y shape)
	if len(targets) > 1 {
		values := []*expr{rhs}
		for p.acceptPunct(",") {
			p.skipNL()
			values = append(values, p.parseExpr())
		}
		var body []map[string]any
		for i, tg := range targets {
			if tg == "_" {
				continue
			}
			if i >= len(values) {
				p.failf("multi-assign arity mismatch (v2)")
			}
			w := p.exprToWord(values[i])
			p.registerVar(tg, p.wordType(w))
			body = append(body, assignStmt(tg, w))
		}
		if len(body) == 0 {
			return nil
		}
		return []map[string]any{{
			"type": "Block",
			"body": body,
		}}
	}

	// single assign
	w := p.exprToWord(rhs)
	if memberBase != "" {
		// p.pos = 5 → p = jsonSet("p", "pos", 5); p.varTypes[name] = typ
		// → p = jsonSet("p", "varTypes", jsonSet(jsonGet("p",
		// "varTypes"), name, typ)) — the go-sh self-hosting contract
		// (the write lands on the resolved var).
		p.registerVar(memberBase, "Struct")
		if memberIndex != "" {
			inner := map[string]any{
				"type": "Call", "func": "jsonGet",
				"args":   []any{strExpr(p.resolveVar(memberBase)), strExpr(memberField)},
				"purity": "PureCpu",
			}
			return []map[string]any{assignStmt(memberBase, map[string]any{
				"type": "Call", "func": "jsonSet",
				"args": []any{
					strExpr(p.resolveVar(memberBase)), strExpr(memberField),
					map[string]any{
						"type": "Call", "func": "jsonSet",
						"args":   []any{inner, strExpr(memberIndex), w},
						"purity": "PureCpu",
					},
				},
				"purity": "PureCpu",
			})}
		}
		return []map[string]any{assignStmt(memberBase, map[string]any{
			"type": "Call", "func": "jsonSet",
			"args":   []any{strExpr(p.resolveVar(memberBase)), strExpr(memberField), w},
			"purity": "PureCpu",
		})}
	}
	if op == "+=" {
		p.registerVar(targets[0], "Int")
		// i += len(op) — a string/array LENGTH in arithmetic: the Arith
		// AST can't express `${#op}`, so lower to the arith-TEXT form
		// (the runtime's evalArith expands ${#...} — the golib's
		// `i += len(op)` operator-length advance).
		if rhs.kind == "strlen" || rhs.kind == "arrlen" {
			lenWord := ""
			if rhs.kind == "strlen" {
				lenWord = "${#" + p.resolveVar(rhs.target.name) + "}"
			} else {
				lenWord = "${#" + p.resolveVar(rhs.target.name) + "[@]}"
			}
			return []map[string]any{assignStmt(targets[0], map[string]any{
				"type": "Call", "func": "arith",
				"args":   []any{strExpr(targets[0] + " + " + lenWord)},
				"purity": "PureCpu",
			})}
		}
		return []map[string]any{assignStmt(targets[0],
			arithWrap(arithBin(arithVar(targets[0]), "+", p.exprToArith(rhs))))}
	}
	// inside a func: `name := lit` on a fresh var → local name=lit
	if p.inFunc && op == ":=" && !p.fnParams[targets[0]] && !p.outer[targets[0]] && !p.fnLocals[targets[0]] {
		p.fnLocals[targets[0]] = true
		// register the type FIRST (a byte read / struct element read
		// must be tracked even for locals — the golib's `t :=
		// p.toks[p.pos]` token locals).
		p.registerAssignType(targets[0], rhs, w)
		return []map[string]any{execStmt("local",
			[]map[string]any{strExpr(targets[0] + "=" + localVal(w))}, "Emulable")}
	}
	p.registerAssignType(targets[0], rhs, w)
	return []map[string]any{assignStmt(targets[0], w)}
}

// registerAssignType — the target's type for an assign RHS: a byte read
// (`c := src[i]`), a struct VALUE (`p := &parser{...}`), a struct-field
// ELEMENT read (`t := p.toks[p.pos]` — the field's element type), or the
// word type.
func (p *parser) registerAssignType(name string, rhs *expr, w map[string]any) {
	if rhs.kind == "index" && rhs.target != nil && rhs.target.kind == "var" &&
		p.varTypes[p.resolveVar(rhs.target.name)] != "Array" {
		p.registerVar(name, "Byte")
	} else if rhs.kind == "structlit" {
		p.registerVar(name, "Struct")
		p.varStructTypes[name] = rhs.name
	} else if rhs.kind == "index" && rhs.target != nil && rhs.target.kind == "member" {
		if i := strings.LastIndex(rhs.target.name, "."); i > 0 {
			base, field := rhs.target.name[:i], rhs.target.name[i+1:]
			if elt := p.structFieldTypes[p.structTypeOf(base)][field]; elt != "" {
				if _, ok := p.structFields[elt]; ok {
					p.registerVar(name, "Struct")
					return
				}
			}
		}
		p.registerVar(name, p.wordType(w))
	} else {
		p.registerVar(name, p.wordType(w))
	}
}

// localVal renders the value text for `local name=val`.
func localVal(w map[string]any) string {
	switch v := w["type"].(string); v {
	case "Interpolate":
		if parts, ok := w["parts"].([]any); ok && len(parts) == 1 {
			if pt, ok := parts[0].(map[string]any); ok && pt["kind"] == "lit" {
				return pt["text"].(string)
			}
		}
	case "Str":
		return w["value"].(string)
	}
	return ""
}

// parseMapLiteral: `map[K]V{ k: v, ... }` → one assocSet per pair (the
// runtime's by-name associative-array store; t54). Shared by the
// `name := map[...]...{...}` assign and the `var name = map[...]...{...}`
// decl (the cpp frontend's `var refuseKeywords = map[string]string{...}`
// and `var allowedKinds = map[string]bool{...}` whitelist tables). The
// type position `map[K]V` is consumed as TOKENS, never parsed as
// expressions — `string` inside `[string]` must not hit the `string(x)`
// conversion path. Values accept bool literals (true/false) alongside
// str/num/rawstr — a `map[string]bool` whitelist's `true` is a literal,
// not a `$true` var read.
func (p *parser) parseMapLiteral(targets []string) []map[string]any {
	p.pos++ // map
	p.expect(tPunct, "[")
	for p.tok().kind == tIdent {
		p.next()
	}
	p.expect(tPunct, "]")
	for p.tok().kind == tIdent {
		p.next()
	}
	p.skipNL()
	p.expect(tPunct, "{")
	if len(targets) > 1 {
		p.failf("map literal with multiple targets (v2)")
	}
	p.maps[targets[0]] = true
	p.registerVar(targets[0], "Map")
	var body []map[string]any
	for {
		p.skipNL()
		if p.atPunct("}") {
			p.pos++
			break
		}
		key := p.parseExpr()
		if key.kind != "str" && key.kind != "num" && key.kind != "rawstr" {
			p.failf("map keys must be literals (v2)")
		}
		p.expect(tPunct, ":")
		p.skipNL()
		val := p.parseExpr()
		valText := ""
		switch val.kind {
		case "str", "num", "rawstr":
			valText = val.text
		case "var":
			// bool literal values (map[string]bool whitelists)
			if val.name == "true" || val.name == "false" {
				valText = val.name
				break
			}
			p.failf("map values must be literals (v2)")
		default:
			p.failf("map values must be literals (v2)")
		}
		body = append(body, map[string]any{
			"type": "Expr",
			"expr": map[string]any{
				"type": "Call", "func": "assocSet",
				"args":   []any{strExpr(targets[0]), strExpr(key.text), strExpr(valText)},
				"purity": "Emulable",
			},
		})
		if !p.acceptPunct(",") {
			p.skipNL()
			p.expect(tPunct, "}")
			break
		}
	}
	return []map[string]any{{"type": "Block", "body": body}}
}

// arrayLitAhead: the token stream at pos is `[ ] <type> {` — an array
// literal in an EXPRESSION position (the golib's A1 builders' `[]any{...}`).
func (p *parser) arrayLitAhead() bool {
	if p.tok().kind != tPunct || p.tok().text != "[" ||
		p.toks[p.pos+1].kind != tPunct || p.toks[p.pos+1].text != "]" {
		return false
	}
	for i := p.pos + 2; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.kind == tPunct && t.text == "{" {
			return true
		}
		if t.kind == tNL || t.kind == tEOF {
			return false
		}
	}
	return false
}

// parseArrayLiteral parses `[]T{ e1, e2, ... }` and returns the element
// words plus the element type.
// byteConvAhead: the token stream at pos is `[ ] byte (` — the
// []byte(x) conversion (grammar conversion; identity in the A1's
// strings-are-bytes model).
func (p *parser) byteConvAhead() bool {
	return p.pos+3 < len(p.toks) &&
		p.toks[p.pos].kind == tPunct && p.toks[p.pos].text == "[" &&
		p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "]" &&
		p.toks[p.pos+2].kind == tIdent && p.toks[p.pos+2].text == "byte" &&
		p.toks[p.pos+3].kind == tPunct && p.toks[p.pos+3].text == "("
}

// byteSliceLitAhead: the token stream at pos is `[ ] byte {` — the
// []byte{...} byte-slice composite literal (refused, see parseAssignStmt).
func (p *parser) byteSliceLitAhead() bool {
	return p.pos+3 < len(p.toks) &&
		p.toks[p.pos].kind == tPunct && p.toks[p.pos].text == "[" &&
		p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "]" &&
		p.toks[p.pos+2].kind == tIdent && p.toks[p.pos+2].text == "byte" &&
		p.toks[p.pos+3].kind == tPunct && p.toks[p.pos+3].text == "{"
}

func (p *parser) parseArrayLiteral() ([]map[string]any, string) {
	p.expect(tPunct, "[")
	p.skipNL()
	typ := "Str"
	for !p.atPunct("{") {
		t := p.next()
		if t.kind == tEOF {
			p.failf("unterminated array literal")
		}
		if t.kind == tIdent {
			typ = t.text
		}
		p.skipNL()
	}
	p.expect(tPunct, "{")
	// non-nil so an EMPTY literal marshals to `[]`, never `null` — the
	// core's Array.elements is a Vec (shir_json.rs always collects).
	elems := []map[string]any{}
	for {
		p.skipNL()
		if p.atPunct("}") {
			p.pos++
			break
		}
		e := p.parseExpr()
		elems = append(elems, p.exprToArrayElem(e))
		if !p.acceptPunct(",") {
			p.skipNL()
			p.expect(tPunct, "}")
			break
		}
	}
	if typ == "int" || typ == "int64" || typ == "float64" {
		typ = "Int"
	} else {
		typ = "Str"
	}
	return elems, typ
}

// ── compound statements ─────────────────────────────────────────────

// ifInitReadFileAhead: `if <name>, err := os.ReadFile(` — the if-init
// form of the whole-file read with a NAMED first target (the
// `if _, err := os.Stat(` arm above handles the underscore path-test
// form; the statement form `b, _ := os.ReadFile(p)` lowers in
// parseAssignStmt). Returns the target name. Everything else falls
// through to the generic expression cond.
func (p *parser) ifInitReadFileAhead() (string, bool) {
	if p.tok().kind != tIdent || p.atIdent("_") || p.atIdent("err") {
		return "", false
	}
	name := p.tok().text
	t := p.toks
	i := p.pos + 1
	if i+6 >= len(t) {
		return "", false
	}
	if t[i].kind != tPunct || t[i].text != "," ||
		t[i+1].kind != tIdent || t[i+1].text != "err" ||
		t[i+2].kind != tOp || t[i+2].text != ":=" ||
		t[i+3].kind != tIdent || t[i+3].text != "os" ||
		t[i+4].kind != tPunct || t[i+4].text != "." ||
		t[i+5].kind != tIdent || t[i+5].text != "ReadFile" ||
		t[i+6].kind != tPunct || t[i+6].text != "(" {
		return "", false
	}
	return name, true
}

// readFileIfCond: the `if b, err := os.ReadFile(p); err (==|!=) nil`
// cond — byte-identical to the core's `if b=$(cat p); then` /
// `if ! b=$(cat p); then` lowering (verified against `debashc file
// --shir`): Call{func:"assign", args:[Str(name), Str("="),
// Capture{native:false, expr:Arrow{body:[Expr exec cat …]}}]}, wrapped
// in the Not BinOp for err != nil (lhs+rhs both carry the assign,
// matching the core's shape). cat is an Emulable sync builtin; the
// []byte result is a string in the A1's strings-are-bytes model (same
// as the statement form).
func (p *parser) readFileIfCond(name string, path map[string]any, neg bool) map[string]any {
	assignCall := map[string]any{
		"type": "Call", "func": "assign",
		"args": []any{
			strExpr(name),
			strExpr("="),
			map[string]any{
				"type": "Capture", "native": false,
				"expr": map[string]any{
					"type": "Arrow",
					"body": []any{execStmt("cat", []map[string]any{path}, "Emulable")},
				},
			},
		},
		"purity": "Emulable",
	}
	if neg {
		return map[string]any{
			"type": "BinOp", "op": "Not",
			"lhs": assignCall, "rhs": assignCall,
		}
	}
	return assignCall
}

func (p *parser) parseIf() []map[string]any {
	p.expect(tIdent, "if")
	p.skipNL()
	var pre []map[string]any
	var cond *expr
	if p.atIdent("_") && p.pos+4 < len(p.toks) &&
		p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "," &&
		p.toks[p.pos+3].kind == tOp && p.toks[p.pos+3].text == ":=" &&
		!(p.toks[p.pos+4].kind == tIdent && p.toks[p.pos+4].text == "os") {
		// if _, X := <expr>; cond { ... } — a GENERAL `_`-discard
		// if-init (the golib's `if _, ok := p.structFields[callName(e)];
		// !ok` map-presence checks): X = <expr> as a pre-statement, then
		// the If on cond. A map-index RHS sets X to the PRESENCE bool
		// (assocHas), so a later `!ok` / `ok` tests it correctly.
		p.pos++ // _
		p.pos++ // ,
		p.skipNL()
		xName := p.expect(tIdent, "").text
		p.expect(tOp, ":=")
		p.skipNL()
		val := p.parseExpr()
		var w map[string]any
		if val.kind == "index" && val.target != nil && val.target.kind == "var" && p.maps[val.target.name] {
			// `_, ok := m[k]` — ok is the PRESENCE bool (assocHas); the
			// key is resolved at runtime (a member/call key lowers to its
			// cmdsub word, which assocHas expandWord's).
			var keyWord map[string]any
			if val.idx1e != nil {
				keyWord = p.exprToWord(val.idx1e)
			} else {
				keyWord = strExpr(val.idx1)
			}
			w = assocHasCall(val.target.name, keyWord)
		} else {
			w = p.exprToWord(val)
		}
		pre = append(pre, assignStmt(xName, w))
		p.registerAssignType(xName, val, w)
		p.skipNL()
		p.expect(tPunct, ";")
		p.skipNL()
		cond = p.parseExpr()
	} else if p.atIdent("_") {
		// if _, err := os.Stat("path"); err == nil { → test("-e path")
		// (-e = exists, ANY type — Go's os.Stat succeeds for devices,
		// dirs and symlinks alike; bash `-f` is regular files only, and
		// a probe stat-ing /dev/null would flip to the else branch).
		p.pos++
		p.expect(tPunct, ",")
		p.expect(tIdent, "err")
		p.expect(tPunct, ":=")
		p.expect(tIdent, "os")
		p.expect(tPunct, ".")
		p.expect(tIdent, "Stat")
		p.expect(tPunct, "(")
		pathTok := p.expect(tStr, "")
		p.skipNL()
		p.expect(tPunct, ")")
		p.expect(tPunct, ";")
		p.skipNL()
		cond = p.parseExpr() // err == nil / err != nil
		neg := false
		// err != nil → `test ! -e path` (the negated stat test). The
		// condTestString prefix sniff alone would miss this polarity:
		// `err != nil` renders as `"$?" -ne 0`, which has no "! "
		// prefix, and the then-body would run on EXISTING files — the
		// inverse of Go's err != nil.
		if cond.kind == "binop" && cond.BOp == "!=" {
			neg = true
		} else if c := p.condTestString(cond); strings.HasPrefix(c, "! ") {
			neg = true
		}
		arg := "-e " + decodeGoStr(pathTok.raw)
		if neg {
			arg = "! -e " + decodeGoStr(pathTok.raw)
		}
		cond = &expr{kind: "cond", text: arg}
	} else if name, ok := p.ifInitReadFileAhead(); ok {
		// if b, err := os.ReadFile(path); err == nil { — the if-init
		// whole-file read: the read's SUCCESS is the branch condition,
		// the `if b=$(cat p); then` shape (Call func=assign wrapping a
		// Capture — byte-identical to the core's own lowering of the
		// shell form, verified against debashc file --shir). err == nil
		// → the assign cond; err != nil → the core's `if ! b=$(cat p)`
		// Not BinOp (lhs+rhs both carry the assign). Any other
		// condition refuses loudly (Refuse > guess).
		p.pos++ // <name>
		p.expect(tPunct, ",")
		p.expect(tIdent, "err")
		p.expect(tOp, ":=")
		p.expect(tIdent, "os")
		p.expect(tPunct, ".")
		p.expect(tIdent, "ReadFile")
		p.expect(tPunct, "(")
		p.skipNL()
		path := p.parseExpr()
		p.skipNL()
		p.expect(tPunct, ")")
		p.skipNL()
		p.expect(tPunct, ";")
		p.skipNL()
		c := p.parseExpr()
		errEqNil := c.kind == "binop" && c.BOp == "==" &&
			c.lhs.kind == "var" && c.lhs.name == "err" &&
			c.rhs.kind == "var" && c.rhs.name == "nil"
		neg := c.kind == "binop" && c.BOp == "!=" &&
			c.lhs.kind == "var" && c.lhs.name == "err" &&
			c.rhs.kind == "var" && c.rhs.name == "nil"
		if !errEqNil && !neg {
			p.failf("unsupported if-init os.ReadFile condition (v2)")
		}
		p.registerVar(name, "Str")
		cond = &expr{kind: "rawjson", rawJSON: p.readFileIfCond(name, p.exprToWord(path), neg)}
	} else if p.tok().kind == tIdent && p.toks[p.pos+1].kind == tOp && p.toks[p.pos+1].text == ":=" {
		// if t := expr; cond { ... } — an if-init ASSIGNMENT (the golib's
		// `if t := p.tok(); (t.kind == tOp ...)`). The assignment lowers
		// to a pre-`t = expr` statement, then the If on the condition.
		name := p.next().text
		p.pos++ // :=
		p.skipNL()
		val := p.parseExpr()
		w := p.exprToWord(val)
		pre = append(pre, assignStmt(name, w))
		p.registerAssignType(name, val, w)
		p.skipNL()
		p.expect(tPunct, ";")
		p.skipNL()
		cond = p.parseExpr()
	} else {
		cond = p.parseExpr()
	}
	then := p.parseBlockStmts()
	var elseBody []map[string]any
	p.skipNL()
	if p.atIdent("else") {
		p.pos++
		p.skipNL()
		if p.atIdent("if") {
			elseBody = p.parseIf()
		} else {
			elseBody = p.parseBlockStmts()
		}
	} else {
		elseBody = []map[string]any{}
	}
	return append(pre, map[string]any{
		"type":   "If",
		"cond":   p.condToJSON(cond),
		"then":   then,
		"elsifs": []any{},
		"else":   elseBody,
	})
}

func (p *parser) parseFor() []map[string]any {
	p.expect(tIdent, "for")
	p.skipNL()
	// for i := range N { — Go's range-over-int (i = 0..N-1, exclusive
	// end) → the core For Range shape (t73).
	if p.tok().kind == tIdent && p.toks[p.pos+1].text == ":=" &&
		p.pos+2 < len(p.toks) && p.toks[p.pos+2].kind == tIdent && p.toks[p.pos+2].text == "range" {
		v := p.next().text
		p.pos++ // :=
		p.skipNL()
		p.next() // range
		p.skipNL()
		n := p.expect(tNum, "").text
		end, err := strconv.Atoi(n)
		if err != nil || end < 0 {
			p.failf("range over int needs a non-negative literal (v2)")
		}
		body := p.parseBlockStmts()
		p.registerVar(v, "Int")
		return []map[string]any{{
			"type": "For",
			"var":  v,
			"iter": map[string]any{"type": "Range", "start": 0, "end": end - 1},
			"body": body,
		}}
	}
	// range forms
	if p.atIdent("_") || p.atIdent("range") ||
		(p.tok().kind == tIdent && p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == ",") {
		var v string
		idxVar := ""
		if p.atIdent("_") {
			p.pos++
			p.expect(tPunct, ",")
			p.skipNL()
			v = p.expect(tIdent, "").text
			p.expect(tPunct, ":=")
		} else if p.tok().kind == tIdent && p.toks[p.pos+1].kind == tPunct && p.toks[p.pos+1].text == "," {
			// for i, s := range arr — the index+value form: the index is a
			// COUNTER (i=0; the body increments it), the value is the A1
			// For loop var (the go-sh self-hosting contract — the golib's
			// 37 `for i, s := range` loops).
			idxVar = p.next().text
			p.pos++ // ,
			p.skipNL()
			v = p.expect(tIdent, "").text
			p.expect(tPunct, ":=")
		} else {
			v = p.expect(tIdent, "").text
			p.expect(tPunct, ":=")
		}
		p.skipNL()
		p.expect(tIdent, "range")
		p.skipNL()
		var iter []map[string]any
		typ := "Str"
		if p.atPunct("[") {
			elems, t := p.parseArrayLiteral()
			iter, typ = elems, t
		} else {
			rv := p.parseExpr()
			if rv.kind != "var" {
				p.failf("range over a non-var (v2)")
			}
			if info, ok := p.arrays[rv.name]; ok {
				iter, typ = info.elems, info.typ
			} else if p.varTypes[rv.name] == "Array" {
				// Runtime-loaded array (e.g. `args := os.Args[1:]` → the
				// setArray param-slice; `a = append(a, …)`): the A1 For
				// iter is a STATIC element list, but its elements are
				// EXPRESSIONS — the contract's `${arr[@]}` shape is ONE
				// array-valued param("slice", name, "@", "") element,
				// which the runtime's forLoop FLATTENS (the core emits
				// exactly this for `for x in "${arr[@]}"`; setArray has
				// spliced the positional slice into the array store, so
				// the iter reads the store). Loop vars are Str elements.
				// Untyped vars stay a loud refusal (Refuse > guess).
				body := p.parseBlockStmts()
				p.registerVar(v, "Str")
				return p.rangeFor(idxVar, v, map[string]any{"type": "Array", "elements": []any{
					paramCall("slice", rv.name, "@", ""),
				}}, body)
			} else {
				p.failf("range over unknown array %q (v2)", rv.name)
			}
		}
		body := p.parseBlockStmts()
		p.registerVar(v, typ)
		elems := make([]any, len(iter))
		for i, el := range iter {
			elems[i] = el
		}
		return p.rangeFor(idxVar, v, map[string]any{"type": "Array", "elements": elems}, body)
	}
	// for sc.Scan() { → While(read sc) — bufio.Scanner stdin loop
	if p.pos+3 < len(p.toks) && p.tok().kind == tIdent && p.stdinRdr[p.tok().text] &&
		p.toks[p.pos+1].text == "." && p.toks[p.pos+2].kind == tIdent &&
		p.toks[p.pos+2].text == "Scan" && p.toks[p.pos+3].text == "(" {
		scName := p.next().text
		p.pos += 2 // . Scan
		p.expect(tPunct, "(")
		p.skipNL()
		p.expect(tPunct, ")")
		body := p.parseBlockStmts()
		return []map[string]any{{
			"type": "While",
			"cond": execCond("read", []map[string]any{strExpr(scName)}),
			"body": body,
		}}
	}
	// for i := 1; i <= 2; i++ {  — header form
	if p.tok().kind == tIdent && p.toks[p.pos+1].text == ":=" {
		initName := p.next().text
		p.pos++ // :=
		p.skipNL()
		rhs := p.parseExpr()
		pre := []map[string]any{assignStmt(initName, p.exprToWord(rhs))}
		p.registerVar(initName, "Int")
		p.expect(tPunct, ";")
		p.skipNL()
		cond := p.parseExpr()
		p.expect(tPunct, ";")
		p.skipNL()
		postName := p.expect(tIdent, "").text
		p.expect(tPunct, "++")
		post := []map[string]any{assignStmt(postName,
			arithWrap(arithBin(arithVar(postName), "+", arithNum(1))))}
		body := p.parseBlockStmts()
		// `i := N; i <= M; i++` (or `i < M`) → the core's ForInit shape
		// (byte-identical to the `for ((i=N; i<=M; i++))` lowering in
		// shir.rs): init Assign(Arith Assign) / cond exec `let "i<=M"` /
		// step Assign(Arith IncDec) / body. NOT the For+Range iter form:
		// the core emits Range only for `for i in $(seq N M)` (the
		// seq_range_for transform), and the ESTree lowering of that Range
		// counter keeps the loop var as a native JS local WITHOUT syncing
		// sh2.vars — string-context body reads (`echo n$i` / printf) then
		// see "" (t58_seq_range DIFF: n vs n2/n3/n4). ForInit lowers to
		// the setVar-synced while machinery, which matches bash exactly.
		if rhs.kind == "num" && initName == postName &&
			cond.kind == "binop" && cond.BOpKind == "cmp" &&
			cond.lhs.kind == "var" && cond.lhs.name == initName &&
			cond.rhs.kind == "num" {
			if start, err := strconv.Atoi(rhs.text); err == nil {
				if _, err2 := strconv.Atoi(cond.rhs.text); err2 == nil {
					if cond.BOp == "<" || cond.BOp == "<=" {
						return []map[string]any{{
							"type": "ForInit",
							"init": []any{arithAssignStmt(initName, start)},
							"cond": execCond("let", []map[string]any{
								strExpr(initName + cond.BOp + cond.rhs.text),
							}),
							"step": []any{arithIncDecStmt(postName)},
							"body": body,
						}}
					}
				}
			}
		}
		body = append(body, post...)
		return append(pre, map[string]any{
			"type": "While",
			"cond": p.condToJSON(cond),
			"body": body,
		})
	}
	// `for { ... }` — an infinite loop (the golib's parse loops): the
	// While with a constant-true cond.
	if p.atPunct("{") {
		body := p.parseBlockStmts()
		return []map[string]any{{
			"type": "While",
			"cond": testCall("true"),
			"body": body,
		}}
	}
	// for cond {  → While
	cond := p.parseExpr()
	body := p.parseBlockStmts()
	return []map[string]any{{
		"type": "While",
		"cond": p.condToJSON(cond),
		"body": body,
	}}
}

// rangeFor: build the A1 For (with the index COUNTER when the loop is
// `for i, s := range arr` — the go-sh self-hosting contract: i=0 before,
// i++ after the body; the golib's 37 index+value ranges).
func (p *parser) rangeFor(idxVar, v string, iter map[string]any, body []map[string]any) []map[string]any {
	if idxVar == "" {
		return []map[string]any{{"type": "For", "var": v, "iter": iter, "body": body}}
	}
	p.registerVar(idxVar, "Int")
	body = append(body, assignStmt(idxVar,
		arithWrap(arithBin(arithVar(idxVar), "+", arithNum(1)))))
	return []map[string]any{
		assignStmt(idxVar, strExpr("0")),
		{"type": "For", "var": v, "iter": iter, "body": body},
	}
}

func (p *parser) parseSwitch() []map[string]any {
	p.expect(tIdent, "switch")
	p.skipNL()
	// type switch: `switch [v :=] x.(type) {` — runtime type dispatch
	// (core request go-sh-20260813-154009, now implemented in the A1):
	// lowered to the EXISTING Case node with discriminant
	// Call{func:"typeof", args:[getVar x]} (the renderer maps the
	// `typeof` callee to sh2.typeOf) and type-name clause patterns
	// ("*" for default). The guard var v binds to getVar x in every arm
	// (reads resolve through p.varAlias), so a store-backed x dispatches
	// on its text and a lifted x on its runtime value — faithfully.
	if gv, x, ok := p.peekTypeSwitch(); ok {
		p.skipNL()
		p.expect(tPunct, "{")
		var clauses []any
		for {
			p.skipNL()
			if p.atPunct("}") {
				p.pos++
				break
			}
			if p.atIdent("default") {
				p.pos++
				p.expect(tPunct, ":")
				clauses = append(clauses, map[string]any{
					"patterns": []any{"*"},
					"body":     p.parseTypeSwitchBody(gv, x),
				})
			} else if p.atIdent("case") {
				p.pos++
				var pats []any
				pats = append(pats, p.typePattern())
				for p.acceptPunct(",") {
					p.skipNL()
					pats = append(pats, p.typePattern())
				}
				p.expect(tPunct, ":")
				clauses = append(clauses, map[string]any{
					"patterns": pats,
					"body":     p.parseTypeSwitchBody(gv, x),
				})
			} else {
				p.failf("expected case/default in type switch, got %q", p.tok().text)
			}
		}
		return []map[string]any{{
			"type": "Case",
			"discriminant": map[string]any{
				"type": "Call", "func": "typeof",
				"args":   []any{getVarExpr(x)},
				"purity": "PureCpu",
			},
			"clauses": clauses,
		}}
	}
	// boolean switch: `switch { case cond: ... }` — no discriminant
	// (the cpp frontend's lexer dispatch: `switch { case c == ' ': ... }`).
	// The A1 Case node needs a discriminant, so lower to the nested
	// if-else chain — the faithful shape (each case condition is a
	// boolean test; default is the final else).
	if p.atPunct("{") {
		p.pos++
		var conds []*expr
		var bodies [][]map[string]any
		var defBody []map[string]any
		hasDefault := false
		for {
			p.skipNL()
			if p.atPunct("}") {
				p.pos++
				break
			}
			if p.atIdent("default") {
				p.pos++
				p.expect(tPunct, ":")
				defBody = p.parseSwitchBody()
				hasDefault = true
			} else if p.atIdent("case") {
				p.pos++
				c := p.parseExpr()
				p.expect(tPunct, ":")
				conds = append(conds, c)
				bodies = append(bodies, p.parseSwitchBody())
			} else {
				p.failf("expected case/default in boolean switch, got %q", p.tok().text)
			}
		}
		var chain []map[string]any
		for i := len(conds) - 1; i >= 0; i-- {
			elseBody := defBody
			if i < len(conds)-1 {
				elseBody = chain
			}
			chain = []map[string]any{{
				"type":   "If",
				"cond":   p.condToJSON(conds[i]),
				"then":   bodies[i],
				"elsifs": []any{},
				"else":   elseBody,
			}}
		}
		if len(conds) == 0 && hasDefault {
			chain = []map[string]any{{"type": "Block", "body": defBody}}
		}
		return chain
	}
	// value switch (existing path)
	disc := p.parseExpr()
	p.skipNL()
	p.expect(tPunct, "{")
	var clauses []any
	for {
		p.skipNL()
		if p.atPunct("}") {
			p.pos++
			break
		}
		if p.atIdent("default") {
			p.pos++
			p.expect(tPunct, ":")
			clauses = append(clauses, map[string]any{
				"patterns": []any{"*"},
				"body":     p.parseSwitchBody(),
			})
		} else if p.atIdent("case") {
			p.pos++
			var pats []any
			pats = append(pats, p.switchPattern(p.parseExpr()))
			for p.acceptPunct(",") {
				p.skipNL()
				pats = append(pats, p.switchPattern(p.parseExpr()))
			}
			p.expect(tPunct, ":")
			clauses = append(clauses, map[string]any{
				"patterns": pats,
				"body":     p.parseSwitchBody(),
			})
		} else {
			p.failf("expected case/default in switch, got %q", p.tok().text)
		}
	}
	return []map[string]any{{
		"type":         "Case",
		"discriminant": p.exprToWord(disc),
		"clauses":      clauses,
	}}
}

// peekTypeSwitch detects `switch [v :=] x.(type) {` and consumes the
// guard tokens. Returns (guardVar, guardedVar, true) for a type switch
// (guardVar "" for the bare `switch x.(type)` form); anything else
// leaves the token stream untouched and reports false — the value-switch
// path parses the discriminant as an expression.
func (p *parser) peekTypeSwitch() (string, string, bool) {
	i := p.pos
	t := p.toks
	gv := ""
	if i+1 < len(t) && t[i].kind == tIdent && t[i+1].kind == tOp && t[i+1].text == ":=" {
		gv = t[i].text
		i += 2
	}
	if i+4 < len(t) &&
		t[i].kind == tIdent &&
		t[i+1].kind == tPunct && t[i+1].text == "." &&
		t[i+2].kind == tPunct && t[i+2].text == "(" &&
		t[i+3].kind == tIdent && t[i+3].text == "type" &&
		t[i+4].kind == tPunct && t[i+4].text == ")" {
		x := t[i].text
		p.pos = i + 5
		return gv, x, true
	}
	return "", "", false
}

// typePattern: a type-switch case label — a plain type NAME (int,
// string, ...). Composite type lists ([]byte, *T, ...) stay refused
// (Refuse > guess: the Case pattern vocabulary is the type-name string).
func (p *parser) typePattern() string {
	p.skipNL()
	t := p.expect(tIdent, "")
	return t.text
}

// parseTypeSwitchBody parses one arm with the guard var v aliased to the
// guarded var x, so reads of v lower to getVar x (the contract binding).
func (p *parser) parseTypeSwitchBody(gv, x string) []map[string]any {
	if gv != "" {
		old, had := p.varAlias[gv]
		p.varAlias[gv] = x
		defer func() {
			if had {
				p.varAlias[gv] = old
			} else {
				delete(p.varAlias, gv)
			}
		}()
	}
	return p.parseSwitchBody()
}

// resolveVar: reads of a type-switch guard var resolve to the guarded
// var (the v -> x binding); all other names pass through.
func (p *parser) resolveVar(name string) string {
	if t, ok := p.varAlias[name]; ok {
		return t
	}
	return name
}

func (p *parser) parseSwitchBody() []map[string]any {
	out := []map[string]any{}
	for {
		p.skipNL()
		if p.atIdent("case") || p.atIdent("default") || p.atPunct("}") {
			return out
		}
		out = append(out, p.parseStmt()...)
	}
}

// parseGo: go func() { ... }() → Background(Subshell(body)) + wait
func (p *parser) parseGo() []map[string]any {
	p.expect(tIdent, "go")
	p.skipNL()
	if !p.atIdent("func") {
		p.failf("go statement needs a func literal (v2)")
	}
	fn := p.parseFuncLit()
	p.skipNL()
	p.expect(tPunct, "(")
	p.expect(tPunct, ")")
	return []map[string]any{
		{
			"type": "Background",
			"body": []any{map[string]any{"type": "Subshell", "body": fn.body}},
		},
		execStmt("wait", []map[string]any{}, "Spawn"),
	}
}

// ── exec / capture / return lowering ────────────────────────────────

func (p *parser) isSyncBuiltin(cmd string) bool {
	switch cmd {
	case "echo", "printf", "read", "cat", "export", "local", "false", "true":
		return true
	}
	return false
}

func (p *parser) execFromArgs(args []*expr) map[string]any {
	if len(args) == 0 {
		p.failf("exec.Command needs a command name (v2)")
	}
	cmd := args[0].text
	words := make([]map[string]any, 0, len(args)-1)
	for _, a := range args[1:] {
		if a.kind == "str" {
			words = append(words, strExpr(a.text))
		} else {
			p.failf("exec.Command args must be string literals (v2)")
		}
	}
	purity := "Spawn"
	if p.isSyncBuiltin(cmd) {
		purity = "Emulable"
	}
	return execStmt(cmd, words, purity)
}

// captureAssign: x, _ := exec.Command(...).Output() → Assign x =
// capture(Arrow(exec ...))  (the `X=$(echo hi)` shape).
func (p *parser) captureAssign(targets []string, args []*expr) map[string]any {
	name := ""
	for _, tg := range targets {
		if tg != "_" {
			name = tg
			break
		}
	}
	if name == "" {
		p.failf("capture needs a target var (v2)")
	}
	inner := p.execFromArgs(args)
	capture := map[string]any{
		"type": "Call", "func": "capture",
		"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
		"purity": "Spawn",
	}
	p.registerVar(name, "Str")
	return assignStmt(name, capture)
}

// returnToStmt: `return expr` inside a func → echo of expr.
// isBoolExpr — a Go boolean expression (a comparison, a logical
// and/or chain, a negation, or a bool-returning function call): the
// golib's `return isIdentStart(c) || (c >= '0' && c <= '9')`.
func (p *parser) isBoolExpr(e *expr) bool {
	switch e.kind {
	case "binop":
		return true
	case "not":
		return true
	case "call":
		// a bool-returning function call: a USER function/method
		// (isIdentStart/isIdentPart/p.atPunct/...) or a bool-returning
		// stdlib (strings.Contains/HasPrefix/HasSuffix). NOT a
		// string/map returning call like b.String().
		return p.isBoolFnCall(e) ||
			e.callee == "strings.Contains" || e.callee == "strings.HasPrefix" || e.callee == "strings.HasSuffix"
	case "var":
		return e.name == "true" || e.name == "false"
	}
	return false
}

// isBoolFnCall — is the call a USER function or method (the golib's
// isIdentStart/isIdentPart/atIdent/atPunct/...)? Methods (p.atPunct)
// resolve to the method name.
func (p *parser) isBoolFnCall(e *expr) bool {
	fn := e.callee
	if i := strings.LastIndex(fn, "."); i > 0 {
		if p.varTypes[p.resolveVar(fn[:i])] == "Struct" {
			fn = fn[i+1:]
		}
	}
	return p.fnNames[fn]
}

// returnToStmt: `return expr` inside a func → echo of expr. A BOOLEAN
// expression echoes "true"/"false" (the caller's condition test
// compares the captured output — the go-sh self-hosting contract).
func (p *parser) returnToStmt(e *expr) map[string]any {
	if p.isBoolExpr(e) {
		return map[string]any{
			"type": "If",
			"cond": p.condToJSON(e),
			"then": []map[string]any{execStmt("echo", []map[string]any{strExpr("true")}, "Emulable")},
			"elsifs": []any{},
			"else":  []map[string]any{execStmt("echo", []map[string]any{strExpr("false")}, "Emulable")},
		}
	}
	return execStmt("echo", []map[string]any{p.exprToWord(e)}, "Emulable")
}

// ── word lowering ───────────────────────────────────────────────────

func (p *parser) registerVar(name, typ string) {
	if p.inFunc && !p.fnParams[name] && !p.outer[name] && !p.fnLocals[name] {
		p.fnLocals[name] = true
	}
	if p.varTypes[name] == "" || p.varTypes[name] == "Array" {
		p.varTypes[name] = typ
	}
	p.outer[name] = true
}

func (p *parser) wordType(w map[string]any) string {
	switch w["type"] {
	case "Str":
		if v, ok := w["value"].(string); ok {
			if _, err := strconv.ParseInt(v, 10, 64); err == nil {
				return "Int"
			}
		}
		return "Str"
	case "Arith":
		return "Int"
	}
	return "Str"
}

// indexArithText renders an index expression as arithmetic text for the
// runtime's sliceOff (var names resolve via arithExpand; + - * / chains
// and unary minus). Returns "" for anything inexpressible (Refuse >
// guess — the caller fails loudly).
func (p *parser) indexArithText(e *expr) string {
	switch e.kind {
	case "var":
		return p.resolveVar(e.name)
	case "member":
		// a struct field bound (p.pos) — the dotted name; the runtime's
		// arithExpand resolves it via the struct's JSON (the golib's
		// `p.toks[p.pos+1]` cursor arithmetic).
		return strings.TrimPrefix(e.name, ".")
	case "num":
		return e.text
	case "add", "mul":
		l, r := p.indexArithText(e.lhs), p.indexArithText(e.rhs)
		if l == "" || r == "" {
			return ""
		}
		return l + e.op + r
	case "neg":
		l := p.indexArithText(e.lhs)
		if l == "" {
			return ""
		}
		return "-" + l
	}
	return ""
}

// exprToWord lowers an expression to its A1 word JSON.
func (p *parser) exprToWord(e *expr) map[string]any {
	switch e.kind {
	case "str":
		return interpLit(e.text)
	case "rawstr":
		return interpLit(e.text)
	case "char":
		// a Go rune literal is an INTEGER — its ASCII code (the A1 is
		// string-flavored, so the code is the value; `c := ' '` → c =
		// "32", and byte comparisons `c == ' '` → `"$c"="32"`).
		return strExpr(charCode(e))
	case "structlit":
		// a struct VALUE — the JSON object string (jsonNew). The field
		// names: the NAMED fields, or the tracked struct type's fields
		// (positional `tok{"a", "b"}`).
		names := e.fieldNames
		if len(names) == 0 {
			names = p.structFields[e.name]
		}
		if len(names) != len(e.args) {
			p.failf("struct literal %q field count mismatch (v2)", e.name)
		}
		var args []any
		for i, f := range e.args {
			args = append(args, strExpr(names[i]), p.exprToWord(f))
		}
		return map[string]any{
			"type": "Call", "func": "jsonNew",
			"args":   args,
			"purity": "PureCpu",
		}
	case "arrlit":
		// an array literal in an expression position — the jsonArrNew
		// call (carried in rawJSON).
		return e.rawJSON
	case "num":
		return strExpr(e.text)
	case "var":
		if e.name == "nil" {
			return strExpr("")
		}
		// true/false are bool LITERALS, not var reads — `raw := false`
		// must lower to `raw = "false"`, never `raw = $false` (a read
		// of a var named "false", which the runtime resolves to "").
		if e.name == "true" {
			return strExpr("true")
		}
		if e.name == "false" {
			return strExpr("false")
		}
		name := p.resolveVar(e.name)
		if n, ok := p.paramNumber(name); ok {
			return getVarExpr(strconv.Itoa(n))
		}
		return getVarExpr(name)
	case "member":
		// p.tok().line — a field read on a METHOD CALL's result (the
		// golib's parser methods): jsonGet of the captured result.
		if e.memberTarget != nil {
			field := strings.TrimPrefix(e.name, ".")
			return map[string]any{
				"type": "Call", "func": "jsonGet",
				"args":   []any{p.exprToWord(e.memberTarget), strExpr(field)},
				"purity": "PureCpu",
			}
		}
		// c.Args on a stored exec.Command → the Go-style bracketed argv
		// ("[echo hi]"). exec.Command args are string literals by the
		// frontend contract, so the argv is statically known (t53).
		if i := strings.LastIndex(e.name, "."); i > 0 {
			base, field := e.name[:i], e.name[i+1:]
			if field == "Args" {
				if args, ok := p.cmds[base]; ok {
					var b strings.Builder
					b.WriteByte('[')
					for j, a := range args {
						if j > 0 {
							b.WriteByte(' ')
						}
						b.WriteString(a.text)
					}
					b.WriteByte(']')
					return strExpr(b.String())
				}
			}
			// p.toks — a struct field read → the jsonGet helper (the
			// go-sh self-hosting contract: structs are JSON object
			// strings; the receiver is resolved by name — a param holding
			// the receiver's name follows).
			if p.varTypes[p.resolveVar(base)] == "Struct" {
				return map[string]any{
					"type": "Call", "func": "jsonGet",
					"args":   []any{strExpr(p.resolveVar(base)), strExpr(field)},
					"purity": "PureCpu",
				}
			}
		}
		p.failf("bare member %q (v2)", e.name)
	case "add", "mul", "neg":
		return p.arithOrConcat(e)
	case "index":
		// p.varTypes[name] / p.toks[p.pos] — an index into a STRUCT FIELD
		// (the go-sh self-hosting contract: structs are JSON object
		// strings; a map field is a JSON object, an array field a JSON
		// array). Lower to jsonGet(jsonGet(...)) / jsonArrGet(jsonGet(...)).
		if e.target != nil && e.target.kind == "member" {
			if i := strings.LastIndex(e.target.name, "."); i > 0 {
				base, field := e.target.name[:i], e.target.name[i+1:]
				if p.varTypes[p.resolveVar(base)] == "Struct" {
					inner := map[string]any{
						"type": "Call", "func": "jsonGet",
						"args":   []any{strExpr(p.resolveVar(base)), strExpr(field)},
						"purity": "PureCpu",
					}
					key := e.idx1
					if e.idx1e != nil {
						// the index is an EXPRESSION (a struct field read like
						// `p.toks[p.pos]` — the jsonGet call result is the
						// key, which the runtime evalAriths for an array
						// field or uses as a name for a map field).
						key = ""
						if off := p.indexArithText(e.idx1e); off != "" {
							key = off
						} else if e.idx1e.kind == "member" {
							// p.pos — a struct field index: the jsonGet call
							// result is the key (the runtime evalAriths it).
							return map[string]any{
								"type": "Call", "func": "jsonGet",
								"args":   []any{inner, p.exprToWord(e.idx1e)},
								"purity": "PureCpu",
							}
						} else {
							p.failf("struct field index must be literal or arith (v2)")
						}
					}
					return map[string]any{
						"type": "Call", "func": "jsonGet",
						"args":   []any{inner, strExpr(key)},
						"purity": "PureCpu",
					}
				}
			}
		}
		// m["key"] on a map var → assocGet (the runtime's by-name
		// associative-array read; t54).
		if e.target != nil && e.target.kind == "var" && p.maps[e.target.name] {
			key := ""
			if e.idx1e != nil {
				if e.idx1e.kind != "str" && e.idx1e.kind != "num" {
					p.failf("map key must be a literal (v2)")
				}
				key = e.idx1e.text
			} else {
				key = e.idx1
			}
			return map[string]any{
				"type": "Call", "func": "assocGet",
				"args":   []any{strExpr(e.target.name), strExpr(key)},
				"purity": "Emulable",
			}
		}
		if e.idx1e != nil {
			// src[i] with a VAR/arith index — byte access (the golib's
			// lexer: `src[i+1] == '/'`). The index renders as arithmetic
			// text (the runtime's byteAt evalAriths it).
			if e.target != nil && e.target.kind == "var" && p.varTypes[p.resolveVar(e.target.name)] != "Array" {
				if off := p.indexArithText(e.idx1e); off != "" {
					return map[string]any{
						"type": "Call", "func": "byteAt",
						"args":   []any{strExpr(p.resolveVar(e.target.name)), strExpr(off)},
						"purity": "PureCpu",
					}
				}
			}
			p.failf("index key must be a number literal (v2)")
		}
		if e.target != nil && e.target.kind == "var" {
			// src[i] — Go byte access into a string (the golib's lexer:
			// `c := src[i]`). Go bytes are INTEGERS (the ASCII code); the
			// A1 is string-flavored, so the value is the code as a string.
			// Lower to the sh2.byteAt helper (the runtime resolves the var
			// name and the arithmetic index; the same text form renders
			// inside test strings).
			if p.varTypes[p.resolveVar(e.target.name)] == "Array" {
				return joinCall(paramCall("", e.target.name+"["+e.idx1+"]"))
			}
			return map[string]any{
				"type": "Call", "func": "byteAt",
				"args":   []any{strExpr(p.resolveVar(e.target.name)), strExpr(e.idx1)},
				"purity": "PureCpu",
			}
		}
		p.failf("index target must be a var (v2)")
	case "slice":
		if e.target != nil && e.target.kind == "var" {
			return p.sliceWord(e)
		}
		p.failf("slice target must be a var (v2)")
	case "strlen":
		if e.target != nil && e.target.kind == "var" {
			return paramCall("len", e.target.name)
		}
		// len("lit") — folded at emit time with Go's byte-length
		// semantics, matching the native run exactly (t75).
		if e.target != nil && (e.target.kind == "str" || e.target.kind == "rawstr") {
			return strExpr(strconv.Itoa(len(e.target.text)))
		}
		p.failf("len() arg must be a var (v2)")
	case "arrlen":
		if e.target != nil && e.target.kind == "var" {
			return joinCall(paramCall("slice", "#"+e.target.name, "@", ""))
		}
		p.failf("len() of array must be a var (v2)")
	case "call":
		// a call to a USER-DEFINED function (the golib's A1 builders:
		// strExpr, getVarExpr, testCall, ...) — the capture of exec (the
		// function echoes its return value; the caller captures it).
		if p.fnNames[e.callee] {
			var words []map[string]any
			for _, a := range e.args {
				words = append(words, p.exprToWord(a))
			}
			inner := execStmt(e.callee, words, "Spawn")
			return map[string]any{
				"type": "Call", "func": "capture",
				"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
				"purity": "Spawn",
			}
		}
		// p.parseExpr() — a METHOD call on a struct (the go-sh
		// self-hosting contract): the receiver is passed BY NAME; the
		// method's echoed return is captured (the value twin of the
		// statement form).
		if i := strings.LastIndex(e.callee, "."); i > 0 {
			base, method := e.callee[:i], e.callee[i+1:]
			if p.varTypes[p.resolveVar(base)] == "Struct" {
				var words []map[string]any
				words = append(words, strExpr(p.resolveVar(base)))
				for _, a := range e.args {
					words = append(words, p.exprToWord(a))
				}
				inner := execStmt(method, words, "Spawn")
				return map[string]any{
					"type": "Call", "func": "capture",
					"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
					"purity": "Spawn",
				}
			}
		}
		switch e.callee {
		case "os.Getenv":
			if len(e.args) == 1 && e.args[0].kind == "str" {
				return getVarExpr(e.args[0].text)
			}
			p.failf("os.Getenv needs a string literal (v2)")
		case "strings.ReplaceAll":
			// ReplaceAll(s, old, new) → ${s//old/new} (ALL occurrences)
			if len(e.args) == 3 && e.args[0].kind == "var" &&
				e.args[1].kind == "str" && e.args[2].kind == "str" {
				return paramCall("//", e.args[0].name, e.args[1].text, e.args[2].text)
			}
			p.failf("strings.ReplaceAll needs (var, str, str) (v2)")
		case "strings.TrimPrefix":
			// TrimPrefix(s, p) → ${s#p} — remove ONE leading literal
			// (bash `#` strips a single occurrence; a glob-metachar p
			// would glob-match in the shell, so literals only).
			if len(e.args) == 2 {
				if e.args[0].kind == "var" && e.args[1].kind == "str" {
					return paramCall("#", e.args[0].name, e.args[1].text)
				}
				if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
					return strExpr(w)
				}
			}
			p.failf("strings.TrimPrefix needs (var|str, str) (v2)")
		case "strings.TrimSuffix":
			// TrimSuffix(s, p) → ${s%p} — remove ONE trailing literal.
			if len(e.args) == 2 {
				if e.args[0].kind == "var" && e.args[1].kind == "str" {
					return paramCall("%", e.args[0].name, e.args[1].text)
				}
				if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
					return strExpr(w)
				}
			}
			p.failf("strings.TrimSuffix needs (var|str, str) (v2)")
		case "strings.Join":
			// Join(arr[lo:hi], " ") → ${arr[@]:lo:len} joined with a space
			// — exactly the A1 join(param("slice", …)) shape (the runtime
			// joins arrays with " ", matching Go's space separator).
			if len(e.args) == 2 && e.args[1].kind == "str" && e.args[1].text == " " {
				if e.args[0].kind == "slice" && e.args[0].target != nil && e.args[0].target.kind == "var" {
					return p.sliceWord(e.args[0])
				}
				// Join(arr, " ") on a FULL array → the `${arr[@]}` join
				// shape (join(param("slice", name, "@", "")) — the runtime's
				// arrayItems + space join; the app's
				// `strings.Join(redirects, " ")` form).
				if e.args[0].kind == "var" && p.varTypes[e.args[0].name] == "Array" {
					return joinCall(paramCall("slice", e.args[0].name, "@", ""))
				}
			}
			// Any other separator ("\n" — the app's dominant form,
			// bat-sh-go's `strings.Join(bodyLines, "\n")`) has no
			// separator arg on the A1 join — declared boundary
			// (core-requests go-sh-dogfood-20260815 §8); refuse loudly.
			p.failf(`strings.Join needs (arr[lo:hi]|arr, " ") (v2)`)
		case "filepath.Dir", "filepath.Ext":
			// Pure path ops on string literals — folded at emit time with
			// exact Go stdlib semantics (t55).
			if len(e.args) == 1 && e.args[0].kind == "str" {
				if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
					return strExpr(w)
				}
			}
			p.failf("%s needs a string literal (v2)", e.callee)
		case "strings.HasPrefix":
			if len(e.args) == 2 {
				if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
					return strExpr(w)
				}
			}
			p.failf("strings.HasPrefix needs (str, str) literals (v2)")
		case "strings.ToUpper", "strings.ToLower":
			// var arg → the `${s,,}` / `${s^^}` param ops
			// (LowercaseAll/UppercaseAll — byte-identical to the core's
			// `param(",," name)` / `param("^^" name)` lowering of the
			// shell expansions; the runtime folds to toLowerCase /
			// toUpperCase). Literals keep folding at emit time (t71).
			if len(e.args) == 1 && e.args[0].kind == "var" {
				op := ",,"
				if e.callee == "strings.ToUpper" {
					op = "^^"
				}
				return paramCall(op, e.args[0].name)
			}
			if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
				return strExpr(w)
			}
			p.failf("%s needs (var|str) args (v2)", e.callee)
		case "strings.Contains", "strings.HasSuffix":
			if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
				return strExpr(w)
			}
			p.failf("%s needs literal args (v2)", e.callee)
		case "fmt.Sprintf":
			// Sprintf over literals folds at emit time with the Go stdlib
			// (t74).
			if w, ok := foldPureLiteralCall(e.callee, e.args); ok {
				return strExpr(w)
			}
			// Var-arg Sprintf with a LITERAL format → the command-
			// substitution shape `$(printf FMT ARGS...)` —
			// capture(Arrow[exec printf …]): the value twin of the
			// fmt.Printf statement path (printfStmt delegates the same
			// format verbs to the runtime printf; t46), and the A1's
			// capture of an Emulable printf is the shell-flavored
			// formatted-string value. A NON-literal format (a var
			// format, `args...` variadics — the app's go-sh.go failf)
			// stays refused: the format string is the verb contract.
			if len(e.args) >= 2 && e.args[0].kind == "str" {
				var words []map[string]any
				words = append(words, interpLit(e.args[0].raw))
				for _, a := range e.args[1:] {
					words = append(words, p.exprToWord(a))
				}
				inner := execStmt("printf", words, "Emulable")
				return map[string]any{
					"type": "Call", "func": "capture",
					"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
					"purity": "Spawn",
				}
			}
			// a VAR format (`fmt.Sprintf(f, args...)` — the golib's failf
			// error paths): the capture of printf with the var format (the
			// runtime's printfFormat handles it).
			if len(e.args) >= 1 && e.args[0].kind == "var" {
				var words []map[string]any
				words = append(words, p.exprToWord(e.args[0]))
				for _, a := range e.args[1:] {
					words = append(words, p.exprToWord(a))
				}
				inner := execStmt("printf", words, "Emulable")
				return map[string]any{
					"type": "Call", "func": "capture",
					"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
					"purity": "Spawn",
				}
			}
			p.failf("fmt.Sprintf needs a literal format (v2)")
		}
		// err.Error() — the error value's message. The A1 has no error
		// objects; an error return is dropped at the call site, so err
		// is a plain var whose value IS the message (in the expressible
		// subset it is never assigned — the error branch is dead code).
		if strings.HasSuffix(e.callee, ".Error") && len(e.args) == 0 {
			base := strings.TrimSuffix(e.callee, ".Error")
			return getVarExpr(base)
		}
		// sc.Text() inside a scanner read-loop → the read var
		if strings.HasSuffix(e.callee, ".Text") {
			base := strings.TrimSuffix(e.callee, ".Text")
			if p.stdinRdr[base] {
				return getVarExpr(base)
			}
		}
		// b.String() on a bytes.Buffer → the accumulated contents
		if strings.HasSuffix(e.callee, ".String") {
			base := strings.TrimSuffix(e.callee, ".String")
			if _, ok := p.bufs[base]; ok {
				if p.runtimeBufs[base] {
					// a runtime-dependent buffer — read the runtime value
					return getVarExpr(base)
				}
				return strExpr(p.bufs[base])
			}
		}
		p.failf("unsupported call %q in word position (v2)", e.callee)
	case "binop":
		p.failf("comparison in word position (v2)")
	}
	p.failf("unsupported expression %q (v2 subset)", e.kind)
	return nil
}

// foldPureLiteralCall folds a pure stdlib call over string/number
// literals at emit time, returning the Go-stdlib-exact result string.
// The frontend's literal-only contract makes this deterministic (t55,
// t57); non-literal args keep the shell-shape paths above.
func foldPureLiteralCall(callee string, args []*expr) (string, bool) {
	for _, a := range args {
		if a.kind != "str" && a.kind != "num" {
			return "", false
		}
	}
	s := func(i int) string { return args[i].text }
	switch callee {
	case "filepath.Dir":
		if len(args) == 1 {
			return filepath.Dir(s(0)), true
		}
	case "filepath.Ext":
		if len(args) == 1 {
			return filepath.Ext(s(0)), true
		}
	case "strings.TrimPrefix":
		if len(args) == 2 {
			return strings.TrimPrefix(s(0), s(1)), true
		}
	case "strings.TrimSuffix":
		if len(args) == 2 {
			return strings.TrimSuffix(s(0), s(1)), true
		}
	case "strings.ToUpper":
		if len(args) == 1 {
			return strings.ToUpper(s(0)), true
		}
	case "strings.ToLower":
		if len(args) == 1 {
			return strings.ToLower(s(0)), true
		}
	case "strings.Contains":
		if len(args) == 2 {
			return fmt.Sprintf("%t", strings.Contains(s(0), s(1))), true
		}
	case "strings.HasSuffix":
		if len(args) == 2 {
			return fmt.Sprintf("%t", strings.HasSuffix(s(0), s(1))), true
		}
	case "strings.ReplaceAll":
		if len(args) == 3 {
			return strings.ReplaceAll(s(0), s(1), s(2)), true
		}
	case "fmt.Sprintf":
		if len(args) >= 1 && args[0].kind == "str" {
			rest := make([]any, 0, len(args)-1)
			for _, a := range args[1:] {
				if a.kind == "num" {
					if n, err := strconv.ParseInt(a.text, 0, 64); err == nil {
						rest = append(rest, n)
						continue
					}
				}
				rest = append(rest, a.text)
			}
			return fmt.Sprintf(args[0].text, rest...), true
		}
	case "strings.HasPrefix":
		if len(args) == 2 {
			return fmt.Sprintf("%t", strings.HasPrefix(s(0), s(1))), true
		}
	}
	return "", false
}

// sliceWord lowers a slice expr to its A1 word JSON. A1 slice args are
// (var, start, LENGTH) — Go's [i:j] end index is EXCLUSIVE, so emit
// length = j - i (t37; matches the ${s:off:len} shape the core emits).
// Open ends: `s[:j]` starts at 0; `s[i:]` / `s[:]` carry no length (the
// runtime renders that as `v.slice(off)` — the ${s:off} shape).
//
// Computed bounds (Go index expressions) lower to the parameter-
// expansion glob ops, which are EXACT for literal needles:
//   - x[strings.LastIndex(x, n)+1:] → ${x##*n} — the longest-prefix
//     removal of `*n` strips through the LAST occurrence of n, which is
//     precisely Go's LastIndex(n)+1 tail.
//   - x[:strings.Index(x, n)] → ${x%%n*} — the longest-suffix removal
//     of `n*` strips from the FIRST occurrence of n, Go's exclusive
//     end index.
func (p *parser) sliceWord(e *expr) map[string]any {
	name := e.target.name
	// x[strings.LastIndex(x, n)+1:] → param("##", x, "*"+n)
	if e.idx2e == nil && e.idx2 == "" && e.idx1e != nil {
		if v, n, ok := p.lastIndexPlusOne(e.idx1e); ok && v == name {
			return paramCall("##", name, "*"+n)
		}
	}
	// x[:strings.Index(x, n)] → param("%%", x, n+"*")
	if e.idx1e == nil && e.idx1 == "" && e.idx2e != nil {
		if v, n, ok := p.indexCall(e.idx2e); ok && v == name {
			return paramCall("%%", name, n+"*")
		}
	}
	if e.idx1e != nil || e.idx2e != nil {
		// computed bounds — the `${src:off:len}` slice with arithmetic
		// text (the golib's `src[start+1 : i]`; the runtime's sliceOff
		// evalAriths the offset and length).
		off := "0"
		if e.idx1e != nil {
			if t := p.indexArithText(e.idx1e); t != "" {
				off = t
			} else {
				p.failf("unsupported computed slice bound (v2)")
			}
		} else if e.idx1 != "" {
			off = e.idx1
		}
		length := ""
		if e.idx2e != nil {
			if t := p.indexArithText(e.idx2e); t != "" {
				length = t + "-(" + off + ")"
			} else {
				p.failf("unsupported computed slice bound (v2)")
			}
		} else if e.idx2 != "" {
			length = e.idx2 + "-(" + off + ")"
		}
		return joinCall(paramCall("slice", name, off, length))
	}
	lo, loErr := strconv.Atoi(e.idx1)
	hi, hiErr := strconv.Atoi(e.idx2)
	switch {
	case e.idx2 == "":
		start := e.idx1
		if e.idx1 == "" {
			start = "0"
		}
		return joinCall(paramCall("slice", name, start, ""))
	case loErr == nil && hiErr == nil:
		return joinCall(paramCall("slice", name,
			strconv.Itoa(lo), strconv.Itoa(hi-lo)))
	}
	return joinCall(paramCall("slice", name, e.idx1, e.idx2))
}

// lastIndexPlusOne: matches `strings.LastIndex(v, "n") + 1` (the Go
// idiom for "one past the last occurrence") → (v, n).
func (p *parser) lastIndexPlusOne(e *expr) (string, string, bool) {
	if e == nil || e.kind != "add" || e.op != "+" {
		return "", "", false
	}
	if e.rhs == nil || e.rhs.kind != "num" || e.rhs.text != "1" {
		return "", "", false
	}
	if e.lhs == nil || e.lhs.kind != "call" || e.lhs.callee != "strings.LastIndex" {
		return "", "", false
	}
	// (same (var, str) arg shape as Index)
	if len(e.lhs.args) != 2 || e.lhs.args[0].kind != "var" || e.lhs.args[1].kind != "str" {
		return "", "", false
	}
	return e.lhs.args[0].name, e.lhs.args[1].text, true
}

// indexCall: matches `strings.Index(v, "n")` → (v, n). Literal n only —
// a glob-metachar needle would change meaning under the `%`/`#` ops.
func (p *parser) indexCall(e *expr) (string, string, bool) {
	if e == nil || e.kind != "call" || e.callee != "strings.Index" {
		return "", "", false
	}
	if len(e.args) != 2 || e.args[0].kind != "var" || e.args[1].kind != "str" {
		return "", "", false
	}
	return e.args[0].name, e.args[1].text, true
}

// arithOrConcat: `+` chains involving a string (literal, Str-typed var,
// index/param — anything non-numeric) → concat Interpolate, flattening
// the whole add chain into parts; otherwise (and all * / -) → Arith.
func (p *parser) arithOrConcat(e *expr) map[string]any {
	if e.kind == "add" && p.addHasString(e) {
		var parts []any
		p.addConcatParts(e, &parts)
		return interpParts(parts)
	}
	return arithWrap(p.exprToArith(e))
}

// addHasString: does this add chain involve a string anywhere? (An
// `a[0] + " " + a[1]` chain has no direct string operand at the top
// level, so a shallow check misses it.)
func (p *parser) addHasString(e *expr) bool {
	switch e.kind {
	case "add":
		return p.addHasString(e.lhs) || p.addHasString(e.rhs)
	case "str", "rawstr":
		return true
	case "var":
		return e.name == "nil" || p.varTypes[p.resolveVar(e.name)] == "Str"
	}
	return false
}

// addConcatParts: flatten a concat chain into parts; a sub-chain that
// is itself pure arithmetic stays a single expr part (its Arith value).
func (p *parser) addConcatParts(e *expr, parts *[]any) {
	if e.kind == "add" && p.addHasString(e) {
		p.addConcatParts(e.lhs, parts)
		p.addConcatParts(e.rhs, parts)
		return
	}
	*parts = append(*parts, p.concatPart(e))
}

func (p *parser) concatPart(e *expr) any {
	switch e.kind {
	case "str":
		return partLit(e.text)
	case "num":
		return partLit(e.text)
	default:
		return partExpr(p.exprToWord(e))
	}
}

// operandKind: "str" for string literals and Str-typed vars, "int" for
// numeric operands.
func (p *parser) operandKind(e *expr) string {
	switch e.kind {
	case "str", "rawstr":
		return "str"
	case "num":
		return "int"
	case "var":
		if e.name == "nil" {
			return "str"
		}
		switch p.varTypes[p.resolveVar(e.name)] {
		case "Str":
			return "str"
		case "Int":
			return "int"
		}
		return "int"
	}
	return "int"
}

func (p *parser) exprToArith(e *expr) map[string]any {
	switch e.kind {
	case "num":
		if n, err := strconv.Atoi(e.text); err == nil {
			return arithNum(n)
		}
		p.failf("non-integer numeric literal %q (v2)", e.text)
	case "var":
		name := p.resolveVar(e.name)
		if n, ok := p.paramNumber(name); ok {
			return arithVar(strconv.Itoa(n))
		}
		return arithVar(name)
	case "add", "mul":
		return arithBin(p.exprToArith(e.lhs), e.op, p.exprToArith(e.rhs))
	case "neg":
		return arithBin(arithNum(0), "-", p.exprToArith(e.lhs))
	case "str":
		if n, err := strconv.Atoi(e.text); err == nil {
			return arithNum(n)
		}
	}
	p.failf("non-numeric operand in arithmetic (v2): %s", e.kind)
	return nil
}

func (p *parser) exprToArrayElem(e *expr) map[string]any {
	switch e.kind {
	case "str", "num", "var":
		return strExpr(e.text)
	case "char":
		return strExpr(charCode(e))
	default:
		// a call/structlit/arrlit element (the golib's A1 builders'
		// `[]any{strExpr(name)}`) — the lowered word (a JSON fragment).
		return p.exprToWord(e)
	}
}

func (p *parser) switchPattern(e *expr) string {
	switch e.kind {
	case "str", "num", "var":
		return e.text
	case "char":
		// a Go rune literal is an INTEGER — its ASCII code (the golib's
		// `switch raw[i] { case 'n': ... }` escape decoder).
		return charCode(e)
	}
	p.failf("unsupported case pattern (v2)")
	return ""
}

// ── condition lowering ──────────────────────────────────────────────

// hasMemberCall — does the expr contain a member-on-call (`p.tok().kind`)?
func (p *parser) hasMemberCall(e *expr) bool {
	if e == nil {
		return false
	}
	if e.kind == "member" && e.memberTarget != nil {
		return true
	}
	return p.hasMemberCall(e.lhs) || p.hasMemberCall(e.rhs) || p.hasMemberCall(e.target) ||
		p.hasMemberCall(e.idx1e) || p.hasMemberCall(e.idx2e)
}

// hoistMemberCalls — rewrite member-on-call exprs to temp-var jsonGet
// reads, emitting the assign-capture into pre (the cond's BinOp And
// chain runs the assigns before the test).
func (p *parser) hoistMemberCalls(e *expr, pre *[]map[string]any) *expr {
	if e == nil {
		return nil
	}
	if e.kind == "member" && e.memberTarget != nil {
		tmp := fmt.Sprintf("__t%d", p.tmpSeq)
		p.tmpSeq++
		field := strings.TrimPrefix(e.name, ".")
		capture := p.exprToWord(e.memberTarget) // the method-call capture
		*pre = append(*pre, map[string]any{
			"type": "Call", "func": "assign",
			"args":   []any{strExpr(tmp), strExpr("="), capture},
			"purity": "Emulable",
		})
		return &expr{kind: "member", name: tmp + "." + field}
	}
	e.lhs = p.hoistMemberCalls(e.lhs, pre)
	e.rhs = p.hoistMemberCalls(e.rhs, pre)
	e.target = p.hoistMemberCalls(e.target, pre)
	e.idx1e = p.hoistMemberCalls(e.idx1e, pre)
	e.idx2e = p.hoistMemberCalls(e.idx2e, pre)
	return e
}

func (p *parser) condToJSON(c *expr) map[string]any {
	if c.kind == "cond" {
		return testCall(c.text)
	}
	if c.kind == "rawjson" {
		return c.rawJSON
	}
	// a comparison with a member-on-call operand (`p.tok().kind ==
	// tNL` — the golib's parser methods): hoist the method call into a
	// temp via the assign-capture, then test the temp's field — the
	// BinOp And of the assign and the test (the assign runs as part of
	// the cond; the And's status is the test's).
	if c.kind == "binop" && c.BOpKind == "cmp" {
		if p.hasMemberCall(c.lhs) || p.hasMemberCall(c.rhs) {
			var pre []map[string]any
			l := p.hoistMemberCalls(c.lhs, &pre)
			r := p.hoistMemberCalls(c.rhs, &pre)
			// the rewritten comparison
			test := testCall(p.condTestString(&expr{kind: "binop", BOp: c.BOp, BOpKind: "cmp", lhs: l, rhs: r}))
			// chain the pre-assigns with the test via And
			out := test
			for i := len(pre) - 1; i >= 0; i-- {
				out = map[string]any{
					"type": "BinOp", "op": "And",
					"lhs": pre[i], "rhs": out,
				}
			}
			return out
		}
	}
	// a bool-returning function call in a condition (the golib's
	// `isIdentPart(src[i])`): capture the function's echoed
	// "true"/"false" into a temp, then test it — the BinOp And of the
	// assign-capture and the test (the assign runs as part of the cond;
	// the And's status is the test's).
	// only USER-DEFINED functions AND methods (stdlib strings.* has
	// specific glob-test lowering below).
	if c.kind == "call" {
		fn := c.callee
		rec := ""
		if i := strings.LastIndex(fn, "."); i > 0 {
			if p.varTypes[p.resolveVar(fn[:i])] == "Struct" {
				// a METHOD call (`p.atPunct("(")`) — the receiver passed
				// by name, the method name in fnNames.
				rec = p.resolveVar(fn[:i])
				fn = fn[i+1:]
			}
		}
		if p.fnNames[fn] {
			tmp := fmt.Sprintf("__r%d", p.tmpSeq)
			p.tmpSeq++
			var words []map[string]any
			if rec != "" {
				words = append(words, strExpr(rec))
			}
			for _, a := range c.args {
				words = append(words, p.exprToWord(a))
			}
			inner := execStmt(fn, words, "Spawn")
			capture := map[string]any{
				"type": "Call", "func": "capture",
				"args":   []any{map[string]any{"type": "Arrow", "body": []any{inner}}},
				"purity": "Spawn",
			}
			assignCall := map[string]any{
				"type": "Call", "func": "assign",
				"args":   []any{strExpr(tmp), strExpr("="), capture},
				"purity": "Emulable",
			}
			return map[string]any{
				"type": "BinOp", "op": "And",
				"lhs": assignCall,
				"rhs": testCall(`"$` + tmp + `"="true"`),
			}
		}
	}
	if c.kind == "not" {
		// !(a && b) → (!a) || (!b) — De Morgan: the test-string form
		// can't express a nested and/or (the cpp frontend's
		// `!(src[i] == '*' && src[i+1] == '/')` loop guards).
		if c.lhs.kind == "binop" && c.lhs.BOpKind == "and" {
			return map[string]any{
				"type": "BinOp", "op": "Or",
				"lhs": p.condToJSON(&expr{kind: "not", lhs: c.lhs.lhs}),
				"rhs": p.condToJSON(&expr{kind: "not", lhs: c.lhs.rhs}),
			}
		}
		if c.lhs.kind == "binop" && c.lhs.BOpKind == "or" {
			return map[string]any{
				"type": "BinOp", "op": "And",
				"lhs": p.condToJSON(&expr{kind: "not", lhs: c.lhs.lhs}),
				"rhs": p.condToJSON(&expr{kind: "not", lhs: c.lhs.rhs}),
			}
		}
		return testCall("! " + strings.TrimSpace(p.condTestString(c.lhs)))
	}
	if c.kind == "binop" && c.BOpKind == "and" {
		return map[string]any{
			"type": "BinOp", "op": "And",
			"lhs": p.condToJSON(c.lhs), "rhs": p.condToJSON(c.rhs),
		}
	}
	if c.kind == "binop" && c.BOpKind == "or" {
		return map[string]any{
			"type": "BinOp", "op": "Or",
			"lhs": p.condToJSON(c.lhs), "rhs": p.condToJSON(c.rhs),
		}
	}
	// strings.Contains(s, p) → the `[[ $s == *p* ]]` glob-test shape —
	// the HasPrefix/Suffix precedent (`"$s"=h*`): operand quoted, the
	// pattern bare so the glob engine sees it (t68). The `test` call
	// records its verdict in sh2.lastExit, so it works as a BinOp
	// And/Or operand too; the `contains` call (PureCpu → native
	// String.includes) does NOT set lastExit, and the core's native
	// &&/|| lowering of a pure operand branches on the STALE status —
	// t68 was silently dropping the `alt2` branch.
	if c.kind == "call" && c.callee == "strings.Contains" {
		if len(c.args) != 2 || c.args[1].kind != "str" {
			p.failf("strings.Contains needs (haystack, str needle) (v2)")
		}
		hk := c.args[0].kind
		if hk != "var" && hk != "str" && hk != "num" {
			p.failf("strings.Contains haystack must be var|str|num (v2): %s", hk)
		}
		return testCall(p.condOperandQ(c.args[0]) + "=*" + c.args[1].text + "*")
	}
	// strings.HasPrefix(s, p) / strings.HasSuffix(s, p) → the `[[ $s ==
	// p* ]]` / `[[ $s == *p ]]` glob-test shape (the core's `$s==p*`
	// string; the operand stays quoted, the pattern bare so the glob
	// engine sees it — t68). The golib's lexer uses a SLICE haystack and
	// a VAR pattern (`strings.HasPrefix(src[i:], op)`), both supported.
	if c.kind == "call" && (c.callee == "strings.HasPrefix" || c.callee == "strings.HasSuffix") {
		if len(c.args) != 2 {
			p.failf("%s needs two args (v2)", c.callee)
		}
		// the haystack: a var OR a slice (`src[i:]`)
		hay := ""
		switch c.args[0].kind {
		case "var":
			hay = p.condOperandQ(c.args[0])
		case "slice":
			e := c.args[0]
			off := e.idx1
			if e.idx1e != nil {
				if t := p.indexArithText(e.idx1e); t != "" {
					off = t
				} else if e.idx1e.kind == "member" {
					off = e.idx1e.name
				} else {
					p.failf("HasPrefix slice bound must be literal/arith (v2)")
				}
			}
			if e.target == nil || e.target.kind != "var" {
				p.failf("HasPrefix slice target must be a var (v2)")
			}
			hay = `"${` + p.resolveVar(e.target.name) + `:` + off + `}"`
		default:
			p.failf("%s haystack must be var|slice (v2)", c.callee)
		}
		// the pattern: a literal OR a var (`op` — the loop var)
		pat := ""
		switch c.args[1].kind {
		case "str":
			pat = c.args[1].text
		case "var":
			pat = `"$` + p.resolveVar(c.args[1].name) + `"`
		default:
			p.failf("%s pattern must be literal/var (v2)", c.callee)
		}
		if c.callee == "strings.HasSuffix" {
			pat = "*" + pat
		} else {
			pat = pat + "*"
		}
		return testCall(hay + "=" + pat)
	}
	// a bare map-index / struct-member used as a TRUTHINESS (the golib's
	// `exprBoundary[t.text]` / `t.kind` conditions): the value's boolean
	// reading — `"$(sh2.assocGet …)"="true"` / `"$(sh2.jsonGet …)"="true"`
	// (Go maps/fields carry true/false values; the runtime resolves dotted
	// member keys via the nested cmdsub handler).
	if c.kind == "index" && c.target != nil && c.target.kind == "var" && p.maps[c.target.name] {
		key := ""
		if c.idx1e != nil {
			if c.idx1e.kind == "member" {
				key = `$(sh2.jsonGet(` + strings.TrimPrefix(c.idx1e.name, ".") + `))`
			} else if off := p.condTestText(c.idx1e); off != "" {
				key = off
			} else {
				p.failf("map-index cond key must be member/literal (v2)")
			}
		} else {
			key = c.idx1
		}
		return testCall(`"$(sh2.assocGet(` + p.resolveVar(c.target.name) + `, ` + key + `))"="true"`)
	}
	if c.kind == "member" && c.memberTarget == nil {
		if i := strings.LastIndex(c.name, "."); i > 0 {
			return testCall(`"$(sh2.jsonGet(` + p.resolveVar(c.name[:i]) + `, ` + c.name[i+1:] + `))"="true"`)
		}
	}
	return testCall(p.condTestString(c))
}

// assocHasCall — `Call{func:"assocHas", args:[Str(map), <key-word>]}` — the
// presence check (the golib's `_, ok := m[k]` if-inits); the key word is
// resolved at runtime (expandWord handles member/call cmdsubs).
func assocHasCall(name string, key map[string]any) map[string]any {
	return map[string]any{
		"type": "Call", "func": "assocHas",
		"args":   []any{strExpr(name), key},
		"purity": "PureCpu",
	}
}

// condTestText — a bare value as a cmdsub key/name (var/num/str without
// quotes — the runtime resolves them by name).
func (p *parser) condTestText(e *expr) string {
	switch e.kind {
	case "var":
		return p.resolveVar(e.name)
	case "num", "str":
		return e.text
	}
	return ""
}

// condTestString renders a comparison as the core's [ ] argument string
// (the `"$X"="1"` / `1 -lt 2` / ` -z "$X"` shapes).
func (p *parser) condTestString(c *expr) string {
	if c.kind == "cond" {
		return c.text
	}
	if c.kind == "not" {
		return "! " + strings.TrimSpace(p.condTestString(c.lhs))
	}
	// bare var condition: `if raw` where raw is a bool var → the
	// `"$raw"="true"` test (Go conditions are bools; the var holds
	// "true"/"false" — the `if !raw` twin wraps this in the Not
	// prefix above). `if true` / `if false` are the literal tests.
	if c.kind == "var" {
		if c.name == "true" {
			return "true"
		}
		if c.name == "false" {
			return "false"
		}
		return p.condOperandQ(c) + "=" + `"true"`
	}
	if c.kind != "binop" {
		p.failf("unsupported condition (v2): %s", c.kind)
	}
	l, r := c.lhs, c.rhs
	// err == nil / err != nil → $? tests
	if l.kind == "var" && l.name == "err" && r.kind == "var" && r.name == "nil" {
		if c.BOp == "==" {
			return `"$?" -eq 0`
		}
		return `"$?" -ne 0`
	}
	ls, rs := p.condOperandQ(l), p.condOperandQ(r)
	switch c.BOp {
	case "==":
		if r.kind == "str" && r.text == "" {
			return " -z " + p.condOperandArg(l)
		}
		return ls + "=" + rs
	case "!=":
		if r.kind == "str" && r.text == "" {
			return " -n " + p.condOperandArg(l)
		}
		return ls + "!=" + rs
	case "<":
		return p.condOperandArg(l) + " -lt " + p.condOperandArg(r)
	case "<=":
		return p.condOperandArg(l) + " -le " + p.condOperandArg(r)
	case ">":
		return p.condOperandArg(l) + " -gt " + p.condOperandArg(r)
	case ">=":
		return p.condOperandArg(l) + " -ge " + p.condOperandArg(r)
	}
	p.failf("unsupported comparison %q (v2)", c.BOp)
	return ""
}

// charCode — the ASCII code of a Go rune literal (a char expr's text
// is the decoded char; Go chars are integers, so the code is the value
// in byte contexts).
func charCode(e *expr) string {
	if e == nil || e.text == "" {
		return "0"
	}
	return strconv.Itoa(int(e.text[0]))
}

// condOperandQ: `==`/`!=` operand — quoted: `"$x"` / `"lit"` / `"42"`.
func (p *parser) condOperandQ(e *expr) string {
	switch e.kind {
	case "var":
		return `"$` + p.resolveVar(e.name) + `"`
	case "str":
		return `"` + e.text + `"`
	case "char":
		// a Go rune literal is an INTEGER — its ASCII code (the golib's
		// `c == ' '` byte tests; c holds the code from byteAt).
		return `"` + charCode(e) + `"`
	case "num":
		return `"` + e.text + `"`
	case "call":
		// src[i] in a comparison — the sh2.byteAt helper rendered in a
		// cmdsub word (the runtime's test evaluator resolves it in JS).
		if e.callee == "byteAt" && len(e.args) == 2 {
			return `"$(sh2.byteAt(` + e.args[0].text + `, ` + e.args[1].text + `))"`
		}
		p.failf("unsupported comparison operand (v2): %s", e.kind)
	case "member":
		// t.kind — a struct field read in a comparison (the golib's
		// `t.kind != tEOF` token-kind tests): the jsonGet helper in a
		// cmdsub word.
		if e.memberTarget == nil {
			if i := strings.LastIndex(e.name, "."); i > 0 {
				return `"$(sh2.jsonGet(` + p.resolveVar(e.name[:i]) + `, ` + e.name[i+1:] + `))"`
			}
		}
		p.failf("unsupported comparison operand (v2): %s", e.kind)
	case "arrlen":
		// len(arr) → the `${#arr[@]}` length word, quoted like `"$x"`
		// (the statement-position lowering at emitExpr uses the same
		// shape via join(param("slice", "#arr", "@", ""))).
		return `"${#` + p.resolveVar(e.target.name) + `[@]}"`
	case "strlen":
		// len(s) → the quoted `${#s}` length word (the golib's
		// `i < len(src)` loop guards).
		return `"${#` + p.resolveVar(e.target.name) + `}"`
	case "add", "mul", "neg":
		// arithmetic operand — the `"$((i+1))"` word (the cpp
		// frontend's `i+1 < n` loop guards; the runtime's test
		// evaluator expands the arith word).
		if t := p.indexArithText(e); t != "" {
			return `"$((` + t + `))"`
		}
		p.failf("unsupported comparison operand (v2): %s", e.kind)
	case "index":
		// src[i] in a comparison — the sh2.byteAt helper rendered in a
		// cmdsub word (the golib's `src[i] != '\n'` byte tests; the
		// runtime's test evaluator resolves the sh2.* call in JS).
		if e.target != nil && e.target.kind == "var" && p.varTypes[p.resolveVar(e.target.name)] != "Array" {
			if e.idx1e != nil {
				if off := p.indexArithText(e.idx1e); off != "" {
					return `"$(sh2.byteAt(` + p.resolveVar(e.target.name) + `, ` + off + `))"`
				}
				p.failf("index key must be a number literal (v2)")
			}
			return `"$(sh2.byteAt(` + p.resolveVar(e.target.name) + `, ` + e.idx1 + `))"`
		}
		// a[0] → the quoted `${a[0]}` array-element word — the CLI's
		// `filtered[0] != "--shir"` argv gate (frontends/go-sh/cmd/
		// go-sh/main.go:24; the refusal surfaced with the line
		// attribution of the NEXT statement, `inp := filtered[1]`,
		// because condToJSON runs after the if-body parse — the same
		// attribution trap as the arrlen operand). Same mechanism as
		// the arrlen case above: the core's try_native_test falls back
		// to sh2.test for non-plain operands, and the runtime's
		// tokenizeTest expands the quoted word via expandWord
		// (arrayIndex) inside a quoted test word.
		if e.idx1e != nil {
			p.failf("index key must be a number literal (v2)")
		}
		if e.target != nil && e.target.kind == "var" {
			if p.maps[e.target.name] {
				p.failf("map key in comparison unsupported (v2)")
			}
			return `"${` + p.resolveVar(e.target.name) + `[` + e.idx1 + `]}"`
		}
		p.failf("index target must be a var (v2)")
	}
	p.failf("unsupported comparison operand (v2): %s", e.kind)
	return ""
}

// condOperandArg: `-lt`-style operand — `"$x"` for vars, digits for nums.
func (p *parser) condOperandArg(e *expr) string {
	switch e.kind {
	case "var":
		return `"$` + p.resolveVar(e.name) + `"`
	case "str":
		return `"` + e.text + `"`
	case "char":
		// a Go rune literal is an INTEGER — its ASCII code (the golib's
		// `c >= '0'` digit tests).
		return `"` + charCode(e) + `"`
	case "num":
		return e.text
	case "call":
		if e.callee == "byteAt" && len(e.args) == 2 {
			return `"$(sh2.byteAt(` + e.args[0].text + `, ` + e.args[1].text + `))"`
		}
		p.failf("unsupported comparison operand (v2): %s", e.kind)
	case "arrlen":
		// len(arr) in a numeric comparison — the quoted length word
		// (e.g. `"${#arr[@]}" -gt 1`; a bare ${#arr[@]} would need the
		// word-splitting the quoted form avoids).
		return `"${#` + p.resolveVar(e.target.name) + `[@]}"`
	case "strlen":
		// len(s) in a numeric comparison — the quoted `${#s}` length
		// word (the golib's `i < len(src)` loop guards).
		return `"${#` + p.resolveVar(e.target.name) + `}"`
	case "add", "mul", "neg":
		// arithmetic operand — the `"$((i+1))"` word (the cpp
		// frontend's `i+1 < n` loop guards; the runtime's test
		// evaluator expands the arith word).
		if t := p.indexArithText(e); t != "" {
			return `"$((` + t + `))"`
		}
		p.failf("unsupported comparison operand (v2): %s", e.kind)
	case "index":
		// src[i] in a numeric comparison — the sh2.byteAt cmdsub word
		// (the golib's byte tests).
		if e.target != nil && e.target.kind == "var" && p.varTypes[p.resolveVar(e.target.name)] != "Array" {
			if e.idx1e != nil {
				if off := p.indexArithText(e.idx1e); off != "" {
					return `"$(sh2.byteAt(` + p.resolveVar(e.target.name) + `, ` + off + `))"`
				}
				p.failf("index key must be a number literal (v2)")
			}
			return `"$(sh2.byteAt(` + p.resolveVar(e.target.name) + `, ` + e.idx1 + `))"`
		}
		// a[0] in a numeric comparison — the quoted element word
		// (`[ "${a[0]}" -gt 1 ]`; bare would need the word-splitting
		// the quoted form avoids). Literal keys only, like the
		// statement-position lowering (exprToWord).
		if e.idx1e != nil {
			p.failf("index key must be a number literal (v2)")
		}
		if e.target != nil && e.target.kind == "var" {
			if p.maps[e.target.name] {
				p.failf("map key in comparison unsupported (v2)")
			}
			return `"${` + p.resolveVar(e.target.name) + `[` + e.idx1 + `]}"`
		}
		p.failf("index target must be a var (v2)")
	}
	p.failf("unsupported comparison operand (v2): %s", e.kind)
	return ""
}

// ─────────────────────────────────────────────────────────────────────
// Shir — go-sh as a library: Go source -> A1 shIR JSON bytes (no
// trailing newline). Both the CLI (cmd/go-sh) and the combined busybox
// dispatch through this single entry point.
// ─────────────────────────────────────────────────────────────────────

func Shir(src string) ([]byte, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{
		toks:      toks,
		varTypes:  map[string]string{},
		consts:    map[string]int{},
		constStrs: map[string]string{},
		arrays:    map[string]arrayInfo{},
		maps:      map[string]bool{},
		bufs:      map[string]string{},
		cmds:      map[string][]*expr{},
		stdinRdr:  map[string]bool{},
		fnNames:   map[string]bool{},
		outer:     map[string]bool{},
		varAlias:  map[string]string{},
		structFields:     map[string][]string{},
		structFieldTypes: map[string]map[string]string{},
		varStructTypes:   map[string]string{},
		receiverTypes:    map[string]string{},
		runtimeBufs:      map[string]bool{},
	}
	stmts, err := p.run()
	if err != nil {
		return nil, err
	}
	prog := &shiremit.Program{Stmts: stmts}
	return shiremit.Emit(prog)
}

// run drives the parser with panic-based error recovery.
func (p *parser) run() (stmts []map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return p.parseTopLevel(), nil
}
