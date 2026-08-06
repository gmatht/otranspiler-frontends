package main

// c-sh-go: C source -> A1 shIR JSON (the shell-flavored subset of C).
// v1 subset: printf, int assignments (+=/-=), binary arith, comparisons,
// if/else, while, for (lowered to the equivalent while — the A1 For node
// is for value-list iteration), function signatures (skipped; main's body
// becomes the program), user functions (a single pure `return <expr>;`
// body — a call with LITERAL args constant-folds through it; the body is
// never emitted), return (skipped), strcmp/strlen/atoi (literal-arg
// folding, see foldCallConst), comments, #include (skipped).
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

// arrayVars — `int a[3]` declarations: lowered to setArray (the runtime's
// array store). ptrTargets — a pointer whose target is STATICALLY known
// (p = &a[1] / p = a): the POINTER-TO-ARRAY reduction — the pointer
// becomes (array, index) and every use folds to direct array indexing;
// the pointer variable is compile-time only, never emitted.
var arrayVars = map[string]bool{}
// scalarAliases — STATIC ALIAS FOLDING for scalars: `int *p = &x;` where
// x is a scalar and p never escapes -> p is ELIMINATED; *p reads/writes
// become x directly (zero pointer machinery, zero mem calls). The alias
// chain (`int *q = p;`) folds too. Raw pointer-value uses (p == NULL,
// printf("%p", p)) refuse via the unsupported marker.
var scalarAliases = map[string]string{}
var ptrTargets = map[string]ptrTarget{}

type ptrTarget struct {
	arr  string
	base int
}

// ptrTargetFromExpr — is e a statically-resolvable pointer-into-array?
// &a[i] -> (a, i); &a / a (decay) -> (a, 0).
func ptrTargetFromExpr(e *expr) (ptrTarget, bool) {
	if e == nil {
		return ptrTarget{}, false
	}
	if e.kind == "addr" && e.l != nil {
		if e.l.kind == "index" && e.l.l != nil && e.l.l.kind == "id" && arrayVars[e.l.l.name] {
			b, ok := foldIndex(e.l.r)
			return ptrTarget{arr: e.l.l.name, base: b}, ok
		}
		if e.l.kind == "id" && arrayVars[e.l.name] {
			return ptrTarget{arr: e.l.name, base: 0}, true
		}
	}
	if e.kind == "id" && arrayVars[e.name] {
		return ptrTarget{arr: e.name, base: 0}, true
	}
	return ptrTarget{}, false
}

// foldIndex — a literal index (num, or unary-minus num) as an int.
func foldIndex(e *expr) (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.kind == "num" {
		n, err := strconv.Atoi(e.num)
		return n, err == nil
	}
	if e.kind == "bin" && e.op == "-" && e.l != nil && e.l.kind == "num" && e.l.num == "0" && e.r != nil && e.r.kind == "num" {
		n, err := strconv.Atoi(e.r.num)
		return -n, err == nil
	}
	return 0, false
}

// recordPtrTarget — for a pointer variable `name` initialized with expr e:
// array target -> ptrTargets; scalar &x -> scalarAliases; pointer copy
// (p = q) -> chase the existing alias. Returns true if recorded (emit nothing).
func recordPtrTarget(name string, e *expr) bool {
	if e == nil {
		return false
	}
	if t, ok := ptrTargetFromExpr(e); ok {
		ptrTargets[name] = t
		return true
	}
	if e.kind == "addr" && e.l != nil && e.l.kind == "id" && !arrayVars[e.l.name] {
		scalarAliases[name] = e.l.name
		return true
	}
	if e.kind == "id" {
		if t, ok := scalarAliases[e.name]; ok {
			scalarAliases[name] = t
			return true
		}
		if t, ok := ptrTargets[e.name]; ok {
			ptrTargets[name] = t
			return true
		}
	}
	return false
}

// charPtrVars — `char *name` declarations: the POINTER-TO-STRING lowering.
// A char* is lowered to the string itself — no mem.* handles, no arena:
//   &"lit"     -> the literal string
//   s + n      -> a substring (param slice)
//   s[i]       -> a 1-char slice
//   %s / %c    -> the string / first char
// The pointer IS the string; the seam is bypassed entirely.
var charPtrVars = map[string]bool{}

func assignStmt(name string, expr any) map[string]any {
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

// refuse — unsupported constructs fail loud (refuse > guess).
func refuse(msg string) {
	fmt.Fprintln(os.Stderr, "REFUSE: "+msg)
	os.Exit(1)
}

// callNode — a C stdlib function-call expression in a VALUE position.
// The v1 subset implements the pure conversions the shell runtime
// already models:
//   strlen(s) — a string LITERAL is a compile-time constant (fold to its
//     length); a VARIABLE lowers to ${#s} — the A1 param("len", name)
//     idiom, which the estree renderer lowers to String(v).length for
//     lifted vars (exact for the NUL-free subset).
//   atoi(s)  — a literal folds to its integer text (C leading-digit
//     parse, 0 on failure); a variable IS its value (the store is
//     string-typed; the Arith nodes coerce).
//   strcmp(a, b) / a user function call — constant-fold when every
//     argument is a literal (see foldCallConst); anything else REFUSES
//     (exit 1) — refuse > guess.
func callNode(e *expr) any {
	switch e.name {
	case "strlen", "atoi":
		if len(e.args) != 1 {
			refuse("unsupported function call " + e.name)
		}
		a := e.args[0]
		if e.name == "strlen" {
			if a.kind == "str" {
				return st(strconv.Itoa(len(a.num)))
			}
			if a.kind == "id" {
				return call("param", []any{st("len"), st(a.name)})
			}
		} else {
			if a.kind == "str" {
				s := strings.TrimSpace(a.num)
				n, err := strconv.Atoi(s)
				if err != nil {
					n = 0 // C atoi: no leading digits -> 0
				}
				return st(strconv.Itoa(n))
			}
			if a.kind == "id" {
				return call("getVar", []any{st(a.name)})
			}
		}
		refuse("unsupported function call " + e.name)
	}
	// strcmp / user functions — fold with all-literal args; refuse otherwise
	if s, ok := foldCallConst(e, nil); ok {
		return st(s)
	}
	refuse("unsupported function call " + e.name)
	return nil
}

// userFuncs — USER function definitions (`static int triple(int n) {
// return n * 3; }`). The v1 subset models a function as a single PURE
// return expression: a call with LITERAL arguments constant-folds through
// the body (substitution + evaluation); the A1 v1 grammar has no call
// stack, so anything richer (variable args, body side effects) REFUSES.
// The body is never emitted — only main's body becomes the program.
type userFunc struct {
	params []string
	ret    *expr // the return expression (nil for `return;` / empty body)
}

var userFuncs = map[string]*userFunc{}

// boolStr — a C truth value as its string form (the store is string-typed).
func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// foldConst — evaluate a pure v1 expression over a LITERAL environment to
// its string constant. C int semantics (int64) for the arithmetic subset;
// comparisons and &&/||/! yield 1/0; ==/!= on non-numeric literals falls
// back to string equality (strcmp("a","b") == 0 folds through this).
func foldConst(e *expr, env map[string]string) (string, bool) {
	if e == nil {
		return "", false
	}
	switch e.kind {
	case "num", "str":
		return e.num, true
	case "id":
		v, ok := env[e.name]
		return v, ok
	case "call":
		return foldCallConst(e, env)
	case "bin":
		switch e.op {
		case "+", "-", "*", "/", "%":
			l, ok1 := foldConst(e.l, env)
			r, ok2 := foldConst(e.r, env)
			if !ok1 || !ok2 {
				return "", false
			}
			a, err1 := strconv.ParseInt(l, 10, 64)
			b, err2 := strconv.ParseInt(r, 10, 64)
			if err1 != nil || err2 != nil {
				return "", false
			}
			var v int64
			switch e.op {
			case "+":
				v = a + b
			case "-":
				v = a - b
			case "*":
				v = a * b
			case "/":
				if b == 0 {
					return "", false
				}
				v = a / b // C truncation toward zero (Go division matches)
			case "%":
				if b == 0 {
					return "", false
				}
				v = a % b
			}
			return strconv.FormatInt(v, 10), true
		case "==", "!=", "<", ">", "<=", ">=":
			l, ok1 := foldConst(e.l, env)
			r, ok2 := foldConst(e.r, env)
			if !ok1 || !ok2 {
				return "", false
			}
			a, err1 := strconv.ParseInt(l, 10, 64)
			b, err2 := strconv.ParseInt(r, 10, 64)
			if err1 == nil && err2 == nil {
				c := a
				d := b
				switch e.op {
				case "==":
					return boolStr(c == d), true
				case "!=":
					return boolStr(c != d), true
				case "<":
					return boolStr(c < d), true
				case ">":
					return boolStr(c > d), true
				case "<=":
					return boolStr(c <= d), true
				case ">=":
					return boolStr(c >= d), true
				}
			}
			// non-numeric operands: string equality only (C's byte-wise
			// ordering of arbitrary strings is not modeled)
			if e.op == "==" {
				return boolStr(l == r), true
			}
			if e.op == "!=" {
				return boolStr(l != r), true
			}
			return "", false
		case "&&", "||":
			l, ok1 := foldConst(e.l, env)
			r, ok2 := foldConst(e.r, env)
			if !ok1 || !ok2 {
				return "", false
			}
			a, err1 := strconv.ParseInt(l, 10, 64)
			b, err2 := strconv.ParseInt(r, 10, 64)
			if err1 != nil || err2 != nil {
				return "", false
			}
			if e.op == "&&" {
				return boolStr(a != 0 && b != 0), true
			}
			return boolStr(a != 0 || b != 0), true
		case "!":
			v, ok := foldConst(e.l, env)
			if !ok {
				return "", false
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return "", false
			}
			return boolStr(n == 0), true
		}
	}
	return "", false
}

// foldCallConst — a stdlib/user call with all-literal arguments -> its
// string constant. strlen("lit") folds to the length, atoi("lit") to its
// integer text, strcmp(a,b) to the SIGN of the C result (-1/0/1 — the
// magnitude is implementation-defined). A user function call substitutes
// the literal args for the params and folds the return expression.
func foldCallConst(e *expr, env map[string]string) (string, bool) {
	if e == nil {
		return "", false
	}
	switch e.name {
	case "strlen":
		if len(e.args) == 1 {
			if s, ok := foldConst(e.args[0], env); ok {
				return strconv.Itoa(len(s)), true
			}
		}
	case "atoi":
		if len(e.args) == 1 {
			if s, ok := foldConst(e.args[0], env); ok {
				t := strings.TrimSpace(s)
				n, err := strconv.Atoi(t)
				if err != nil {
					n = 0 // C atoi: no leading digits -> 0
				}
				return strconv.Itoa(n), true
			}
		}
	case "strcmp":
		if len(e.args) == 2 {
			a, ok1 := foldConst(e.args[0], env)
			b, ok2 := foldConst(e.args[1], env)
			if ok1 && ok2 {
				switch {
				case a == b:
					return "0", true
				case a < b:
					return "-1", true
				default:
					return "1", true
				}
			}
		}
	}
	// user function: substitute the literal args for the params, fold the body
	if fn, ok := userFuncs[e.name]; ok {
		if len(e.args) != len(fn.params) || fn.ret == nil {
			return "", false
		}
		sub := map[string]string{}
		for i, a := range e.args {
			v, ok := foldConst(a, env)
			if !ok {
				return "", false
			}
			sub[fn.params[i]] = v
		}
		return foldConst(fn.ret, sub)
	}
	return "", false
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
			case "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "++", "--":
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
type parser struct {
	ts       []tok
	p        int
	retExpr  *expr // the most recent `return <expr>;` (user-function bodies)
}

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
	kind string // num id bin addr deref str index call
	num  string
	name string
	op   string
	l, r *expr
	args []*expr // call: the argument expressions
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
	if p.isOp("-") {
		p.next()
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &expr{kind: "bin", op: "-", l: &expr{kind: "num", num: "0"}, r: e}, nil
	}
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
		if p.isOp("(") {
			// id(args) — a function call (strlen / atoi in the v1 subset)
			p.next()
			var args []*expr
			if !p.isOp(")") {
				for {
					a, err := p.expr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.isOp(")") {
						break
					}
					if err := p.expectOp(","); err != nil {
						return nil, err
					}
				}
			}
			p.next() // )
			return &expr{kind: "call", name: t.text, args: args}, nil
		}
		return &expr{kind: "id", name: t.text}, nil
	case "op":
		if t.text == "(" {
			// `(int) e` / `(char) e` — a C type cast (identity in the v1
			// subset: every value is a string in the shell store)
			if p.p+2 < len(p.ts) {
				n1, n2 := p.ts[p.p+1], p.ts[p.p+2]
				if n1.kind == "id" && (n1.text == "int" || n1.text == "char") && n2.kind == "op" && n2.text == ")" {
					p.next()
					p.next()
					p.next()
					return p.unaryExpr()
				}
			}
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
	case "call":
		// a call in an arithmetic context (strcmp(a,b) < 0, triple(2) + 1):
		// fold to a constant when every argument is literal. The A1 Arith
		// grammar has no Call node, so a variable value cannot be modeled
		// — refuse (refuse > guess; silently folding to 0 would be a lie).
		s, ok := foldCallConst(e, nil)
		if !ok {
			refuse("unsupported function call " + e.name + " in arithmetic context")
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			refuse("unsupported function call " + e.name + " (non-integer value)")
		}
		return map[string]any{"type": "Num", "value": n}
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
		if _, ok := scalarAliases[e.name]; ok {
			return call("unsupported", []any{st("raw pointer value use of " + e.name)})
		}
		return call("getVar", []any{st(e.name)})
	case "call":
		return callNode(e)
	case "addr":
		// &x — a handle to x's storage (allocation_id + offset; offset 0)
		if e.l != nil && e.l.kind == "id" {
			return call("addrOf", []any{st(e.l.name)})
		}
		return call("addrOf", []any{valueNode(e.l)})
	case "deref":
		// *p on a statically-aliased scalar — the alias folding: direct read
		if e.l != nil && e.l.kind == "id" {
			if t, ok := scalarAliases[e.l.name]; ok {
				return call("getVar", []any{st(t)})
			}
			if t, ok := ptrTargets[e.l.name]; ok {
				return call("arrayIndex", []any{st(t.arr), st(strconv.Itoa(t.base))})
			}
		}
		// *p — load through the handle
		return call("memLoad", []any{valueNode(e.l)})
	case "index":
		// s[i] on a char* — the pointer-to-string lowering: a 1-char slice
		if e.l != nil && e.l.kind == "id" && charPtrVars[e.l.name] {
			return call("param", []any{st("slice"), st(e.l.name), offsetArg(e.r), st("1")})
		}
		// p[k] on a static pointer-into-array — the pointer-to-array
		// reduction: fold to arrayIndex(arr, base+k)
		if e.l != nil && e.l.kind == "id" {
			if t, ok := ptrTargets[e.l.name]; ok {
				if k, ok := foldIndex(e.r); ok {
					return call("arrayIndex", []any{st(t.arr), st(strconv.Itoa(t.base + k))})
				}
			}
			if arrayVars[e.l.name] {
				return call("arrayIndex", []any{st(e.l.name), offsetArg(e.r)})
			}
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
	case p.isId("static"):
		// storage-class qualifier — `static int triple(...)`: consume and
		// fall through to the type-keyword handling
		p.next()
		return p.stmt()
	case p.isId("int") || p.isId("char") || p.isId("return"):
		kw := p.next().text
		if kw == "return" {
			var e *expr
			if !p.isOp(";") {
				var err error
				e, err = p.expr()
				if err != nil {
					return nil, err
				}
			}
			if err := p.expectOp(";"); err != nil {
				return nil, err
			}
			p.retExpr = e // captured for user-function constant folding
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
		// function signature `int main ( void ) {` — or a USER function
		// `static int triple ( int n ) { return n * 3; }`. main's body
		// becomes the program; a user function is registered for
		// literal-arg constant folding (the body is parsed for its return
		// expression but never emitted).
		if p.isOp("(") {
			p.next()
			var params []string
			if !p.isOp(")") {
				if p.isId("void") {
					p.next() // main(void)
				} else {
					for {
						t := p.peek()
						if t == nil || t.kind != "id" || (t.text != "int" && t.text != "char") {
							return nil, fmt.Errorf("expected parameter type (int|char) at token %v", t)
						}
						p.next() // int | char
						for p.isOp("*") {
							p.next()
						}
						pn := p.next()
						if pn == nil || pn.kind != "id" {
							return nil, fmt.Errorf("expected parameter name at token %v", pn)
						}
						params = append(params, pn.text)
						if p.isOp(")") {
							break
						}
						if err := p.expectOp(","); err != nil {
							return nil, err
						}
					}
				}
			}
			p.next() // )
			if name.text == "main" {
				return nil, nil // signature consumed; the body follows as a block
			}
			// a user function: the v1 subset is a single pure return
			// expression — body side effects cannot be folded, refuse
			p.retExpr = nil
			body, err := p.block()
			if err != nil {
				return nil, err
			}
			if len(body) > 0 {
				refuse("user function body must be a single return in the v1 subset: " + name.text)
			}
			userFuncs[name.text] = &userFunc{params: params, ret: p.retExpr}
			return nil, nil
		}
		if p.isOp("[") {
			// array declaration: name[ size ] ( = { e, e, ... } )? ;
			p.next()
			if _, err := p.expr(); err != nil {
				return nil, err
			}
			if err := p.expectOp("]"); err != nil {
				return nil, err
			}
			arrayVars[name.text] = true
			if p.isOp("=") {
				p.next()
				if err := p.expectOp("{"); err != nil {
					return nil, err
				}
				var elems []any
				if !p.isOp("}") {
					for {
						e, err := p.expr()
						if err != nil {
							return nil, err
						}
						elems = append(elems, valueNode(e))
						if p.isOp("}") {
							break
						}
						if err := p.expectOp(","); err != nil {
							return nil, err
						}
					}
				}
				p.next() // }
				if err := p.expectOp(";"); err != nil {
					return nil, err
				}
				return map[string]any{"type": "Expr", "expr": call("setArray", []any{st(name.text), map[string]any{"elements": elems, "type": "Array"}})}, nil
			}
			p.next() // bare `int a[3];`
			return nil, nil
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
			// static pointer target (array / scalar alias / copy): the pointer
			// is compile-time only — emit NOTHING
			if isPtr && recordPtrTarget(name.text, e) {
				return nil, nil
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
	if t == nil {
		return nil, nil
	}
	// a declaration in the for header: `for (int i = 1; ...)` — the type
	// keyword is consumed, the declaration lowers like a plain assignment
	if t.kind == "id" && (t.text == "int" || t.text == "char") {
		p.next()
		t = p.peek()
	}
	if t == nil || t.kind != "id" {
		return nil, nil
	}
	name := p.next().text
	if p.isOp("++") || p.isOp("--") {
		// i++ / i-- — postfix increment, lowered to i = i +/- 1
		op := p.next().text
		return p.buildAssign(name, "=", &expr{
			kind: "bin", op: op[:1],
			l: &expr{kind: "id", name: name},
			r: &expr{kind: "num", num: "1"},
		})
	}
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
		// *p = v on a statically-aliased scalar — the alias folding: a direct
		// assignment to the aliased var
		if target != nil && target.kind == "id" {
			if t, ok := scalarAliases[target.name]; ok {
				return assignStmt(t, valueNode(e)), nil
			}
			// *p = v on a static pointer-into-array — reduce to the baked-name
			// array assign (the core's arr[1]=x shape; the runtime handles it)
			if t, ok := ptrTargets[target.name]; ok {
				return assignStmt(t.arr+"["+strconv.Itoa(t.base)+"]", valueNode(e)), nil
			}
		}
		return map[string]any{
			"type": "Expr",
			"expr": call("memStore", []any{valueNode(target), valueNode(e)}),
		}, nil
	}
	name := p.next().text
	if p.isOp("++") || p.isOp("--") {
		// x++ / x-- — postfix increment (lowered to x = x +/- 1)
		op := p.next().text
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		return p.buildAssign(name, "=", &expr{
			kind: "bin", op: op[:1],
			l: &expr{kind: "id", name: name},
			r: &expr{kind: "num", num: "1"},
		})
	}
	if p.isOp("=") || p.isOp("+=") || p.isOp("-=") {
		op := p.next().text
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(";"); err != nil {
			return nil, err
		}
		if op == "=" && recordPtrTarget(name, e) {
			return nil, nil
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

// applyStoreRouting — after the whole program is parsed (and the pointer
// folding is fully known), route the assignments of address-taken vars
// that were NOT folded (their storage is still reachable through the
// mem.* seam, which reads/writes the sh2 store — a native JS binding
// would be invisible to it) through setVar. Folded vars keep the native
// Assign stmt: the alias writes ARE the variable, so no store is needed.
func applyStoreRouting(stmts []any) []any {
	folded := map[string]bool{}
	for _, v := range scalarAliases {
		folded[v] = true
	}
	for _, t := range ptrTargets {
		folded[t.arr] = true
	}
	var walk func([]any) []any
	walk = func(ss []any) []any {
		if len(ss) == 0 {
			return []any{}
		}
		var out []any
		for _, sx := range ss {
			m, ok := sx.(map[string]any)
			if !ok {
				out = append(out, st)
				continue
			}
			if m["type"] == "Assign" {
				tgts, _ := m["targets"].([]any)
				if len(tgts) > 0 {
					t0, _ := tgts[0].(map[string]any)
					if v, _ := t0["var"].(string); v != "" && addrTaken[v] && !folded[v] {
						out = append(out, map[string]any{
							"type": "Expr",
							"expr": call("setVar", []any{st(v), m["expr"]}),
						})
						continue
					}
				}
			}
			// recurse into compound stmts (if/while/block bodies)
			for _, key := range []string{"then", "else", "body"} {
				if b, ok := m[key].([]any); ok {
					m[key] = walk(b)
				}
			}
			out = append(out, sx)
		}
		return out
	}
	return walk(stmts)
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
	stmts = applyStoreRouting(stmts)
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
