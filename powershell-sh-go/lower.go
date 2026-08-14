package ps1lib

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// maxRangeSpan — the t24 range rung's compile-time fold cap: a range
// lowers to ONE A1 echo statement PER element (each element is its own
// pipeline object in pwsh), so an unbounded span would blow up the
// emitted A1 from a tiny source (`1..1000000` is 9 bytes → a million
// statements). The subset pins short literal ranges; anything beyond
// the cap REFUSES (the runtime array rung is a later milestone).
const maxRangeSpan = 1000

// ── CST structure (tree-sitter-powershell, vendored grammar) ──────────
//
//	program
//	  comment*                    (skip)
//	  statement_list
//	    label                     (t18 — the `:name` prefix of a labeled
//	                                loop, a SIBLING before the loop
//	                                statement; unreferenced in v1 → dropped)
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
//	            expandable_bareword               ($foo-bar — t09)
//	            concatenated_command_argument     (adjacent pieces — t05)
//	      pipeline_chain_tail*    (`&&` / `||` — the t26 pipeline-chain
//	                                operators: TWO chains + ONE tail →
//	                                the A1 BinOp And/Or. The `|` pipe is
//	                                NOT this node — it is the anonymous
//	                                _pipeline_tail token INSIDE a chain
//	                                (two command children) → the t08
//	                                text-pipe rung, REFUSE)
//	    do_statement                (t06 — the do-while duplication)
//	      statement_block → statement_list → … (the body, lowered once)
//	      `while` / `until` keyword, `(` while_condition `)`
//	        while_condition → pipeline → pipeline_chain → variable
//	    if_statement (t07 — then + optional else_clause; elseif_clauses REFUSES)
//	    for_statement (t12 — the condition-only `for (; $c; )` form → While;
//	      for_initializer / for_iterator / conditionless REFUSE)
//	    foreach_statement (t13 — `foreach ($x in $list) { B }` → the A1 For;
//	      the `-parallel` foreach_parameter REFUSES)
//	    empty_statement                 (t08 — a lone `;`, a NO-OP: dropped,
//	                                      emitting ZERO statements)
//	    param_block                     (t22 — the script-level `param(...)`
//	                                      declaration, BETWEEN the directives
//	                                      and the statement_list; plain
//	                                      variables only → dropped, the
//	                                      t18-label pure-spelling precedent)
//	    requires_directive_list         (t28 — the top-of-file `#requires`
//	                                      directive lines; met requirements
//	                                      only → dropped, the t22 precedent;
//	                                      a mid-file `#requires` parses as a
//	                                      COMMENT — pwsh enforces it there
//	                                      too, so the comment REFUSES)
//	    using_directive_list               (by-design refusal — the plan's
//	                                      "`using` / modules" row)
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
			// a `#requires`-prefixed comment is NOT a plain comment: live
			// pwsh 7.6.4 enforces `#requires` in ANY position (verified
			// 2026-08-19: a mid-file `#requires -Version 99` after a
			// Write-Output fails the run BEFORE the first statement
			// prints) while the vendored grammar builds
			// requires_directive_list only at the top of the file and
			// parses a mid-file `#requires` as a comment (the t19
			// grammar-over-accepts precedent: refuse > guess — a silent
			// drop would miscompile). `# requires` (a space after the
			// #) is a REAL comment (verified: pwsh ignores it) and
			// stays dropped.
			if requiresComment(ch, src) {
				return nil, refuse(ch, src, "`#requires` outside the top-of-file directive list %q (pwsh 7.6.4 enforces it in any position while the vendored grammar parses it as a comment — refuse > guess)", ch.Content(src))
			}
			continue
		case "param_block":
			// the script-level `param(...)` block — validates the v1
			// subset (plain variables only) and lowers to ZERO
			// statements: the declaration is dropped (see
			// lowerParamBlock).
			if err := lowerParamBlock(ch, src); err != nil {
				return nil, err
			}
		case "requires_directive_list":
			// the top-of-file `#requires` directive lines — validates the
			// v1 subset (met requirements only) and lowers to ZERO
			// statements: the directives are dropped (see
			// lowerRequiresDirectiveList).
			if err := lowerRequiresDirectiveList(ch, src); err != nil {
				return nil, err
			}
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

// lowerParamBlock — the param_block node: the script-level `param(...)`
// parameter declaration. The grammar's program rule is
// `[using/requires] [param_block] statement_list`, so the node sits
// BETWEEN the directives and the statement_list as a direct child of
// program (the same node also hosts the param-block form of FUNCTION
// bodies — `function name { param($a) … }` — which refuses on the
// function itself: functions are the function rung, refused in v1).
//
// Semantics within the v1 text-closed subset (verified against live
// pwsh 7.6.4): the transpiled program is ALWAYS run with NO arguments
// (the oracle runs `pwsh -NoProfile -File` / the ESTree runner with an
// empty argv), and v1 has no script-arguments channel ($args refuses;
// assignment refuses) — so a param block with plain variables and NO
// defaults leaves every parameter $null, EXACTLY like an undeclared
// variable: reads of the parameters lower through the usual getVar
// slots and interpolate as "" (the t09/t10 consistent edge), and the
// declaration itself has no runtime effect. Lowering: ZERO statements
// — the node is dropped exactly like the t18 label (a label has no
// runtime effect unless a break/continue targets it, and v1 refuses
// break/continue; the t04 invocation-operator precedent), so the
// emitted program is byte-identical to the same program without the
// param line and the executed-stdout oracle matches live pwsh by
// construction.
//
// The pinned subset is PLAIN VARIABLES only: `param($a, $b)` / `param()`
// (the parameter_list is optional — `param()` parses as a childless
// param_block) / the t02 braced spelling `param(${a})`. The two
// child/parameter forms that would change the observable output REFUSE:
//
//   - attribute_list (param_block-level `[CmdletBinding()]` or
//     per-parameter `[string]$a` / `[Parameter()]`) — the plan's
//     "`Param()` advanced attributes" refusal (PLAN_POWERSHELL_F.md §1
//     pinned refusals); metadata that affects binding, unpinned.
//   - script_parameter_default (`param($a = "d")`) — pwsh binds the
//     DEFAULT when no argument is passed (a real assignment), so the
//     reads would print the default where the A1 store reads "" —
//     divergent output; the assignment rung lands it (refuse > guess).
func lowerParamBlock(n *sitter.Node, src []byte) error {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "attribute_list":
			return refuse(ch, src, "param attribute_list %q (the `Param()` advanced-attribute refusal — PLAN_POWERSHELL_F.md §1 pinned refusals)", ch.Content(src))
		case "parameter_list":
			for j := 0; j < int(ch.NamedChildCount()); j++ {
				sp := ch.NamedChild(j)
				if sp.Type() != "script_parameter" {
					return refuse(sp, src, "parameter_list element %q", sp.Type())
				}
				for k := 0; k < int(sp.NamedChildCount()); k++ {
					pc := sp.NamedChild(k)
					switch pc.Type() {
					case "variable":
						// a plain `$name` / `${name}` parameter — the
					// t02 brace precedent; the declaration is dropped.
					case "attribute_list":
						return refuse(pc, src, "param attribute_list %q (the `Param()` advanced-attribute refusal — PLAN_POWERSHELL_F.md §1 pinned refusals)", pc.Content(src))
					case "script_parameter_default":
						return refuse(pc, src, "param default %q (pwsh binds the default when no argument is passed — divergent output; the assignment rung lands it)", pc.Content(src))
					default:
						return refuse(pc, src, "script_parameter part %q", pc.Type())
					}
				}
			}
		default:
			return refuse(ch, src, "param_block part %q", ch.Type())
		}
	}
	return nil
}

// requiresMaxMinor — the t28 requires subset's major-7 minor ceiling:
// the executed-stdout oracle is pinned to LIVE pwsh 7.6.4 (the
// FRONTEND.md pin: the direct snap binary), and the subset accepts
// only requirements that oracle SATISFIES. `-Version` is NOT a numeric
// comparison in pwsh — it is a WHITELIST of the real PowerShell
// release lines (see requiresVersionOK); the major-7 line reaches the
// oracle's own minor, 6.
const requiresMaxMinor = 6

// lowerRequiresDirectiveList — the requires_directive_list node: the
// script-level `#requires` directive lines, the SECOND of the grammar's
// program-level directive lists (the program rule is
// `[using/requires] [param_block] statement_list`, so the node sits at
// the TOP of the file, BEFORE the param_block / statement_list — the
// t22 param_block's sibling; the `using` form stays a by-design
// refusal, the plan's "`using` / modules" row). The node is a repeat
// of requires_statement children, each a requires_keyword token + one
// requires_argument_group per argument (each group wraps ONE
// requires_argument — a command_parameter / generic_token /
// integer_literal / real_literal / string_literal /
// hash_literal_expression — verified against the CST).
//
// Live pwsh 7.6.4 (verified 2026-08-19): `#requires` is a script-level
// REQUIREMENT check enforced at startup in -File mode — an unmet
// requirement fails the run BEFORE any statement prints (a mid-file
// `#requires -Version 99` after a Write-Output still fails without
// printing it). The v1 subset pins ONLY requirements the pinned oracle
// is GUARANTEED to satisfy, so within the subset the directive has NO
// runtime effect and the lowering is ZERO statements: the node is
// dropped exactly like the t22 param_block (the t18-label pure-
// spelling precedent), byte-identical to the same program without the
// directive lines, and the executed-stdout oracle matches live pwsh by
// construction. The accepted requirements:
//
//   - `-Version M.m` — a real_literal decimal version on a real
//     PowerShell release line the oracle accepts (verified against
//     live pwsh 7.6.4: `#requires -Version 5.1` and `-Version 5` run
//     clean) or `-Version M` — an integer_literal (the t11
//     decimal-integer precedent). The acceptance is the ORACLE'S OWN
//     whitelist, NOT a numeric comparison (PSVersionInfo.
//     IsValidPSVersion, verified 2026-08-19 against live pwsh):
//     majors 1-4 accept minor 0 only, major 5 accepts 0/1, major 6
//     accepts 0-2, major 7 accepts 0-6 (the oracle's minor), build
//     parts are ignored — `-Version 6.2` runs clean while `-Version
//     6.3` / `5.2` / `7.7` / `99` all fail the run (a numeric ≤ 7.6
//     check would have ACCEPTED 6.3 — a silent miscompile, pinned
//     testdata_refuse/t28_requires_version_high_minor.ps1).
//
// EACH parameter may appear ONCE per script: pwsh binds the whole
// directive list into ONE parameter set (verified 2026-08-19:
// `#requires -Version 5.1` + `#requires -Version 5` — even on
// SEPARATE lines — fails the run with "Cannot bind parameter because
// parameter 'version' is specified more than once"), so a duplicate
// REFUSES (pinned testdata_refuse/t28_requires_duplicate.ps1). The
// integer-literal and multi-argument statement forms share the
// pairwise group validation but cannot coexist with the example's
// `-Version` in ONE script — the once-per-script binding (both are
// exercised by the probes that pinned this rung).
//   - `-PSEdition Core` — a generic_token whose text is "Core"
//     (case-insensitive): pwsh IS the Core edition (verified:
//     `-PSEdition Desktop` fails the run — "does not match the
//     currently running PowerShell Core edition").
//
// The other forms REFUSE (pinned testdata_refuse/t28_*): `-Modules`
// (module presence is environment-dependent — the plan's "`using` /
// modules" refusal), `-RunAsAdministrator` (elevation-dependent),
// `-ShellId` (pwsh 7.6.4 rejects it — "must specify a required
// PowerShell snap-in"), a parameter with no value / a value with no
// parameter (pwsh parse errors), string / hash values, a comma-list
// group and multi-dot versions (`5.1.1` parses as a generic_token —
// the t27 unpinned-shape precedent).
//
// The vendored grammar OVER-ACCEPTS two shapes, both guarded here
// (refuse > guess — a silent drop would miscompile):
//
//   - a bare `#requires` (no arguments) makes the requires_statement
//     rule SWALLOW the following statements as argument groups
//     (verified against the CST: `#requires\nWrite-Output "ran"`
//     parses the echo as argument groups, no statement_list at all).
//     The swallowed text usually refuses on the group validation, but
//     `#requires\n-Version 5.1` would accidentally validate — so a
//     requires_statement's content must be a SINGLE LINE (a legit
//     directive is one line; the swallow always spans a newline).
//   - `#requires-Version 5.1` (NO space after the keyword) parses as
//     a directive while live pwsh 7.6.4 rejects it at parse time
//     (verified: ParserError). The guard: the bytes between the
//     requires_keyword and the first argument group must be non-empty
//     space/tab whitespace.
func lowerRequiresDirectiveList(n *sitter.Node, src []byte) error {
	// pwsh binds the WHOLE directive list into one parameter set: each
	// parameter may appear once per script (a duplicate is a parse
	// error — "Cannot bind parameter because parameter 'x' is
	// specified more than once", verified 2026-08-19).
	seen := map[string]bool{}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		stmt := n.NamedChild(i)
		if stmt.Type() != "requires_statement" {
			return refuse(stmt, src, "requires_directive_list element %q", stmt.Type())
		}
		// guard: a legit directive is a single line; the bare-`#requires`
		// swallow spans the following statements (and their newline).
		if strings.ContainsRune(stmt.Content(src), '\n') {
			return refuse(stmt, src, "#requires across lines %q (the grammar's bare-`#requires` swallow — live pwsh parses `#requires` only on its own line; refuse > guess)", stmt.Content(src))
		}
		var kw *sitter.Node
		var groups []*sitter.Node
		for j := 0; j < int(stmt.NamedChildCount()); j++ {
			c := stmt.NamedChild(j)
			switch c.Type() {
			case "requires_keyword":
				kw = c
			case "requires_argument_group":
				groups = append(groups, c)
			default:
				return refuse(c, src, "requires_statement part %q", c.Type())
			}
		}
		if kw == nil || len(groups) == 0 {
			return refuse(stmt, src, "#requires with no arguments %q (the bare form is unpinned — the grammar swallows the following statements)", stmt.Content(src))
		}
		// guard: live pwsh needs whitespace after the `#requires` keyword
		// (`#requires-Version 5.1` is a ParserError in pwsh 7.6.4 while
		// the vendored grammar over-accepts it as a directive).
		gap := src[kw.EndByte():groups[0].StartByte()]
		if len(gap) == 0 || len(bytes.Trim(gap, " \t")) != 0 {
			return refuse(kw, src, "#requires keyword %q (live pwsh needs whitespace after `#requires`; the no-space form is a pwsh ParserError — refuse > guess)", kw.Content(src))
		}
		// validate the argument groups pairwise: a whitelisted parameter
		// consumes the FOLLOWING group as its value (the pwsh semantics
		// — `-Version 5.1 -PSEdition Core` in ONE statement is four
		// groups).
		g := 0
		for g < len(groups) {
			v, err := requiresArgument(groups[g], src)
			if err != nil {
				return err
			}
			if v.Type() != "command_parameter" {
				return refuse(v, src, "requires argument %q (a value with no parameter — the grammar's bare-`#requires` swallow?)", v.Content(src))
			}
			p := strings.ToLower(v.Content(src))
			if seen[p] {
				return refuse(v, src, "duplicate #requires parameter %q (pwsh 7.6.4 binds the whole directive list into ONE parameter set — a repeat is a parse error; refuse > guess)", p)
			}
			seen[p] = true
			switch p {
			case "-version":
				g++
				if g >= len(groups) {
					return refuse(v, src, "#requires -Version with no version value (pwsh 7.6.4 parse-errors too)")
				}
				if err := checkRequiresVersion(groups[g], src); err != nil {
					return err
				}
			case "-psedition":
				g++
				if g >= len(groups) {
					return refuse(v, src, "#requires -PSEdition with no edition value (pwsh 7.6.4 parse-errors too)")
				}
				e, err := requiresArgument(groups[g], src)
				if err != nil {
					return err
				}
				if e.Type() != "generic_token" || !strings.EqualFold(e.Content(src), "core") {
					return refuse(e, src, "#requires -PSEdition value %q (the subset pins \"Core\" — pwsh IS the Core edition; \"Desktop\" fails the oracle run)", e.Content(src))
				}
			default:
				return refuse(v, src, "#requires parameter %q (the v1 subset pins -Version on a release line the oracle accepts and -PSEdition Core; the %s forms are environment-dependent / unpinned — refuse > guess)", v.Content(src), p)
			}
			g++
		}
	}
	return nil
}

// requiresArgument — the single argument node inside a
// requires_argument_group (the grammar: `requires_argument (","
// requires_argument)*` — a comma-list group is an unpinned shape, so
// the group must wrap EXACTLY one argument). Returns the inner
// argument node (the command_parameter / literal / token).
func requiresArgument(g *sitter.Node, src []byte) (*sitter.Node, error) {
	if g.Type() != "requires_argument_group" || g.NamedChildCount() != 1 ||
		g.NamedChild(0).Type() != "requires_argument" || g.NamedChild(0).NamedChildCount() != 1 {
		return nil, refuse(g, src, "requires argument group %q (the subset pins a single plain argument per group)", g.Content(src))
	}
	return g.NamedChild(0).NamedChild(0), nil
}

// checkRequiresVersion — the `-Version` requirement value: a bare
// decimal version the pinned oracle pwsh 7.6.4 satisfies. Accepts
// real_literal `M.m` and integer_literal `M` ON THE ORACLE'S OWN
// release-line whitelist (requiresVersionOK — both forms verified
// against live pwsh: `-Version 5.1` / `-Version 5` run clean). Any
// version OFF the whitelist fails the oracle run (verified: `-Version
// 99` / `-Version 6.3` / `-Version 5.2` → "does not match the
// currently running version of PowerShell 7.6.4") while the
// transpiled drop would run — divergent, so it REFUSES (pinned
// testdata_refuse/t28_requires_version_*). The other shapes REFUSE
// too: a multi-dot `5.1.1` parses as a generic_token (the t27
// unpinned-shape precedent — pwsh ignores build parts, but the
// two-part spelling is the pinned surface) and a string value is
// unpinned.
func checkRequiresVersion(g *sitter.Node, src []byte) error {
	v, err := requiresArgument(g, src)
	if err != nil {
		return err
	}
	text := v.Content(src)
	switch v.Type() {
	case "real_literal":
		maj, min, ok := decimalVersion(text)
		if !ok || !requiresVersionOK(maj, min) {
			return refuse(v, src, "#requires -Version value %q (the subset pins a PowerShell release-line version the oracle 7.6.4 accepts — an off-whitelist version fails the native run where the transpiled drop would run)", text)
		}
	case "integer_literal":
		n, err := strconv.Atoi(text)
		if err != nil || !requiresVersionOK(n, 0) {
			return refuse(v, src, "#requires -Version value %q (the subset pins a PowerShell release-line version the oracle 7.6.4 accepts — an off-whitelist version fails the native run where the transpiled drop would run)", text)
		}
	default:
		return refuse(v, src, "#requires -Version value %q (the subset pins a bare decimal version — the multi-dot / string / bareword forms are unpinned)", text)
	}
	return nil
}

// requiresVersionOK — the pwsh `#requires -Version` acceptance
// whitelist, replicated from the ORACLE'S OWN check
// (PSVersionInfo.IsValidPSVersion in the PowerShell source, verified
// empirically 2026-08-19 against live pwsh 7.6.4): majors 1-4 accept
// minor 0 ONLY, major 5 accepts 0/1, major 6 accepts 0-2, major 7
// accepts 0..requiresMaxMinor (the oracle's minor, 6). The check is
// NOT a numeric comparison — `-Version 6.2` runs clean while `-Version
// 6.3` / `-Version 5.2` fail the run, and build parts are ignored
// (`-Version 7.6.99` runs clean). Every accepted value is a version
// the pinned oracle satisfies; everything else REFUSES (refuse >
// guess).
func requiresVersionOK(maj, min int) bool {
	switch {
	case maj >= 1 && maj <= 4:
		return min == 0
	case maj == 5:
		return min == 0 || min == 1
	case maj == 6:
		return min >= 0 && min <= 2
	case maj == 7:
		return min >= 0 && min <= requiresMaxMinor
	}
	return false
}

// decimalVersion — parse a real_literal text as a two-part decimal
// version `M.m`. Rejects exponents (`5.1e3`), sign forms and missing
// digits: the accepted subset is the plain `\p{Nd}+\.\p{Nd}+` token
// form. Leading zeros parse numerically (Atoi), matching the [version]
// parse pwsh uses — `-Version 7.06` and `-Version 5.001` run clean in
// live pwsh and both accept here, while `-Version 5.02` (minor 2, off
// the 5.x line) refuses on the whitelist.
func decimalVersion(text string) (int, int, bool) {
	parts := strings.Split(text, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || maj < 0 || min < 0 {
		return 0, 0, false
	}
	return maj, min, true
}

// requiresComment — whether a comment node is REALLY a `#requires`
// directive: live pwsh 7.6.4 treats ANY comment starting with
// `#requires` (case-insensitive, no space — `#requiresx` too, verified
// 2026-08-19) as a requires statement, wherever it appears. The
// vendored grammar builds requires_directive_list only at the top of
// the file, so mid-file the directive is a comment node.
func requiresComment(n *sitter.Node, src []byte) bool {
	return strings.HasPrefix(strings.ToLower(n.Content(src)), "#requires")
}

// lowerStatementList — the statements of one statement_list.
func lowerStatementList(n *sitter.Node, src []byte) ([]any, error) {
	var stmts []any
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		if ch.Type() == "label" {
			// the `:name` prefix of a labeled loop (the grammar's
			// _statement rule: [label] _labeled_statement — the label
			// is a SIBLING node before the loop statement in the
			// statement_list; _labeled_statement is one of switch /
			// foreach / for / while / do). A label has NO runtime
			// effect unless a break/continue targets it, and v1
			// REFUSES break/continue (lowerFlowControl — the loop
			// signals) — so every expressible program's label is
			// UNREFERENCED: pure spelling, dropped exactly like the
			// t02 braces / t04 invocation operator (the t18 pin). The
			// following loop statement lowers through its normal path
			// (a label before a refused loop — while / switch — still
			// refuses on the loop itself, loudly).
			continue
		}
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
		ss, err := lowerStatement(ch, src)
		if err != nil {
			return nil, err
		}
		// a statement expands to one or more A1 statements: the t06
		// do-while duplication and the t14 head+argument_list echo
		// split both emit SEVERAL statements (the nil empty_statement
		// return is an empty slice — a no-op append).
		stmts = append(stmts, ss...)
	}
	return stmts, nil
}

// lowerStatement — one top-level statement, as the A1 statements it
// lowers to (the t06 do-while duplication and the t14 argument_list
// echo split expand to SEVERAL; everything else is a one-element
// slice).
func lowerStatement(n *sitter.Node, src []byte) ([]any, error) {
	switch n.Type() {
	case "pipeline":
		return lowerPipeline(n, src)
	case "if_statement":
		s, err := lowerIfStatement(n, src)
		if err != nil {
			return nil, err
		}
		return []any{s}, nil
	case "do_statement":
		// NOTE: handled in lowerStatementList (it expands to several
		// statements — the do-while duplication); reaching this switch
		// means a new call site appeared — refuse loudly rather than
		// box the multi-statement slice as one element.
		return nil, refuse(n, src, "do_statement outside a statement_list")
	case "for_statement":
		s, err := lowerForStatement(n, src)
		if err != nil {
			return nil, err
		}
		return []any{s}, nil
	case "foreach_statement":
		s, err := lowerForEachStatement(n, src)
		if err != nil {
			return nil, err
		}
		return []any{s}, nil
	case "switch_statement":
		s, err := lowerSwitchStatement(n, src)
		if err != nil {
			return nil, err
		}
		return []any{s}, nil
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
		// construction. The nil return (an empty slice) is skipped by
		// lowerStatementList (and covers block bodies via lowerBlock,
		// which shares this path).
		return nil, nil
	case "flow_control_statement":
		s, err := lowerFlowControl(n, src)
		if err != nil {
			return nil, err
		}
		return []any{s}, nil
	default:
		return nil, refuse(n, src, "statement type %q", n.Type())
	}
}

// lowerFlowControl — the flow_control_statement node: the grammar's five
// keyword statements `break` / `continue` / `throw` / `return` / `exit`
// (keyword token + optional tail — a label_expression for break/continue,
// a pipeline for throw/return/exit). The t11 rung lands the EXIT form:
// `exit N` → the A1 Exit statement with an Int code, `exit` bare → Exit
// with value null (the lastExit channel) — the bat frontend's `exit /b`
// precedent, the only A1 Exit the renderer family already emits (estree
// → process.exit, all backends render it). The other four forms REFUSE:
//
//   - break / continue — the A1 Break/Continue loop signals (the plan's
//     "loop signals" row) need a LOOP to signal. v1's only landed loop is
//     the t06 do_statement, whose do-while duplication lowers the body
//     ONCE OUTSIDE the loop (`do { B } while (C)` → `B; while (C) { B }`) —
//     a break/continue inside B would land its first copy at top level,
//     a miscompile (the A1 renders a top-level Break as a runtime signal
//     throw; bash's top-level break is an error-and-continue, pwsh's
//     halts the script — three different semantics, none soundly
//     mappable). The while/for rungs (not yet landed) host them; until
//     then refuse > guess.
//   - return — the function rung's value channel (the plan maps `return
//     v` inside functions, which refuse in v1; a top-level return in pwsh
//     halts the script — unpinned).
//   - throw — the exception model: the A1 has no exceptions (the plan's
//     try/catch/finally refusal, PLAN_POWERSHELL_F.md §1).
func lowerFlowControl(n *sitter.Node, src []byte) (any, error) {
	var kw string
	var pipeline *sitter.Node
	// NOTE: the keyword token (break/continue/throw/return/exit) is an
	// ANONYMOUS alias in this grammar (ALIAS named:false — the --cst
	// dump shows it because dumpNode walks ChildCount, ALL children), so
	// the scan iterates ChildCount and matches the token types; the
	// optional tail is a named child (pipeline / label_expression).
	for i := 0; i < int(n.ChildCount()); i++ {
		ch := n.Child(i)
		switch ch.Type() {
		case "break", "continue", "throw", "return", "exit":
			if kw != "" {
				return nil, refuse(n, src, "flow_control_statement with multiple keywords")
			}
			kw = ch.Type()
		case "pipeline":
			if pipeline != nil {
				return nil, refuse(n, src, "flow_control_statement with multiple pipelines")
			}
			pipeline = ch
		default:
			if ch.IsNamed() {
				return nil, refuse(ch, src, "flow_control_statement part %q (a labeled break/continue is outside the v1 subset)", ch.Type())
			}
		}
	}
	switch kw {
	case "exit":
		if pipeline == nil {
			// bare `exit` — the A1 Exit with no code (lastExit)
			return exitStmt(nil), nil
		}
		code, err := lowerExitCode(pipeline, src)
		if err != nil {
			return nil, err
		}
		return exitStmt(code), nil
	case "break", "continue":
		return nil, refuse(n, src, "%s: the A1 %s is a loop signal, and v1's only landed loop (the t06 do_statement) duplicates its body OUTSIDE the loop — a break/continue there would miscompile; the while/for rungs host it (the t11 pin lands the exit form)", kw, strings.ToUpper(kw[:1])+kw[1:])
	case "return":
		return nil, refuse(n, src, "return: the A1 Return is the function rung's value channel (functions refuse in v1); a top-level return in pwsh halts the script — unpinned")
	case "throw":
		return nil, refuse(n, src, "throw: the exception model — the A1 has no exceptions (the try/catch/finally refusal, PLAN_POWERSHELL_F.md §1)")
	default:
		return nil, refuse(n, src, "flow_control_statement keyword %q", kw)
	}
}

// lowerExitCode — the `exit` form's optional pipeline as an Int code.
// The t11 subset pins a BARE decimal integer (`exit 5` — the plan's
// `exit N` row): pipeline → pipeline_chain → unary_expression →
// integer_literal, no pipes, no unary operators (a `-` head parses as
// expression_with_unary_operator — refused), no variables. Anything else
// is unpinned and refuses (refuse > guess).
func lowerExitCode(pipeline *sitter.Node, src []byte) (any, error) {
	var chain *sitter.Node
	for i := 0; i < int(pipeline.NamedChildCount()); i++ {
		ch := pipeline.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			if chain != nil {
				return nil, refuse(pipeline, src, "exit code with multiple chains")
			}
			chain = ch
		case "pipeline_chain_tail":
			return nil, refuse(ch, src, "`|` in an exit code")
		default:
			return nil, refuse(ch, src, "exit code %q", ch.Type())
		}
	}
	if chain == nil || chain.NamedChildCount() != 1 {
		return nil, refuse(pipeline, src, "exit code without a single chain")
	}
	op := chain.NamedChild(0)
	if op.Type() != "unary_expression" || op.NamedChildCount() != 1 {
		return nil, refuse(op, src, "exit code %q (the t11 subset pins a bare decimal integer)", op.Type())
	}
	inner := op.NamedChild(0)
	if inner.Type() != "integer_literal" {
		return nil, refuse(inner, src, "exit code operand %q (the t11 subset pins a bare decimal integer)", inner.Type())
	}
	n, err := strconv.ParseInt(inner.Content(src), 10, 64)
	if err != nil {
		return nil, refuse(inner, src, "exit code %q is not a decimal integer", inner.Content(src))
	}
	return intExpr(n), nil
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
	return lowerCondVar(op, src, "condition")
}

// lowerCondVar — a condition operand as a bare variable read (the
// unary_expression → variable tail of the t06/t07 condition subset,
// shared by the while/do/if conditions AND the t20 null-coalesce LHS —
// the coalesce lowers through the SAME condition semantics: `??` is a
// NULL check, and in the pinned subset (variables are never assigned)
// an unset variable reads $null in pwsh / "" from the A1 store — both
// FALSY — so the condition-position null edge is CONSISTENT there too
// (the t06/t07 precedent; the t02 PRINT edge stays outside the subset).
// `what` names the construct in the refusal messages ("condition" /
// "null-coalesce left operand").
func lowerCondVar(op *sitter.Node, src []byte, what string) (any, error) {
	if op.Type() != "unary_expression" {
		return nil, refuse(op, src, "%s %q (the t06 subset pins a bare variable read)", what, op.Type())
	}
	if op.NamedChildCount() != 1 {
		return nil, refuse(op, src, "%s unary_expression with %d children", what, op.NamedChildCount())
	}
	inner := op.NamedChild(0)
	if inner.Type() != "variable" {
		return nil, refuse(inner, src, "%s operand %q (the t06 subset pins a bare variable read)", what, inner.Type())
	}
	name := variableName(inner, src)
	if name == "true" {
		return nil, refuse(inner, src, "`$true` in a %s: pwsh reads a TRUTHY automatic while the A1 getVar read is \"\" (falsy) — the branches DIVERGE (a while condition would loop forever, an if condition would take the wrong branch); the t06/t07 subset pins unset-user-variable conditions", what)
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

// lowerForStatement — the for_statement node: `for` `(` [for_initializer]
// `;` [for_condition] `;` [for_iterator] `)` statement_block (the
// grammar admits ANY subset of the three clauses; the `for` keyword is
// a non-named alias token). The t12 rung pins the CONDITION-ONLY form
// `for (; $c; ) { B }`, which lowers EXACTLY to the A1 While statement
// `while ($c) { B }` — with empty init/iter clauses a for loop IS a
// while loop, and the emission is byte-identical to the t06 do-while
// duplication's While shape (the same whileStmt; the for_condition
// node has the SAME single-pipeline shape as while_condition —
// verified against node-types.json — so it lowers through the same
// lowerCondition / lowerCondPipeline, the t06/t07 condition subset: a
// bare variable read). Live pwsh 7.6.4: with $c unset the condition
// reads $null (FALSY) and the body NEVER runs — CONSISTENT with the
// A1 getVar read "" (the t06/t07 condition-position null edge) — so
// the executed-stdout oracle matches by construction (both print the
// statement after the loop, never the body).
//
// The OTHER clause combinations REFUSE (refuse > guess): a
// for_initializer / for_iterator is the assignment / `++` /
// comparison machinery — the plan's full row "the C frontend's
// for-lowering (init/cond/update → while)" (PLAN_POWERSHELL_F.md §1)
// lands with the assignment rung; a CONDITIONLESS for (`for (;;)`) is
// an infinite loop — pwsh runs forever while the A1 would need a
// true-literal condition the subset doesn't pin (the t06 `$true`
// divergence precedent). Pinned testdata_refuse/t12_for_init_iter.ps1
// + t12_for_conditionless.ps1.
func lowerForStatement(n *sitter.Node, src []byte) (any, error) {
	var body []any
	var cond any
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "statement_block":
			// the body lowers through the same statement_list path as
			// top-level statements (the t06/t07 bodies)
			b, err := lowerBlock(ch, src)
			if err != nil {
				return nil, err
			}
			body = b
		case "for_condition":
			c, err := lowerCondition(ch, src)
			if err != nil {
				return nil, err
			}
			cond = c
		case "for_initializer", "for_iterator":
			clause := strings.TrimPrefix(ch.Type(), "for_")
			return nil, refuse(ch, src, "for %s (the t12 pin is the condition-only `for (; $c; )` form; the %s clause is the assignment/`++`/comparison machinery of the plan's full for-lowering row — outside the v1 subset)", clause, clause)
		default:
			return nil, refuse(ch, src, "for_statement part %q", ch.Type())
		}
	}
	if cond == nil {
		return nil, refuse(n, src, "conditionless for (`for (;;)` is an infinite loop — pwsh runs forever while the A1 has no pinned true-literal condition; the t06 `$true` divergence precedent: refuse > guess)")
	}
	// `for (; C; ) { B }` ≡ `while (C) { B }` — the A1 While statement
	// (the same shape the t06 do-while duplication emits).
	return whileStmt(cond, body), nil
}

// lowerForEachStatement — the foreach_statement node: `foreach` `(`
// [foreach_parameter] variable `in` pipeline `)` statement_block (the
// `foreach` keyword, the parens and the `in` keyword are anonymous
// alias tokens; the named children are the optional foreach_parameter,
// the loop variable, the `in` pipeline and the body). The t13 rung
// lands the plan's "For over the A1 array" row (PLAN_POWERSHELL_F.md
// §1): `foreach ($x in $list) { B }` lowers to the A1 For statement
// `{var: x, iter: Array [ split [ getVar "list" ] ], body: B}` — the
// CORE's exact shape for bash `for i in $list; do …; done` (verified
// byte-identical against `debashc file --shir`), because the iter
// pipeline has the SAME single-pipeline shape as the t06/t07
// conditions (verified against node-types.json), so the `in` collection
// lowers through the same lowerCondPipeline subset: a bare variable
// read. The split wrapper is the bash word-splitting the A1 For's
// iter semantics implement (the estree renderer emits `[].concat(…)`
// and the runtime splits the item list); WITHOUT it a bare getVar
// would render as `[].concat("")` → ONE empty item → the body would
// run once — a miscompile. Pinned for an UNSET variable: live pwsh
// 7.6.4 reads $null and iterates ZERO times; the A1 side reads "" and
// split("") is the EMPTY list — the same zero iterations, so the
// executed-stdout oracle matches by construction (the body never runs
// on either side and the statement after the loop prints on both; the
// body echo is structural — a wrongly-run body would DIFF). The
// foreach_parameter (`foreach -parallel (…)`) REFUSES: parallel foreach
// is a different execution model (iterations run concurrently — the
// plan's `&`-parallelism machinery, unpinned; refuse > guess).
func lowerForEachStatement(n *sitter.Node, src []byte) (any, error) {
	var varName string
	var iter any
	var body []any
	gotVar, gotIter, gotBody := false, false, false
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "foreach_parameter":
			return nil, refuse(ch, src, "foreach -parallel (the parallel foreach runs the iterations concurrently — a different execution model, outside the v1 subset)")
		case "variable":
			if gotVar {
				return nil, refuse(ch, src, "foreach_statement with multiple loop variables")
			}
			varName = variableName(ch, src)
			gotVar = true
		case "pipeline":
			if gotIter {
				return nil, refuse(ch, src, "foreach_statement with multiple `in` collections")
			}
			// the `in` collection: the SAME single-pipeline subset as the
			// t06/t07 conditions — a bare variable read (lowerCondPipeline
			// shares the shape check, including the `$true` automatic
			// refusal: iterating a truthy automatic would run the body on
			// one side only).
			v, err := lowerCondPipeline(ch, src)
			if err != nil {
				return nil, err
			}
			// the split wrapper — the core's `for i in $list` iter shape
			// (byte-identical; see the function comment).
			iter = arrayExpr([]any{splitCall(v)})
			gotIter = true
		case "statement_block":
			if gotBody {
				return nil, refuse(ch, src, "foreach_statement with multiple bodies")
			}
			b, err := lowerBlock(ch, src)
			if err != nil {
				return nil, err
			}
			body = b
			gotBody = true
		default:
			return nil, refuse(ch, src, "foreach_statement part %q", ch.Type())
		}
	}
	if !gotVar {
		return nil, refuse(n, src, "foreach_statement without a loop variable")
	}
	if !gotIter {
		return nil, refuse(n, src, "foreach_statement without an `in` collection")
	}
	if !gotBody {
		return nil, refuse(n, src, "foreach_statement without a body")
	}
	return forStmt(varName, iter, body), nil
}

// lowerSwitchStatement — the switch_statement node: `switch (cond) {
// clauses }` (the grammar: switch + optional switch_parameters +
// switch_condition + switch_body; the switch_body wraps a
// switch_clauses list of switch_clause children, each a
// switch_clause_condition + statement_block). The plan's switch row
// (PLAN_POWERSHELL_F.md §1) was a refuse-node while the grammar could
// not parse switch clause blocks — "the clib switch lowering is ready
// when the grammar closes the gap" — and the vendored grammar parses
// the full shape (verified against the CST), so the rung lands the
// plan's "clib switch lowering": the A1 Case node, the shape the core
// emits for bash `case` (byte-identical, verified against `debashc
// --shir --raw`).
//
// The t31 subset pins: a discriminant that is a bare variable read (the
// t06/t07 condition shape — an UNSET variable reads $null in pwsh /
// "" from the A1 store, and $null -eq <literal> is False exactly like
// "" failing every non-* case pattern, so the null edge is CONSISTENT)
// or a bare decimal integer (the t24 range-bound / t11 exit-code
// precedent — pwsh matches `switch (2)` by -eq against the literal
// clauses and the A1 `case "2"` pattern text coincides); clause
// conditions that are bare decimal integer_literals or a trailing
// `default` keyword (the _switch_condition_token — case-insensitive,
// verified against live pwsh); and clause bodies through the usual
// lowerBlock path. The executed-stdout oracle matches live pwsh by
// construction: the unset-variable discriminant runs the default clause
// on both sides and the literal discriminant runs its matching clause
// on both sides (a wrongly-matched clause would DIFF).
//
// The divergent edges REFUSE (refuse > guess):
//
//   - switch_parameters (`switch -Regex/-Wildcard/-Case/-Exact …`)
//     change the matching semantics (regex / glob / case-sensitive /
//     exact) — outside the subset; the -File form reaches the
//     condition as switch_filename, also refused;
//   - a `default` clause that is NOT the last clause — pwsh runs a
//     matching later clause and skips the default (ALL matching
//     clauses run, default only when nothing matched — verified: `switch
//     (1) { default { "d" } 1 { "one" } }` prints one) while the A1
//     `*` pattern would match FIRST — the branches DIVERGE;
//   - duplicate clause conditions — pwsh runs EVERY matching clause
//     (no fallthrough suppression) while the A1 case runs the first
//     match only;
//   - a string / bareword clause condition — pwsh matches strings with
//     the CASE-INSENSITIVE `-eq` (and coerces barewords to strings)
//     while the A1 case pattern is case-sensitive; integer clauses
//     coincide (the t31 pin), everything else refuses;
//   - a hex / real / variable discriminant or clause (the t27
//     bare-decimal-integer discipline, the t24 rangeBound precedent).
func lowerSwitchStatement(n *sitter.Node, src []byte) (any, error) {
	var condNode, bodyNode *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "switch_condition":
			if condNode != nil {
				return nil, refuse(n, src, "switch_statement with multiple conditions")
			}
			condNode = ch
		case "switch_body":
			if bodyNode != nil {
				return nil, refuse(n, src, "switch_statement with multiple bodies")
			}
			bodyNode = ch
		case "switch_parameters":
			return nil, refuse(ch, src, "switch parameters (the -regex / -wildcard / -exact / -casesensitive / -parallel flags change the matching semantics — outside the t31 subset)")
		default:
			return nil, refuse(ch, src, "switch_statement part %q", ch.Type())
		}
	}
	if condNode == nil {
		return nil, refuse(n, src, "switch_statement without a condition")
	}
	disc, err := lowerSwitchCondition(condNode, src)
	if err != nil {
		return nil, err
	}
	var clauses []any
	if bodyNode != nil {
		clauses, err = lowerSwitchClauses(bodyNode, src)
		if err != nil {
			return nil, err
		}
	}
	return caseStmt(disc, clauses), nil
}

// lowerSwitchCondition — the switch_condition node: `(` pipeline `)`
// (the -File form reaches the condition as a switch_filename child —
// line-based file matching, refused). The discriminant is a VALUE
// compared by -eq against each clause condition, not a truthiness
// condition, so the t31 subset pins a bare variable read (the t06/t07
// lowerCondVar shape — including the `$true` automatic refusal) or a
// bare decimal integer (the t24 rangeBound shape; the text passes
// through as a Str, the t27 argument precedent — the core emits the
// same Str for a literal `case 2 in` discriminant, verified).
func lowerSwitchCondition(n *sitter.Node, src []byte) (any, error) {
	var pipeline *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "pipeline":
			if pipeline != nil {
				return nil, refuse(n, src, "switch condition with multiple pipelines")
			}
			pipeline = ch
		case "switch_filename":
			return nil, refuse(ch, src, "switch -File: line-based file matching is outside the v1 subset (the t31 switch pins an expression condition)")
		default:
			return nil, refuse(ch, src, "switch condition %q", ch.Type())
		}
	}
	if pipeline == nil {
		return nil, refuse(n, src, "switch condition without a pipeline")
	}
	var chain *sitter.Node
	for i := 0; i < int(pipeline.NamedChildCount()); i++ {
		ch := pipeline.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			if chain != nil {
				return nil, refuse(pipeline, src, "switch condition with multiple chains")
			}
			chain = ch
		case "pipeline_chain_tail":
			return nil, refuse(ch, src, "`|` inside a switch condition")
		default:
			return nil, refuse(ch, src, "switch condition %q", ch.Type())
		}
	}
	if chain == nil {
		return nil, refuse(pipeline, src, "switch condition without a chain")
	}
	if chain.NamedChildCount() != 1 {
		return nil, refuse(chain, src, "switch condition chain with %d children", chain.NamedChildCount())
	}
	op := chain.NamedChild(0)
	if op.Type() != "unary_expression" || op.NamedChildCount() != 1 {
		return nil, refuse(op, src, "switch discriminant %q (the t31 subset pins a bare variable read or a bare decimal integer)", op.Type())
	}
	inner := op.NamedChild(0)
	switch inner.Type() {
	case "variable":
		return lowerCondVar(op, src, "switch discriminant")
	case "integer_literal":
		if _, err := strconv.Atoi(inner.Content(src)); err != nil {
			return nil, refuse(inner, src, "switch discriminant %q (the t31 subset pins a bare decimal integer)", inner.Content(src))
		}
		return strExpr(inner.Content(src), "DoubleQuoted"), nil
	default:
		return nil, refuse(inner, src, "switch discriminant %q (the t31 subset pins a bare variable read or a bare decimal integer)", inner.Type())
	}
}

// lowerSwitchClauses — the switch_body's switch_clauses list, as the
// A1 Case clause array. An empty `switch ($x) { }` body has NO
// switch_clauses child (node-types: required false) — no clause
// matches, no output, the empty clause array.
func lowerSwitchClauses(body *sitter.Node, src []byte) ([]any, error) {
	var sc *sitter.Node
	for i := 0; i < int(body.NamedChildCount()); i++ {
		ch := body.NamedChild(i)
		if ch.Type() == "switch_clauses" {
			if sc != nil {
				return nil, refuse(body, src, "switch body with multiple switch_clauses")
			}
			sc = ch
		} else {
			return nil, refuse(ch, src, "switch body %q", ch.Type())
		}
	}
	if sc == nil {
		return []any{}, nil
	}
	var clauses []any
	seen := map[int]bool{}
	for i := 0; i < int(sc.NamedChildCount()); i++ {
		cl := sc.NamedChild(i)
		if cl.Type() != "switch_clause" {
			return nil, refuse(cl, src, "switch clause %q", cl.Type())
		}
		patterns, clBody, err := lowerSwitchClause(cl, src)
		if err != nil {
			return nil, err
		}
		for _, p := range patterns {
			if p == "*" {
				if i != int(sc.NamedChildCount())-1 {
					return nil, refuse(cl, src, "default clause before a later clause: pwsh runs a matching later clause and SKIPS the default (all matching clauses run, default only when nothing matched) while the A1 `*` pattern would match FIRST — the branches DIVERGE; the t31 subset pins default LAST")
				}
				continue
			}
			v, err := strconv.Atoi(p)
			if err != nil {
				return nil, refuse(cl, src, "switch clause condition %q", p)
			}
			if seen[v] {
				return nil, refuse(cl, src, "duplicate switch clause condition %q: pwsh runs EVERY matching clause (no fallthrough suppression) while the A1 case runs the first match only — the branches DIVERGE; the t31 subset pins distinct clause values", p)
			}
			seen[v] = true
		}
		clauses = append(clauses, map[string]any{"patterns": patterns, "body": clBody})
	}
	return clauses, nil
}

// lowerSwitchClause — one switch_clause: the switch_clause_condition
// (the `default` keyword or a bare decimal integer) + the
// statement_block body (the usual lowerBlock path — the clause bodies
// go through the same statement lowering as every other body, so
// break/continue/exit inside a clause refuse the same way).
func lowerSwitchClause(cl *sitter.Node, src []byte) ([]string, []any, error) {
	var condNode, blockNode *sitter.Node
	for i := 0; i < int(cl.NamedChildCount()); i++ {
		ch := cl.NamedChild(i)
		switch ch.Type() {
		case "switch_clause_condition":
			if condNode != nil {
				return nil, nil, refuse(cl, src, "switch clause with multiple conditions")
			}
			condNode = ch
		case "statement_block":
			if blockNode != nil {
				return nil, nil, refuse(cl, src, "switch clause with multiple bodies")
			}
			blockNode = ch
		default:
			return nil, nil, refuse(ch, src, "switch clause part %q", ch.Type())
		}
	}
	if condNode == nil {
		return nil, nil, refuse(cl, src, "switch clause without a condition")
	}
	patterns, err := lowerSwitchClauseCondition(condNode, src)
	if err != nil {
		return nil, nil, err
	}
	body, err := lowerBlock(blockNode, src)
	if err != nil {
		return nil, nil, err
	}
	return patterns, body, nil
}

// lowerSwitchClauseCondition — one clause condition. The `default`
// keyword is the grammar's ANONYMOUS _switch_condition_token (the node
// has NO named children; the keyword is case-insensitive — `Default`
// parses the same and pwsh treats it as the default clause, verified
// 2026-08-21) → the A1 `*` pattern. A named child must be a bare
// decimal integer_literal → its raw text as the case pattern (pwsh
// matches `$x -eq <int>` and the A1 pattern text coincides). Strings /
// barewords REFUSE: pwsh's `-eq` is CASE-INSENSITIVE for strings (and
// coerces barewords), while the A1 case pattern match is
// case-sensitive — the branches would diverge for any discriminant
// differing in case (refuse > guess; the t27 bare-decimal-integer
// discipline).
func lowerSwitchClauseCondition(n *sitter.Node, src []byte) ([]string, error) {
	if n.NamedChildCount() == 0 {
		if strings.EqualFold(n.Content(src), "default") {
			return []string{"*"}, nil
		}
		return nil, refuse(n, src, "switch clause condition %q: a bareword condition is a pwsh STRING match (the case-insensitive -eq vs the case-sensitive A1 pattern diverges); the t31 subset pins integer clauses and default", n.Content(src))
	}
	if n.NamedChildCount() != 1 {
		return nil, refuse(n, src, "switch clause condition with %d children", n.NamedChildCount())
	}
	op := n.NamedChild(0)
	if op.Type() != "integer_literal" {
		return nil, refuse(op, src, "switch clause condition %q (the t31 subset pins a bare decimal integer clause or default; pwsh string/-eq conditions are case-insensitive and diverge from the case-sensitive A1 pattern)", op.Type())
	}
	if _, err := strconv.Atoi(op.Content(src)); err != nil {
		return nil, refuse(op, src, "switch clause condition %q (the t31 subset pins a bare decimal integer)", op.Content(src))
	}
	return []string{op.Content(src)}, nil
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
// t08 rung (the plan's text-pipe approximation). The grammar's pipeline
// node is a chain of pipeline_chain parts joined by pipeline_chain_tail
// tokens — the pwsh 7.0+ pipeline-chain operators `&&` / `||` (NOTE the
// `|` pipe is NOT this node: `a | b` parses as ONE pipeline_chain whose
// anonymous `|` token, the _pipeline_tail rule, sits between two
// command children — the t26 rung's refusal below). Returns the
// command's A1 statements — normally one, but the t14 argument_list
// echo split can emit several.
func lowerPipeline(n *sitter.Node, src []byte) ([]any, error) {
	var chains []*sitter.Node
	var tails []*sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			chains = append(chains, ch)
		case "pipeline_chain_tail":
			tails = append(tails, ch)
		default:
			return nil, refuse(ch, src, "statement %q (outside the v1 subset)", ch.Type())
		}
	}
	if len(chains) == 0 {
		return nil, refuse(n, src, "pipeline without a chain")
	}
	if len(tails) == 0 {
		// one chain — the plain statement (the t01 command shape and
		// every rung that lands a single-chain form). A chain with TWO
		// command children is the `|` pipe — the anonymous _pipeline_tail
		// token sits INSIDE the chain, NOT as a pipeline_chain_tail (that
		// node is `&&` / `||` only) — REFUSE loudly (the t08 text-pipe
		// rung; the pre-t26 loop returned on the first command and
		// silently DROPPED the pipe's right-hand command — a miscompile).
		return lowerSingleChain(chains[0], src)
	}
	// the t26 rung: the pipeline-chain operators. The subset pins
	// exactly TWO chains and ONE operator; a longer chain nests BinOps
	// and REFUSES (refuse > guess — the nested shape is the next rung).
	if len(chains) != 2 || len(tails) != 1 {
		return nil, refuse(tails[0], src, "pipeline chain with %d chains (the t26 subset pins exactly two chains and one `&&`/`||`)", len(chains))
	}
	return lowerChainOperator(chains[0], tails[0], chains[1], src)
}

// lowerSingleChain — one pipeline_chain as a statement (the shared
// single-chain path: the t01 command and every rung that lands a
// single-chain form). A chain with a SECOND command child is the `|`
// pipe — the grammar's anonymous _pipeline_tail token (`|` + command,
// INSIDE the chain; NOT the pipeline_chain_tail `&&`/`||` node, which
// lives BETWEEN chains) — REFUSE (the t08 text-pipe rung: silently
// emitting only the first command would miscompile the RHS away).
func lowerSingleChain(chain *sitter.Node, src []byte) ([]any, error) {
	var cmd *sitter.Node
	for i := 0; i < int(chain.NamedChildCount()); i++ {
		ch := chain.NamedChild(i)
		switch ch.Type() {
		case "command":
			if cmd != nil {
				return nil, refuse(ch, src, "pipeline `|` (the t08 text-pipe rung)")
			}
			cmd = ch
		default:
			return nil, refuse(ch, src, "expression %q at statement level", ch.Type())
		}
	}
	if cmd == nil {
		return nil, refuse(chain, src, "empty pipeline chain")
	}
	return lowerCommand(cmd, src)
}

// lowerChainOperator — the t26 rung: `c1 && c2` / `c1 || c2` — the
// pipeline_chain_tail node of tree-sitter-powershell (the pwsh 7.0+
// pipeline-chain operators; the `|` pipe is a DIFFERENT node — the
// anonymous _pipeline_tail token inside one chain, lowerSingleChain's
// two-command refusal). Live pwsh 7.6.4 (verified 2026-08-18): the
// right-hand pipeline runs only when the left-hand's success flag is
// set — `Write-Output "a" && Write-Output "b"` prints a then b,
// `Write-Output "c" || Write-Output "d"` prints c only. Lowering:
// the A1 BinOp And/Or — {"type":"BinOp","op":"And"|"Or",
// "lhs":call,"rhs":call} — byte-identical to the core's `echo a &&
// echo b` / `echo c || echo d` emission (verified against `debashc
// --shir --raw`): the A1→ESTree renderer lowers the BinOp to an `if
// (sh2.lastExit === 0)` / `if (sh2.lastExit !== 0)` guard, and within
// the v1 subset every expressible command SUCCEEDS on both sides — the
// transpiled run prints the same output as live pwsh by construction
// (the `&&` tail runs on both, the `||` tail is skipped on both). The
// subset pins the plain command shape on BOTH sides: each operand must
// lower to EXACTLY ONE Expr statement carrying a Call (the t01 shape) —
// a chain whose command emits several statements (the t14/t24 argument
// forms) or a non-Call statement (the t19 Redirect) has no single
// value to chain on, and a longer chain (3+ chains) nests BinOps —
// both REFUSE (refuse > guess; the nested shape is the next rung).
func lowerChainOperator(lhsChain, tail, rhsChain *sitter.Node, src []byte) ([]any, error) {
	lhs, err := chainOperand(lhsChain, src, "left")
	if err != nil {
		return nil, err
	}
	rhs, err := chainOperand(rhsChain, src, "right")
	if err != nil {
		return nil, err
	}
	switch tail.Content(src) {
	case "&&":
		return []any{exprStmt(binOpExpr("And", lhs, rhs))}, nil
	case "||":
		return []any{exprStmt(binOpExpr("Or", lhs, rhs))}, nil
	default:
		return nil, refuse(tail, src, "pipeline chain tail %q (the t26 subset pins `&&` / `||`)", tail.Content(src))
	}
}

// chainOperand — one `&&` / `||` side as the A1 expr of its single
// statement: the t26 subset pins the plain command shape on both sides
// (the chain lowers through lowerSingleChain — the t01 echo surface).
func chainOperand(chain *sitter.Node, src []byte, what string) (any, error) {
	stmts, err := lowerSingleChain(chain, src)
	if err != nil {
		return nil, err
	}
	if len(stmts) != 1 {
		return nil, refuse(chain, src, "%s chain with %d statements (the t26 subset pins one command per chain side)", what, len(stmts))
	}
	st, ok := stmts[0].(map[string]any)
	if !ok || st["type"] != "Expr" {
		return nil, refuse(chain, src, "%s chain %q (the t26 subset pins a plain command)", what, st["type"])
	}
	return st["expr"], nil
}

// lowerCommand — `Write-Output "…"` / `Write-Host "…"` / `echo …` lower
// to the core's exec-echo Call (byte-identical to the core frontend's
// `echo …` lowering). Every other command name REFUSES.
//
// A command element may be an argument_list (`foo(…)` — the t14 rung)
// or a parenthesized null-coalesce (`Write-Output ($x ?? "d")` — the
// t21 rung, intercepted via lowerParenCoalesce): live pwsh 7.6.4
// writes ONE pipeline OBJECT per command argument — the head
// argument is its own object and the parenthesized list's value(s)
// follow as further objects — so the command lowers to one echo
// statement PER OBJECT (the A1 echo joins its own args with spaces on
// one line, which would miscompile the object-per-argument reality).
// The t14 subset pins EXACTLY ONE head argument before the list and
// the list itself must be the `-f` format form; the t21 subset pins
// the parenthesized null-coalesce as the command's ONLY element; the
// t25 subset pins the parenthesized range the same way; the t29
// subset pins the stop-parsing token `--%` as the command's only
// argument-producing element (the token and its verbatim remainder
// are SEPARATE pipeline objects — one echo each, see the element
// loop); anything else REFUSES.
func lowerCommand(n *sitter.Node, src []byte) ([]any, error) {
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
	var stmts []any
	var redirects []any
	seenList := false
	seenParenCoalesce := false
	seenParenRange := false
	seenParenTernary := false
	seenStopParsing := false
	if elems := n.ChildByFieldName("command_elements"); elems != nil {
		for i := 0; i < int(elems.NamedChildCount()); i++ {
			e := elems.NamedChild(i)
			if seenStopParsing {
				// the `--%` token consumes the rest of ITS line — the
				// grammar cannot place a command element after it (the
				// token ends at the newline; the next line starts a new
				// statement). Defensive (refuse > guess).
				return nil, refuse(e, src, "element after a stop-parsing token (the `--%%` token consumes the rest of its line)")
			}
			if e.Type() == "argument_list" {
				// the head argument(s) accumulated so far form ONE
				// pipeline object each — the t14 subset pins a single
				// head argument (the `foo(fmt -f args)` shape); flush it
				// as its own echo before the list's echoes.
				if len(redirects) > 0 {
					return nil, refuse(e, src, "argument_list combined with a redirection (the t19 subset pins merging redirections on a plain command)")
				}
				if len(argExprs) != 1 {
					return nil, refuse(e, src, "argument_list with %d preceding arguments (the t14 subset pins exactly ONE head argument — the `head(fmt -f args)` shape)", len(argExprs))
				}
				stmts = append(stmts, exprStmt(execCall("echo", argExprs)))
				argExprs = nil
				ls, err := lowerArgumentList(e, src)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, ls...)
				seenList = true
				continue
			}
			if seenList {
				// a separator after the parens is whitespace — skip it;
				// any real argument after the argument_list would be
				// another pipeline object — unpinned (refuse > guess).
				if e.Type() == "command_argument_sep" {
					continue
				}
				return nil, refuse(e, src, "argument after an argument_list (the t14 subset pins the `head(fmt -f args)` shape with nothing after the parens)")
			}
			if cs, handled, err := lowerParenCoalesce(e, src); err != nil {
				return nil, err
			} else if handled {
				// the t21 rung: `Write-Output ($x ?? "d")` — the
				// parenthesized coalesce is ONE argument = ONE pipeline
				// object, so the command lowers to the coalesce If ALONE
				// (the t20 machinery, minus the head flush — the A1 If is
				// a statement, not an expression). The t21 subset pins the
				// paren as the command's ONLY element: a further argument
				// would be a SECOND pipeline object (the multi-object
				// shape stays outside the v1 single-object echo mapping;
				// refuse > guess).
				if len(redirects) > 0 {
					return nil, refuse(e, src, "parenthesized null-coalesce combined with a redirection (the t21 subset pins a plain command)")
				}
				if len(argExprs) != 0 {
					return nil, refuse(e, src, "parenthesized null-coalesce with %d preceding argument(s) (the t21 subset pins `Write-Output ($x ?? \"d\")` — the paren is the command's only element)", len(argExprs))
				}
				stmts = append(stmts, cs...)
				seenParenCoalesce = true
				continue
			}
			if rs, handled, err := lowerParenRange(e, src); err != nil {
				return nil, err
			} else if handled {
				// the t25 rung: `Write-Output (1 .. 3)` — the
				// parenthesized RANGE command element: the paren's range
				// elements are SEPARATE pipeline objects (live pwsh
				// 7.6.4: `Write-Output (1 .. 3)` prints 1, 2, 3 on THREE
				// lines), so the command lowers to ONE echo per element
				// (the t24 fold, shared through lowerRange) and the t25
				// subset pins the paren as the command's ONLY element — a
				// further argument would be another pipeline object (the
				// t21 precedent; refuse > guess).
				if len(redirects) > 0 {
					return nil, refuse(e, src, "parenthesized range combined with a redirection (the t25 subset pins a plain command)")
				}
				if len(argExprs) != 0 {
					return nil, refuse(e, src, "parenthesized range with %d preceding argument(s) (the t25 subset pins `Write-Output (1 .. 3)` — the paren is the command's only element)", len(argExprs))
				}
				stmts = append(stmts, rs...)
				seenParenRange = true
				continue
			}
			if seenParenRange {
				if e.Type() == "command_argument_sep" {
					continue
				}
				return nil, refuse(e, src, "element after a parenthesized range (the t25 subset pins the paren as the command's only element; a further argument would be another pipeline object — refuse > guess)")
			}
			if seenParenCoalesce {
				if e.Type() == "command_argument_sep" {
					continue
				}
				return nil, refuse(e, src, "element after a parenthesized null-coalesce (the t21 subset pins the paren as the command's only element; a further argument would be another pipeline object — refuse > guess)")
			}
			if ts, handled, err := lowerParenTernary(e, src); err != nil {
				return nil, err
			} else if handled {
				// the t33 rung: `Write-Output ($x ? "a" : "b")` — the
				// parenthesized ternary is ONE pipeline object, so the
				// command lowers to the ternary If ALONE (the t32
				// machinery, minus the head flush — the A1 If is a
				// statement, not an expression). The t33 subset pins the
				// paren as the command's ONLY element: a further argument
				// would be a SECOND pipeline object (the multi-object
				// shape stays outside the v1 single-object echo mapping;
				// refuse > guess).
				if len(redirects) > 0 {
					return nil, refuse(e, src, "parenthesized ternary combined with a redirection (the t33 subset pins a plain command)")
				}
				if len(argExprs) != 0 {
					return nil, refuse(e, src, "parenthesized ternary with %d preceding argument(s) (the t33 subset pins `Write-Output ($x ? \"a\" : \"b\")` — the paren is the command's only element)", len(argExprs))
				}
				stmts = append(stmts, ts...)
				seenParenTernary = true
				continue
			}
			if seenParenTernary {
				if e.Type() == "command_argument_sep" {
					continue
				}
				return nil, refuse(e, src, "element after a parenthesized ternary (the t33 subset pins the paren as the command's only element; a further argument would be another pipeline object — refuse > guess)")
			}
			if e.Type() == "redirection" {
				// the t19 rung: the pwsh merging redirection (stream N
				// into the SUCCESS stream) applies to the WHOLE command —
				// collect the specs and wrap the command's statements in
				// one A1 Redirect at the end (the core's `echo hi 2>&1`
				// shape).
				spec, err := lowerRedirection(e, src)
				if err != nil {
					return nil, err
				}
				redirects = append(redirects, spec)
				continue
			}
			if e.Type() == "stop_parsing" {
				// the t29 rung: the pwsh stop-parsing token `--%` — the
				// grammar's stop_parsing token (`--%[^\r\n]*`) consumes
				// the REST of its line VERBATIM as ONE command element
				// (the node text is `--%` plus the remainder; the token
				// ends at the newline, so it is always the command's LAST
				// element). Live pwsh 7.6.4 (verified 2026-08-20): with a
				// CMDLET the `--%` token is passed as its OWN pipeline
				// object and the verbatim remainder as ONE more —
				// `Write-Output --% hello world` prints `--%` then
				// `hello world` on TWO lines, and `Write-Output --%
				// $HOME tail` prints `$HOME tail` UNEXPANDED (verbatim is
				// the point of the token — no variable interpolation, no
				// quote processing; leading whitespace after the token is
				// trimmed, interior spacing preserved). Both objects are
				// COMPILE-TIME literal text (the t14 fold precedent), so
				// the element lowers to ONE echo per object (the t14
				// one-object-per-argument rule) — byte-identical to the
				// core's `echo "--%"` + `echo "…"` emissions. The t29
				// subset pins the token as the command's ONLY
				// argument-producing element on the enumeration commands
				// (Write-Output / echo — the t01 whitelist): preceding
				// arguments (`Write-Output "pre" --% tail` — pwsh writes
				// THREE objects, the unpinned head-args shape), a
				// redirection, and `Write-Host --% …` (Write-Host JOINS
				// its objects on one line — the two-object enumeration
				// would miscompile) all REFUSE (refuse > guess).
				if len(redirects) > 0 {
					return nil, refuse(e, src, "stop-parsing token combined with a redirection (the t29 subset pins a plain command)")
				}
				if len(argExprs) != 0 {
					return nil, refuse(e, src, "stop-parsing token with %d preceding argument(s) (the t29 subset pins `Write-Output --%% …` — the token is the command's only element)", len(argExprs))
				}
				if strings.ToLower(name) == "write-host" {
					return nil, refuse(e, src, "Write-Host with the stop-parsing token (Write-Host JOINS its arguments on one line — the t29 two-object enumeration would miscompile; the Write-Output / echo form is the pinned surface)")
				}
				text := e.Content(src)
				rest := strings.TrimLeft(text[3:], " \t")
				stmts = append(stmts, exprStmt(execCall("echo", []any{strExpr("--%", "DoubleQuoted")})))
				stmts = append(stmts, exprStmt(execCall("echo", []any{strExpr(rest, "DoubleQuoted")})))
				seenStopParsing = true
				continue
			}
			a, err := lowerCommandElement(e, src)
			if err != nil {
				return nil, err
			}
			if a != nil {
				argExprs = append(argExprs, a)
			}
		}
	}
	if len(stmts) == 0 {
		// no argument_list — the plain command, ONE echo (byte-identical
		// to the pre-t14 emission).
		stmts = append(stmts, exprStmt(execCall("echo", argExprs)))
	}
	if len(redirects) > 0 {
		// the command's statements (the t14 split may have produced
		// several) run inside ONE A1 Redirect — the pwsh merge applies
		// to the whole command. Byte-identical to the core's
		// `echo hi 2>&1` Redirect emission.
		return []any{redirectStmt(stmts, redirects)}, nil
	}
	return stmts, nil
}

// lowerArgumentList — the argument_list node: `(` argument_expression_list
// `)` attached to a command argument (`foo(…)`). Live pwsh 7.6.4: the
// parens evaluate and the resulting objects are passed as further
// arguments after the head — so each list value is its own echo
// statement (the one-object-per-argument rule of lowerCommand). The
// t14 subset pins the `-f` FORMAT form: the first argument_expression
// must be a format_argument_expression (which consumes the RHS AND
// every following list element as its arguments — the comma-list is
// the format operator's argument array in pwsh; see lowerFormatArg);
// the t24 subset pins the `..` RANGE form (lowerRangeArg — the range
// must be the ONLY list element); the t32 subset pins the `? :`
// TERNARY form (lowerTernary — the ternary must be the ONLY list
// element, the t20 precedent); a plain literal list (`foo("x")`)
// is a different rung and REFUSES.
func lowerArgumentList(n *sitter.Node, src []byte) ([]any, error) {
	list := n.ChildByFieldName("argument_expression_list")
	if list == nil {
		return nil, refuse(n, src, "empty argument_list (the t14 subset pins `head(fmt -f args)`; `foo()` is unpinned)")
	}
	var out []any
	for i := 0; i < int(list.NamedChildCount()); i++ {
		ae := list.NamedChild(i)
		if ae.Type() != "argument_expression" || ae.NamedChildCount() != 1 {
			return nil, refuse(ae, src, "argument_list part %q (the t14 subset pins the `-f` format operator in an argument list)", ae.Type())
		}
		c := ae.NamedChild(0)
		switch c.Type() {
		case "format_argument_expression":
			v, consumed, err := lowerFormatArg(c, list, i, src)
			if err != nil {
				return nil, err
			}
			// the folded format value is ONE pipeline object — its own
			// echo statement (the one-object-per-argument rule).
			out = append(out, exprStmt(execCall("echo", []any{v})))
			i = consumed
		case "null_coalesce_argument_expression":
			// the t20 rung: `head($x ?? "d")` — the coalesce must be the
			// ONLY list element (a following comma-list is the plain
			// argument-list / array rung — refuse > guess).
			if i != int(list.NamedChildCount())-1 {
				return nil, refuse(c, src, "element after a null-coalesce argument (the t20 subset pins `head($x ?? \"d\")` with the coalesce as the only list element; a comma-list is the array rung)")
			}
			cs, err := lowerCoalesce(c, src)
			if err != nil {
				return nil, err
			}
			out = append(out, cs...)
		case "ternary_argument_expression":
			// the t32 rung: `head($x ? "a" : "b")` — the ternary must be
			// the ONLY list element (a following comma-list is the
			// plain argument-list / array rung — the t20 precedent;
			// refuse > guess).
			if i != int(list.NamedChildCount())-1 {
				return nil, refuse(c, src, "element after a ternary argument (the t32 subset pins `head($x ? \"a\" : \"b\")` with the ternary as the only list element; a comma-list is the array rung)")
			}
			ts, err := lowerTernary(c, src)
			if err != nil {
				return nil, err
			}
			out = append(out, ts...)
		case "range_argument_expression":
			// the t24 rung: `head(1..3)` — the `..` range argument must
			// be the ONLY list element (a following comma-list is the
			// plain argument-list / array rung — the t20 precedent;
			// refuse > guess).
			if i != int(list.NamedChildCount())-1 {
				return nil, refuse(c, src, "element after a range argument (the t24 subset pins `head(1..3)` with the range as the only list element; a comma-list is the array rung)")
			}
			rs, err := lowerRange(c, src)
			if err != nil {
				return nil, err
			}
			out = append(out, rs...)
		default:
			return nil, refuse(c, src, "%q in an argument list (the t14 subset pins the `-f` format operator; a plain argument list is unpinned)", c.Type())
		}
	}
	return out, nil
}

// lowerFormatArg — the format_argument_expression node: `LHS -f RHS`
// (unary_expression + the named format_operator + unary_expression) —
// the .NET composite-format operator, `"{0} {1}" -f "a","b"`. The
// grammar parses the comma-list RHS as the format's single RHS
// expression plus FOLLOWING argument_expression elements of the
// enclosing argument_expression_list (verified against the CST), while
// pwsh takes the whole comma-list as the -f argument ARRAY — so the
// lowering collects [RHS] + the following list elements as the format's
// arguments and returns the index past the last consumed element.
//
// The t14 subset pins an ALL-LITERAL format — the format string and
// every argument are literal strings / decimal integers — so the
// formatted result is a COMPILE-TIME constant and the whole `LHS -f
// args` folds to ONE A1 Str (the executed-stdout oracle then matches
// live pwsh by construction). A variable anywhere (`"{0}" -f $x`),
// a non-literal RHS, escaped braces (`{{`), alignment/format
// specifiers (`{0:D2}`) and a placeholder/argument count mismatch all
// REFUSE (the runtime printf-style rung is a later milestone; refuse
// > guess).
func lowerFormatArg(n *sitter.Node, list *sitter.Node, start int, src []byte) (any, int, error) {
	var lhs, rhs *sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		ch := n.Child(i)
		switch ch.Type() {
		case "unary_expression":
			if lhs == nil {
				lhs = ch
			} else if rhs == nil {
				rhs = ch
			} else {
				return nil, start, refuse(ch, src, "format_argument_expression with more than two operands")
			}
		case "format_operator", "-f":
			// the operator itself — structure only
		default:
			return nil, start, refuse(ch, src, "format_argument_expression part %q", ch.Type())
		}
	}
	if lhs == nil || rhs == nil {
		return nil, start, refuse(n, src, "format_argument_expression without a format string or arguments")
	}
	fmtText, err := literalStringText(lhs, src)
	if err != nil {
		return nil, start, err
	}
	// the format's arguments: the RHS plus every following list element
	// (the pwsh comma-array semantics; a nested format / a non-literal
	// following element is unpinned and REFUSES).
	args := []*sitter.Node{rhs}
	i := start + 1
	for ; i < int(list.NamedChildCount()); i++ {
		ae := list.NamedChild(i)
		if ae.Type() != "argument_expression" || ae.NamedChildCount() != 1 {
			return nil, start, refuse(ae, src, "argument_list part after a format operator (the t14 subset pins literal arguments)")
		}
		c := ae.NamedChild(0)
		if c.Type() != "unary_expression" {
			return nil, start, refuse(c, src, "%q after a format operator (the t14 subset pins literal format arguments)", c.Type())
		}
		args = append(args, c)
	}
	vals := make([]string, len(args))
	for j, a := range args {
		v, err := literalArgText(a, src)
		if err != nil {
			return nil, start, err
		}
		vals[j] = v
	}
	folded, err := foldFormat(fmtText, vals)
	if err != nil {
		return nil, start, err
	}
	return strExpr(folded, "DoubleQuoted"), i, nil
}

// lowerFormatExpr — the format_expression node: `LHS -f RHS` in
// EXPRESSION position — the t17 rung, the general-expression twin of
// the t14 format_argument_expression (which the grammar reaches ONLY
// inside an argument_list; the parenthesized `Write-Output ("{0}" -f
// "a")` reaches THIS node). Live pwsh 7.6.4 (verified 2026-08-14):
// the parens evaluate the format to ONE object — `Write-Output
// ("{0}" -f "a")` prints `a`, `Write-Output ("{1} {0}" -f "x","y")`
// prints `y x` — and in the paren the grammar parses the comma-list
// RHS as ONE array_literal_expression (the t14 argument_list split
// does not apply here), so the lowering collects the array's
// unary_expression elements as the format's argument list, the pwsh
// semantics. The t17 subset pins the SAME all-literal fold as t14:
// the format string and every argument are literal strings / decimal
// integers, so `LHS -f args` folds to ONE compile-time A1 Str via the
// shared foldFormat (a variable anywhere, a nested format, a range
// RHS and an alignment/format-specifier or count mismatch REFUSE
// exactly like t14 — the runtime printf-style rung is a later
// milestone; refuse > guess).
func lowerFormatExpr(n *sitter.Node, src []byte) (any, error) {
	var lhs, rhs *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "unary_expression":
			if lhs == nil {
				lhs = ch
			} else if rhs == nil {
				rhs = ch
			} else {
				return nil, refuse(ch, src, "format_expression with more than two operands")
			}
		case "array_literal_expression":
			// the comma-list RHS (`"{0} {1}" -f "a","b"`) — the pwsh
			// argument-array form; can only be the RHS (a format string
			// LHS is always a literal).
			if rhs != nil {
				return nil, refuse(ch, src, "format_expression with more than two operands")
			}
			rhs = ch
		case "format_operator", "-f":
			// the operator itself — structure only
		default:
			return nil, refuse(ch, src, "format_expression part %q (the t17 subset pins `LHS -f args` with literal operands)", ch.Type())
		}
	}
	if lhs == nil || rhs == nil {
		return nil, refuse(n, src, "format_expression without a format string or arguments")
	}
	fmtText, err := literalStringText(lhs, src)
	if err != nil {
		return nil, err
	}
	// the format's arguments: a single RHS or the array_literal_expression
	// comma-list (the pwsh argument-array semantics — the parenthesized
	// twin of lowerFormatArg's [RHS] + following-elements collection).
	var argNodes []*sitter.Node
	switch rhs.Type() {
	case "array_literal_expression":
		for i := 0; i < int(rhs.NamedChildCount()); i++ {
			c := rhs.NamedChild(i)
			if c.Type() != "unary_expression" {
				return nil, refuse(c, src, "format argument %q (the t17 subset pins literal format arguments)", c.Type())
			}
			argNodes = append(argNodes, c)
		}
	case "unary_expression":
		argNodes = append(argNodes, rhs)
	default:
		return nil, refuse(rhs, src, "format RHS %q (the t17 subset pins literal format arguments)", rhs.Type())
	}
	vals := make([]string, len(argNodes))
	for j, a := range argNodes {
		v, err := literalArgText(a, src)
		if err != nil {
			return nil, err
		}
		vals[j] = v
	}
	folded, err := foldFormat(fmtText, vals)
	if err != nil {
		return nil, err
	}
	return strExpr(folded, "DoubleQuoted"), nil
}

// lowerParenCoalesce — a parenthesized null-coalesce COMMAND element:
// `unary_expression` → `parenthesized_expression` whose single
// pipeline_chain is a null_coalesce_expression (`Write-Output ($x ??
// "d")` — the t21 rung, the parenthesized twin of the t20
// null_coalesce_argument_expression; the grammar reaches the
// null_coalesce_expression node exactly in this parenthesized command
// element and in bare statement position, which stays REFUSED — the
// t17 statement-level precedent). The coalesce lowers to the A1 If
// STATEMENT, and the A1 has no conditional-expression node, so the
// command element cannot ride the argument-expression channel — the
// caller (lowerCommand) intercepts the shape and appends the If to
// its statement list. Returns (stmts, true, nil) for the coalesce
// shape, (nil, false, nil) for any other element / parenthesized
// content (the caller falls through to the t17 expression path), and
// refuses a malformed paren.
func lowerParenCoalesce(e *sitter.Node, src []byte) ([]any, bool, error) {
	if e.Type() != "unary_expression" || e.NamedChildCount() != 1 {
		return nil, false, nil
	}
	pe := e.NamedChild(0)
	if pe.Type() != "parenthesized_expression" {
		return nil, false, nil
	}
	chain, err := parenPipelineChain(pe, src)
	if err != nil {
		return nil, false, err
	}
	if chain.NamedChildCount() != 1 || chain.NamedChild(0).Type() != "null_coalesce_expression" {
		return nil, false, nil
	}
	cs, err := lowerCoalesce(chain.NamedChild(0), src)
	if err != nil {
		return nil, false, err
	}
	return cs, true, nil
}

// lowerParenRange — a parenthesized RANGE command element:
// `unary_expression` → `parenthesized_expression` whose single
// pipeline_chain is a range_expression (`Write-Output (1 .. 3)` — the
// t25 rung, the parenthesized twin of the t24 range_argument_expression;
// the grammar reaches the range_expression node in this parenthesized
// command element and in bare statement position, which stays REFUSED —
// the t17 statement-level precedent). In this position the `..` must
// lex as its OWN token, so the range needs the SPACED spelling `1 .. 3`:
// the unspaced `(1..3)` lexes the whole text as ONE command_name token
// and parses as a `command` node instead (verified against the CST —
// the t24 note's unspaced `1..3` claim holds for the argument_list
// position only). The range's elements are SEPARATE pipeline objects
// (live pwsh 7.6.4: `Write-Output (1 .. 3)` prints 1, 2, 3 on THREE
// lines), so the element lowers to ONE echo per element — the t24
// fold (the shared lowerRange), matching the executed-stdout oracle
// by construction. The caller (lowerCommand) intercepts the shape and
// appends the echoes to its statement list. Returns (stmts, true,
// nil) for the range shape, (nil, false, nil) for any other element /
// parenthesized content (the caller falls through to the t17
// expression path), and refuses a malformed paren.
func lowerParenRange(e *sitter.Node, src []byte) ([]any, bool, error) {
	if e.Type() != "unary_expression" || e.NamedChildCount() != 1 {
		return nil, false, nil
	}
	pe := e.NamedChild(0)
	if pe.Type() != "parenthesized_expression" {
		return nil, false, nil
	}
	chain, err := parenPipelineChain(pe, src)
	if err != nil {
		return nil, false, err
	}
	if chain.NamedChildCount() != 1 || chain.NamedChild(0).Type() != "range_expression" {
		return nil, false, nil
	}
	rs, err := lowerRange(chain.NamedChild(0), src)
	if err != nil {
		return nil, false, err
	}
	return rs, true, nil
}

// lowerParenTernary — a parenthesized TERNARY command element:
// `unary_expression` → `parenthesized_expression` whose single
// pipeline_chain is a ternary_expression (`Write-Output ($x ? "a" :
// "b")` — the t33 rung, the parenthesized twin of the t32
// ternary_argument_expression; the grammar reaches the
// ternary_expression node exactly in this parenthesized command
// element and in bare statement position, which stays REFUSED — the
// t17 statement-level precedent). Live pwsh 7.6.4 (verified
// 2026-08-21): the parens evaluate the ternary to ONE object —
// `Write-Output ($x ? "a" : "b")` with $x unset prints `b` — the
// standard v1 single-object echo mapping, so the command lowers to
// the ternary If ALONE (the t32 machinery, shared through
// lowerTernary, minus the head flush; the A1 If is a statement, not
// an expression, so lowerCommand intercepts the element via
// lowerParenTernary). The t33 subset pins the paren as the command's
// ONLY element — a further argument would be a SECOND pipeline
// object (the multi-object shape stays outside the v1 single-object
// echo mapping; refuse > guess). Returns (stmts, true, nil) for the
// ternary shape, (nil, false, nil) for any other element /
// parenthesized content (the caller falls through to the t17
// expression path), and refuses a malformed paren.
func lowerParenTernary(e *sitter.Node, src []byte) ([]any, bool, error) {
	if e.Type() != "unary_expression" || e.NamedChildCount() != 1 {
		return nil, false, nil
	}
	pe := e.NamedChild(0)
	if pe.Type() != "parenthesized_expression" {
		return nil, false, nil
	}
	chain, err := parenPipelineChain(pe, src)
	if err != nil {
		return nil, false, err
	}
	if chain.NamedChildCount() != 1 || chain.NamedChild(0).Type() != "ternary_expression" {
		return nil, false, nil
	}
	ts, err := lowerTernary(chain.NamedChild(0), src)
	if err != nil {
		return nil, false, err
	}
	return ts, true, nil
}

// lowerCoalesce — the `??` null-coalescing operator, shared by the TWO
// grammar nodes that reach it (verified identical child structure
// against the CST — unary_expression, `??`, unary_expression):
//   - null_coalesce_argument_expression (the t20 rung): inside an
//     argument_list, as an argument_expression child (`head($x ?? "d")`);
//   - null_coalesce_expression (the t21 rung): inside a
//     parenthesized_expression command element (`Write-Output ($x ??
//     "d")` — reached via lowerParenCoalesce; the argument-list and
//     parenthesized spellings parse as DIFFERENT nodes, verified
//     against the CST).
//
// Live pwsh 7.6.4: the head argument and the coalesced value are
// SEPARATE pipeline objects — `Write-Output foo($x ?? "d")` with $x
// unset prints `foo` then `d` (the t20 form, verified 2026-08-15) —
// while the paren evaluates the coalesce to ONE object — `Write-Output
// ($x ?? "d")` prints `d`, `Write-Output ($x ?? 7)` prints `7` (the
// t21 form, verified 2026-08-14). The t20/t21 subset pins the t06/t07
// condition shape: the LHS is a bare variable read (UNSET in the
// subset — variables are never assigned in v1) and the RHS is a
// literal string / decimal integer — a NON-null constant — so `??` (a
// NULL check) lowers through the SAME condition semantics as if/while:
// pwsh reads $null (FALSY) and the A1 store reads "" (FALSY), both
// take the default: `LHS ?? RHS` → `if (LHS) { echo LHS } else { echo
// RHS }` — the t07 If shape, ONE pipeline object → the A1 If (the t14
// one-object-per-argument rule; the then-branch echo is dead within
// the subset, where every variable reads falsy). Truthy pwsh
// automatics (`$true`) and a variable RHS DIVERGE and REFUSE (both
// pinned in testdata_refuse/): `$true ?? "d"` prints True in pwsh vs
// the falsy-lowering's d (the t06 `$true` refusal), and `$x ?? $y`
// with both unset coalesces to $null — `Write-Output` of a bare $null
// prints NOTHING vs the A1 echo's blank line (the t02 PRINT edge).
func lowerCoalesce(n *sitter.Node, src []byte) ([]any, error) {
	var lhs, rhs *sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		ch := n.Child(i)
		switch ch.Type() {
		case "unary_expression":
			if lhs == nil {
				lhs = ch
			} else if rhs == nil {
				rhs = ch
			} else {
				return nil, refuse(ch, src, "null-coalesce with more than two operands")
			}
		case "??":
			// the operator itself — structure only
		default:
			return nil, refuse(ch, src, "null-coalesce part %q", ch.Type())
		}
	}
	if lhs == nil || rhs == nil {
		return nil, refuse(n, src, "null-coalesce without both operands")
	}
	cond, err := lowerCondVar(lhs, src, "null-coalesce left operand")
	if err != nil {
		return nil, err
	}
	// the default: a literal string / decimal integer — a NON-null
	// constant (a variable RHS would coalesce to $null for an unset
	// variable → the t02 bare-$null PRINT edge; refuse > guess).
	if rhs.NamedChildCount() != 1 {
		return nil, refuse(rhs, src, "null-coalesce right operand with %d children", rhs.NamedChildCount())
	}
	op := rhs.NamedChild(0)
	var def any
	switch op.Type() {
	case "string_literal":
		def, err = lowerStringLiteral(op, src)
	case "integer_literal":
		def = strExpr(op.Content(src), "DoubleQuoted")
	default:
		return nil, refuse(op, src, "null-coalesce right operand %q (the t20/t21 subset pins a literal string / decimal integer default)", op.Type())
	}
	if err != nil {
		return nil, err
	}
	// the coalesce is ONE pipeline object → the A1 If with an echo per
	// branch; the unset-variable subset takes the default on BOTH
	// oracles, so the executed-stdout gate matches live pwsh by
	// construction (the t07 shape: cond/then/elsifs/else, byte-identical
	// to the core's if emission).
	return []any{ifStmt(cond, []any{exprStmt(execCall("echo", []any{cond}))}, []any{exprStmt(execCall("echo", []any{def}))})}, nil
}

// lowerTernary — the `? :` ternary operator, shared by the TWO grammar
// nodes that reach it (verified identical child structure against the
// CST — unary_expression, `?`, unary_expression, `:`, unary_expression):
//   - ternary_argument_expression (the t32 rung): inside an argument_list,
//     as an argument_expression child (`head($x ? "a" : "b")` — the
//     grammar reaches this node ONLY inside an argument_list);
//   - ternary_expression (the t33 rung): inside a parenthesized_expression
//     command element (`Write-Output ($x ? "a" : "b")` — reached via
//     lowerParenTernary; the argument-list and parenthesized spellings
//     parse as DIFFERENT nodes, the t20/t21 precedent).
//
// A bare `?` in argument position is a command_parameter.
//
// Live pwsh 7.6.4 (verified 2026-08-21): the head argument and the
// ternary value are SEPARATE pipeline objects — `Write-Output
// foo($x ? "a" : "b")` with $x unset prints `foo` then `b` — so the
// command lowers to ONE echo per object (the t14
// one-object-per-argument rule; the head flush is the t14 machinery),
// the ternary itself being ONE object (and in the t33 parenthesized
// position the paren evaluates to that ONE object alone — `Write-Output
// ($x ? "a" : "b")` prints just `b`). The t32/t33 subset pins the
// t06/t07 condition shape — a bare variable read condition (UNSET in
// the subset: variables are never assigned in v1) and a literal
// string / decimal integer on EACH branch (the t20 literal-default
// discipline) — so the ternary lowers through the SAME condition
// semantics as if/while: pwsh reads $null (FALSY) and the A1 store
// reads "" (FALSY), both take the else branch — `C ? A : B` →
// `if (C) { echo A } else { echo B }`, the t07 If shape
// (cond/then/elsifs/else, byte-identical to the core's if emission;
// the then-branch echo is dead within the subset, where every
// variable reads falsy). Truthy pwsh automatics (`$true`) and a
// variable branch DIVERGE and REFUSE (both pinned in
// testdata_refuse/): `$true ? "a" : "b"` prints the then-branch a in
// pwsh vs the falsy-lowering's b (the t06 `$true` refusal), and `$x
// ? "a" : $y` with both unset evaluates to $null — `Write-Output` of
// a bare $null prints NOTHING vs the A1 echo's blank line (the t02
// PRINT edge). A chained `$x ? "a" : $y ? "b" : "c"` parses as a
// NESTED ternary branch and REFUSES on the operand-shape check
// (refuse > guess).
func lowerTernary(n *sitter.Node, src []byte) ([]any, error) {
	var cond, thenN, elseN *sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		ch := n.Child(i)
		switch ch.Type() {
		case "unary_expression":
			if cond == nil {
				cond = ch
			} else if thenN == nil {
				thenN = ch
			} else if elseN == nil {
				elseN = ch
			} else {
				return nil, refuse(ch, src, "ternary with more than three operands")
			}
		case "?", ":":
			// the operator tokens — structure only
		default:
			return nil, refuse(ch, src, "ternary part %q (the t32 subset pins the `cond ? a : b` shape)", ch.Type())
		}
	}
	if cond == nil || thenN == nil || elseN == nil {
		return nil, refuse(n, src, "ternary without all three operands")
	}
	condExpr, err := lowerCondVar(cond, src, "ternary condition")
	if err != nil {
		return nil, err
	}
	thenV, err := ternaryBranch(thenN, src, "then")
	if err != nil {
		return nil, err
	}
	elseV, err := ternaryBranch(elseN, src, "else")
	if err != nil {
		return nil, err
	}
	// the ternary is ONE pipeline object → the A1 If with an echo per
	// branch; the unset-variable subset takes the else branch on BOTH
	// oracles, so the executed-stdout gate matches live pwsh by
	// construction (the t07 shape: cond/then/elsifs/else, byte-identical
	// to the core's if emission).
	return []any{ifStmt(condExpr, []any{exprStmt(execCall("echo", []any{thenV}))}, []any{exprStmt(execCall("echo", []any{elseV}))})}, nil
}

// ternaryBranch — ONE ternary branch as the A1 value: the t32 subset
// pins a literal string / decimal integer on each branch (the t20
// literal-default discipline — a variable branch would evaluate to
// $null for an unset variable → the t02 bare-$null PRINT edge;
// refuse > guess).
func ternaryBranch(n *sitter.Node, src []byte, what string) (any, error) {
	if n.NamedChildCount() != 1 {
		return nil, refuse(n, src, "ternary %s branch with %d children", what, n.NamedChildCount())
	}
	op := n.NamedChild(0)
	var v any
	var err error
	switch op.Type() {
	case "string_literal":
		v, err = lowerStringLiteral(op, src)
	case "integer_literal":
		v = strExpr(op.Content(src), "DoubleQuoted")
	default:
		return nil, refuse(op, src, "ternary %s branch %q (the t32 subset pins a literal string / decimal integer branch)", what, op.Type())
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// lowerRange — the `..` range operator's compile-time fold, shared by
// the TWO grammar nodes that reach it (verified identical child
// structure against the CST — unary_expression, `..`, unary_expression):
//   - range_argument_expression (the t24 rung): inside an argument_list,
//     as an argument_expression child (`head(1..3)` — the unspaced
//     spelling; the grammar reaches this node ONLY inside an
//     argument_list);
//   - range_expression (the t25 rung): inside a parenthesized_expression
//     command element (`Write-Output (1 .. 3)` — reached via
//     lowerParenRange; in THIS position the `..` must lex as its own
//     token, so the range needs the SPACED spelling — the unspaced
//     `(1..3)` lexes as ONE command_name token and parses as a
//     `command` node instead, verified against the CST; the
//     argument-list and parenthesized spellings parse as DIFFERENT
//     nodes, the t20/t21 precedent).
//
// The `..` range operator (the plan's array rung sibling) evaluates to
// an ARRAY — live pwsh 7.6.4 (verified 2026-08-14): `Write-Output
// foo(1..3)` prints `foo`, `1`, `2`, `3` on FOUR lines and
// `Write-Output (1 .. 3)` prints `1`, `2`, `3` on THREE lines — the
// range's elements are enumerated as SEPARATE pipeline objects, so the
// range lowers to ONE echo statement PER ELEMENT (the t14
// one-object-per-argument rule, extended: the range is an ARRAY of
// objects, not one object).
// The t24/t25 subsets pin an ALL-LITERAL range: both bounds are bare
// decimal integers, so the element list is a COMPILE-TIME constant
// (the t14 fold precedent) — ascending `1..3` emits 1 2 3, descending
// `3..1` emits 3 2 1 (both verified against live pwsh) — and each
// element lowers to its own A1 echo Str, so the executed-stdout
// oracle matches live pwsh by construction. A variable / non-decimal
// bound (`$a..3`), a chained range (`1..3..5` — a nested
// range_argument_expression / range_expression LHS), a span beyond the
// fold cap (the A1 would otherwise blow up one echo per element) and,
// in the t24 argument-list form, a following comma-list element all
// REFUSE (the runtime array rung is a later milestone — the A1 Range
// bounded-iterable node is the shape that rung needs; refuse > guess).
func lowerRange(n *sitter.Node, src []byte) ([]any, error) {
	var lhs, rhs *sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		ch := n.Child(i)
		switch ch.Type() {
		case "unary_expression":
			if lhs == nil {
				lhs = ch
			} else if rhs == nil {
				rhs = ch
			} else {
				return nil, refuse(ch, src, "range with more than two operands")
			}
		case "..":
			// the operator itself — structure only
		default:
			return nil, refuse(ch, src, "range part %q (the t24 subset pins `LHS .. RHS` with decimal-integer bounds)", ch.Type())
		}
	}
	if lhs == nil || rhs == nil {
		return nil, refuse(n, src, "range without both bounds")
	}
	start, err := rangeBound(lhs, src)
	if err != nil {
		return nil, err
	}
	end, err := rangeBound(rhs, src)
	if err != nil {
		return nil, err
	}
	span := end - start
	if span < 0 {
		span = -span
	}
	if span > maxRangeSpan {
		return nil, refuse(n, src, "range span %d (the t24 subset pins spans up to %d — one echo per element would blow up the A1; refuse > guess)", span, maxRangeSpan)
	}
	// the range's elements are SEPARATE pipeline objects — one echo
	// statement per element, descending ranges step -1 (verified
	// against live pwsh 7.6.4: `3..1` prints 3, 2, 1).
	step := 1
	if end < start {
		step = -1
	}
	var stmts []any
	for v := start; ; v += step {
		stmts = append(stmts, exprStmt(execCall("echo", []any{strExpr(strconv.Itoa(v), "DoubleQuoted")})))
		if v == end {
			break
		}
	}
	return stmts, nil
}

// rangeBound — one `..` operand: a bare decimal integer (the t11
// `exit 5` precedent — the subset pins decimal integers; a negative
// bound parses as expression_with_unary_operator and a variable as
// `variable`, both REFUSED here).
func rangeBound(n *sitter.Node, src []byte) (int, error) {
	if n.Type() != "unary_expression" || n.NamedChildCount() != 1 {
		return 0, refuse(n, src, "range bound %q (the t24 subset pins a bare decimal integer)", n.Type())
	}
	op := n.NamedChild(0)
	if op.Type() != "integer_literal" {
		return 0, refuse(op, src, "range bound %q (the t24 subset pins a bare decimal integer)", op.Type())
	}
	v, err := strconv.Atoi(op.Content(src))
	if err != nil {
		return 0, refuse(op, src, "range bound %q (the t24 subset pins a bare decimal integer)", op.Content(src))
	}
	return v, nil
}

// literalStringText — a string_literal node whose interior is EXACTLY
// one literal text part (no interpolation): the t14 format string.
func literalStringText(n *sitter.Node, src []byte) (string, error) {
	if n.Type() != "unary_expression" || n.NamedChildCount() != 1 {
		return "", refuse(n, src, "format string %q (the t14 subset pins a literal string)", n.Type())
	}
	op := n.NamedChild(0)
	if op.Type() != "string_literal" {
		return "", refuse(op, src, "format string %q (the t14 subset pins a literal string)", op.Type())
	}
	parts, err := stringLiteralParts(op, src)
	if err != nil {
		return "", err
	}
	if len(parts) != 1 {
		return "", refuse(op, src, "format string with %d parts (the t14 subset pins a literal string — no $var interpolation)", len(parts))
	}
	pm := parts[0].(map[string]any)
	if pm["kind"] != "lit" {
		return "", refuse(op, src, "format string with an interpolated variable (the runtime printf-style rung is a later milestone)")
	}
	return pm["text"].(string), nil
}

// literalArgText — one format argument: a literal string or a bare
// decimal integer (the t14 all-literal subset).
func literalArgText(n *sitter.Node, src []byte) (string, error) {
	if n.Type() != "unary_expression" || n.NamedChildCount() != 1 {
		return "", refuse(n, src, "format argument %q (the t14 subset pins literal strings / integers)", n.Type())
	}
	op := n.NamedChild(0)
	switch op.Type() {
	case "string_literal":
		parts, err := stringLiteralParts(op, src)
		if err != nil {
			return "", err
		}
		if len(parts) != 1 {
			return "", refuse(op, src, "format argument with %d parts (the t14 subset pins literal strings — no $var interpolation)", len(parts))
		}
		pm := parts[0].(map[string]any)
		if pm["kind"] != "lit" {
			return "", refuse(op, src, "format argument with an interpolated variable (the runtime printf-style rung is a later milestone)")
		}
		return pm["text"].(string), nil
	case "integer_literal":
		return op.Content(src), nil
	default:
		return "", refuse(op, src, "format argument %q (the t14 subset pins literal strings / integers)", op.Type())
	}
}

// foldFormat — constant-fold a .NET composite format string with its
// literal arguments: `{N}` placeholders substitute the N-th argument.
// The t14 subset pins BARE `{N}` placeholders with an EXACT
// placeholder/argument match: escaped braces (`{{` / `}}`),
// alignment/format specifiers (`{0,5}` / `{0:D2}`) and out-of-range or
// mismatched counts REFUSE (pwsh would format-error or silently ignore
// extras — both unpinned; refuse > guess).
func foldFormat(fmtText string, vals []string) (string, error) {
	var b strings.Builder
	placeholders := 0
	for i := 0; i < len(fmtText); {
		c := fmtText[i]
		switch {
		case c == '{':
			if i+1 < len(fmtText) && fmtText[i+1] == '{' {
				return "", fmt.Errorf("REFUSE: escaped brace `{{` in a format string (the t14 subset pins bare `{N}` placeholders; refuse > guess)")
			}
			j := i + 1
			for j < len(fmtText) && fmtText[j] >= '0' && fmtText[j] <= '9' {
				j++
			}
			if j == i+1 {
				return "", fmt.Errorf("REFUSE: non-numeric placeholder in a format string near %q (the t14 subset pins bare `{N}` placeholders)", fmtText[i:])
			}
			idx, err := strconv.Atoi(fmtText[i+1 : j])
			if err != nil || idx >= len(vals) {
				return "", fmt.Errorf("REFUSE: placeholder {%d} out of range for %d format arguments (pwsh throws a FormatException there; refuse > guess)", idx, len(vals))
			}
			if j < len(fmtText) && (fmtText[j] == ':' || fmtText[j] == ',') {
				return "", fmt.Errorf("REFUSE: alignment/format specifier %q in a format string (the t14 subset pins bare `{N}` placeholders)", fmtText[i:j+1])
			}
			if j >= len(fmtText) || fmtText[j] != '}' {
				return "", fmt.Errorf("REFUSE: unterminated placeholder in a format string near %q", fmtText[i:])
			}
			b.WriteString(vals[idx])
			i = j + 1
			placeholders++
		case c == '}':
			return "", fmt.Errorf("REFUSE: lone `}` in a format string near %q (the t14 subset pins bare `{N}` placeholders)", fmtText[i:])
		default:
			b.WriteByte(c)
			i++
		}
	}
	if placeholders != len(vals) {
		return "", fmt.Errorf("REFUSE: %d placeholders vs %d format arguments (pwsh format-errors on missing args and silently ignores extras — both unpinned; refuse > guess)", placeholders, len(vals))
	}
	return b.String(), nil
}

// lowerRedirection — the redirection node in command_elements: the
// grammar's `redirection` rule is a CHOICE of the merging operator
// (merging_redirection_operator — `2>&1` / `3>&1` / …) and the file
// form (file_redirection_operator + redirected_file_name — `> file`;
// on the by-design refused ledger, refused-powershell-sh-go.txt). The
// t19 rung lands the MERGING form — the pwsh stream-merge `N>&1`
// (stream N into the SUCCESS stream): the A1 Redirect spec
// `{"fd":N,"mode":"w","target":Str "&1","interpolate":true}`,
// byte-identical to the core's `echo hi N>&1` emission (the "&N"
// target is the fd-dup the ESTree renderer/runtime install as a
// shared-fd duplicate). Live pwsh 7.6.4 (verified 2026-08-14):
// `Write-Output "hi" 2>&1` prints hi, exit 0 — nothing in the v1
// subset writes to streams 2-6, so the merge never changes the
// observable output (the pin guards the EMIT; the oracle guards that
// the redirect installs without breaking the run).
//
// The other operator forms REFUSE (refuse > guess):
//   - `*>&1` — pwsh's all-streams merge has NO numeric fd, and the A1
//     IrRedirect.fd is an int — the contract lacks the "all streams"
//     fd (a core request would be needed to express it);
//   - every `X>&2` form (`1>&2`, `2>&2`, `3>&2`…`6>&2`, `*>&2`) — live
//     pwsh 7.6.4 REJECTS them at parse time ("The 'N>&2' operator is
//     reserved for future use", verified 2026-08-14) while the
//     vendored grammar over-accepts — a real pwsh program can never
//     contain them (the t04 command whitelist precedent).
func lowerRedirection(n *sitter.Node, src []byte) (any, error) {
	if n.NamedChildCount() == 0 {
		return nil, refuse(n, src, "redirection without an operator")
	}
	op := n.NamedChild(0)
	if op.Type() != "merging_redirection_operator" {
		return nil, refuse(op, src, "redirection form %q (the file form is on the by-design refused ledger)", op.Type())
	}
	text := op.Content(src)
	// `N>&1` (N = 2..6 — the grammar's member list): the pwsh
	// stream-merge into the SUCCESS stream.
	if len(text) != 4 || text[1:] != ">&1" || text[0] < '2' || text[0] > '6' {
		if text == "*>&1" {
			return nil, refuse(op, src, "merging operator %q (the A1 fd is a number — the all-streams merge needs a contract extension; refuse > guess)", text)
		}
		return nil, refuse(op, src, "merging operator %q (pwsh 7.6.4 rejects every `X>&2` form at parse time — \"reserved for future use\"; the grammar over-accepts)", text)
	}
	return redirectSpec(int(text[0]-'0'), "&1"), nil
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
	case "expandable_bareword":
		return lowerExpandableBareword(n, src)
	case "concatenated_command_argument":
		return lowerConcatArg(n, src)
	default:
		return nil, refuse(n, src, "argument form %q", n.Type())
	}
}

// lowerExpandableBareword — the expandable_bareword node: a variable
// immediately followed by unquoted literal text with no whitespace
// (`$foo-bar`: the grammar's variable + a generic_token tail; the
// tail's first char cannot be `.` / `$` / `[` / `{` / a quote, so
// `$foo.txt` is member_access and `$foo2` is ONE variable token — this
// node is exactly the bareword-tail twin of the t05 variable-headed
// concatenation). Live pwsh 7.6.4 argument-mode tokenization (verified
// 2026-08-13): the variable expands and the tail is literal —
// `Write-Output $foo-bar` with foo unset prints `-bar` (the argument
// starts with `$`, so `-bar` is NOT parsed as a parameter) and with
// `$foo = "abc"` prints `abc-bar`; the braced spelling `${foo}-bar`
// parses as the SAME node (the t02 brace precedent — braces are pure
// spelling, both name the same getVar slot). The pieces lower exactly
// like the core's adjacent-word folding for bash `echo $foo-bar`
// (verified byte-identical: Interpolate [expr getVar("foo"), lit
// "-bar"]) via the same mergeConcatParts fold as t05.
func lowerExpandableBareword(n *sitter.Node, src []byte) (any, error) {
	var varNode, tail *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "variable":
			varNode = ch
		case "generic_token":
			tail = ch
		default:
			return nil, refuse(ch, src, "expandable_bareword part %q", ch.Type())
		}
	}
	if varNode == nil || tail == nil {
		return nil, refuse(n, src, "expandable_bareword without a variable+tail pair")
	}
	return mergeConcatParts([]any{
		exprPart(getVarCall(variableName(varNode, src))),
		litPart(tail.Content(src)),
	})
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
	case "parenthesized_expression":
		return lowerParenthesized(n, src)
	case "string_literal":
		return lowerStringLiteral(n, src)
	case "variable":
		return getVarCall(variableName(n, src)), nil
	case "integer_literal", "real_literal":
		// `Write-Output 5` / `Write-Output 1.5` — the core's `echo 5` /
		// `echo 1.5` emits a Str. The real_literal spelling passes
		// through raw like the t16 hex form: argument-mode pwsh does
		// NOT evaluate the literal — `Write-Output 1.50` prints the
		// text `1.50` and `Write-Output 1.5e3` prints `1.5e3`
		// (verified against live pwsh 7.6.4), so the raw byte content
		// IS the argument text (the t27 pin).
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

// parenPipelineChain — the single pipeline_chain inside a
// parenthesized_expression: exactly one pipeline, exactly one chain,
// no `|` tails (the shared shape of the t17 format host and the t21
// null-coalesce host).
func parenPipelineChain(n *sitter.Node, src []byte) (*sitter.Node, error) {
	var pl *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		if ch.Type() != "pipeline" {
			return nil, refuse(ch, src, "parenthesized_expression part %q", ch.Type())
		}
		if pl != nil {
			return nil, refuse(ch, src, "parenthesized_expression with multiple pipelines")
		}
		pl = ch
	}
	if pl == nil {
		return nil, refuse(n, src, "parenthesized_expression without a pipeline")
	}
	var chain *sitter.Node
	for i := 0; i < int(pl.NamedChildCount()); i++ {
		ch := pl.NamedChild(i)
		switch ch.Type() {
		case "pipeline_chain":
			if chain != nil {
				return nil, refuse(ch, src, "parenthesized pipeline with multiple chains")
			}
			chain = ch
		case "pipeline_chain_tail":
			return nil, refuse(ch, src, "pipeline `|` in a parenthesized expression (the t08 text-pipe rung)")
		default:
			return nil, refuse(ch, src, "parenthesized pipeline part %q", ch.Type())
		}
	}
	if chain == nil {
		return nil, refuse(n, src, "parenthesized_expression without a pipeline_chain")
	}
	return chain, nil
}

// lowerParenthesized — the parenthesized_expression node: `( expr )` in
// COMMAND-ARGUMENT position. Live pwsh 7.6.4 (verified 2026-08-14):
// the parens evaluate the inner pipeline to ONE object, and the
// argument passes that single object on (`Write-Output ("{0}" -f "a")`
// prints `a`) — the standard v1 single-object echo mapping. The t17
// subset pins the parens ONLY as the format_expression host (the
// grammar reaches the format_expression node exactly here and in a
// bare statement position — both the t14 twin `-f` rung; the
// statement form stays a loud REFUSE): the inner pipeline must be
// exactly ONE pipeline_chain containing ONE format_expression. Any
// other parenthesized content (`Write-Output ("a")` / `($x)` — a
// different expression rung) REFUSES (refuse > guess), matching the
// t14 plain-literal-list refusal.
func lowerParenthesized(n *sitter.Node, src []byte) (any, error) {
	chain, err := parenPipelineChain(n, src)
	if err != nil {
		return nil, err
	}
	if chain.NamedChildCount() != 1 {
		return nil, refuse(n, src, "parenthesized pipeline chain with %d children (the t17/t21/t33 subsets pin a single format_expression / null_coalesce_expression / ternary_expression host)", chain.NamedChildCount())
	}
	op := chain.NamedChild(0)
	switch op.Type() {
	case "format_expression":
		return lowerFormatExpr(op, src)
	case "null_coalesce_expression":
		// the command path intercepts this shape BEFORE the expression
		// lowering (lowerCommand → lowerParenCoalesce — the t21 rung:
		// the coalesce lowers to the If STATEMENT, and the A1 has no
		// conditional-expression node); reaching here means the paren is
		// nested inside another operand (a cast operand, a t14
		// argument-list element, …) — unpinned, refuse > guess.
		return nil, refuse(n, src, "parenthesized null-coalesce in a nested operand position (the t21 subset pins the `Write-Output ($x ?? \"d\")` command element; refuse > guess)")
	case "ternary_expression":
		// the command path intercepts this shape BEFORE the expression
		// lowering (lowerCommand → lowerParenTernary — the t33 rung:
		// the ternary lowers to the If STATEMENT, and the A1 has no
		// conditional-expression node); reaching here means the paren is
		// nested inside another operand (a cast operand, a t14
		// argument-list element, …) — unpinned, refuse > guess.
		return nil, refuse(n, src, "parenthesized ternary in a nested operand position (the t33 subset pins the `Write-Output ($x ? \"a\" : \"b\")` command element; refuse > guess)")
	default:
		return nil, refuse(n, src, "parenthesized expression %q (the t17/t21/t33 subsets pin the `-f` format_expression host, the parenthesized null-coalesce and the parenthesized ternary; other parenthesized expressions are unpinned)", n.Content(src))
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
	case "expandable_here_string_literal":
		return lowerExpandableHereString(inner, src)
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
		case "sub_expression":
			// the `$(…)` subexpression — the t30 rung. The interior
			// text before it is a lit part; the subexpression lowers to
			// the A1 capture Call (see lowerSubExpression).
			if txt := string(src[pos:ch.StartByte()]); txt != "" {
				parts = append(parts, litPart(txt))
			}
			cap, err := lowerSubExpression(ch, src)
			if err != nil {
				return nil, err
			}
			parts = append(parts, exprPart(cap))
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

// lowerExpandableHereString — the @"…"@ body of a string_literal: the
// expandable here-string, the multi-line twin of the t01 double-quoted
// string (the grammar's string_literal admits both bodies). Live pwsh
// 7.6.4 semantics (verified 2026-08-13): the opening @" must sit at
// END of line — the content starts AFTER the first newline following
// it — and the newline(s) immediately before the closing "@ are NOT
// part of the string (the closing delimiter is `(\r?\n)+"@`, so the
// content ends before that final newline run). Variables interpolate
// exactly like a double-quoted string — an UNSET variable reads $null
// and interpolates as EMPTY text (verified: `Write-Output @"a $foo
// b"@` with foo unset prints "a  b"), CONSISTENT with the A1 store's
// "" (the t09 argument-string edge, unlike the bare-$null t02 print
// edge) — so the lowering is the SAME Interpolate fold as
// lowerExpandableStringParts, byte-identical to the core's bash `echo
// "a $foo b\nc"` emission. Backtick escape_character text REFUSES:
// pwsh processes `x escapes inside an expandable here-string
// (backtick-n is a real newline — verified), but the vendored runtime
// does not materialize them as named children, so the byte-span
// reconstruction would emit them as literal text — a silent
// miscompile (the t05 backtick precedent: refuse > guess).
func lowerExpandableHereString(n *sitter.Node, src []byte) (any, error) {
	parts, err := lowerExpandableHereStringParts(n, src)
	if err != nil {
		return nil, err
	}
	return interpExpr(parts), nil
}

// lowerExpandableHereStringParts — the interior of a @"…"@ here-string
// as Interpolate parts (lit text + getVar exprs), reconstructed from
// the byte spans between the named children — the same vendored-runtime
// quirk as lowerExpandableStringParts (the interior text tokens are
// not materialized as children; only the interpolated variables are).
// The only differences from the double-quoted path are the content
// boundaries: the opening newline after @" is skipped and the final
// newline run before "@ is excluded.
func lowerExpandableHereStringParts(n *sitter.Node, src []byte) ([]any, error) {
	// opening: `@"` + optional spaces + the FIRST newline — the content
	// starts after it (a here-string header must end its line).
	pos := n.StartByte() + 2 // skip `@"`
	for pos < n.EndByte() && (src[pos] == ' ' || src[pos] == '\t') {
		pos++
	}
	if pos < n.EndByte() && src[pos] == '\r' {
		pos++
	}
	if pos < n.EndByte() && src[pos] == '\n' {
		pos++
	} else {
		return nil, refuse(n, src, "here-string without a newline after the opening @\"")
	}
	// closing: `(\r?\n)+"@` — the content ends before the final newline
	// run preceding the closing `"@`.
	end := n.EndByte() - 2 // skip the final `"@`
	for end > pos && (src[end-1] == '\n' || src[end-1] == '\r') {
		end--
	}
	// backtick escapes: pwsh processes `x inside an expandable
	// here-string (verified: backtick-n prints a real newline), but the
	// byte-span reconstruction would emit them as literal text — a
	// silent miscompile. Refuse loudly (refuse > guess), the t05
	// precedent.
	if bytes.Contains(src[pos:end], []byte{'`'}) {
		return nil, refuse(n, src, "backtick escape_character inside a here-string (pwsh processes `x; the byte-span reconstruction would emit it literally)")
	}
	var parts []any
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		switch ch.Type() {
		case "variable":
			if txt := string(src[pos:ch.StartByte()]); txt != "" {
				parts = append(parts, litPart(txt))
			}
			parts = append(parts, exprPart(getVarCall(variableName(ch, src))))
			pos = ch.EndByte()
		case "sub_expression":
			// the `$(…)` subexpression — the t30 rung, the here-string
			// twin of the double-quoted case (the t10 fold is shared
			// with lowerExpandableStringParts).
			if txt := string(src[pos:ch.StartByte()]); txt != "" {
				parts = append(parts, litPart(txt))
			}
			cap, err := lowerSubExpression(ch, src)
			if err != nil {
				return nil, err
			}
			parts = append(parts, exprPart(cap))
			pos = ch.EndByte()
		default:
			return nil, refuse(ch, src, "%q inside a here-string", ch.Type())
		}
	}
	if txt := string(src[pos:end]); txt != "" {
		parts = append(parts, litPart(txt))
	}
	return parts, nil
}

// lowerSubExpression — the `$(…)` subexpression (the sub_expression
// node: `$(` + statement_list + `)`), reached ONLY inside an expandable
// string / here-string (the t30 rung; other positions — a bare command
// element, a concatenated-argument piece — stay REFUSED: the t05 pin
// and lowerCommandElement's default). Live pwsh 7.6.4 (verified
// 2026-08-20): the subexpression evaluates its statements and
// interpolates their OUTPUT — `Write-Output "a $(Write-Output b) c"`
// prints `a b c` — EXACTLY the core's bash command-substitution
// semantics, so the lowering is the A1 capture Call: the core emits
// `echo "a $(echo b) c"` as exec echo with an Interpolate part whose
// expr is `{"func":"capture","args":[{"type":"Arrow","body":
// [Expr echo b]}],"purity":"Spawn","type":"Call"}` (verified
// against `debashc --shir --raw`), and the A1→ESTree renderer lowers
// that shape to a runtime capture (the executed-stdout oracle matches
// live pwsh by construction: both print `a b c`). The t30 subset pins
// the body as exactly ONE plain command (the t26 chainOperand
// precedent): a multi-statement body would emit several statements
// whose capture-join semantics are unpinned (refuse > guess), and the
// body statement goes through the usual pipeline → command lowering
// (the t01 echo whitelist).
func lowerSubExpression(n *sitter.Node, src []byte) (any, error) {
	var sl *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ch := n.NamedChild(i)
		if ch.Type() != "statement_list" {
			return nil, refuse(ch, src, "sub_expression part %q", ch.Type())
		}
		sl = ch
	}
	if sl == nil {
		return nil, refuse(n, src, "sub_expression without a statement_list")
	}
	stmts, err := lowerStatementList(sl, src)
	if err != nil {
		return nil, err
	}
	if len(stmts) != 1 {
		return nil, refuse(sl, src, "sub_expression body with %d statements (the t30 subset pins exactly ONE plain command — the multi-statement join semantics are unpinned)", len(stmts))
	}
	st, ok := stmts[0].(map[string]any)
	if !ok || st["type"] != "Expr" {
		return nil, refuse(sl, src, "sub_expression body %q (the t30 subset pins a plain command — the t01 echo whitelist)", st["type"])
	}
	return captureExpr(stmts), nil
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
