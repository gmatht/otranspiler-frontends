// powershell-sh-go: PowerShell (.ps1) source -> A1 shIR JSON.
//
// STUB (worker kickoff state): the frontend is implemented by the
// failure-driven worker (run_frontend_worker.sh), one pinned testdata
// example at a time, per PLAN_POWERSHELL_F.md. The stub is honest:
// comment/whitespace-only sources emit a valid EMPTY A1 program;
// everything else REFUSES loudly — refuse > guess until the worker
// lands the first construct.
package ps1lib

import (
	"encoding/json"
	"fmt"
	"strings"
)

// emptyProgram — the A1 Program shape the core deserializer accepts
// (contract_version 1; empty stmts). Mirrors the c-sh-go emitter.
func emptyProgram() map[string]any {
	return map[string]any{
		"type":             "Program",
		"contract_version": 1,
		"imports":          []any{},
		"requires":         []any{},
		"stmt_lines":       []any{},
		"stmts":            []any{},
		"subs":             []any{},
		"var_const":        []any{},
		"var_lengths":      []any{},
		"var_lifetimes":    []any{},
		"var_types":        []any{},
	}
}

// stripComments — remove `#` line comments so a comment-only source is
// recognized as empty. (A `#` inside a string is untouched — the v1
// lexer's job; the stub only recognizes the comment-only case.)
func stripComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			out.WriteString(line[:i])
		} else {
			out.WriteString(line)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// Shir — the frontend entry (mirrors c-sh-go's clib.Shir).
func Shir(src string) (out []byte, err error) {
	if strings.TrimSpace(stripComments(src)) == "" {
		return json.Marshal(emptyProgram())
	}
	return nil, fmt.Errorf("REFUSE: powershell-sh-go stub — not implemented yet (PLAN_POWERSHELL_F.md); the worker lands constructs one pinned testdata example at a time")
}
