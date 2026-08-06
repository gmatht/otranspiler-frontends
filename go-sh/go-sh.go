// go-sh: Go source -> shIR JSON (A1 contract), ANTLR4+Go. IN-PLACE
// UPGRADE of the previous hand-rolled Go frontend (the hand-rolled
// go-sh.go was removed from the workspace before this rewrite; the
// worker / git history has the old version if needed).
//
// INITIAL VERSION (per the plan): the proper antlr4-generated Go
// parser from grammars-v4/golang/{GoLexer,GoParser}.g4 is TODO; the
// worker (run_worker.sh -> pi/deepseek-v4-flash) runs `antlr4
// -Dlanguage=Go -package go_sh -visitor -o gen grammars/GoLexer.g4
// grammars/GoParser.g4` and wires up the full listener. For now, the
// hand-rolled stub parser (parseSimple) handles the v1 Go subset
// (package/import/func-main boilerplate, fmt.Println, := and =
// assignments of literals, os.Env access) — enough to build and pass
// the A1 ingress gate.
//
// EMISSION CONSTRAINT (learned the hard way — the gate):
// `debashc --shir-in-estree` deserializes the A1 JSON and then runs
// the ESTree renderer, which has NO arms for Output/Declare/RawExpr/
// Unsupported (they panic with "Perl-only IR ... reached the ESTree
// renderer", or are unknown stmt types at ingress). The frontend must
// emit ONLY the renderer-safe subset: Expr(Call("exec"|"getVar", ...))
// and Assign — the exact shapes the core itself emits for the shell
// analogs (echo hello / x=1 / echo $x), byte-identical modulo the
// language mapping. See sh2perl/src/shir.rs stmt_to_estree /
// expr_to_estree for the supported node sets.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// shirArg — one argument of a lowered call (v1: string literal or var read).
type shirArg struct {
	kind string // "str" | "getvar"
	val  string
}

// shirStmt — v1 statements the stub parser can express.
type shirStmt struct {
	kind    string // "println" | "assign"
	args    []shirArg
	name    string // assign target
	val     string // assign value (raw literal text)
	argKind string // "str" | "num" — RHS literal class (quoted vs unquoted analog)
}

// parseSimple is a deliberately-minimal hand-rolled parser for the v1
// Go subset. The full antlr4 listener (TODO) replaces it. Refuse >
// guess: any construct outside the subset is an error (the Makefile
// gate then reports FAIL), never silently mis-lowered.
func parseSimple(src string) ([]shirStmt, error) {
	// Strip shebang (first line starting with #!).
	if i := strings.IndexByte(src, '\n'); i > 0 && strings.HasPrefix(src, "#!") {
		src = src[i+1:]
	}
	var out []shirStmt
	for n, ln := range strings.Split(src, "\n") {
		where := fmt.Sprintf("line %d", n+1)
		// Strip // comments and single-line /* */ comments.
		if i := strings.Index(ln, "//"); i >= 0 {
			ln = ln[:i]
		}
		if i := strings.Index(ln, "/*"); i >= 0 {
			j := strings.Index(ln[i+2:], "*/")
			if j < 0 {
				return nil, fmt.Errorf("%s: unterminated block comment (v1)", where)
			}
			ln = ln[:i] + ln[i+2+j+2:]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// Structural boilerplate: package clause, import declarations,
		// func main header, braces. These are not executable statements
		// in the shell analog — skip them entirely.
		if isBoilerplate(ln) {
			continue
		}
		s, err := parseLine(ln)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
		out = append(out, s...)
	}
	return out, nil
}

// isBoilerplate reports whether ln is Go scaffolding with no shell
// analog: `package main`, `import "fmt"` / `import (`, `func main() {`,
// bare braces.
func isBoilerplate(ln string) bool {
	if ln == "{" || ln == "}" || ln == ")" {
		return true
	}
	if strings.HasPrefix(ln, "package ") {
		return true
	}
	if ln == "import" || strings.HasPrefix(ln, "import ") {
		return true
	}
	// `func main() {` — the program body (also `func main(){`).
	if m, _ := regexp.MatchString(`^func\s+main\s*\([^)]*\)\s*\{?\s*$`, ln); m {
		return true
	}
	return false
}

var (
	rePrintln  = regexp.MustCompile(`^fmt\.Println\s*\((.*)\)\s*$`)
	reAssign   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(:?=)\s*([^;]+?)\s*$`)
	reStrLit   = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"$`)
	reIntLit   = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	reIdent    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reMainFn   = regexp.MustCompile(`^func\s+\w+\s*\(`)
)

func parseLine(ln string) ([]shirStmt, error) {
	// fmt.Println("...", name, 42)
	if m := rePrintln.FindStringSubmatch(ln); m != nil {
		args, err := splitArgs(m[1])
		if err != nil {
			return nil, err
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("fmt.Println() with no args (v1)")
		}
		out := make([]shirArg, 0, len(args))
		for _, a := range args {
			switch {
			case reStrLit.MatchString(a):
				out = append(out, shirArg{kind: "str", val: strings.Trim(a, `"`)})
			case reIntLit.MatchString(a):
				out = append(out, shirArg{kind: "num", val: a}) // shell analog: echo 42 → Str "42"
			case reIdent.MatchString(a):
				out = append(out, shirArg{kind: "getvar", val: a})
			default:
				return nil, fmt.Errorf("fmt.Println arg %q unsupported (v1: string/int literal or var)", a)
			}
		}
		return []shirStmt{{kind: "println", args: out}}, nil
	}
	// name := "..." | name := 42 | name = ... (Go short/plain declaration)
	if m := reAssign.FindStringSubmatch(ln); m != nil {
		name, rhs := m[1], m[3]
		val, kind := "", "str"
		switch {
		case reStrLit.MatchString(rhs):
			val = strings.Trim(rhs, `"`)
		case reIntLit.MatchString(rhs):
			val, kind = rhs, "num"
		default:
			return nil, fmt.Errorf("assignment %q rhs unsupported (v1: string/int literal)", ln)
		}
		return []shirStmt{{kind: "assign", name: name, val: val, argKind: kind}}, nil
	}
	// Explicitly refuse what the v1 subset excludes (Refuse > guess).
	if reMainFn.MatchString(ln) {
		return nil, fmt.Errorf("non-main func %q unsupported (v1)", ln)
	}
	if strings.HasPrefix(ln, "if ") || strings.HasPrefix(ln, "for ") ||
		strings.HasPrefix(ln, "switch ") || strings.HasPrefix(ln, "return ") {
		return nil, fmt.Errorf("compound statement %q unsupported (v1)", ln)
	}
	return nil, fmt.Errorf("unrecognized statement %q (v1 subset: fmt.Println / := / = of literals)", ln)
}

// splitArgs splits a fmt.Println(...) argument list on top-level commas,
// respecting double-quoted string literals (which may contain commas).
func splitArgs(s string) ([]string, error) {
	var out []string
	depth := 0
	cur := strings.Builder{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			depth++
			cur.WriteByte(c)
			for i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				if s[i] == '\\' && i+1 < len(s) {
					i++
					cur.WriteByte(s[i])
				} else if s[i] == '"' {
					depth--
					break
				}
			}
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	for _, a := range out {
		if a == "" {
			return nil, fmt.Errorf("empty arg in fmt.Println call")
		}
	}
	return out, nil
}

// toShir maps v1 statements to the renderer-safe A1 shIR subset, with
// the EXACT byte shape the core emits for the faithful shell analog
// (verified against `debashc --shir` on the quoted form):
//   - fmt.Println("lit")  ≡ echo "lit"      → Expr(Call("exec", [Str "echo", Array[Interpolate lit]]))
//   - fmt.Println(name)    ≡ echo "$name"    → Expr(Call("exec", [Str "echo", Array[Call getVar]]))
//   - fmt.Println(42)      ≡ echo 42         → Expr(Call("exec", [Str "echo", Array[Str "42"]]))
//   - name := "lit"        ≡ name="lit"      → Assign(targets=[{var,sigil:null,indices:[]}], expr=Interpolate lit)
//   - name := 42           ≡ name=42         → Assign(..., expr=Str "42")
// The core lowers EVERY double-quoted word to Interpolate (even with no
// expansion) and unquoted words to Str — the emitter mirrors that.
func toShir(stmts []shirStmt) []map[string]any {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		switch s.kind {
		case "println":
			elems := make([]any, 0, len(s.args))
			for _, a := range s.args {
				elems = append(elems, wordExpr(a))
			}
			out = append(out, map[string]any{
				"type": "Expr",
				"expr": map[string]any{
					"type":   "Call",
					"func":   "exec",
					"args":   []any{strExpr("echo"), map[string]any{"type": "Array", "elements": elems}},
					"purity": "Emulable", // echo ∈ SYNC_BUILTINS (A4)
				},
			})
		case "assign":
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     s.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": wordExpr(shirArg{kind: s.argKind, val: s.val}),
			})
		}
	}
	return out
}

// wordExpr lifts one v1 arg to the A1 expr the core emits for the
// corresponding shell word: quoted string → Interpolate[lit], var read
// → Call getVar, number → Str of the digits (unquoted analog).
func wordExpr(a shirArg) map[string]any {
	switch a.kind {
	case "getvar":
		return map[string]any{
			"type":   "Call",
			"func":   "getVar",
			"args":   []any{strExpr(a.val)},
			"purity": "Emulable",
		}
	case "num":
		return strExpr(a.val)
	default: // "str" (quoted literal)
		return map[string]any{
			"type":  "Interpolate",
			"parts": []any{map[string]any{"kind": "lit", "text": a.val}},
		}
	}
}

func strExpr(v string) map[string]any {
	return map[string]any{"type": "Str", "value": v, "style": "DoubleQuoted"}
}

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
		fmt.Fprintln(os.Stderr, "usage: go-sh --shir <file.go> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	if strings.Contains(inp, ".go") || !strings.ContainsAny(inp, " \t\n") {
		if b, err := os.ReadFile(inp); err == nil {
			src = string(b)
		}
	}
	stmts, err := parseSimple(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-sh: "+err.Error())
		os.Exit(2)
	}
	prog := &shiremit.Program{Stmts: toShir(stmts)}
	out, err := shiremit.Emit(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit: "+err.Error())
		os.Exit(1)
	}
	if raw {
		os.Stdout.Write(out)
	} else {
		os.Stdout.Write(out)
		os.Stdout.Write([]byte{'\n'})
	}
}
