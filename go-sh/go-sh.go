// go-sh — shell-subset frontend emitting ShIR JSON (the A1 contract).
//
// Hand-rolled Go port of frontends/py-sh/pysh.py. Targets the same v1
// subset and the same pinned word-shaping / type / purity rules
// (frontends/plan.md L2, L3). Single file, no external runtime deps.
//
// Build:  go build -o go-sh .
// Run:    ./go-sh --shir file.sh        # emit ShIR JSON to stdout
//         ./go-sh --shir file.sh --raw  # compact, no trailing newline
//
// Why hand-rolled (not ANTLR): (a) no ANTLR toolchain in the scaffold
// environment, and the v1-subset semantics are the hard part (not the
// syntax); (b) shell word expansion is context-sensitive and ANTLR's
// pipeline is a poor fit; (c) the emitter is a mechanical port of the
// proven pysh reference, so the grammar matters less than mirroring the
// pinned lowering rules. To regenerate the parser from a grammar
// later, see README ("ANTLR port").
//
// Contract: sh2perl/src/shir_json.rs CONTRACT_VERSION (=1). The
// SYNC_BUILTINS list is embedded from sh2perl/data/sh2-builtins.json
// (regenerate with `make sync-builtins` or the script in README; the
// JSON is the source of truth — this embed is a scaffold-time copy).
//
// Status: SCAFFOLD — not compiled or run in this environment (no `go`
// toolchain). Validated by code review against pysh; the byte-equality
// oracle (frontends/equiv.py) and pipe test (frontends/test_pipe.py)
// apply unchanged when the binary is built.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// CONTRACT_VERSION must match sh2perl/src/shir_json.rs CONTRACT_VERSION.
const CONTRACT_VERSION = 1

// SYNC_BUILTINS embedded from sh2perl/data/sh2-builtins.json (A4 namespace).
// Source of truth is the JSON; this is a scaffold-time copy. The test
// `a4_sync_builtins_matches_rust` in sh2perl asserts the two stay in sync.
var SYNC_BUILTINS = map[string]bool{
	".": true, ":": true, "basename": true, "break": true, "cat": true,
	"cd": true, "cmp": true, "comm": true, "continue": true, "cut": true,
	"declare": true, "dirname": true, "echo": true, "eval": true,
	"exit": true, "export": true, "false": true, "head": true, "let": true,
	"local": true, "mapfile": true, "mktemp": true, "printf": true,
	"pwd": true, "read": true, "readarray": true, "readonly": true,
	"return": true, "sed": true, "seq": true, "set": true, "shift": true,
	"sort": true, "source": true, "stat": true, "tail": true, "test": true,
	"touch": true, "tr": true, "trap": true, "true": true, "type": true,
	"typeset": true, "uniq": true, "unset": true, "wc": true,
}

// REFUSE_BUILTINS — store-writing / arith / control builtins; v1 fails
// loud rather than half-lowering (frontends/plan.md L5).
var REFUSE_BUILTINS = map[string]bool{
	"let": true, "eval": true, "declare": true, "typeset": true,
	"local": true, "readonly": true, "export": true, "unset": true,
	"read": true, "readarray": true, "mapfile": true, "shift": true,
	"source": true, "trap": true, "set": true, "exec": true, "test": true,
	"time": true, "[": true, "[[": true, ".": true,
}

// KEYWORDS — compound-command keywords; v1 supports simple commands only.
var KEYWORDS = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"while": true, "do": true, "done": true, "until": true, "for": true,
	"case": true, "esac": true, "function": true, "select": true,
	"[[": true, "!": true, "{": true, "}": true,
}

// Unsupported: construct outside the v1 subset — fail loud, never guess.
type Unsupported struct{ msg string }

func (u *Unsupported) Error() string { return "UNSUPPORTED: " + u.msg }

// ── JSON helpers (build maps; encoding/json sorts map keys alphabetically,
// producing the same compact, sorted, UTF-8 layout as serde_json's BTreeMap,
// which is what the A1 contract uses. Byte-equality with pysh depends on
// this — do not switch to ordered structs or a custom marshaler.)

func O(m map[string]any) map[string]any { return m }

func stmt(type_ string, kw map[string]any) map[string]any {
	m := map[string]any{"type": type_}
	for k, v := range kw {
		m[k] = v
	}
	return m
}

func expr(type_ string, kw map[string]any) map[string]any {
	m := map[string]any{"type": type_}
	for k, v := range kw {
		m[k] = v
	}
	return m
}

func str_(v string) map[string]any {
	return expr("Str", map[string]any{"value": v, "style": "DoubleQuoted"})
}

func getVar(name string) map[string]any {
	return expr("Call", map[string]any{
		"func": "getVar", "args": []any{str_(name)}, "purity": "Emulable",
	})
}

func execCall(cmd string, args []any, env []any) map[string]any {
	callArgs := []any{str_(cmd), expr("Array", map[string]any{"elements": args})}
	if env != nil {
		callArgs = append(callArgs, expr("Object", map[string]any{
			"properties": env,
		}))
	}
	purity := "Spawn"
	if SYNC_BUILTINS[cmd] {
		purity = "Emulable"
	}
	return expr("Call", map[string]any{
		"func": "exec", "args": callArgs, "purity": purity,
	})
}

func interp(parts []any) map[string]any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		t := p.([]any) // [kind, value]
		if t[0] == "lit" {
			out = append(out, map[string]any{"kind": "lit", "text": t[1]})
		} else {
			out = append(out, map[string]any{"kind": "expr", "expr": t[1]})
		}
	}
	return expr("Interpolate", map[string]any{"parts": out})
}

// dump serializes a Program (map) as compact, sorted, UTF-8 JSON —
// byte-comparable to serde_json's compact output (the A1 contract).
func dump(program map[string]any) ([]byte, error) {
	return json.Marshal(program)
}

// ── word scanning ─────────────────────────────────────────────────────
// Segments: ["lit", text] | ["sq", text] | ["dq", [("lit"|"ref", ...)]] | ["ref", name]

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !(first == '_' || (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c != '_' && !isAlphaNum_(c) {
			return false
		}
	}
	return true
}

func isAlphaNum_(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isWordStop(c byte) bool {
	// Structural separators only. Chars with dedicated branches
	// (apostrophe, dq, ref '$', escape '\\', backtick, '{') are
	// EXCLUDED — the run must stop and the dedicated branch must fire.
	return c == ' ' || c == '\t' || c == '|' || c == ';' || c == '(' ||
		c == ')' || c == '<' || c == '>' || c == '\n' || c == '#'
}

func scanWord(src string, i int) (segs [][]any, startsWithDQ bool, ni int, err error) {
	first := true
	for i < len(src) {
		c := src[i]
		// dedicated branches first (consume the char and continue)
		if c == '\'' {
			j := strings.IndexByte(src[i+1:], '\'')
			if j < 0 {
				return nil, false, i, &Unsupported{"unterminated single quote"}
			}
			segs = append(segs, []any{"sq", src[i+1 : i+1+j]})
			i = i + 1 + j + 1
		} else if c == '"' {
			if first {
				startsWithDQ = true
			}
			parts := [][]any{}
			hasNewline, hasEscQuote := false, false
			j := i + 1
			for j < len(src) {
				if src[j] == '\n' {
					hasNewline = true
				}
				if src[j] == '\\' && j+1 < len(src) && src[j+1] == '"' {
					hasEscQuote = true
				}
				if src[j] == '"' {
					break
				}
				if src[j] == '\\' && j+1 < len(src) {
					nxt := src[j+1]
					if nxt == '\n' {
						// dq line continuation: \<newline> → drop both
						j += 2
						continue
					}
					if nxt == '\\' {
						parts = append(parts, []any{"lit", "\\"})
						j += 2
						continue
					}
					if nxt == '$' {
						parts = append(parts, []any{"lit", "$"})
						j += 2
						continue
					}
					parts = append(parts, []any{"lit", string([]byte{src[j], nxt})})
					j += 2
					continue
				}
				if src[j] == '$' {
					n, j2, err2 := scanRef(src, j)
					if err2 != nil {
						return nil, false, i, err2
					}
					parts = append(parts, []any{"ref", n})
					j = j2
					continue
				}
				// accumulate literal run
				k := j
				for k < len(src) && src[k] != '"' && src[k] != '\\' && src[k] != '$' {
					if src[k] == '\n' {
						hasNewline = true
					}
					k++
				}
				parts = append(parts, []any{"lit", src[j:k]})
				j = k
			}
			if j >= len(src) {
				return nil, false, i, &Unsupported{"unterminated double quote"}
			}
			if hasNewline && hasEscQuote {
				return nil, false, i, &Unsupported{
					"escaped quote inside multiline double-quoted word",
				}
			}
			segs = append(segs, []any{"dq", parts})
			i = j + 1
		} else if c == '$' {
			n, j2, err2 := scanRef(src, i)
			if err2 != nil {
				return nil, false, i, err2
			}
			segs = append(segs, []any{"ref", n})
			i = j2
		} else if c == '\\' {
			if i+1 >= len(src) {
				return nil, false, i, &Unsupported{"trailing backslash"}
			}
			// unquoted \X → X (drop backslash); pinned: \$x→$x, a\ b→a b,
			// \<newline>→newline kept (NOT line continuation in unquoted).
			segs = append(segs, []any{"lit", string([]byte{src[i+1]})})
			i += 2
		} else if c == '`' || c == '{' {
			return nil, false, i, &Unsupported{
				fmt.Sprintf("construct char %q outside v1 subset", c),
			}
		} else if isWordStop(c) {
			// structural separator (space, ;, |, #, etc.) ends the word
			break
		} else {
			// accumulate a run of literal chars; stop at structural
			// word-boundaries AND at chars with dedicated branches
			// (the outer loop handles '\''/"'/$/\\/`/{ next iteration)
			k := i
			for k < len(src) && !isWordStop(src[k]) && src[k] != '\'' && src[k] != '"' && src[k] != '$' && src[k] != '\\' && src[k] != '`' && src[k] != '{' {
				k++
			}
			if k == i {
				segs = append(segs, []any{"lit", string([]byte{c})})
				i++
			} else {
				segs = append(segs, []any{"lit", src[i:k]})
				i = k
			}
		}
		first = false
	}
	return segs, startsWithDQ, i, nil
}

func scanRef(src string, i int) (name string, ni int, err error) {
	if src[i] != '$' {
		return "", i, &Unsupported{"internal: scanRef"}
	}
	if i+1 < len(src) && (src[i+1] == '(' || src[i+1] == '{' || src[i+1] == '[' || src[i+1] == '`') {
		return "", i, &Unsupported{"$ with complex expansion outside v1 subset"}
	}
	j := i + 1
	if j < len(src) && (isAlpha_(src[j]) || src[j] == '_') {
		k := j
		for k < len(src) && (isAlphaNum_(src[k]) || src[k] == '_') {
			k++
		}
		return src[j:k], k, nil
	}
	return "", i, &Unsupported{
		fmt.Sprintf("$ followed by %q outside v1 subset", src[j]),
	}
}

func isAlpha_(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func segsToText(segs [][]any) string {
	var b strings.Builder
	for _, s := range segs {
		switch s[0] {
		case "lit":
			b.WriteString(s[1].(string))
		case "sq":
			b.WriteString(s[1].(string))
		case "dq":
			for _, p := range s[1].([][]any) {
				if p[0] == "lit" {
					b.WriteString(p[1].(string))
				}
			}
		}
	}
	return b.String()
}

func hasRef(segs [][]any) bool {
	for _, s := range segs {
		if s[0] == "ref" {
			return true
		}
		if s[0] == "dq" {
			for _, p := range s[1].([][]any) {
				if p[0] == "ref" {
					return true
				}
			}
		}
	}
	return false
}

func wordExpr(segs [][]any, startsWithDQ bool) map[string]any {
	refs := hasRef(segs)
	if !refs {
		text := segsToText(segs)
		if startsWithDQ {
			return interp([]any{[]any{"lit", text}})
		}
		return str_(text)
	}
	parts := []any{}
	for _, s := range segs {
		switch s[0] {
		case "lit":
			parts = append(parts, []any{"lit", s[1].(string)})
		case "sq":
			parts = append(parts, []any{"lit", s[1].(string)})
		case "ref":
			parts = append(parts, []any{"expr", getVar(s[1].(string))})
		case "dq":
			for _, p := range s[1].([][]any) {
				if p[0] == "ref" {
					parts = append(parts, []any{"expr", getVar(p[1].(string))})
				} else {
					parts = append(parts, []any{"lit", p[1].(string)})
				}
			}
		}
	}
	if len(parts) == 1 && parts[0].([]any)[0] == "expr" {
		return parts[0].([]any)[1].(map[string]any)
	}
	return interp(parts)
}

// ── statement parsing ─────────────────────────────────────────────────

// splitAssignment: word of the form "name=value…" at command position.
func splitAssignment(segs [][]any, startsWithDQ bool) (name string, valueSegs [][]any, valueStartsDQ bool, ok bool) {
	if startsWithDQ || len(segs) == 0 {
		return "", nil, false, false
	}
	first := segs[0]
	if first[0] != "lit" {
		return "", nil, false, false
	}
	text := first[1].(string)
	eq := strings.IndexByte(text, '=')
	if eq < 0 {
		return "", nil, false, false
	}
	n := text[:eq]
	if !isIdent(n) {
		return "", nil, false, false
	}
	vs := [][]any{}
	if eq+1 < len(text) {
		vs = append(vs, []any{"lit", text[eq+1:]})
	}
	vs = append(vs, segs[1:]...)
	// drop leading empty lit (x="a" → lit "x=" + dq)
	if len(vs) > 0 && vs[0][0] == "lit" && vs[0][1].(string) == "" {
		vs = vs[1:]
	}
	vsd := len(vs) > 0 && vs[0][0] == "dq"
	return n, vs, vsd, true
}

func parseLine(src string, i int) (words [][2]any, ni int, err error) {
	// words: list of (segs, startsWithDQ)
	words = [][2]any{}
	n := len(src)
	for i < n {
		c := src[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '#' {
			nl := strings.IndexByte(src[i:], '\n')
			if nl < 0 {
				return words, n, nil
			}
			return words, i + nl, nil
		}
		if c == ';' || c == '|' || c == '\n' {
			return words, i, nil
		}
		if c == '&' || c == '(' || c == ')' {
			return nil, i, &Unsupported{fmt.Sprintf("operator %q outside v1 subset", c)}
		}
		prevI := i
		segs, swdq, ni2, err2 := scanWord(src, i)
		if err2 != nil {
			return nil, i, err2
		}
		if len(segs) == 0 {
			if ni2 == prevI {
				return nil, i, &Unsupported{
					fmt.Sprintf("operator %q outside v1 subset", src[i]),
				}
			}
			i = ni2
			continue
		}
		words = append(words, [2]any{segs, swdq})
		i = ni2
	}
	return words, n, nil
}

func parseStatement(words [][2]any) ([]map[string]any, error) {
	stmts := []map[string]any{}
	assigns := []any{} // [name, valueExpr]
	i := 0
	for i < len(words) {
		segs := words[i][0].([][]any)
		swdq := words[i][1].(bool)
		name, vs, vsd, ok := splitAssignment(segs, swdq)
		if !ok {
			break
		}
		assigns = append(assigns, []any{name, wordExpr(vs, vsd)})
		i++
	}
	if i < len(words) {
		cmdSegs := words[i][0].([][]any)
		// v1: command name must be a plain literal (no refs/quotes)
		for _, s := range cmdSegs {
			if s[0] != "lit" {
				return nil, &Unsupported{"command name with refs/quotes outside v1 subset"}
			}
		}
		cmd := segsToText(cmdSegs)
		if cmd == "" {
			return nil, &Unsupported{"empty command name"}
		}
		if KEYWORDS[cmd] || REFUSE_BUILTINS[cmd] {
			return nil, &Unsupported{cmd + " outside v1 subset (keyword/builtin)"}
		}
		args := []any{}
		for j := i + 1; j < len(words); j++ {
			segs2 := words[j][0].([][]any)
			swdq2 := words[j][1].(bool)
			args = append(args, wordExpr(segs2, swdq2))
		}
		var env []any
		if len(assigns) > 0 {
			env = make([]any, 0, len(assigns))
			for _, a := range assigns {
				pair := a.([]any)
				env = append(env, map[string]any{"key": pair[0].(string), "value": pair[1]})
			}
		}
		stmts = append(stmts, stmt("Expr", map[string]any{
			"expr": execCall(cmd, args, env),
		}))
	} else {
		for _, a := range assigns {
			pair := a.([]any)
			stmts = append(stmts, stmt("Assign", map[string]any{
				"targets": []any{map[string]any{
					"var": pair[0].(string), "sigil": nil, "indices": []any{},
				}},
				"expr": pair[1],
			}))
		}
	}
	return stmts, nil
}

// ── A2 var_types (subset of analyze_var_types) ───────────────────────

func collectAssigns(stmts []map[string]any) (assigns map[string][]map[string]any, excluded map[string]bool) {
	assigns = map[string][]map[string]any{}
	excluded = map[string]bool{}
	var walkExpr func(e map[string]any)
	walkExpr = func(e map[string]any) {
		t, _ := e["type"].(string)
		switch t {
		case "Call":
			if f, _ := e["func"].(string); f == "exec" {
				if args, _ := e["args"].([]any); len(args) >= 3 {
					if obj, ok := args[2].(map[string]any); ok {
						if props, _ := obj["properties"].([]any); ok {
							for _, p := range props {
								pm := p.(map[string]any)
								if k, _ := pm["key"].(string); k != "" {
									excluded[k] = true
								}
							}
						}
					}
				}
			}
			if args, ok := e["args"].([]any); ok {
				for _, a := range args {
					if am, ok := a.(map[string]any); ok {
						walkExpr(am)
					}
				}
			}
		case "Array":
			if els, ok := e["elements"].([]any); ok {
				for _, el := range els {
					if em, ok := el.(map[string]any); ok {
						walkExpr(em)
					}
				}
			}
		case "Object":
			if props, ok := e["properties"].([]any); ok {
				for _, p := range props {
					if pm, ok := p.(map[string]any); ok {
						if v, ok := pm["value"].(map[string]any); ok {
							walkExpr(v)
						}
					}
				}
			}
		case "Interpolate":
			if parts, ok := e["parts"].([]any); ok {
				for _, p := range parts {
					if pm, ok := p.(map[string]any); ok {
						if v, ok := pm["expr"].(map[string]any); ok {
							walkExpr(v)
						}
					}
				}
			}
		}
	}
	for _, s := range stmts {
		t, _ := s["type"].(string)
		switch t {
		case "Assign":
			if tg, _ := s["targets"].([]any); len(tg) > 0 {
				if t0, ok := tg[0].(map[string]any); ok {
					if ind, _ := t0["indices"].([]any); len(ind) > 0 {
						if v, _ := t0["var"].(string); v != "" {
							excluded[v] = true
						}
					} else {
						if v, _ := t0["var"].(string); v != "" {
							assigns[v] = append(assigns[v], s["expr"].(map[string]any))
						}
					}
				}
			}
			if e, ok := s["expr"].(map[string]any); ok {
				walkExpr(e)
			}
		case "Expr":
			if e, ok := s["expr"].(map[string]any); ok {
				walkExpr(e)
			}
		}
	}
	return
}

func sourceNumeric(e map[string]any, lifted map[string]bool) bool {
	t, _ := e["type"].(string)
	switch t {
	case "Int":
		return true
	case "Str":
		if v, _ := e["value"].(string); v != "" {
			_, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return err == nil
		}
	case "Call":
		if f, _ := e["func"].(string); f == "getVar" {
			if args, _ := e["args"].([]any); len(args) > 0 {
				if a0, ok := args[0].(map[string]any); ok {
					if v, _ := a0["value"].(string); lifted[v] {
						return true
					}
				}
			}
		}
	}
	return false
}

func sourceStringLit(e map[string]any, lifted map[string]bool) bool {
	t, _ := e["type"].(string)
	switch t {
	case "Str":
		// pinned: multiline Str values are NOT lifted (core keeps var_types empty)
		if v, _ := e["value"].(string); strings.Contains(v, "\n") {
			return false
		}
		return true
	case "Interpolate":
		// pinned: any interpolation is string-typed by construction
		// (e.g. dest_root=$emmccheck'p3' → Str verdict in the core)
		return true
	case "Call":
		if f, _ := e["func"].(string); f == "getVar" {
			if args, _ := e["args"].([]any); len(args) > 0 {
				if a0, ok := args[0].(map[string]any); ok {
					if v, _ := a0["value"].(string); lifted[v] {
						return true
					}
				}
			}
		}
	}
	return false
}

func varTypes(stmts []map[string]any) []any {
	assigns, excluded := collectAssigns(stmts)
	numeric := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for name, sources := range assigns {
			if numeric[name] || excluded[name] {
				continue
			}
			all := true
			for _, s := range sources {
				if !sourceNumeric(s, numeric) {
					all = false
					break
				}
			}
			if all {
				numeric[name] = true
				changed = true
			}
		}
	}
	stringSet := map[string]bool{}
	changed = true
	for changed {
		changed = false
		for name, sources := range assigns {
			if numeric[name] || stringSet[name] || excluded[name] {
				continue
			}
			all := true
			for _, s := range sources {
				if !sourceStringLit(s, stringSet) {
					all = false
					break
				}
			}
			if all {
				stringSet[name] = true
				changed = true
			}
		}
	}
	names := []string{}
	for n := range numeric {
		names = append(names, n)
	}
	for n := range stringSet {
		if !numeric[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, n := range names {
		t := "Str"
		if numeric[n] {
			t = "Int"
		}
		out = append(out, map[string]any{"name": n, "type": t})
	}
	return out
}

// ── program assembly ──────────────────────────────────────────────────

func compile(src string) (map[string]any, error) {
	stmts := []map[string]any{}
	i := 0
	n := len(src)
	for i < n {
		if src[i] == ' ' || src[i] == '\t' || src[i] == '\n' {
			i++
			continue
		}
		prevI := i
		words, ni, err := parseLine(src, i)
		if err != nil {
			return nil, err
		}
		if len(words) == 0 {
			if ni == prevI {
				return nil, &Unsupported{fmt.Sprintf("operator %q outside v1 subset", src[i])}
			}
			i = ni
			continue
		}
		i = ni
		ss, err := parseStatement(words)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, ss...)
		// advance past separator
		for i < n && (src[i] == ' ' || src[i] == '\t') {
			i++
		}
		if i < n && src[i] == ';' {
			i++
		}
	}
	return map[string]any{
		"type":            "Program",
		"contract_version": CONTRACT_VERSION,
		"imports":         []any{},
		"requires":        []any{},
		"var_types":       varTypes(stmts),
		"subs":            []any{},
		"stmts":           stmts,
	}, nil
}

// ── main ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]
	raw := false
	filtered := []string{}
	for _, a := range args {
		if a == "--raw" {
			raw = true
		} else {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) != 2 || filtered[0] != "--shir" {
		fmt.Fprintln(os.Stderr, "usage: go-sh --shir <file.sh|string> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	// pysh's heuristic: if the arg contains ".sh" or has no whitespace,
	// try to open as a file; else treat as direct input.
	if strings.Contains(inp, ".sh") || !strings.ContainsAny(inp, " \t\n") {
		if b, err := os.ReadFile(inp); err == nil {
			src = string(b)
		}
		// else: fall through to direct-input
	}
	doc, err := compile(src)
	if err != nil {
		if u, ok := err.(*Unsupported); ok {
			fmt.Fprintln(os.Stderr, u.Error())
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := dump(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if raw {
		os.Stdout.Write(b)
	} else {
		os.Stdout.Write(b)
		os.Stdout.Write([]byte{'\n'})
	}
}
