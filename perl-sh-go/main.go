// perl-sh-go: Perl source -> shIR JSON (A1 contract), initial
// ANTLR4+Go frontend. INITIAL VERSION (per the plan): the worker
// (run_worker.sh -> pi/deepseek-v4-flash) handles correctness and
// grammar expansion. The hand-rolled stub parser (parseSimple)
// handles the v1 shell-flavored Perl subset.
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
	if m := regexp.MustCompile(`^print\s+(.+?);$`).FindStringSubmatch(ln); m != nil {
		arg := strings.TrimSpace(m[1])
		// Strip surrounding quotes.
		if (strings.HasPrefix(arg, `"`) && strings.HasSuffix(arg, `"`)) ||
			(strings.HasPrefix(arg, `'`) && strings.HasSuffix(arg, `'`)) {
			arg = arg[1 : len(arg)-1]
		}
		return []shirStmt{{kind: "print", raw: arg, val: arg}}, nil
	}
	// $VAR = EXPR;
	if m := regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+?);$`).FindStringSubmatch(ln); m != nil {
		val := strings.TrimSpace(m[2])
		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
			val = val[1 : len(val)-1]
		}
		return []shirStmt{{kind: "assign", name: m[1], val: val}}, nil
	}
	// system("CMD");
	if m := regexp.MustCompile(`^system\s*\(\s*"([^"]+)"\s*\);$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "exec", raw: "system", name: m[1]}}, nil
	}
	// exec("CMD");
	if m := regexp.MustCompile(`^exec\s*\(\s*"([^"]+)"\s*\);$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "exec", raw: "exec", name: m[1]}}, nil
	}
	// `cmd` (qx)
	if m := regexp.MustCompile("^`([^`]+)`;?$").FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "exec", raw: m[1]}}, nil
	}
	// $ENV{NAME} (treated as a getVar)
	if m := regexp.MustCompile(`^\$ENV\{([A-Za-z_][A-Za-z0-9_]*)\};?$`).FindStringSubmatch(ln); m != nil {
		return []shirStmt{{kind: "env", name: m[1]}}, nil
	}
	// if / elsif / else / unless — crude: just capture as if
	if strings.HasPrefix(ln, "if ") || strings.HasPrefix(ln, "unless ") {
		return []shirStmt{{kind: "if", cond: &shirStmt{kind: "exec", raw: ln}}}, nil
	}
	if strings.HasPrefix(ln, "while ") || strings.HasPrefix(ln, "until ") ||
		strings.HasPrefix(ln, "foreach ") {
		return []shirStmt{{kind: "exec", raw: ln}}, nil
	}
	return []shirStmt{{kind: "unsupported", raw: ln}}, nil
}

func toShir(stmts []shirStmt) []map[string]any {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		switch s.kind {
		case "print":
			out = append(out, map[string]any{
				"type":    "Output",
				"value":   map[string]any{"type": "Str", "value": s.val, "style": "DoubleQuoted"},
				"newline": true,
				"target":  nil,
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
		case "env":
			out = append(out, map[string]any{
				"type":    "Output",
				"value":   map[string]any{"type": "Call", "func": "getVar", "args": []any{map[string]any{"type": "Str", "value": s.name, "style": "DoubleQuoted"}}, "purity": "Emulable"},
				"newline": true,
				"target":  nil,
			})
		case "exec":
			out = append(out, map[string]any{
				"type":     "Exec",
				"cmd":      map[string]any{"type": "Str", "value": s.raw, "style": "DoubleQuoted"},
				"args":     []any{map[string]any{"type": "Str", "value": s.name, "style": "DoubleQuoted"}},
				"purity":   "Spawn",
				"env":      []any{},
				"redirects": []any{},
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
