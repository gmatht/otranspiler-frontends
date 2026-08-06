package main

// c-sh-go: C source -> A1 shIR JSON (the shell-flavored subset of C).
// v1 subset: printf, int assignments (+=/-=), binary arith, comparisons,
// if/else, while, for (lowered to the equivalent while — the A1 For node
// is for value-list iteration), function signatures (skipped; the body
// becomes the program), return (skipped), comments, #include (skipped).
// Emit shapes mirror the py-sh-go frontend so the estree runner executes
// them identically. Unsupported constructs fail loud (refuse > guess).
import (
	"encoding/json"
	"regexp"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ── A1 node helpers ──────────────────────────────────────────────────
func st(s string) any { return map[string]any{"style": "DoubleQuoted", "type": "Str", "value": s} }
func call(f string, args []any) map[string]any {
	return map[string]any{"args": args, "func": f, "purity": "Emulable", "type": "Call"}
}
// addrTaken — names whose address is taken (&x): their storage must live
// in the sh2 store (the emitter would otherwise lift them to native JS
// bindings, and the mem.* seam reads/writes the store — divergence).
var addrTaken = map[string]bool{}

// charPtrVars — `char *name` declarations: the POINTER-TO-STRING lowering.
// A char* is lowered to the string itself — no mem.* handles, no arena:
//   &"lit"     -> the literal string
//   s + n      -> a substring (param slice)
//   s[i]       -> a 1-char slice
//   %s / %c    -> the string / first char
// The pointer IS the string; the seam is bypassed entirely.
var charPtrVars = map[string]bool{}

func assignStmt(name string, expr any) map[string]any {
	if addrTaken[name] {
		return map[string]any{"type": "Expr", "expr": call("setVar", []any{st(name), expr})}
	}
	return map[string]any{
		"expr":    expr,
		"targets": []any{map[string]any{"indices": []any{}, "sigil": nil, "var": name}},
		"type":    "Assign",
	}
}
func testCall(s string) map[string]any { return call("test", []any{st(s)}) }
func execPrintf(args []any) map[string]any {
	return map[string]any{"type": "Expr", "expr": call("exec", []any{st("printf"), map[string]any{"elements": args, "type": "Array"}})}
}

// ── lexer ────────────────────────────────────────────────────────────
type tok struct{ kind, text string } // id num str op ; { } ( ) , + += - -= * / % ! == != < > <= >= && ||

func lex(src string) ([]tok, error) {
	var out []tok
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		case c == '#': // preprocessor line — skip to newline
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"':
			j := i + 1
			var sb strings.Builder
			for j < n && src[j] != '"' {
				if src[j] == '\\' && j+1 < n {
					switch src[j+1] {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case '\\':
						sb.WriteByte('\\')
					case '"':
						sb.WriteByte('"')
					default:
						sb.WriteByte(src[j+1])
					}
					j += 2
					continue
				}
				sb.WriteByte(src[j])
				j++
			}
			out = append(out, tok{"str", sb.String()})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < n && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			out = append(out, tok{"num", src[i:j]})
			i = j
		case isIdent(c):
			j := i
			for j < n && (isIdent(src[j]) || (src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			out = append(out, tok{"id", src[i:j]})
			i = j
		default:
			two := ""
			if i+1 < n {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||", "+=", "-=":
				out = append(out, tok{"op", two})
				i += 2
				continue
			}
			if strings.ContainsRune("=+-*/%!<>&;{}()[],", rune(c)) {
				out = append(out, tok{"op", string(c)})
				i++
			} else {
				return nil, fmt.Errorf("lex: unexpected %q", string(c))
			}
		}
	}
	return out, nil
}
func isIdent(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ── parser ───────────────────────────────────────────────────────────
type parser struct{ ts []tok; p int }

func (p *parser) peek() *tok {
	if p.p < len(p.ts) {
		return &p.ts[p.p]
	}
	return nil
}
func (p *parser) next() *tok {
	t := p.peek()
	if t != nil {
		p.p++
	}
	return t
}
func (p *parser) isOp(s string) bool { t := p.peek(); return t != nil && t.kind == "op" && t.text == s }
func (p *parser) isId(s string) bool { t := p.peek(); return t != nil && t.kind == "id" && t.text == s }
func (p *parser) expectOp(s string) error {
	if !p.isOp(s) {
		return fmt.Errorf("expected %q at token %v", s, p.peek())
	}
	p.next()
	return nil
}

// expr: or -> and -> cmp -> add -> mul -> unary -> primary
type expr struct {
	kind string // num id bin addr deref str index
	num  string
	name string
	op   string
	l, r *expr
}

func (p *parser) expr() (*expr, error) { return p.orExpr() }
func (p *parser) orExpr() (*expr, error) {
	l, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for p.isOp("||") {
		p.next()
		r, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		l = &expr{kind: "bin", op: "||", l: l, r: r}
	}
	return l, nil
}
func (p *parser) andExpr() (*expr, error) {
	l, err := p.cmpExpr()
	if err != nil {
		return nil, err
	}
	for p.isOp("&&") {
		p.next()
		r, err := p.cmpExpr()
		if err != nil {
			return nil, err
		}
		l = &expr{kind: "bin", op: "&&", l: l, r: r}
	}
	return l, nil
}
func isCmpOp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

func (p *parser) cmpExpr() (*expr, error) {
	l, err := p.addExpr()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "op" || !isCmpOp(t.text) {
			break
		}
		op := t.text
		p.next()
		r, err := p.addExpr()
		if err != nil {
			return nil, err
		}
		l = &expr{kind: "bin", op: op, l: l, r: r}
	}
	return l, nil
}
func (p *parser) addExpr() (*expr, error) {
	l, err := p.mulExpr()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().text
		r, err := p.mulExpr()
		if err != nil {
			return nil, err
		}
		l = &expr{kind: "bin", op: op, l: l, r: r}
	}
	return l, nil
}
func (p *parser) mulExpr() (*expr, error) {
	l, err := p.unaryExpr()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.next().text
		r, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		l = &expr{kind: "bin", op: op, l: l, r: r}
	}
	return l, nil
}
func (p *parser) unaryExpr() (*expr, error) {
	if p.isOp("!") {
		p.next()
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &expr{kind: "bin", op: "!", l: e}, nil
	}
	if p.isOp("&") {
		p.next()
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &expr{kind: "addr", l: e}, nil
	}
	if p.isOp("*") {
		p.next()
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &expr{kind: "deref", l: e}, nil
	}
	return p.primary()
}
func (p *parser) primary() (*expr, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch t.kind {
	case "num":
		p.next()
		return &expr{kind: "num", num: t.text}, nil
	case "str":
		p.next()
		return &expr{kind: "str", num: t.text}, nil
	case "id":
		p.next()
		if p.isOp("[") {
			// id[expr] — indexing (pointer-to-string lowering: a 1-char slice)
			p.next()
			idx, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp("]"); err != nil {
				return nil, err
			}
			return &expr{kind: "index", l: &expr{kind: "id", name: t.text}, r: idx}, nil
		}
		return &expr{kind: "id", name: t.text}, nil
	case "op":
		if t.text == "(" {
			p.next()
			e, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	return nil, fmt.Errorf("unexpected token in expression: %v", t)
}

// ── statement lowering → A1 ──────────────────────────────────────────
func arithNode(e *expr) any {
	switch e.kind {
	case "num":
		n, _ := strconv.Atoi(e.num)
		return map[string]any{"type": "Num", "value": n}
	case "id":
		return map[string]any{"type": "Var", "name": e.name}
	case "bin":
		return map[string]any{"type": "Bin", "lhs": arithNode(e.l), "op": e.op, "rhs": arithNode(e.r)}
	}
	return map[string]any{"type": "Num", "value": 0}
}
func valueNode(e *expr) any {
	switch e.kind {
	case "num":
		return st(e.num)
	case "str":
		return st(e.num)
	case "id":
		return call("getVar", []any{st(e.name)})
	case "addr":
		// &x — a handle to x's storage (allocation_id + offset; offset 0)
		if e.l != nil && e.l.kind == "id" {
			return call("addrOf", []any{st(e.l.name)})
		}
		return call("addrOf", []any{valueNode(e.l)})
	case "deref":
		// *p — load through the handle
		return call("memLoad", []any{valueNode(e.l)})
	case "index":
		// s[i] on a char* — the pointer-to-string lowering: a 1-char slice
		if e.l != nil && e.l.kind == "id" && charPtrVars[e.l.name] {
			return call("param", []any{st("slice"), st(e.l.name), offsetArg(e.r), st("1")})
		}
		return nil
	case "bin":
		// char* + n / char* - n — pointer arithmetic lowered to a substring
		if (e.op == "+" || e.op == "-") && e.l != nil && e.l.kind == "id" && charPtrVars[e.l.name] {
			off := e.r
			if e.op == "-" {
				off = &expr{kind: "bin", op: "-", l: &expr{kind: "num", num: "0"}, r: e.r}
			}
			return call("param", []any{st("slice"), st(e.l.name), offsetArg(off), st("")})
		}
	}
	return map[string]any{"ast": arithNode(e), "type": "Arith"}
}

// offsetArg — a literal slice offset as its string form (dynamic offsets
// via a variable index are a follow-up; the runner's sliceOff parses arith).
func offsetArg(e *expr) any {
	if e != nil && e.kind == "num" {
		return st(e.num)
	}
	return st("0")
}

// testExpr — C comparison → the bash test-expression string ($x -gt 1).
func testExpr(e *expr) string {
	switch e.kind {
	case "num":
		return e.num
	case "id":
		return "$" + e.name
	case "bin":
		if e.op == "!" {
			return "! " + testExpr(e.l)
		}
		if e.op == "&&" {
			return testExpr(e.l) + " -a " + testExpr(e.r)
		}
		if e.op == "||" {
			return testExpr(e.l) + " -o " + testExpr(e.r)
		}
		return testExpr(e.l) + " " + cmpOp(e.op) + " " + testExpr(e.r)
	}
	return ""
}
func cmpOp(op string) string {
	switch op {
	case ">":
		return "-gt"
	case "<":
		return "-lt"
	case ">=":
		return "-ge"
	case "<=":
		return "-le"
	case "==":
		return "-eq"
	case "!=":
		return "-ne"
	}
	return op
}

func (p *parser) stmts() ([]any, error) {
	var out []any
	for {
		t := p.peek()
		if t == nil {
			break
		}
		if t.kind == "op" && t.text == "}" {
			break
		}
		if t.kind == "op" && t.text == "{" {
			inner, err := p.block()
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
			continue
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		if s != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (p *parser) stmt() (any, error) {
	t := p.peek()
	if t == nil {
		return nil, nil
	}
	switch {
	case p.isId("int") || p.isId("char") || p.isId("return"):
		kw := p.next().text
		if kw == "return" {
			for !p.isOp(";") && p.peek() != nil {
				p.next()
			}
			p.next() // consume ;
			return nil, nil // return: no stdout effect in the v1 subset
		}
		// [int|char] [*]* NAME ( = expr )? ;  — pointers: `int *p` / `char *s`
		isPtr := false
		for p.isOp("*") {
			p.next()
			isPtr = true
		}
		name := p.next()
		if name.kind != "id" {
			return nil, fmt.Errorf("expected identifier after type")
		}
		if kw == "char" && isPtr {
			charPtrVars[name.text] = true
		}
		// function signature `int main ( ) {`
		if p.isOp("(") {
			p.next()
			depth := 1
			for depth > 0 {
				tk := p.next()
				if tk == nil {
					break
				}
				if tk.kind == "op" && tk.text == "(" {
					depth++
				}
				if tk.kind == "op" && tk.text == ")" {
					depth--
				}
			}
			return nil, nil // signature consumed; the body follows as a block
		}
		if p.isOp("=") {
			p.next()
			e, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(";"); err != nil {
				return nil, err
			}
			return assignStmt(name.text, valueNode(e)), nil
		}
		p.next() // bare declaration `int x;`
		return nil, nil
	case p.isId("if"):
		p.next()
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		c, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		thenB, err := p.block()
		if err != nil {
			return nil, err
		}
		elseB := []any{}
		if p.isId("else") {
			p.next()
			if p.isId("if") {
				// else if — nested If as the else arm
				nested, err := p.stmt()
				if err != nil {
					return nil, err
				}
				elseB = []any{nested}
			} else {
				elseB, err = p.block()
				if err != nil {
					return nil, err
				}
			}
		}
		return map[string]any{"cond": testCall(testExpr(c)), "then": thenB, "elsifs": []any{}, "else": elseB, "type": "If"}, nil
	case p.isId("while"):
		p.next()
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		c, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		b, err := p.block()
		if err != nil {
			return nil, err
		}
		return map[string]any{"cond": testCall(testExpr(c)), "body": b, "type": "While"}, nil
	case p.isId("for"):
		// for (init; cond; inc) body → init; while (cond) { body; inc }
		p.next()
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		var err error
		var init any
		if !p.isOp(";") {
			init, err = p.forHeaderAssign()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		var cond *expr
		if !p.isOp(";") {
			cond, err = p.expr()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		var inc any
		if !p.isOp(")") {
			inc, err = p.forHeaderAssign()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		b, err := p.block()
		if err != nil {
			return nil, err
		}
		var body []any
		body = append(body, b...)
		if inc != nil {
			body = append(body, inc)
		}
		condStr := "1"
		if cond != nil {
			condStr = testExpr(cond)
		}
		var out []any
		if init != nil {
			out = append(out, init)
		}
		out = append(out, map[string]any{"cond": testCall(condStr), "body": body, "type": "While"})
		return map[string]any{"body": out, "type": "Block"}, nil
	case p.isId("printf"):
		p.next()
		if err := p.expectOp("("); err != nil {
			return nil, err
		}
		var args []any
		for {
			tk := p.peek()
			if tk != nil && tk.kind == "str" {
				args = append(args, st(tk.text))
				p.next()
			} else {
				e, err := p.expr()
				if err != nil {
					return nil, err
				}
				args = append(args, valueNode(e))
			}
			if p.isOp(")") {
				break
			}
			if err := p.expectOp(","); err != nil {
				return nil, err
			}
		}
		p.next() // )
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		return execPrintf(args), nil
	case p.isId("int"):
		// handled above
		return nil, fmt.Errorf("unhandled int")
	default:
		// plain assignment `x = e;` / `x += e;`
		return p.simpleAssign()
	}
}

// simpleAssign — id ( = | += | -= ) expr ;  (or a bare `id;` skip)
// forHeaderAssign — like simpleAssign but does NOT consume the trailing
// ';' (the for header's separators are consumed by the for loop itself).
func (p *parser) forHeaderAssign() (any, error) {
	t := p.peek()
	if t == nil || t.kind != "id" {
		return nil, nil
	}
	name := p.next().text
	if p.isOp("=") || p.isOp("+=") || p.isOp("-=") {
		op := p.next().text
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		return p.buildAssign(name, op, e)
	}
	return nil, nil
}

func (p *parser) buildAssign(name, op string, e *expr) (any, error) {
	if op == "=" {
		return assignStmt(name, valueNode(e)), nil
	}
	arithOp := strings.TrimSuffix(op, "=")
	return assignStmt(name, map[string]any{"ast": map[string]any{
		"type": "Bin", "lhs": map[string]any{"type": "Var", "name": name},
		"op": arithOp, "rhs": arithNode(e)}, "type": "Arith"}), nil
}

func (p *parser) simpleAssign() (any, error) {
	if p.isOp("*") {
		// *p = v — a store through the handle
		p.next()
		target, err := p.expr()
		if err != nil {
			return nil, err
		}
		if !p.isOp("=") && !p.isOp("+=") && !p.isOp("-=") {
			return nil, fmt.Errorf("expected assignment after deref")
		}
		p.next()
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		return map[string]any{
			"type": "Expr",
			"expr": call("memStore", []any{valueNode(target), valueNode(e)}),
		}, nil
	}
	name := p.next().text
	if p.isOp("=") || p.isOp("+=") || p.isOp("-=") {
		op := p.next().text
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		return p.buildAssign(name, op, e)
	}
	// bare id ; — skip
	for !p.isOp(";") && p.peek() != nil {
		p.next()
	}
	if p.isOp(";") {
		p.next()
	}
	return nil, nil
}

func (p *parser) block() ([]any, error) {
	if err := p.expectOp("{"); err != nil {
		return nil, err
	}
	body, err := p.stmts()
	if err != nil {
		return nil, err
	}
	if err := p.expectOp("}"); err != nil {
		return nil, err
	}
	return body, nil
}

// ── main ─────────────────────────────────────────────────────────────
func main() {
	args := os.Args[1:]
	var file string
	for _, a := range args {
		if a == "--shir" || a == "--raw" {
			continue
		}
		file = a
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: c-sh-go --shir <file.c> [--raw]")
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	srcStr := string(src)
	// pre-scan: address-taken names (&x) keep their storage in the store
	addrTaken = map[string]bool{}
	for _, m := range regexp.MustCompile(`&([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(srcStr, -1) {
		addrTaken[m[1]] = true
	}
	ts, err := lex(srcStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "REFUSE: "+err.Error())
		os.Exit(1)
	}
	pr := &parser{ts: ts}
	stmts, err := pr.stmts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "REFUSE: "+err.Error())
		os.Exit(1)
	}
	prog := map[string]any{
		"type":             "Program",
		"contract_version": 1,
		"imports":          []any{},
		"requires":         []any{},
		"stmt_lines":       []any{},
		"stmts":            stmts,
		"subs":             []any{},
		"var_const":        []any{},
		"var_lengths":      []any{},
		"var_lifetimes":    []any{},
		"var_types":        []any{},
	}
	out, err := json.Marshal(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
