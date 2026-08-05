// fish-sh-go: fish source -> shIR JSON (A1 contract), ANTLR4+Go.
//
// INITIAL VERSION: a hand-rolled stub parser handling the v1 fish subset
// (`echo ARG...`, `set NAME VALUE`, `#` comments) — enough to build and
// pass the A1 ingress gate (the emitted JSON must deserialize through
// `debashc --shir-in-estree`). The full antlr4-generated fish parser
// (grammars/FishLexer.g4 + FishParser.g4) is the WORKER's job.
//
// EMISSION CONSTRAINT (shared with the other Go frontends): emit ONLY the
// renderer-safe A1 subset — Expr(Call("exec"|"getVar", ...)) and Assign —
// the exact shapes the core itself emits for the shell analogs. The ESTree
// renderer panics on Perl-only nodes (Output/Declare/...), so the frontend
// must not emit them.
package main

import (
	"fmt"
	"os"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// stmt — one v1 statement.
type stmt struct {
	kind string // "echo" | "set"
	args []string
	name string // set target
}

func parseSimple(src string) ([]stmt, error) {
	if i := strings.IndexByte(src, '\n'); i > 0 && strings.HasPrefix(src, "#!") {
		src = src[i+1:]
	}
	var out []stmt
	for n, ln := range strings.Split(src, "\n") {
		where := fmt.Sprintf("line %d", n+1)
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		fields := strings.Fields(ln)
		switch fields[0] {
		case "echo":
			out = append(out, stmt{kind: "echo", args: fields[1:]})
		case "set":
			if len(fields) < 3 {
				return nil, fmt.Errorf("%s: set requires NAME VALUE (v1)", where)
			}
			// v1: ignore fish options like -g/-x, but refuse them loudly.
			if strings.HasPrefix(fields[1], "-") {
				return nil, fmt.Errorf("%s: set options not in the v1 subset", where)
			}
			out = append(out, stmt{kind: "set", name: fields[1], args: fields[2:]})
		default:
			return nil, fmt.Errorf("%s: %q not in the v1 subset", where, fields[0])
		}
	}
	return out, nil
}

func toShir(stmts []stmt) []map[string]any {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		switch s.kind {
		case "echo":
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
					"purity": "Emulable",
				},
			})
		case "set":
			val := strings.Join(s.args, " ")
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     s.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": strExpr(val),
			})
		}
	}
	return out
}

func strExpr(v string) map[string]any {
	return map[string]any{"type": "Str", "value": v, "style": "DoubleQuoted"}
}

func wordExpr(a string) map[string]any {
	if strings.HasPrefix(a, "$") {
		return map[string]any{
			"type":   "Call",
			"func":   "getVar",
			"args":   []any{strExpr(strings.TrimPrefix(a, "$"))},
			"purity": "Emulable",
		}
	}
	return strExpr(a)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fish-sh-go --shir <file.fish> [--raw]")
		os.Exit(2)
	}
	if args[0] != "--shir" || len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fish-sh-go --shir <file.fish> [--raw]")
		os.Exit(2)
	}
	src, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	stmts, err := parseSimple(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	prog := &shiremit.Program{Stmts: toShir(stmts)}
	out, err := shiremit.Emit(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
