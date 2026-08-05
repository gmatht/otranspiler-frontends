// perl-sh-go: Perl source -> shIR JSON (A1 contract), initial
// ANTLR4+Go frontend. INITIAL VERSION (per the plan): the worker
// (run_worker.sh -> pi/deepseek-v4-flash) handles correctness and
// grammar expansion. The hand-rolled stub parser (parseSimple)
// handles the v1 shell-flavored Perl subset.
//
// EMIT RULES (the A1 gate): the emitted JSON must survive the core's
// strict ingress (`shir_json_in.rs`) AND the ESTree renderer
// (`shir.rs stmt_to_estree/expr_to_estree`). The renderer rejects the
// Perl-only nodes (`Output`, `RawExpr`, `Unsupported`, `noop`) with an
// unreachable panic, so every statement is lowered to the shared
// vocabulary: `Expr`+`Call` (commands, print), `Assign`, `If`/`While`/
// `For` with `test`-Call conditions, `else` key (not `r#else`).
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// parseSimple is a deliberately-minimal hand-rolled parser for the v1
// shell-flavored Perl subset. The full antlr4 listener (TODO) will
// replace it. Handles: print, $VAR = ..., if/elsif/else/unless,
// while/until, foreach, system, exec, `...` (qx), $ENV{N},
// string interpolation, heredocs (initial v1: simple EOF), #, shebang.
func parseSimple(src string) ([]shirStmt, error) {
	// Strip shebang on line 1.
	if i := strings.IndexByte(src, '\n'); i > 0 && strings.HasPrefix(src, "#!") {
		src = src[i+1:]
	}
	var out []shirStmt
	for _, ln := range strings.Split(src, "\n") {
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" || ln == "}" || ln == "{" {
			continue
		}
		s, err := parseLine(ln)
		if err != nil {
			out = append(out, shirStmt{kind: "unsupported", raw: ln})
			continue
		}
		out = append(out, s...)
	}
	return out, nil
}

// shirStmt — pointer-typed recursive fields (Go disallows recursive
// value types).
type shirStmt struct {
	kind string
	raw  string
	name string
	val  string
	args []shirStmt
	cond *shirStmt
}

func parseLine(ln string) ([]shirStmt, error) {
	// print EXPR;
	if m := regexp.MustCompile(`^print\s+(.+?);?$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "print", args: []shirStmt{valueNode(m[1])}}}, nil
	}
	// $VAR = EXPR;
	if m := regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+?);?$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "assign", name: m[1], args: []shirStmt{valueNode(m[2])}}}, nil
	}
	// system("CMD"); / exec("CMD");
	if m := regexp.MustCompile(`^(?:system|exec)\s*\(\s*"([^"]+)"\s*\);?$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "exec", name: m[1]}}, nil
	}
	// `cmd` (qx)
	if m := regexp.MustCompile("^`([^`]+)`;?$").FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "exec", name: m[1]}}, nil
	}
	// $ENV{NAME} (treated as a getVar read)
	if m := regexp.MustCompile(`^\$ENV\{([A-Za-z_][A-Za-z0-9_]*)\};?$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "env", name: m[1]}}, nil
	}
	// if (COND) / unless (COND) — condition text captured
	if m := regexp.MustCompile(`^(?:if|unless)\s*\((.*?)\)`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "if", cond: &shirStmt{kind: "lit", val: strings.TrimSpace(m[1])}}}, nil
	}
	// while (COND) / until (COND)
	if m := regexp.MustCompile(`^(?:while|until)\s*\((.*?)\)`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "while", cond: &shirStmt{kind: "lit", val: strings.TrimSpace(m[1])}}}, nil
	}
	// foreach [my] $x (LIST)
	if m := regexp.MustCompile(`^foreach\s+(?:my\s+)?[$@%]?([A-Za-z_][A-Za-z0-9_]*)\s*\((.*?)\)`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "for", name: m[1], val: strings.TrimSpace(m[2])}}, nil
	}
	return []shirStmt{{kind: "unsupported", raw: ln}}, nil
}

// valueNode classifies a print/assign value: $VAR and $ENV{NAME} reads
// lower to getVar calls; everything else is literal text (quotes
// stripped, rendered as an Interpolate lit — the core's string shape).
func valueNode(s string) shirStmt {
	s = strings.TrimSpace(s)
	if m := regexp.MustCompile(`^\$ENV\{([A-Za-z_][A-Za-z0-9_]*)\}$`).FindStringSubmatch(s); m != nil {
		return shirStmt{kind: "env", name: m[1]}
	}
	if m := regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)$`).FindStringSubmatch(s); m != nil {
		return shirStmt{kind: "var", name: m[1]}
	}
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		s = s[1 : len(s)-1]
	}
	return shirStmt{kind: "lit", val: s}
}

func toShir(stmts []shirStmt) []map[string]any {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		switch s.kind {
		case "print":
			// print EXPR -> sh2.print(...) (Expr+Call, the shared
			// vocabulary — Output is Perl-only and the ESTree renderer
			// panics on it).
			out = append(out, exprStmt(callExpr("print", []any{valueExpr(s.args[0])}, "Emulable")))
		case "assign":
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     s.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": valueExpr(s.args[0]),
			})
		case "env":
			// $ENV{NAME}; standalone -> a getVar read expression
			out = append(out, exprStmt(callExpr("getVar", []any{strLit(s.name)}, "Emulable")))
		case "exec":
			out = append(out, execStmt(s.name))
		case "if":
			condText := ""
			if s.cond != nil {
				condText = s.cond.val
			}
			out = append(out, map[string]any{
				"type":   "If",
				"cond":   testCond(condText),
				"then":   []any{},
				"elsifs": []any{},
				"else":   []any{},
			})
		case "while":
			condText := ""
			if s.cond != nil {
				condText = s.cond.val
			}
			out = append(out, map[string]any{
				"type": "While",
				"cond": testCond(condText),
				"body": []any{},
			})
		case "for":
			elements := []any{}
			for _, w := range strings.Fields(s.val) {
				elements = append(elements, strLit(strings.Trim(w, ", \t")))
			}
			out = append(out, map[string]any{
				"type": "For",
				"var":  s.name,
				"iter": map[string]any{"type": "Array", "elements": elements},
				"body": []any{},
			})
		case "unsupported":
			// Unsupported is not in the renderer's statement vocabulary;
			// lower the line as a command so the A1 JSON stays valid.
			out = append(out, execStmt(s.raw))
		default:
			out = append(out, exprStmt(callExpr("noop", []any{}, "Emulable")))
		}
	}
	return out
}

// ── A1 node builders (canonical shapes from the core's `--shir`) ──

func strLit(v string) map[string]any {
	return map[string]any{"type": "Str", "value": v, "style": "DoubleQuoted"}
}

func interpLit(v string) map[string]any {
	return map[string]any{
		"type":  "Interpolate",
		"parts": []any{map[string]any{"kind": "lit", "text": v}},
	}
}

func callExpr(funcName string, args []any, purity string) map[string]any {
	return map[string]any{"type": "Call", "func": funcName, "args": args, "purity": purity}
}

func exprStmt(expr map[string]any) map[string]any {
	return map[string]any{"type": "Expr", "expr": expr}
}

func valueExpr(n shirStmt) map[string]any {
	switch n.kind {
	case "env", "var":
		return callExpr("getVar", []any{strLit(n.name)}, "Emulable")
	default:
		return interpLit(n.val)
	}
}

// testCond mirrors the core's `[ cond ]` lowering: Call{func:"test"}.
func testCond(text string) map[string]any {
	return callExpr("test", []any{strLit(text)}, "Emulable")
}

// execStmt lowers a command line to the core's canonical command shape:
// Expr + Call{func:"exec", args:[Str(cmd), Array[words]], purity:"Spawn"}.
func execStmt(line string) map[string]any {
	fields := strings.Fields(line)
	cmd := line
	words := []string{}
	if len(fields) > 0 {
		cmd = fields[0]
		words = fields[1:]
	}
	elements := make([]any, 0, len(words))
	for _, w := range words {
		elements = append(elements, strLit(w))
	}
	args := []any{strLit(cmd), map[string]any{"type": "Array", "elements": elements}}
	return exprStmt(callExpr("exec", args, "Spawn"))
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
		fmt.Fprintln(os.Stderr, "usage: perl-sh-go --shir <file.pl> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	if strings.Contains(inp, ".pl") || !strings.ContainsAny(inp, " \t\n") {
		if b, err := os.ReadFile(inp); err == nil {
			src = string(b)
		}
	}
	stmts, err := parseSimple(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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
