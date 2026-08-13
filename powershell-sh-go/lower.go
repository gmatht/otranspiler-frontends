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
//	    do_statement                (t06 — the do-while duplication)
//	      statement_block → statement_list → … (the body, lowered once)
//	      `while` / `until` keyword, `(` while_condition `)`
//	        while_condition → pipeline → pipeline_chain → variable
//	    if_statement (t07 — then + optional else_clause; elseif_clauses REFUSES)
//	    empty_statement                 (t08 — a lone `;`, a NO-OP: dropped,
//	                                      emitting ZERO statements)
//	    assignment_expression / …          (REFUSE until pinned)

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
		if ch.Type() == "do_statement" {
			// the do-while duplication expands to SEVERAL statements
			// (body once + the While re-check) — flatten into the list
			ds, err := lowerDoStatement(ch, src)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, ds...)
			continue
		}
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
	case "if_statement":
		return lowerIfStatement(n, src)
	case "do_statement":
		// NOTE: handled in lowerStatementList (it expands to several
		// statements — the do-while duplication); reaching this switch
		// means a new call site appeared — refuse loudly rather than
		// box the multi-statement slice as one element.
		return nil, refuse(n, src, "do_statement outside a statement_list")
	case "empty_statement":
		// the lone `;` — the _statement rule's empty_statement
		// alternative; this grammar parses EVERY standalone `;` as
		// this node, including a trailing `;` after a pipeline (the
		// t08 pin's `Write-Output "a"; ; Write-Output "b"` has TWO
		// empty_statement children). Live pwsh 7.6.4 accepts it as a
		// NO-OP (verified: the oracle run prints "a" then "b", exit
		// 0). Lowering: ZERO statements — the node is dropped exactly
		// like comments (the plan's `#`-comments row, PLAN_POWERSHELL_F.md
		// §1; the A1 has no no-op node and needs none), so the emitted
		// program is byte-identical to the same program without the
		// `;` and the executed-stdout oracle matches live pwsh by
		// construction. The nil return is skipped by lowerStatementList
		// (and covers block bodies via lowerBlock, which shares this
		// path).
		return nil, nil
	default:
		return nil, refuse(n, src, "statement type %q", n.Type())
	}
}

// lowerDoStatement — the do_statement node: `do { … } while (cond)`. The
// grammar's shape is `do` + statement_block + a `while`/`until` keyword
// + `(` while_condition `)`. Live pwsh 7.6.4 semantics: the body runs
// ONCE, then the condition is re-checked. The plan's lowering is "the
// do-while duplication" (PLAN_POWERSHELL_F.md §1): `do { B } while (C)`
// ≡ `B; while (C) { B }` — the body executes once either way, and the
// emission stays on the A1 While node the ESTree renderer supports (the
// A1 DoWhile node is Perl-only there — the renderer panics, "Perl-only
// IR statement reached the ESTree renderer"; the duplication is the
// plan's chosen shape, not a workaround). The `until` keyword form
// (`do { … } until (cond)`, same node) REFUSES: unpinned — its
// lowering would need the A1 Not-cond wrap the core uses for bash
// `until` (a later rung).
func lowerDoStatement(n *sitter.Node, src []byte) ([]any, error) {
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.Child(i).Type() == "until" {
			return nil, refuse(n, src, "do/until (the t06 pin is the do/while form; `until` unpinned)")
		}
	}
	var body []any
	var cond any
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "statement_block":
			// the body lowers through the same statement_list path as
			// top-level statements
			b, err := lowerBlock(ch, src)
			if err != nil {
				return nil, err
			}
			body = b
		case "while_condition":
			c, err := lowerCondition(ch, src)
			if err != nil {
				return nil, err
			}
			cond = c
		default:
			return nil, refuse(ch, src, "do_statement part %q", ch.Type())
		}
	}
	if cond == nil {
		return nil, refuse(n, src, "do_statement without a condition")
	}
	// the do-while duplication: the body once, then the while re-check
	stmts := append([]any{}, body...)
	stmts = append(stmts, whileStmt(cond, body))
	return stmts, nil
}

// lowerCondition — a `(…)` loop condition (the while_condition node: a
// pipeline). The t06 subset pins ONLY a bare variable read (`while
// ($x)`); the same pipeline lowering is shared with the t07 if
// condition (lowerCondPipeline).
func lowerCondition(n *sitter.Node, src []byte) (any, error) {
	var pipeline *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "pipeline":
			if pipeline == nil {
				pipeline = ch
			}
		default:
			return nil, refuse(ch, src, "condition %q (the t06 subset pins a bare variable read)", ch.Type())
		}
	}
	if pipeline == nil {
		return nil, refuse(n, src, "condition without a pipeline")
	}
	return lowerCondPipeline(pipeline, src)
}

// lowerCondPipeline — a condition pipeline lowered as a bare variable
// read (the t06 subset, shared by the while/do conditions and the t07
// if condition). Verified against live pwsh 7.6.4: an UNSET variable
// reads $null there (FALSY) and "" from the A1 store (FALSY) — the
// condition-position null edge is CONSISTENT (unlike the t02 PRINT
// edge, where `Write-Output $x` prints nothing vs the A1 echo's blank
// line — that print edge stays outside the subset). Truthy pwsh
// automatic variables (`$true`, `$PID`, …) DIVERGE — pwsh truthy vs
// the A1 store read "" (falsy) — so `$true` REFUSES explicitly, and
// the general automatic-variable / env-var classes stay outside the v1
// text-closed subset (refuse > guess).
func lowerCondPipeline(pipeline *sitter.Node, src []byte) (any, error) {
	var chain *sitter.Node
	for i := 0; i < int(pipeline.NamedChildCount()); i++ {
		ch := pipeline.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			if chain == nil {
				chain = ch
			}
		case "pipeline_chain_tail":
			return nil, refuse(ch, src, "`|` inside a condition")
		default:
			return nil, refuse(ch, src, "condition %q", ch.Type())
		}
	}
	if chain == nil {
		return nil, refuse(pipeline, src, "condition without a chain")
	}
	if chain.NamedChildCount() != 1 {
		return nil, refuse(chain, src, "condition chain with %d children", chain.NamedChildCount())
	}
	op := chain.NamedChild(0)
	if op.Type() != "unary_expression" {
		return nil, refuse(op, src, "condition %q (the t06 subset pins a bare variable read)", op.Type())
	}
	if op.NamedChildCount() != 1 {
		return nil, refuse(op, src, "condition unary_expression with %d children", op.NamedChildCount())
	}
	inner := op.NamedChild(0)
	if inner.Type() != "variable" {
		return nil, refuse(inner, src, "condition operand %q (the t06 subset pins a bare variable read)", inner.Type())
	}
	name := variableName(inner, src)
	if name == "true" {
		return nil, refuse(inner, src, "`$true` in a condition: pwsh reads a TRUTHY automatic while the A1 getVar read is \"\" (falsy) — the branches DIVERGE (a while condition would loop forever, an if condition would take the wrong branch); the t06/t07 subset pins unset-user-variable conditions")
	}
	return getVarCall(name), nil
}

// lowerBlock — a statement_block's statement_list lowered as a list
// (the t06 do-body and the t07 if/else bodies share this path). An
// empty block (`{ }`) has NO statement_list child in this grammar —
// the t06 do-body path treated that as an empty body (no statements,
// no output); keep the same reading here (an empty list is not a
// guess, and the A1 accepts empty arrays).
func lowerBlock(n *sitter.Node, src []byte) ([]any, error) {
	if bl := n.ChildByFieldName("statement_list"); bl != nil {
		return lowerStatementList(bl, src)
	}
	return []any{}, nil
}

// lowerIfStatement — the if_statement node: `if` `(` condition `)`
// statement_block [elseif_clauses] [else_clause]. The t07 rung lands
// the else_clause tail: `if ($c) { B } else { E }` lowers to the A1 If
// node (cond/then/elsifs/else — the plan's "If / else-if chain" row,
// PLAN_POWERSHELL_F.md §1; byte-identical to the core's `if`
// emission). The condition is the t06 condition subset (a bare
// variable read — lowerCondPipeline, so the `$true` divergence refuses
// the same way). The else_clause is OPTIONAL: a bare `if ($c) { B }`
// lowers with else: [] (the core's shape for an if without an else
// branch). The elseif_clauses field REFUSES — the elseif chain is a
// separate rung (the A1 elsifs slot is ready but unpinned; refuse >
// guess).
func lowerIfStatement(n *sitter.Node, src []byte) (any, error) {
	var cond any
	var then []any
	var els []any
	gotCond, gotThen := false, false
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "pipeline":
			// the `condition` field: `if (…)`
			if gotCond {
				return nil, refuse(ch, src, "if_statement with multiple conditions")
			}
			c, err := lowerCondPipeline(ch, src)
			if err != nil {
				return nil, err
			}
			cond, gotCond = c, true
		case "statement_block":
			// the then-branch body
			if gotThen {
				return nil, refuse(ch, src, "if_statement with multiple bodies")
			}
			b, err := lowerBlock(ch, src)
			if err != nil {
				return nil, err
			}
			then, gotThen = b, true
		case "elseif_clauses":
			return nil, refuse(ch, src, "elseif clauses (the elseif chain is a separate rung — this rung pins the else_clause; the A1 elsifs slot is ready)")
		case "else_clause":
			// the `else` tail: else keyword (anonymous) + statement_block
			var block *sitter.Node
			for j := 0; j < int(ch.NamedChildCount()); j++ {
				ec := ch.NamedChild(j)
				switch ec.Type() {
				case "statement_block":
					block = ec
				default:
					return nil, refuse(ec, src, "else_clause part %q", ec.Type())
				}
			}
			if block == nil {
				return nil, refuse(ch, src, "else_clause without a body")
			}
			b, err := lowerBlock(block, src)
			if err != nil {
				return nil, err
			}
			els = b
		default:
			return nil, refuse(ch, src, "if_statement part %q", ch.Type())
		}
	}
	if !gotCond {
		return nil, refuse(n, src, "if_statement without a condition")
	}
	if !gotThen {
		return nil, refuse(n, src, "if_statement without a body")
	}
	return ifStmt(cond, then, els), nil
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
