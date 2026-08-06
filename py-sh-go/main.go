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
// smoke-run, and produce VALID A1 shIR JSON.
//
// The emitted JSON is byte-identical to the core frontend's
// `debashc --shir <shell-equivalent> --raw` output:
//
//	print("hello from py-sh-go")  →  echo hello from py-sh-go
//	x = "world"                   →  x=world
//	print(x)                      →  echo $x
//
// i.e. `Expr(Call(exec, [Str("echo"), Array(words)]))` for prints,
// `Assign` for assignments, `Expr(Call(getVar, …))` for env reads —
// the EXACT statements the core's ESTree renderer accepts (the
// Perl-only `Output`/`Warn`/… nodes panic the ESTree backend, so
// they are never emitted here). var_types (A2) and stmt_lines are
// emitted to match the core's program shape byte-for-byte.
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// simpleNode is a tiny AST for the initial v1 Python subset.
type simpleNode struct {
	kind  string       // "print", "assign", "env", "noop"
	name  string       // for assign
	value string       // literal value (raw)
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
		// Match the v1 constructs. ORDER MATTERS: the env-print forms
		// must be tested before the generic print form (which would
		// swallow `os.environ[...]` as a bare argument).
		// print(os.environ["NAME"])  →  echo $NAME
		if m := regexp.MustCompile(`^print\s*\(\s*os\.environ\s*\[\s*["']([^"']+)["']\s*\]\s*\)\s*$`).FindStringSubmatch(ln); m != nil {
			out = append(out, simpleNode{kind: "print", args: []simpleNode{{kind: "env", value: m[1]}}})
			continue
		}
		// print EXPR
		if m := regexp.MustCompile(`^print\s*\(\s*(.*?)\s*\)\s*$`).FindStringSubmatch(ln); m != nil {
			arg := strings.TrimSpace(m[1])
			if strings.HasPrefix(arg, `"`) || strings.HasPrefix(arg, `'`) {
				// string literal → echo WORDS (shell word-splits an
				// unquoted literal on whitespace)
				arg = strings.Trim(arg, `"'`)
				out = append(out, simpleNode{kind: "print", args: []simpleNode{{kind: "lit", value: arg}}})
			} else {
				// bare name → echo $NAME
				out = append(out, simpleNode{kind: "print", args: []simpleNode{{kind: "var", value: arg}}})
			}
			continue
		}
		// NAME = EXPR (assignment)
		if m := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`).FindStringSubmatch(ln); m != nil {
			name := m[1]
			val := strings.TrimSpace(m[2])
			if strings.HasPrefix(val, `"`) || strings.HasPrefix(val, `'`) {
				val = strings.Trim(val, `"'`)
			}
			out = append(out, simpleNode{kind: "assign", name: name, value: val})
			continue
		}
		// os.environ["NAME"] (bare, no print)  →  $NAME
		if m := regexp.MustCompile(`^os\.environ\s*\[\s*["']([^"']+)["']\s*\]\s*$`).FindStringSubmatch(ln); m != nil {
			out = append(out, simpleNode{kind: "env", value: m[1]})
			continue
		}
		// Fallback: mark as Unsupported for the worker (still emitted
		// as a VALID ingress node — the core's own `unsupported` call).
		out = append(out, simpleNode{kind: "unsupported", value: ln})
	}
	return out, nil
}

// --- A1 shIR JSON builders (byte-identical to sh2perl/src/shir_json.rs) ---

// st builds an IrExpr::Str node (DoubleQuoted style — the default the
// core's `st()` uses for literal words).
func st(s string) map[string]any {
	return map[string]any{"type": "Str", "value": s, "style": "DoubleQuoted"}
}

// syncBuiltins mirrors shir.rs SYNC_BUILTINS (ask A3 purity for exec).
var syncBuiltins = map[string]bool{
	".": true, ":": true, "basename": true, "break": true, "cat": true,
	"cd": true, "cmp": true, "comm": true, "continue": true, "cut": true,
	"declare": true, "dirname": true, "echo": true, "eval": true,
	"exit": true, "export": true, "false": true, "grep": true, "head": true,
	"let": true, "local": true, "mapfile": true, "mktemp": true,
	"printf": true, "pwd": true, "read": true, "readarray": true,
	"readonly": true, "return": true, "seq": true, "sed": true, "set": true,
	"shift": true, "sort": true, "source": true, "stat": true, "tail": true,
	"test": true, "touch": true, "tr": true, "trap": true, "true": true,
	"type": true, "typeset": true, "uniq": true, "unset": true, "wc": true,
}

// callPurity mirrors shir_json.rs call_purity (ask A3): the purity
// verdict must match the core's byte-for-byte.
func callPurity(func_ string, args []any) string {
	switch func_ {
	case "contains", "join", "brace", "idiv", "imod", "arith", "arithEval",
		"trimCapture", "dirname", "basename", "not", "guard", "caseMatch",
		"param", "callDirect":
		return "PureCpu"
	case "getVar", "setVar", "setLastExit", "assign", "test", "grepText",
		"listVar", "setArray", "setArrayAppend", "arrayItems", "arrayKeys",
		"arrayLen", "arrayIndex", "fnCall", "define", "forLoop", "whileLoop",
		"block", "shopt", "builtin", "bcSqrt":
		return "Emulable"
	case "exec":
		if len(args) > 0 {
			if a, ok := args[0].(map[string]any); ok {
				if a["type"] == "Str" || a["type"] == "Ident" {
					if s, ok := a["value"].(string); ok && syncBuiltins[s] {
						return "Emulable"
					}
				}
			}
		}
		return "Spawn"
	case "capture", "captureWords", "pipeline", "redirect", "subshell",
		"background", "callUndefined", "unsupported":
		return "Spawn"
	case "return", "break", "continue", "exit":
		return "Control"
	default:
		if strings.HasPrefix(func_, "fs.") {
			return "Fs"
		}
		return "Spawn" // unknown → conservative
	}
}

// call builds an IrExpr::Call node with the A3 purity verdict.
func call(func_ string, args []any) map[string]any {
	return map[string]any{"type": "Call", "func": func_, "args": args, "purity": callPurity(func_, args)}
}

// getVar builds Call("getVar", [Str(name)]) — the core's word_ir
// lowering for a shell `$NAME` read.
func getVar(name string) map[string]any {
	return call("getVar", []any{st(name)})
}

// execCall builds Call("exec", [Str(cmd), Array(words)]) — the core's
// exec_stmt lowering for a simple command.
func execCall(cmd string, words []any) map[string]any {
	return call("exec", []any{st(cmd), map[string]any{"type": "Array", "elements": words}})
}

// simpleToShir maps a simpleNode (and its children) into the A1
// shIR JSON shape — byte-identical to what the core's ast_to_ir
// produces for the equivalent shell source. ONLY statements the
// core's ESTree renderer accepts are emitted (Expr/Assign/… — the
// Perl-only nodes Output/Warn/… would panic the ESTree backend).
func simpleToShir(nodes []simpleNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		switch n.kind {
		case "print":
			// print("...") → echo WORDS; print(x) → echo $x
			var words []any
			if len(n.args) > 0 {
				a := n.args[0]
				switch a.kind {
				case "lit":
					// shell word-splits an unquoted literal on whitespace
					for _, w := range strings.Fields(a.value) {
						words = append(words, st(w))
					}
				case "var", "env":
					words = append(words, getVar(a.value))
				default:
					words = append(words, st(a.value))
				}
			}
			out = append(out, map[string]any{
				"type": "Expr",
				"expr": execCall("echo", words),
			})
		case "assign":
			// x = "world" → shell x=world → IrStmt::Assign
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     n.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": st(n.value),
			})
		case "env":
			// bare os.environ["NAME"] → shell $NAME read
			out = append(out, map[string]any{
				"type": "Expr",
				"expr": getVar(n.value),
			})
		case "unsupported":
			// the core's own fallback for constructs it can't lower
			out = append(out, map[string]any{
				"type": "Expr",
				"expr": call("unsupported", []any{st(n.value)}),
			})
		default:
			out = append(out, map[string]any{"type": "Expr", "expr": st("")})
		}
	}
	return out
}

// varTypes computes the A2 verdicts the core attaches for the same
// shell source: every assigned variable, sorted by name, typed Int
// when every assignment is provably numeric, else Str (conservative
// — a Python str is a shell string).
func varTypes(nodes []simpleNode) []shiremit.VarType {
	byName := map[string]string{} // name → "Int"/"Str"
	for _, n := range nodes {
		if n.kind != "assign" {
			continue
		}
		t := "Str"
		if _, err := strconv.ParseInt(strings.TrimSpace(n.value), 10, 64); err == nil {
			t = "Int"
		}
		byName[n.name] = t
	}
	names := make([]string, 0, len(byName))
	for k := range byName {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]shiremit.VarType, 0, len(names))
	for _, k := range names {
		out = append(out, shiremit.VarType{Name: k, Type: byName[k]})
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
		Imports:  []string{},
		Requires: []string{},
		VarTypes: varTypes(nodes),
		Stmts:    simpleToShir(nodes),
	}
	out, err := shiremit.Emit(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit: "+err.Error())
		os.Exit(1)
	}
	// The shared emitter predates the stmt_lines contract field; the
	// core's program JSON always carries it (empty here — no line
	// mappings for the v1 subset). Insert after "requires" — exactly
	// where serde_json's BTreeMap ordering places it.
	out = bytes.Replace(out, []byte(`"requires":[]`), []byte(`"requires":[],"stmt_lines":[]`), 1)
	if raw {
		os.Stdout.Write(out)
	} else {
		os.Stdout.Write(out)
		os.Stdout.Write([]byte{'\n'})
	}
}
