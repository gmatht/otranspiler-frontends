// posix-sh-go: POSIX shell source -> shIR JSON (A1 contract), initial
// ANTLR4+Go frontend. INITIAL VERSION (per the plan): the worker
// (run_worker.sh -> pi/deepseek-v4-flash) handles correctness and
// grammar expansion. The hand-rolled stub parser (parseSimple) below
// handles the v1 shell-flavored POSIX sh subset — enough to build,
// smoke-run, and emit a valid A1 shIR JSON. The proper antlr4
// generation from grammars/POSIX.g4 is TODO for the worker.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

// parseSimple is a deliberately-minimal hand-rolled parser for the v1
// shell-flavored POSIX sh subset. The full antlr4 listener (TODO) will
// replace it. Handles: simple commands, assignments, if/while/for,
// $VAR, $(cmd), |, comments, shebang.
func parseSimple(src string) ([]shirStmt, error) {
	if i := strings.IndexByte(src, '\n'); i > 0 && strings.HasPrefix(src, "#!") {
		src = src[i+1:]
	}
	var out []shirStmt
	for _, ln := range strings.Split(src, "\n") {
		// strip comments
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
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

// shirStmt is a tiny intermediate representation (the antlr4 listener
// will replace it with the proper shir-emit-go node types). Recursive
// fields use pointers (Go does not allow recursive value types).
type shirStmt struct {
	kind string  // "exec", "assign", "if", "while", "for", "output", "unsupported"
	raw  string
	cmd  string
	name string
	val  string
	body []shirStmt // safe: slice (not recursive value)
	cond *shirStmt  // pointer (recursive value would be invalid)
}

// parseLine dispatches on the first keyword. This is intentionally
// crude — the real listener will do the proper work.
func parseLine(ln string) ([]shirStmt, error) {
	// assignment: NAME=val (no spaces around =)
	if m := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "assign", name: m[1], val: m[2]}}, nil
	}
	// if
	if strings.HasPrefix(ln, "if ") {
		return []shirStmt{{kind: "if", cond: &shirStmt{kind: "exec", raw: strings.TrimPrefix(ln, "if ")}}}, nil
	}
	// while / for / until — treat as exec (worker refines)
	if strings.HasPrefix(ln, "while ") || strings.HasPrefix(ln, "for ") || strings.HasPrefix(ln, "until ") {
		return []shirStmt{{kind: "exec", raw: ln}}, nil
	}
	// output (echo)
	if strings.HasPrefix(ln, "echo ") {
		val := strings.TrimPrefix(ln, "echo ")
		val = strings.Trim(val, `"'`)
		return []shirStmt{{kind: "output", raw: val, val: val}}, nil
	}
	// command substitution $(...) or `...`
	if strings.HasPrefix(ln, "$(") || strings.HasPrefix(ln, "`") {
		return []shirStmt{{kind: "exec", raw: ln}}, nil
	}
	// pipe: cmd1 | cmd2 — split on |, emit each
	if i := strings.Index(ln, " | "); i > 0 {
		return []shirStmt{{kind: "exec", raw: ln}}, nil
	}
	// default: simple exec
	return []shirStmt{{kind: "exec", raw: ln}}, nil
}

// toShir maps the intermediate shirStmt into the A1 shIR JSON shape.
func toShir(stmts []shirStmt) []map[string]any {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		switch s.kind {
		case "exec":
			out = append(out, map[string]any{
				"type":     "Exec",
				"cmd":      map[string]any{"type": "Str", "value": "sh2.exec", "style": "DoubleQuoted"},
				"args":     []any{map[string]any{"type": "RawExpr", "text": s.raw}},
				"purity":   "Spawn",
				"env":      []any{},
				"redirects": []any{},
			})
		case "output":
			out = append(out, map[string]any{
				"type":     "Exec",
				"cmd":      map[string]any{"type": "Str", "value": "echo", "style": "DoubleQuoted"},
				"args":     []any{map[string]any{"type": "Str", "value": s.val, "style": "DoubleQuoted"}},
				"purity":   "Emulable",
				"env":      []any{},
				"redirects": []any{},
			})
		case "assign":
			out = append(out, map[string]any{
				"type": "Assign",
				"targets": []any{map[string]any{
					"var":     s.name,
					"sigil":   nil,
					"indices": []any{},
				}},
				"expr": map[string]any{"type": "Str", "value": s.val, "style": "DoubleQuoted"},
			})
		case "if":
			condRaw := ""
			if s.cond != nil {
				condRaw = s.cond.raw
			}
			out = append(out, map[string]any{
				"type":    "If",
				"cond":    map[string]any{"type": "Call", "func": "sh2.test", "args": []any{map[string]any{"type": "RawExpr", "text": condRaw}}, "purity": "Emulable"},
				"then":    []any{},
				"elsifs":  []any{},
				"r#else":  nil,
			})
		case "unsupported":
			out = append(out, map[string]any{"type": "Unsupported", "reason": s.raw})
		default:
			out = append(out, map[string]any{"type": "noop"})
		}
	}
	return out
}

// --- main: CLI (mirrors the shir-emit-go pattern) ---

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
		fmt.Fprintln(os.Stderr, "usage: posix-sh-go --shir <file.sh> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	if strings.Contains(inp, ".sh") || !strings.ContainsAny(inp, " \t\n") {
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
