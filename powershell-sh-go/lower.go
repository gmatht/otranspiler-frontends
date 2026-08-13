package ps1lib

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// ── CST structure (tree-sitter-powershell, vendored grammar) ──────────
//
//	program
//	  comment*                    (skip)
//	  statement_list
//	    pipeline                  (a single statement)
//	      pipeline_chain          (a command / expression)
//	        command
//	          command_name:  command_name          (field)
//	          command_elements: command_elements   (field)
//	            command_argument_sep*              (skip)
//	            unary_expression → string_literal → expandable_string_literal
//	                                            → verbatim_string_characters
//	                             → variable / integer_literal / …
//	                             → expression_with_unary_operator
//	                                 → cast_expression  (type_literal + operand)
//	            generic_token                     (bareword)
//	            variable
//	            concatenated_command_argument     (adjacent pieces — t05)
//	      pipeline_chain_tail*    (REFUSE: `|` pipe — t08 rung)
//	    assignment_expression / if_statement / …  (REFUSE until pinned)

// lowerProgram — walk the CST root and lower the v1 subset to A1
// statements. Comments are skipped; anything outside the subset returns
// a loud REFUSE error (refuse.go).
func lowerProgram(root *sitter.Node, src []byte) ([]any, error) {
	var stmts []any
	for i := 0; i < int(root.NamedChildCount()); i++ {
		ch := root.NamedChild(i)
		switch ch.Type() {
		case "comment":
			continue
		case "statement_list":
			s, err := lowerStatementList(ch, src)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, s...)
		default:
			return nil, refuse(ch, src, "statement type %q", ch.Type())
		}
	}
	return stmts, nil
}

// lowerStatementList — the statements of one statement_list.
func lowerStatementList(n *sitter.Node, src []byte) ([]any, error) {
	var stmts []any
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		s, err := lowerStatement(ch, src)
		if err != nil {
			return nil, err
		}
		if s != nil {
			stmts = append(stmts, s)
		}
	}
	return stmts, nil
}

// lowerStatement — one top-level statement.
func lowerStatement(n *sitter.Node, src []byte) (any, error) {
	switch n.Type() {
	case "pipeline":
		return lowerPipeline(n, src)
	default:
		return nil, refuse(n, src, "statement type %q", n.Type())
	}
}

// lowerPipeline — a single statement; `|` pipelines refuse until the
// t08 rung (the plan's text-pipe approximation).
func lowerPipeline(n *sitter.Node, src []byte) (any, error) {
	var chain *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			if chain == nil {
				chain = ch
			}
		case "pipeline_chain_tail":
			return nil, refuse(ch, src, "pipeline `|` (the t08 text-pipe rung)")
		default:
			return nil, refuse(ch, src, "statement %q (outside the v1 subset)", ch.Type())
		}
	}
	if chain == nil {
		return nil, refuse(n, src, "pipeline without a chain")
	}
	for i := 0; i < int(chain.NamedChildCount()); i++ {
		ch := chain.NamedChild(i)
		switch ch.Type() {
		case "command":
			return lowerCommand(ch, src)
		default:
			return nil, refuse(ch, src, "expression %q at statement level", ch.Type())
		}
	}
	return nil, refuse(chain, src, "empty pipeline chain")
}

// lowerCommand — `Write-Output "…"` / `Write-Host "…"` / `echo …` lower
// to the core's exec-echo Call (byte-identical to the core frontend's
// `echo …` lowering). Every other command name REFUSES.
func lowerCommand(n *sitter.Node, src []byte) (any, error) {
	nameNode := n.ChildByFieldName("command_name")
	if nameNode == nil {
		return nil, refuse(n, src, "command without a name")
	}
	name := nameNode.Content(src)
	switch strings.ToLower(name) {
	case "write-output", "write-host", "echo":
		// echo is PowerShell's builtin alias for Write-Output; the A1
		// surface has no Write-Output, so both map to the core's echo.
	default:
		return nil, refuse(nameNode, src, "command %q (outside the v1 subset)", name)
	}
	var argExprs []any
	if elems := n.ChildByFieldName("command_elements"); elems != nil {
		for i := 0; i < int(elems.NamedChildCount()); i++ {
			e, err := lowerCommandElement(elems.NamedChild(i), src)
			if err != nil {
				return nil, err
			}
			if e != nil {
				argExprs = append(argExprs, e)
			}
		}
	}
	return exprStmt(execCall("echo", argExprs)), nil
}

// lowerCommandElement — one argument of a command invocation.
func lowerCommandElement(n *sitter.Node, src []byte) (any, error) {
	switch n.Type() {
	case "command_argument_sep":
		// whitespace/separator between arguments
		return nil, nil
	case "unary_expression":
		return lowerUnary(n, src)
	case "generic_token":
		// bareword argument (`Write-Output hello`) — the core emits a
		// DoubleQuoted Str for barewords.
		return strExpr(n.Content(src), "DoubleQuoted"), nil
	case "variable":
		return getVarCall(variableName(n, src)), nil
	case "concatenated_command_argument":
		return lowerConcatArg(n, src)
	default:
		return nil, refuse(n, src, "argument form %q", n.Type())
	}
}

// lowerConcatArg — the concatenated_command_argument node: adjacent
// quoted / bareword / variable pieces with NO whitespace between them
// (`pre"mid"post`, `$x"b"`, `2"a"`). Live pwsh 7.6.4 argument-mode
// tokenization (verified 2026-08-13 with a 4-param argument counter):
//
//   - an argument with an UNQUOTED head (bareword / variable / number)
//     absorbs every following piece: `pre"mid"post` is ONE argument
//     "premidpost", `$x"b"` is one argument, `2"a"` is "2a";
//   - an argument that STARTS with a quoted string terminates there:
//     `Write-Output "a"b` passes TWO arguments ("a", "b") — pwsh
//     writes one pipeline OBJECT per argument, and the v1 echo mapping
//     is single-object, so the multi-object form REFUSES (the t02
//     null-edge precedent: refuse > guess) instead of joining the two
//     objects with a space like the core's `echo "a" b` would;
//   - `"a""b"` is NOT a concatenation — the doubled quote is an
//     ESCAPED quote inside one double-quoted string (parsed as a single
//     string_literal, so it never reaches this node).
//
// The pieces lower exactly like the core's equivalent bash word:
// adjacent literal text merges into one lit part, a variable adds an
// expr part, an all-literal tail lowers to a Str (the core's `echo
// pre"mid"post` → Str "premidpost"). Pieces outside the v1 subset
// (backtick escape_character, `$(…)` sub_expression, object-shaped
// array_literal_expression) REFUSE.
func lowerConcatArg(n *sitter.Node, src []byte) (any, error) {
	head := n.NamedChild(0)
	if head == nil {
		return nil, refuse(n, src, "empty concatenated argument")
	}
	switch head.Type() {
	case "string_literal", "expandable_string_literal":
		// pwsh writes one pipeline OBJECT per leading quoted string
		// (`Write-Output "a"b` prints "a" then "b" on separate lines)
		// — multi-object output, outside the single-object echo mapping.
		return nil, refuse(head, src, "quoted head of a concatenated argument (pwsh emits one object per leading quoted string; the multi-object output is outside the v1 single-object echo mapping)")
	case "generic_token", "variable", "unary_expression":
		// unquoted head — the pieces merge into ONE argument
	default:
		return nil, refuse(head, src, "head %q of a concatenated argument", head.Type())
	}
	parts, err := concatParts(n, src, 0)
	if err != nil {
		return nil, err
	}
	return mergeConcatParts(parts)
}

// concatParts — the pieces of the unquoted-headed tail as Interpolate
// parts: quoted strings contribute their interior parts, barewords
// contribute lit text, variables contribute expr parts.
func concatParts(n *sitter.Node, src []byte, start int) ([]any, error) {
	var parts []any
	for i := start; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "string_literal":
			ps, err := stringLiteralParts(ch, src)
			if err != nil {
				return nil, err
			}
			parts = append(parts, ps...)
		case "expandable_string_literal":
			ps, err := lowerExpandableStringParts(ch, src)
			if err != nil {
				return nil, err
			}
			parts = append(parts, ps...)
		case "generic_token":
			// bareword piece (`pre` in `pre"mid"post`)
			parts = append(parts, litPart(ch.Content(src)))
		case "variable":
			parts = append(parts, exprPart(getVarCall(variableName(ch, src))))
		case "unary_expression":
			// number-headed (`2"a"`) / variable-headed (`$x"b"`) piece
			if ch.NamedChildCount() != 1 {
				return nil, refuse(ch, src, "unary_expression with %d children", ch.NamedChildCount())
			}
			op := ch.NamedChild(0)
			switch op.Type() {
			case "variable":
				parts = append(parts, exprPart(getVarCall(variableName(op, src))))
			case "integer_literal":
				parts = append(parts, litPart(op.Content(src)))
			default:
				return nil, refuse(op, src, "%q inside a concatenated argument", op.Type())
			}
		default:
			return nil, refuse(ch, src, "piece %q inside a concatenated argument", ch.Type())
		}
	}
	return parts, nil
}

// mergeConcatParts — fold the pieces into one argument expression the
// way the core folds an adjacent bash word: adjacent lit parts merge
// into one; a variable anywhere makes it an Interpolate; an all-literal
// tail is a Str (the core's `echo pre"mid"post` → Str "premidpost").
func mergeConcatParts(parts []any) (any, error) {
	var merged []any
	for _, p := range parts {
		pm := p.(map[string]any)
		if pm["kind"] == "lit" && len(merged) > 0 {
			if last, ok := merged[len(merged)-1].(map[string]any); ok && last["kind"] == "lit" {
				last["text"] = last["text"].(string) + pm["text"].(string)
				continue
			}
		}
		merged = append(merged, p)
	}
	if len(merged) == 0 {
		return nil, fmt.Errorf("REFUSE: empty concatenated argument (PLAN_POWERSHELL_F.md v1 subset — refuse > guess)")
	}
	hasExpr := false
	var text strings.Builder
	for _, p := range merged {
		pm := p.(map[string]any)
		if pm["kind"] == "expr" {
			hasExpr = true
		} else {
			text.WriteString(pm["text"].(string))
		}
	}
	if hasExpr {
		return interpExpr(merged), nil
	}
	return strExpr(text.String(), "DoubleQuoted"), nil
}

// lowerUnary — unary_expression wraps its single operand.
func lowerUnary(n *sitter.Node, src []byte) (any, error) {
	if n.NamedChildCount() != 1 {
		return nil, refuse(n, src, "unary_expression with %d operands", n.NamedChildCount())
	}
	return lowerExpr(n.NamedChild(0), src)
}

// lowerExpr — an argument/operand expression.
func lowerExpr(n *sitter.Node, src []byte) (any, error) {
	switch n.Type() {
	case "string_literal":
		return lowerStringLiteral(n, src)
	case "variable":
		return getVarCall(variableName(n, src)), nil
	case "integer_literal":
		// `Write-Output 5` — the core's `echo 5` emits a Str.
		return strExpr(n.Content(src), "DoubleQuoted"), nil
	case "expression_with_unary_operator":
		// PowerShell wraps a leading cast (or -not / ! / ++ / --) in this
		// node; the v1 subset admits ONLY the cast form — the unary-
		// operator forms hit the default refuse below.
		if n.NamedChildCount() != 1 {
			return nil, refuse(n, src, "expression_with_unary_operator with %d children", n.NamedChildCount())
		}
		return lowerExpr(n.NamedChild(0), src)
	case "cast_expression":
		return lowerCast(n, src)
	default:
		return nil, refuse(n, src, "expression %q", n.Type())
	}
}

// lowerCast — `[type] operand` in COMMAND-ARGUMENT position. Verified
// against live pwsh 7.6.4 (2026-08-13 live-oracle flip): argument mode
// does NOT evaluate a leading type literal as a cast — `Write-Output
// [string]"cast value"` prints `[string]cast value`, `Write-Output
// [int]5` prints `[int]5`, and `[string]$foo` prints `[string]` followed
// by foo's value. The v1 subset reaches casts ONLY in argument position
// (expression position needs assignment, which refuses), so the whole
// argument lowers as an expandable string: the type_literal text is a
// lit part and the operand expands per argument-mode rules. (The plan's
// original pin — "identity, the C `(int)` precedent" — held only for
// expression position and was written against a guessed record, not
// real pwsh; superseded here.)
func lowerCast(n *sitter.Node, src []byte) (any, error) {
	// named children: type_literal + the operand (unary_expression)
	if n.NamedChildCount() != 2 {
		return nil, refuse(n, src, "cast_expression with %d children", n.NamedChildCount())
	}
	tl := n.NamedChild(0)
	if tl.Type() != "type_literal" {
		return nil, refuse(tl, src, "cast head %q", tl.Type())
	}
	op, err := lowerUnary(n.NamedChild(1), src)
	if err != nil {
		return nil, err
	}
	return interpExpr([]any{litPart(tl.Content(src)), exprPart(op)}), nil
}

// lowerStringLiteral — 'single-quoted' or "double-quoted".
func lowerStringLiteral(n *sitter.Node, src []byte) (any, error) {
	if n.NamedChildCount() != 1 {
		return nil, refuse(n, src, "string_literal without a body")
	}
	inner := n.NamedChild(0)
	switch inner.Type() {
	case "expandable_string_literal":
		return lowerExpandableString(inner, src)
	case "verbatim_string_characters":
		// 'single-quoted' — a literal string, no interpolation. The core
		// emits bash '…' with style DoubleQuoted; match it byte-for-byte.
		return strExpr(innerText(inner.Content(src)), "DoubleQuoted"), nil
	default:
		return nil, refuse(inner, src, "string body %q", inner.Type())
	}
}

// stringLiteralParts — the interior of a string_literal as Interpolate
// parts: double-quoted → the expandable interior's parts (lit/expr);
// single-quoted → ONE lit part (verbatim text, no interpolation).
func stringLiteralParts(n *sitter.Node, src []byte) ([]any, error) {
	if n.NamedChildCount() != 1 {
		return nil, refuse(n, src, "string_literal without a body")
	}
	inner := n.NamedChild(0)
	switch inner.Type() {
	case "expandable_string_literal":
		return lowerExpandableStringParts(inner, src)
	case "verbatim_string_characters":
		return []any{litPart(innerText(inner.Content(src)))}, nil
	default:
		return nil, refuse(inner, src, "string body %q", inner.Type())
	}
}

// lowerExpandableString — `"…"` with optional $var interpolation. The
// core lowers double-quoted strings to Interpolate with lit/expr parts.
//
// NOTE (vendored-runtime quirk): with the smacker runtime the interior
// text tokens of expandable_string_literal are not materialized as
// children — only the interpolated variables (and the closing quote)
// are. The literal text parts are therefore reconstructed from the byte
// spans between the named children (verified against the core for the
// no-variable case: a single lit part).
func lowerExpandableString(n *sitter.Node, src []byte) (any, error) {
	parts, err := lowerExpandableStringParts(n, src)
	if err != nil {
		return nil, err
	}
	return interpExpr(parts), nil
}

// lowerExpandableStringParts — the interior of a "…" string as
// Interpolate parts (lit text + getVar exprs).
//
// NOTE (vendored-runtime quirk): with the smacker runtime the interior
// text tokens of expandable_string_literal are not materialized as
// children — only the interpolated variables (and the closing quote)
// are. The literal text parts are therefore reconstructed from the byte
// spans between the named children (verified against the core for the
// no-variable case: a single lit part).
func lowerExpandableStringParts(n *sitter.Node, src []byte) ([]any, error) {
	var parts []any
	pos := n.StartByte() + 1 // skip the opening quote
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "variable":
			if txt := string(src[pos:ch.StartByte()]); txt != "" {
				parts = append(parts, litPart(txt))
			}
			parts = append(parts, exprPart(getVarCall(variableName(ch, src))))
			pos = ch.EndByte()
		default:
			return nil, refuse(ch, src, "%q inside a double-quoted string", ch.Type())
		}
	}
	end := n.EndByte()
	if end > pos && src[end-1] == '"' {
		end-- // drop the closing quote
	}
	if txt := string(src[pos:end]); txt != "" {
		parts = append(parts, litPart(txt))
	}
	return parts, nil
}

// variableName — `$x` → "x" and `${x}` → "x": the braces are pure
// spelling (braced_variable — the grammar's `variable` rule admits both
// forms; the braces exist for names with special characters), so both
// name the SAME store slot (case preserved; the shell store is
// case-insensitive at runtime).
func variableName(n *sitter.Node, src []byte) string {
	name := strings.TrimPrefix(n.Content(src), "$")
	return strings.TrimSuffix(strings.TrimPrefix(name, "{"), "}")
}

// innerText — the text between the quotes of a quoted-string token.
func innerText(t string) string {
	if len(t) >= 2 {
		return t[1 : len(t)-1]
	}
	return t
}
