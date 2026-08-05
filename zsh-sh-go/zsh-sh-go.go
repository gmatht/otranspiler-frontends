// zsh-sh-go: zsh source -> shIR JSON (A1 contract), ANTLR4+Go.
//
// INITIAL VERSION: a hand-rolled stub parser handling the v1 zsh subset
// (`echo ARG...`, `NAME=VALUE`, `#` comments — the POSIX-shaped core of
// zsh) — enough to build and pass the A1 ingress gate (the emitted JSON
// must deserialize through `debashc --shir-in-estree`). The full
// antlr4-generated zsh parser (grammars/ZshLexer.g4 + ZshParser.g4) is
// the WORKER's job.
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
	kind string // "echo" | "assign"
	args []string
	name string // assign target
	val  string
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
		switch {
		case fields[0] == "echo":
			out = append(out, stmt{kind: "echo", args: fields[1:]})
		case strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-"):
			eq := strings.Index(fields[0], "=")
			name, val := fields[0][:eq], fields[0][eq+1:]
			out = append(out, stmt{kind: "assign", name: name, val: val})
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
		case "assign":
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     s.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": strExpr(s.val),
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
		fmt.Fprintln(os.Stderr, "usage: zsh-sh-go --shir <file.zsh> [--raw]")
		os.Exit(2)
	}
	if args[0] != "--shir" || len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zsh-sh-go --shir <file.zsh> [--raw]")
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
