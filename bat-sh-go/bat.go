// Package batshgo — Windows batch (.bat) source -> A1 shIR JSON.
//
// Frontends parse; they do not optimize. This frontend emits the
// language-neutral A1 contract (frontends/plan.md §0); the core consumes
// it (shir_json_in.rs) and the backends render. The core does NOT parse
// batch, so the go-sh style oracle applies: the emitted JSON must be
// accepted by the core's deserializer (--shir-in-estree) and render/run
// correctly through the backends — see Makefile `test`.
//
// v1 subset (REFUSE > GUESS — anything unlisted errors loudly):
//
//	@echo off, rem/:: comments, :label / goto, echo [text] / echo. /
//	echo:, set VAR=value / set "VAR=value" / set VAR= (empty),
//	set /a VAR=expr (Int + - * / % parens, %var% operands),
//	%var% expansion (case-insensitive), %1..%9, %*, %% literal,
//	%errorlevel% (mapped to the shell's $? — getVar("?")),
//	if [not] A==B (cmd) [else (cmd)] — else only with the parenthesized
//	form (real cmd requires the else on the closing-paren line),
//	for %%v in (word list) do cmd|(cmd), `&` statement separators.
//
// Deliberately NOT in v1 (refuse loud): delayed expansion !var!, call,
// setlocal/endlocal, shift, pause, start, set /p, if defined/exist/
// errorlevel, for /l /f /d /r, ^ line continuation, pipes |.
package batshgo

import (
	"fmt"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// Parse parses batch source into an A1 shIR program.
func Parse(src string) (*shiremit.Program, error) {
	p := newParser(strings.Split(src, "\n"))
	stmts, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	return &shiremit.Program{Stmts: stmts}, nil
}

type parser struct {
	lines   []string
	idx     int
	forVars map[string]bool
}

func newParser(lines []string) *parser {
	return &parser{lines: lines, forVars: map[string]bool{}}
}

// parseBlock reads statements until EOF, or until a bare `)` line when
// inBlock. Each line may hold several `&`-separated commands.
func (p *parser) parseBlock(inBlock bool) ([]map[string]any, error) {
	var out []map[string]any
	for p.idx < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.idx])
		p.idx++
		if line == "" {
			continue
		}
		if inBlock && line == ")" {
			return out, nil
		}
		st, err := p.parseLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, st...)
	}
	if inBlock {
		return nil, fmt.Errorf("unterminated ( block")
	}
	return out, nil
}

// parseLine handles a full line: comments, labels, @-prefix, then the
// `&`-separated commands.
func (p *parser) parseLine(line string) ([]map[string]any, error) {
	if strings.HasPrefix(line, "@") {
		line = strings.TrimSpace(line[1:])
	}
	if line == "" {
		return nil, nil
	}
	if strings.HasPrefix(line, "::") || strings.HasPrefix(line, "rem ") || line == "rem" {
		return nil, nil
	}
	if strings.HasPrefix(line, ":") {
		return []map[string]any{{"type": "Label", "name": strings.TrimSpace(line[1:])}}, nil
	}
	var out []map[string]any
	rest := line
	for {
		st, r, err := p.parseCommand(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, st...)
		rest = strings.TrimSpace(r)
		if rest == "" {
			break
		}
		if strings.HasPrefix(rest, "&") {
			rest = strings.TrimSpace(rest[1:])
			continue
		}
		return nil, fmt.Errorf("unexpected trailing text: %q", rest)
	}
	return out, nil
}

// parseCommand parses one command; returns its statements and the
// unconsumed remainder of the line (after `&`/`else` handling).
func (p *parser) parseCommand(s string) ([]map[string]any, string, error) {
	if s == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(s, "(") {
		// bare ( block )
		inner, rest, err := p.parseParenBlock(s)
		if err != nil {
			return nil, "", err
		}
		return []map[string]any{{"type": "Block", "body": inner}}, rest, nil
	}
	word, rest := splitWord(s)
	w := strings.ToLower(word)
	if strings.HasPrefix(w, "echo") && len(w) > 4 {
		if c := w[4]; c == '.' || c == ':' || c == '\\' {
			// `echo.` / `echo:` / `echo\` — blank-line forms (the dot/colon
			// is glued to the command in batch)
			return []map[string]any{execStmt([]map[string]any{str("echo")}, "Emulable")}, "", nil
		}
	}
	switch w {
	case "echo":
		return p.parseEcho(rest)
	case "set":
		return p.parseSet(rest)
	case "if":
		return p.parseIf(rest)
	case "for":
		return p.parseFor(rest)
	case "goto":
		return []map[string]any{{"type": "Goto", "name": strings.TrimSpace(rest)}}, "", nil
	case "exit":
		return p.parseExit(rest)
	case "rem":
		return nil, "", nil
	case "call", "setlocal", "endlocal", "shift", "pause", "start", "pushd", "popd":
		return nil, "", fmt.Errorf("unsupported batch command %q (v1 subset)", strings.ToLower(word))
	default:
		// external command
		words, err := splitWords(word+" "+rest, p.forVars)
		if err != nil {
			return nil, "", err
		}
		return []map[string]any{execStmt(words, "Spawn")}, "", nil
	}
}

// splitWord splits the first whitespace-delimited word off s.
func splitWord(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// ── echo ────────────────────────────────────────────────────────────

func (p *parser) parseEcho(rest string) ([]map[string]any, string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		// bare `echo` — prints a blank line
		return []map[string]any{execStmt([]map[string]any{str("echo")}, "Emulable")}, "", nil
	}
	if strings.EqualFold(rest, "off") || strings.EqualFold(rest, "on") {
		// echo-control directive — no runtime effect on the transpiled output
		return nil, "", nil
	}
	if rest == "." || rest == ":" {
		// `echo.` / `echo:` — blank line
		return []map[string]any{execStmt([]map[string]any{str("echo")}, "Emulable")}, "", nil
	}
	words, err := splitWords(rest, p.forVars)
	if err != nil {
		return nil, "", err
	}
	return []map[string]any{execStmt(append([]map[string]any{str("echo")}, words...), "Emulable")}, "", nil
}

// ── set ─────────────────────────────────────────────────────────────

func (p *parser) parseSet(rest string) ([]map[string]any, string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, "", fmt.Errorf("unsupported: bare `set` (variable listing)")
	}
	if strings.HasPrefix(strings.ToLower(rest), "/a ") {
		return p.parseSetA(strings.TrimSpace(rest[3:]))
	}
	if strings.HasPrefix(strings.ToLower(rest), "/p ") || strings.HasPrefix(strings.ToLower(rest), "/p") {
		return nil, "", fmt.Errorf("unsupported: set /p (interactive prompt)")
	}
	// set "name=value" — the quotes may wrap name=value; otherwise the
	// whole rest is name=value.
	body := rest
	if strings.HasPrefix(body, "\"") {
		end := strings.Index(body[1:], "\"")
		if end < 0 {
			return nil, "", fmt.Errorf("unterminated quote in set: %q", rest)
		}
		body = body[1 : 1+end]
	}
	eq := strings.Index(body, "=")
	if eq < 0 {
		return nil, "", fmt.Errorf("set without '=': %q", rest)
	}
	name := strings.ToLower(strings.TrimSpace(body[:eq]))
	value := body[eq+1:]
	expr, err := expandWord(value, p.forVars)
	if err != nil {
		return nil, "", err
	}
	return []map[string]any{assignStmt(name, expr)}, "", nil
}

// set /a VAR=expr — mini arithmetic parser (Int + - * / % parens).
func (p *parser) parseSetA(rest string) ([]map[string]any, string, error) {
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return nil, "", fmt.Errorf("set /a without '=': %q", rest)
	}
	name := strings.ToLower(strings.TrimSpace(rest[:eq]))
	ap := &arithParser{src: rest[eq+1:]}
	ast, err := ap.parseExpr()
	if err != nil {
		return nil, "", err
	}
	if ap.i < len(ap.src) {
		return nil, "", fmt.Errorf("set /a trailing text: %q", ap.src[ap.i:])
	}
	return []map[string]any{assignStmt(name, map[string]any{"type": "Arith", "ast": ast})}, "", nil
}

// ── if ──────────────────────────────────────────────────────────────

func (p *parser) parseIf(rest string) ([]map[string]any, string, error) {
	rest = strings.TrimSpace(rest)
	neg := false
	if strings.HasPrefix(strings.ToLower(rest), "not ") {
		neg = true
		rest = strings.TrimSpace(rest[4:])
	}
	// condition: up to the first `(` (block form) or, in the command
	// form, up to the first unquoted space.
	cond, cmd, isBlock, err := splitCondition(rest)
	if err != nil {
		return nil, "", err
	}
	if cond == "" {
		return nil, "", fmt.Errorf("if with empty condition: %q", rest)
	}
	testText := cond
	if neg {
		testText = "!" + testText
	}
	condExpr := testCall(batchTestToShell(testText))

	var thenStmts []map[string]any
	elseStmts := []map[string]any{}
	var rem string
	if isBlock {
		thenStmts, rem, err = p.parseParenBlock(cmd)
		if err != nil {
			return nil, "", err
		}
	} else {
		thenStmts, rem, err = p.parseCommand(cmd)
		if err != nil {
			return nil, "", err
		}
	}
	// optional else — only meaningful with the parenthesized form
	// (real cmd requires `) else (` on the same line)
	rem = strings.TrimSpace(rem)
	if strings.HasPrefix(strings.ToLower(rem), "else") {
		rem = strings.TrimSpace(rem[4:])
		if strings.HasPrefix(rem, "(") {
			elseStmts, rem, err = p.parseParenBlock(rem)
			if err != nil {
				return nil, "", err
			}
		} else {
			elseStmts, rem, err = p.parseCommand(rem)
			if err != nil {
				return nil, "", err
			}
		}
	}
	if rem != "" {
		return nil, "", fmt.Errorf("if trailing text: %q", rem)
	}
	return []map[string]any{{
		"type":   "If",
		"cond":   condExpr,
		"then":   thenStmts,
		"elsifs": []any{},
		"else":   elseStmts,
	}}, "", nil
}

// splitCondition splits "A==B (cmd)" into (cond, rest, isBlock).
// The condition runs to the first `(` (block form) or the first
// unquoted space (command form).
func splitCondition(s string) (string, string, bool, error) {
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inQuote = c
		case '(':
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i:]), true, nil
		case ' ':
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i:]), false, nil
		}
	}
	return strings.TrimSpace(s), "", false, nil
}

// batchTestToShell adapts a batch if-condition to the shell-flavored
// test text the A1 test call carries: %var% -> $var, == stays ==.
func batchTestToShell(t string) string {
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '%' {
			if j := strings.IndexByte(t[i+1:], '%'); j >= 0 && !strings.ContainsAny(t[i+1:i+1+j], " \t\"") {
				name := t[i+1 : i+1+j]
				b.WriteString("$" + strings.ToLower(name))
				i += j + 1
				continue
			}
		}
		b.WriteByte(t[i])
	}
	return b.String()
}

// parseParenBlock parses "( ... )" — content on the same line, or
// spanning subsequent lines until a bare `)`. Returns the inner stmts
// and the remainder after the closing paren.
func (p *parser) parseParenBlock(s string) ([]map[string]any, string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") {
		return nil, "", fmt.Errorf("expected '(': %q", s)
	}
	// same-line close?
	if end := findMatchingParen(s); end >= 0 {
		inner := strings.TrimSpace(s[1:end])
		rem := strings.TrimSpace(s[end+1:])
		if inner == "" {
			return []map[string]any{}, rem, nil
		}
		// the inner text may hold several commands (possibly with their
		// own nested parens); reuse parseLine for the & splitting.
		st, err := p.parseLine(inner)
		if err != nil {
			return nil, "", err
		}
		return st, rem, nil
	}
	// multi-line block: consume lines until one starting with `)`
	// (which may carry `else ( ... )` after it — real cmd requires the
	// else on the closing-paren line)
	var st []map[string]any
	rem := ""
	for p.idx < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.idx])
		p.idx++
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ")") {
			rem = strings.TrimSpace(line[1:])
			return st, rem, nil
		}
		more, err := p.parseLine(line)
		if err != nil {
			return nil, "", err
		}
		st = append(st, more...)
	}
	return nil, "", fmt.Errorf("unterminated ( block")
}

// findMatchingParen finds the index of the ')' matching the first '(' of
// s (s must start with '('). Returns -1 if unbalanced.
func findMatchingParen(s string) int {
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inQuote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ── for ─────────────────────────────────────────────────────────────

func (p *parser) parseFor(rest string) ([]map[string]any, string, error) {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "/") {
		return nil, "", fmt.Errorf("unsupported: for /%c (only the plain word-list form is in v1)", rest[1])
	}
	if !strings.HasPrefix(rest, "%%") {
		return nil, "", fmt.Errorf("expected %%var in for: %q", rest)
	}
	sp := strings.IndexByte(rest[2:], ' ')
	if sp < 0 {
		return nil, "", fmt.Errorf("malformed for: %q", rest)
	}
	varName := strings.ToLower(rest[2 : 2+sp])
	rest = strings.TrimSpace(rest[2+sp:])
	if !strings.HasPrefix(strings.ToLower(rest), "in ") {
		return nil, "", fmt.Errorf("expected 'in' in for: %q", rest)
	}
	rest = strings.TrimSpace(rest[3:])
	items, rest, err := p.parseParenList(rest)
	if err != nil {
		return nil, "", err
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(strings.ToLower(rest), "do ") {
		return nil, "", fmt.Errorf("expected 'do' in for: %q", rest)
	}
	rest = strings.TrimSpace(rest[3:])
	// inside the body, `%%v` reads the loop var (batch's only scoped name)
	p.forVars[varName] = true
	var body []map[string]any
	if strings.HasPrefix(rest, "(") {
		body, rest, err = p.parseParenBlock(rest)
		if err != nil {
			return nil, "", err
		}
	} else {
		body, rest, err = p.parseCommand(rest)
		if err != nil {
			delete(p.forVars, varName)
			return nil, "", err
		}
	}
	delete(p.forVars, varName)
	if rest != "" {
		return nil, "", fmt.Errorf("for trailing text: %q", rest)
	}
	return []map[string]any{{
		"type": "For",
		"var":  varName,
		"iter": map[string]any{"type": "Array", "elements": items},
		"body": body,
	}}, "", nil
}

// parseParenList parses "( item1 item2 ... )" — whitespace-split words.
func (p *parser) parseParenList(s string) ([]map[string]any, string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") {
		return nil, "", fmt.Errorf("expected '(' in for list: %q", s)
	}
	end := findMatchingParen(s)
	if end < 0 {
		return nil, "", fmt.Errorf("unterminated for list: %q", s)
	}
	inner := strings.TrimSpace(s[1:end])
	rem := strings.TrimSpace(s[end+1:])
	var items []map[string]any
	for _, w := range strings.Fields(inner) {
		e, err := expandWord(w, p.forVars)
		if err != nil {
			return nil, "", err
		}
		items = append(items, e)
	}
	return items, rem, nil
}

func (p *parser) parseExit(rest string) ([]map[string]any, string, error) {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(strings.ToLower(rest), "/b") {
		rest = strings.TrimSpace(rest[2:])
	}
	// exit /b [N] — the direct IrStmt::Exit statement form (all backends
	// render it: estree -> process.exit, sh -> exit, perl -> exit).
	// `exit` without a code exits with the last status (lastExit).
	if rest == "" {
		return []map[string]any{{"type": "Exit", "value": nil}}, "", nil
	}
	var n int
	if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
		return nil, "", fmt.Errorf("exit /b code not an integer: %q", rest)
	}
	return []map[string]any{{"type": "Exit", "value": map[string]any{"type": "Int", "value": n}}}, "", nil
}

// ── words & expansion ───────────────────────────────────────────────

// splitWords splits a command argument string into words on whitespace
// (v1: no quoting-aware grouping — batch quotes are mostly cosmetic in
// echo; refinement is a worker item).
func splitWords(s string, forVars map[string]bool) ([]map[string]any, error) {
	var out []map[string]any
	for _, w := range strings.Fields(s) {
		e, err := expandWord(w, forVars)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// expandWord turns a batch word into an A1 expr: plain -> Str, with
// %var% refs -> Interpolate (or a bare getVar for a single ref). forVars
// are the active `for %%v` loop vars: inside a for body, `%%v` is the
// loop-var read, not an escaped literal %.
func expandWord(w string, forVars map[string]bool) (map[string]any, error) {
	if !strings.Contains(w, "%") {
		return str(w), nil
	}
	var parts []map[string]any
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, map[string]any{"kind": "lit", "text": lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(w); i++ {
		if w[i] != '%' {
			lit.WriteByte(w[i])
			continue
		}
		// %% -> literal %, EXCEPT inside a for body where `%%v` is the
		// loop-var read
		if i+1 < len(w) && w[i+1] == '%' {
			j := i + 2
			for j < len(w) && isNameChar(w[j]) {
				j++
			}
			name := strings.ToLower(w[i+2 : j])
			if name != "" && forVars[name] {
				flush()
				parts = append(parts, map[string]any{"kind": "expr", "expr": getVar(name)})
				i = j - 1
				continue
			}
			lit.WriteByte('%')
			i++
			continue
		}
		// %1..%9 / %* — positionals WITHOUT a closing % (batch form)
		if i+1 < len(w) && (w[i+1] >= '1' && w[i+1] <= '9' || w[i+1] == '*') {
			flush()
			parts = append(parts, map[string]any{"kind": "expr", "expr": getVar(strings.ToLower(string(w[i+1])))})
			i++
			continue
		}
		// %name% (no whitespace/quotes in the name)
		j := strings.IndexByte(w[i+1:], '%')
		if j < 0 {
			// stray % — keep literal (echo % is legal-ish)
			lit.WriteByte('%')
			continue
		}
		name := w[i+1 : i+1+j]
		if name == "" || strings.ContainsAny(name, " \t\"") {
			lit.WriteByte('%')
			continue
		}
		flush()
		parts = append(parts, map[string]any{"kind": "expr", "expr": getVarFor(name)})
		i += j + 1
	}
	flush()
	if len(parts) == 0 {
		return str(w), nil
	}
	if len(parts) == 1 && parts[0]["kind"] == "expr" {
		return parts[0]["expr"].(map[string]any), nil
	}
	return map[string]any{"type": "Interpolate", "parts": parts}, nil
}

// getVarFor maps a batch %name% to the shell-flavored A1 read:
// %errorlevel% -> $? (getVar("?")), %1..%9 / %* positionals -> getVar.
func getVarFor(name string) map[string]any {
	n := strings.ToLower(name)
	if n == "errorlevel" {
		n = "?"
	}
	return getVar(n)
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// ── A1 node builders ────────────────────────────────────────────────

func str(s string) map[string]any {
	return map[string]any{"type": "Str", "value": s, "style": "DoubleQuoted"}
}

func getVar(name string) map[string]any {
	return map[string]any{
		"type": "Call", "func": "getVar",
		"args": []any{str(name)}, "purity": "Emulable",
	}
}

func execStmt(words []map[string]any, purity string) map[string]any {
	return map[string]any{
		"type": "Expr",
		"expr": map[string]any{
			"type": "Call", "func": "exec",
			"args":   []any{words[0], map[string]any{"type": "Array", "elements": words[1:]}},
			"purity": purity,
		},
	}
}

func assignStmt(name string, expr map[string]any) map[string]any {
	return map[string]any{
		"type": "Assign",
		"expr": expr,
		"targets": []any{
			map[string]any{"var": name, "sigil": nil, "indices": []any{}},
		},
	}
}

func testCall(text string) map[string]any {
	return map[string]any{
		"type": "Call", "func": "test",
		"args":   []any{str(text), str("[[")},
		"purity": "Emulable",
	}
}

// ── set /a arithmetic ───────────────────────────────────────────────

type arithParser struct {
	src string
	i   int
}

func (a *arithParser) parseExpr() (map[string]any, error) {
	lhs, err := a.parseTerm()
	if err != nil {
		return nil, err
	}
	for a.i < len(a.src) {
		c := a.src[a.i]
		if c == '+' || c == '-' {
			a.i++
			rhs, err := a.parseTerm()
			if err != nil {
				return nil, err
			}
			lhs = map[string]any{"type": "Bin", "op": string(c), "lhs": lhs, "rhs": rhs}
		} else {
			break
		}
	}
	return lhs, nil
}

func (a *arithParser) parseTerm() (map[string]any, error) {
	lhs, err := a.parseFactor()
	if err != nil {
		return nil, err
	}
	for a.i < len(a.src) {
		c := a.src[a.i]
		if c == '*' || c == '/' || c == '%' {
			a.i++
			rhs, err := a.parseFactor()
			if err != nil {
				return nil, err
			}
			lhs = map[string]any{"type": "Bin", "op": string(c), "lhs": lhs, "rhs": rhs}
		} else {
			break
		}
	}
	return lhs, nil
}

func (a *arithParser) parseFactor() (map[string]any, error) {
	for a.i < len(a.src) && a.src[a.i] == ' ' {
		a.i++
	}
	if a.i >= len(a.src) {
		return nil, fmt.Errorf("set /a: unexpected end of expression")
	}
	if a.src[a.i] == '(' {
		a.i++
		inner, err := a.parseExpr()
		if err != nil {
			return nil, err
		}
		for a.i < len(a.src) && a.src[a.i] == ' ' {
			a.i++
		}
		if a.i >= len(a.src) || a.src[a.i] != ')' {
			return nil, fmt.Errorf("set /a: missing ')'")
		}
		a.i++
		return inner, nil
	}
	if a.src[a.i] == '%' {
		end := strings.IndexByte(a.src[a.i+1:], '%')
		if end < 0 {
			return nil, fmt.Errorf("set /a: unterminated %%var%%")
		}
		name := strings.ToLower(a.src[a.i+1 : a.i+1+end])
		a.i += end + 2
		return map[string]any{"type": "Var", "name": name}, nil
	}
	// number
	j := a.i
	for j < len(a.src) && (a.src[j] >= '0' && a.src[j] <= '9') {
		j++
	}
	if j == a.i {
		return nil, fmt.Errorf("set /a: unexpected char %q", a.src[a.i])
	}
	var n int
	fmt.Sscanf(a.src[a.i:j], "%d", &n)
	a.i = j
	return map[string]any{"type": "Num", "value": n}, nil
}
