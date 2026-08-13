package ps1lib

import (
	"bytes"
	"encoding/json"
)

// ContractVersion — must match sh2perl/src/shir_json.rs CONTRACT_VERSION.
const ContractVersion = 1

// emitProgram — serialize the A1 Program JSON byte-identical to the
// core's shir_json.rs output for the same program:
//
//   - all 13 top-level fields, including the analysis slots the core
//     always emits (var_types, stmt_lines, var_lengths, var_const,
//     var_lifetimes, var_nospace, var_bash_env) — empty for a program
//     without variables;
//   - alphabetically-sorted keys (encoding/json sorts map keys, matching
//     serde_json's BTreeMap);
//   - SetEscapeHTML(false) so `<`, `>`, `&` stay raw (serde_json emits
//     them raw — the default Go escaping would differ byte-wise);
//   - no trailing newline (the core's serializer returns a bare string).
func emitProgram(stmts []any) ([]byte, error) {
	if stmts == nil {
		stmts = []any{}
	}
	p := map[string]any{
		"type":             "Program",
		"contract_version": ContractVersion,
		"imports":          []any{},
		"requires":         []any{},
		"var_types":        []any{},
		"stmt_lines":       []any{},
		"var_lengths":      []any{},
		"var_const":        []any{},
		"var_lifetimes":    []any{},
		"var_nospace":      []any{},
		"var_bash_env":     []any{},
		"subs":             []any{},
		"stmts":            stmts,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ── A1 node builders (shapes mirror shir_json.rs exactly) ─────────────

func exprStmt(e any) any {
	return map[string]any{"type": "Expr", "expr": e}
}

// callExpr — the sh2.*-style Call. purity must match the core's
// call_purity verdict (Emulable for builtins like echo, Spawn else).
func callExpr(funcName string, args []any, purity string) any {
	return map[string]any{
		"type":   "Call",
		"func":   funcName,
		"args":   args,
		"purity": purity,
	}
}

func strExpr(value, style string) any {
	return map[string]any{"type": "Str", "value": value, "style": style}
}

func interpExpr(parts []any) any {
	return map[string]any{"type": "Interpolate", "parts": parts}
}

func litPart(text string) any {
	return map[string]any{"kind": "lit", "text": text}
}

func exprPart(e any) any {
	return map[string]any{"kind": "expr", "expr": e}
}

func arrayExpr(elements []any) any {
	return map[string]any{"type": "Array", "elements": elements}
}

// getVarCall — $name reads lower to getVar("name") (the core's var-read
// channel; Emulable per the A4 namespace spec).
func getVarCall(name string) any {
	return callExpr("getVar", []any{strExpr(name, "DoubleQuoted")}, "Emulable")
}

// execCall — a v1 command lowers to exec <cmd> <args…>; the first
// element is the command name (Str), the rest form the Array argument.
// Purity: builtin (echo) → Emulable, everything else → Spawn — the
// core's call_purity verdict.
func execCall(cmd string, argExprs []any) any {
	// The core's SYNC_BUILTINS set (shir.rs) — a call to a builtin is
	// Emulable, anything else Spawn. Keep in sync with the core list.
	builtin := map[string]bool{
		".": true, ":": true, "basename": true, "break": true,
		"cat": true, "cd": true, "cmp": true, "comm": true,
		"continue": true, "cp": true, "cut": true, "date": true,
		"declare": true, "diff": true, "dirname": true, "echo": true,
		"egrep": true, "eval": true, "exit": true, "export": true,
		"false": true, "find": true, "grep": true, "gzip": true,
		"gunzip": true, "head": true, "hostname": true, "let": true,
		"local": true, "ls": true, "mapfile": true, "mkdir": true,
		"mktemp": true, "mv": true, "paste": true, "printf": true,
		"pwd": true, "read": true, "readarray": true, "readlink": true,
		"readonly": true, "return": true, "rm": true, "rmdir": true,
		"seq": true, "sed": true, "set": true, "sha256sum": true,
		"sha512sum": true, "shift": true, "sort": true, "source": true,
		"stat": true, "tail": true, "tee": true, "test": true,
		"touch": true, "tr": true, "trap": true, "true": true,
		"type": true, "typeset": true, "uname": true, "uniq": true,
		"unset": true, "wc": true, "which": true, "whoami": true,
	}
	purity := "Spawn"
	if builtin[cmd] {
		purity = "Emulable"
	}
	return callExpr("exec", []any{strExpr(cmd, "DoubleQuoted"), arrayExpr(argExprs)}, purity)
}
