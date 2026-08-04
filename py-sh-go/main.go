// py-sh-go: Python source -> shIR JSON (A1 contract), initial ANTLR4+Go.
//
// INITIAL VERSION (per the plan: worker handles correctness).
//
// The full antlr4-generated Python parser is TODO. The grammar
// files (grammars/Python3Lexer.g4 + Python3Parser.g4, the official
// Python3 grammar from https://github.com/antlr/grammars-v4) are
// present under grammars/ — the per-frontend worker (run_worker.sh)
// invokes `pi` (opencode-go + deepseek-v4-flash, automatic key
// rotation) on build/test failure to wire up the proper antlr4
// generation and a full listener.
//
// For now, this initial version includes a TINY hand-rolled parser
// (parseSimple) that handles the v1 shell-flavored Python subset
// (print, simple assignments, os.environ access) — enough to build,
// smoke-run, and produce a valid A1 shIR JSON. The worker expands it.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// simpleNode is a tiny AST for the initial v1 Python subset.
type simpleNode struct {
	kind  string      // "print", "assign", "env", "noop"
	name  string      // for assign
	value string      // literal value (raw)
	args  []simpleNode // for print/exec
}

// parseSimple is a deliberately-minimal hand-rolled parser for the v1
// shell-flavored Python subset. The full antlr4 listener (TODO) will
// replace this. Returns the program as a list of simpleNodes, the
// function definitions (ignored in v1), and any parse error.
func parseSimple(src string) ([]simpleNode, error) {
	// Strip shebang.
	if i := strings.IndexByte(src, '\n'); i > 0 && strings.HasPrefix(src, "#!") {
		src = src[i+1:]
	}
	var out []simpleNode
	// Tokenize: split into lines, strip comments (# to EOL), trim.
	lines := strings.Split(src, "\n")
	for _, ln := range lines {
		// Strip comments.
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// Match the v1 constructs.
		// print EXPR
		if m := regexp.MustCompile(`^print\s*\(\s*(.*)\s*\)\s*(#.*)?$`).FindStringSubmatch(ln); m != nil {
			// Extract the argument: a string literal or a bare name.
			arg := strings.TrimSpace(m[1])
			if strings.HasPrefix(arg, `"`) || strings.HasPrefix(arg, `'`) {
				arg = strings.Trim(arg, `"'`)
			} else {
				// Bare name -> worker refines to sh2.getVar.
				arg = "__py_bare:" + arg
			}
			out = append(out, simpleNode{kind: "print", args: []simpleNode{{kind: "lit", value: arg}}})
			continue
		}
		// $VAR = EXPR (assignment)
		if m := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*(#.*)?$`).FindStringSubmatch(ln); m != nil {
			name := m[1]
			val := strings.TrimSpace(m[2])
			if strings.HasPrefix(val, `"`) || strings.HasPrefix(val, `'`) {
				val = strings.Trim(val, `"'`)
			}
			out = append(out, simpleNode{kind: "assign", name: name, value: val})
			continue
		}
		// $ENV{NAME} read (treated as a print with sh2.env call)
		if m := regexp.MustCompile(`^print\s*\(\s*os\.environ\s*\[\s*["']([^"']+)["']\s*\]\s*\)\s*(#.*)?$`).FindStringSubmatch(ln); m != nil {
			out = append(out, simpleNode{kind: "print", args: []simpleNode{{kind: "env", value: m[1]}}})
			continue
		}
		// $ENV{NAME} (bare, no print)
		if m := regexp.MustCompile(`^os\.environ\s*\[\s*["']([^"']+)["']\s*\]\s*(#.*)?$`).FindStringSubmatch(ln); m != nil {
			out = append(out, simpleNode{kind: "env", value: m[1]})
			continue
		}
		// Fallback: mark as Unsupported for the worker.
		out = append(out, simpleNode{kind: "unsupported", value: ln})
	}
	return out, nil
}

// simpleToShir maps a simpleNode (and its children) into the A1
// shIR JSON shape. The initial version handles the v1 constructs; the
// full antlr4 listener (TODO) replaces it.
func simpleToShir(nodes []simpleNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		switch n.kind {
		case "print":
			var val map[string]any
			if len(n.args) > 0 {
				a := n.args[0]
				switch a.kind {
				case "lit":
					val = map[string]any{"type": "Str", "value": a.value, "style": "DoubleQuoted"}
				case "env":
					val = map[string]any{"type": "Call", "func": "getVar", "args": []any{map[string]any{"type": "Str", "value": a.value, "style": "DoubleQuoted"}}, "purity": "Emulable"}
				default:
					val = map[string]any{"type": "RawExpr", "text": a.value}
				}
			} else {
				val = map[string]any{"type": "Str", "value": "", "style": "DoubleQuoted"}
			}
			out = append(out, map[string]any{
				"type":     "Output",
				"value":    val,
				"newline":  true,
				"target":   nil,
			})
		case "assign":
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     n.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": map[string]any{"type": "Str", "value": n.value, "style": "DoubleQuoted"},
			})
		case "env":
			out = append(out, map[string]any{
				"type":  "Output",
				"value": map[string]any{"type": "Call", "func": "getVar", "args": []any{map[string]any{"type": "Str", "value": n.value, "style": "DoubleQuoted"}}, "purity": "Emulable"},
				"newline": true,
				"target":  nil,
			})
		case "unsupported":
			out = append(out, map[string]any{"type": "Unsupported", "reason": n.value})
		default:
			out = append(out, map[string]any{"type": "noop"})
		}
	}
	return out
}

// --- main: CLI (mirrors the pysh.py contract) ---

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
		fmt.Fprintln(os.Stderr, "usage: py-sh-go --shir <file.py> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	if strings.Contains(inp, ".py") || !strings.ContainsAny(inp, " \t\n") {
		if b, err := os.ReadFile(inp); err == nil {
			src = string(b)
		}
	}
	nodes, err := parseSimple(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	prog := &shiremit.Program{
		Imports: []string{},
		Stmts:   simpleToShir(nodes),
	}
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
