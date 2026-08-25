# PLAN_POWERSHELL_F — a PowerShell frontend on the bat precedent

**Status: IN PROGRESS — t36 landed (gate green).** The
proposal is implemented as described; revision history below.

## 0. Revision history

- 2026-08-24 (t36_arithmetic, gate green): the additive_expression /
  multiplicative_expression nodes land — the binary arithmetic
  operators in a PARENTHESIZED command argument (`Write-Output (1 +
  2)`; the grammar reaches them exactly there and in bare statement
  position, which stays REFUSED — the t17 statement-level precedent;
  the argument_list forms (additive_ / multiplicative_argument_
  expression) stay REFUSED on the t14 machinery) — the plan's "`+ - *
  / %` (numeric) → the Arith AST" row, the planned t03_arith.ps1
  surface, now landed as t36. Verified against live pwsh 7.6.4: the
  parens evaluate the arithmetic to ONE object (`Write-Output (1 +
  2)` prints 3, `7 - 2` → 5, `2 * 3` → 6, `7 % 3` → 1, `1 + 2 * 3` →
  7, `1 + 2 + 3` → 6), so the argument lowers to ONE echo of the A1
  Arith expression — the core's `echo $((1+2))` emission,
  byte-identical (verified against `debashc --shir --raw`); the
  A1→ESTree renderer lowers + - * to native JS arithmetic and % to
  the bash-semantics helper, which agree with pwsh on integer
  operands, so the executed-stdout oracle matches live pwsh by
  construction. The subset pins ALL-LITERAL arithmetic (every operand
  a bare decimal integer_literal or a nested expression of the same
  shape — the precedence/associativity pins; the ArithAst is a
  compile-time Num/Bin tree with NO variables) and the paren as the
  command's ONLY element (the t21/t25/t33 precedent). The divergent
  edges refuse, pinned `testdata_refuse/t36_*`: `/` (pwsh REAL
  division — `Write-Output (7 / 2)` prints 3.5 — vs the A1's bash
  INTEGER division Math.trunc — the transpiled run prints 3), `\`
  (pwsh integer division — the A1 ArithAst has no such operator),
  the string-concat trap the plan named ("`\"a\" + \"b\"` must not
  become numeric arith"), a variable operand ($null→0 coercion vs
  the store's "" — the concat coercion trap), a unary-operator
  operand (`-1`) and a head argument before the paren (a SECOND
  pipeline object). The ledger's `ts node additive_expression` /
  `multiplicative_expression` entries were removed from
  `frontends/coverage/refused-powershell-sh-go.txt` (exercised now);
  the argument_list twins stay ledgered.

- 2026-08-14 (t35_while_statement, gate green): the while_statement
  node lands — `while ($c) { B }` (the `while` keyword and the parens
  are anonymous alias tokens; the named children are the
  while_condition field and the statement_block body) — the plan's
  "`while ($c) {}` / `do {} while ($c)` → While / the do-while
  duplication" row's PLAIN form. The t06 do-while duplication's
  re-check shape and the t12 condition-only for BOTH emit this node's
  While, so the rung lowers `while ($c) { B }` to the A1 While
  statement directly — the same whileStmt, byte-identical to the
  core's `while` emission. The while_condition node is the SAME node
  type the t06 do_statement's re-check uses (verified against
  node-types.json), so the condition lowers through the shared
  lowerCondition / lowerCondPipeline — the t06/t07 subset: a bare
  variable read. Pinned for an UNSET variable (the condition-position
  null edge is CONSISTENT — pwsh $null FALSY vs A1 "" FALSY): the
  body NEVER runs on either side and the executed-stdout oracle
  matches live pwsh by construction (both print only "after"; the
  body echo is structural — a wrongly-run body would DIFF). Truthy
  pwsh automatics ($true, …) DIVERGE exactly as in the do/for
  conditions and refuse through the same lowerCondVar gate; the t18
  label before a while now lands (pure spelling, dropped) and
  break/continue inside the body stay refused (their own unpinned
  flow-control construct). Gate: 39/39 refusals + ingress + stdout
  match (35/35).

- 2026-08-21 (t33_ternary_expression, gate green): the
  ternary_expression node lands — the `? :` ternary operator in a
  PARENTHESIZED command argument (`Write-Output ($x ? "a" : "b")`;
  the grammar reaches this node ONLY inside a parenthesized_expression
  command element and in bare statement position, which stays REFUSED
  — the t17 statement-level precedent; the t32 file pins the OTHER
  node, ternary_argument_expression, inside an argument_list —
  verified against the CST that the two spellings parse as DIFFERENT
  nodes with IDENTICAL child structure, the t20/t21 twin cadence).
  Verified against live pwsh 7.6.4: the parens evaluate the ternary
  to ONE object (`Write-Output ($x ? "a" : "b")` with $x unset
  prints `b`), so the command lowers to the ternary If ALONE — the
  t32 machinery, shared through lowerTernary, minus the head flush;
  the A1 If is a statement, not an expression, so lowerCommand
  intercepts the element via lowerParenTernary (the t21/t25
  interceptor precedent). The t33 subset pins the SAME
  condition/branch discipline as t32 (the shared lowerCondVar /
  ternaryBranch checks — the t32 refuse pins carry over by
  construction) and the paren as the command's ONLY element (a
  further argument would be a SECOND pipeline object — refuse >
  guess). Zero new A1 surface; the executed-stdout oracle matches
  live pwsh by construction (both print b, d and 9 — the integer
  else branch exercised at runtime).

- 2026-08-21 (t32_ternary, gate green): the ternary_argument_expression
  node lands — the `? :` ternary operator in an argument list
  (`head($x ? "a" : "b")`; the grammar reaches this node ONLY inside
  an argument_list, the argument_expression alternative at the top of
  the precedence chain — the t14/t20 host; the parenthesized
  `Write-Output ($x ? "a" : "b")` is the DIFFERENT ternary_expression
  node — the t33 rung, above — the t20/t21 twin cadence). Verified against
  live pwsh 7.6.4: the head argument and the ternary value are
  SEPARATE pipeline objects (`Write-Output foo($x ? "a" : "b")`
  prints `foo` then `b`), so the command lowers to ONE echo per
  object — the t14 one-object-per-argument rule, the ternary itself
  ONE object — and the ternary lowers through the SAME condition
  semantics as if/while (the t20 coalesce precedent): a bare variable
  read condition (UNSET: pwsh $null FALSY vs A1 "" FALSY) and a
  literal string / decimal integer on each branch → `if (C) { echo A }
  else { echo B }`, the t07 If shape, byte-identical to the core's if
  emission — zero new A1 surface (the executed-stdout oracle matches
  live pwsh by construction; the integer else branch is exercised at
  runtime, the then-branch echo is structural). The divergent edges
  refuse (pinned testdata_refuse/t32_*): `$true` condition (the t06
  `$true` precedent — pwsh takes the then-branch, the falsy-lowering
  the else), variable branch (evaluates to $null → the t02 PRINT
  edge), chained ternary (a NESTED ternary_argument_expression
  branch — the operand-shape check refuses) and a following
  comma-list element (the array rung).

- 2026-08-21 (t31_switch, gate green): the switch_statement node lands —
  the plan's `switch` row, which §1 held as a refuse-node while the
  grammar could not parse switch clause blocks ("the clib switch
  lowering is ready when the grammar closes the gap"). The vendored
  grammar parses the full shape (switch_condition + switch_body /
  switch_clauses / switch_clause / switch_clause_condition; the
  `default` keyword is an anonymous _switch_condition_token,
  case-insensitive), verified against the CST, so the rung lands the
  "clib switch lowering": the A1 Case node, byte-identical to the
  core's `case "$x" in 1) … ;; *) … ;; esac` emission (verified
  against `debashc --shir --raw`). The subset pins a bare-variable /
  bare-decimal-integer discriminant (pwsh $null vs A1 "" is a
  CONSISTENT null edge in the value position — $null -eq <literal> is
  False exactly like "" failing every non-* case pattern) and decimal
  integer clauses + a TRAILING default; the divergent edges refuse:
  non-last default (pwsh runs a matching later clause and skips the
  default, the A1 `*` would match first), duplicate clause values
  (pwsh runs EVERY matching clause), string / bareword clause
  conditions (pwsh -eq is case-insensitive, the A1 pattern match is
  case-sensitive), the -regex/-wildcard/-exact/-casesensitive/
  -parallel switch_parameters and the -File switch_filename form —
  pinned `testdata_refuse/t31_*`. The executed-stdout oracle matches
  live pwsh 7.6.4 by construction (both print "d" then "two").

- 2026-08-20 (t30_sub_expression, gate green): the sub_expression node
  lands — the `$(…)` subexpression inside an expandable string
  (`Write-Output "a $(Write-Output b) c"`; the grammar reaches the
  node as a named child of expandable_string_literal /
  expandable_here_string_literal — the t01 string rung's interior —
  the plan's `"str $x $(expr)"` interpolation row, which the §1 table
  has always mapped to "the A1 Interpolate/template"). Verified
  against live pwsh 7.6.4: the subexpression evaluates its statements
  and interpolates their OUTPUT — `Write-Output "a $(Write-Output b)
  c"` prints `a b c` — EXACTLY the core's bash command-substitution
  semantics, so the lowering is the A1 capture Call, byte-identical
  to the core's `echo "a $(echo b) c"` emission (`func "capture",
  args [Arrow body], purity Spawn` — verified against `debashc --shir
  --raw`); the A1→ESTree renderer lowers that shape to a runtime
  capture and the executed-stdout oracle matches live pwsh by
  construction. The subset pins the body as exactly ONE plain command
  (the t26 chainOperand precedent); a multi-statement body (`"$(a;
  b)"`) REFUSES (its capture-join semantics are unpinned — refuse >
  guess). The here-string twin lowers through the SAME capture shape
  (the t10 fold is shared). The bare `Write-Output $(…)`
  command-element form and a `$(…)` concatenated-argument piece stay
  REFUSED (the t05 machinery).

- 2026-08-20 (t29_stop_parsing, gate green): the stop_parsing token
  lands — the pwsh stop-parsing `--%` (the grammar's stop_parsing
  token `--%[^\r\n]*`, a _command_element of command_elements; the
  node text is `--%` PLUS the rest of its line, consumed VERBATIM —
  the token ends at the newline, so it is always the command's LAST
  element). Verified against live pwsh 7.6.4: with a CMDLET the
  `--%` token is passed as its OWN pipeline object and the verbatim
  remainder as ONE more — `Write-Output --% hello world` prints
  `--%` then `hello world` on TWO lines, and `Write-Output --%
  $HOME tail` prints `$HOME tail` UNEXPANDED (verbatim is the point
  of the token; leading whitespace after the token is trimmed,
  interior spacing preserved). Both objects are COMPILE-TIME literal
  text (the t14 fold precedent), so the element lowers to ONE echo
  per object (the t14 one-object-per-argument rule), byte-identical
  to the core's `echo "--%"` + `echo "…"` emissions; the
  executed-stdout oracle matches live pwsh by construction (the
  `$HOME` line pins the verbatim-ness). The subset pins the token as
  the command's ONLY argument-producing element on the enumeration
  commands (Write-Output / echo): preceding arguments, a redirection
  and `Write-Host --% …` (Write-Host JOINS its objects on one line
  — the two-object enumeration would miscompile) all REFUSE (refuse
  > guess). Zero new A1 surface: the two objects are Str arguments
  of the same exec-echo Call every rung uses.

- 2026-08-14 (t25_range_expression, gate green): the range_expression
  node lands — the `..` range operator in a parenthesized command
  argument (`Write-Output (1 .. 3)`; the grammar reaches this node
  inside a parenthesized_expression — the parenthesized twin of the
  t24 range_argument_expression, which the grammar reaches ONLY inside
  an argument_list; in the paren the `..` must lex as its OWN token,
  so the range needs the SPACED spelling `1 .. 3` — the unspaced
  `(1..3)` lexes the whole text as ONE command_name token and parses
  as a `command` node instead, verified against the CST). Verified
  against live pwsh 7.6.4: the paren evaluates the range to an ARRAY
  whose elements are enumerated as SEPARATE pipeline objects
  (`Write-Output (1 .. 3)` prints `1`, `2`, `3` on three lines), so
  the argument lowers to ONE echo statement PER ELEMENT — the t24
  fold, shared through lowerRange (the t14 one-object-per-argument
  rule, extended). The t25 subset pins the SAME all-literal fold as
  t24: both bounds bare decimal integers, the element list a
  COMPILE-TIME constant — ascending `(1 .. 3)` emits 1 2 3,
  descending `(3 .. 1)` emits 3 2 1, each element its own A1 echo Str
  — the executed-stdout oracle matches live pwsh by construction
  (zero new A1 surface: the A1 Range bounded-iterable node stays for
  the runtime array rung), and the paren is the command's ONLY
  element (the t21 precedent; a head/tail argument refuses). The t24
  refuse edges carry over by construction (the shared lowerRange): a
  variable / non-decimal bound (pinned
  `testdata_refuse/t25_range_expression_var_bound.ps1`), a chained
  range (`(1 .. 3 .. 5)`) and a span beyond the fold cap all REFUSE.
- 2026-08-17 (t24_range, gate green): the range_argument_expression
  node lands — the `..` range operator in an argument list
  (`head(1..3)`; the grammar reaches this node ONLY inside an
  argument_list, the argument_expression alternative at the bottom of
  the precedence chain — the t14/t20 host; the parenthesized
  `Write-Output (1..3)` is the DIFFERENT range_expression node — the
  t25 rung lands it 2026-08-14 (the spaced `1 .. 3` spelling; the
  unspaced `(1..3)` lexes as a command node instead)). Verified
  against live pwsh 7.6.4: the range evaluates to
  an ARRAY whose elements are enumerated as SEPARATE pipeline objects
  (`Write-Output foo(1..3)` prints `foo`, `1`, `2`, `3` on four
  lines), so the argument lowers to ONE echo statement PER ELEMENT —
  the t14 one-object-per-argument rule, extended (the range is an
  array of objects, not one object). The t24 subset pins an
  ALL-LITERAL range — both bounds are bare decimal integers (the t11
  decimal-integer precedent) — so the element list is a COMPILE-TIME
  constant (the t14 fold precedent): ascending `1..3` emits 1 2 3,
  descending `3..1` emits 3 2 1, each element its own A1 echo Str —
  the executed-stdout oracle matches live pwsh by construction
  (zero new A1 surface: the A1 Range bounded-iterable node stays for
  the runtime array rung). The refuse edges REFUSE, pinned
  `testdata_refuse/t24_range_var_bound.ps1`: a variable / non-decimal
  bound, a chained range (`1..3..5`), a following comma-list element
  (the array-literal rung) and a span beyond the fold cap (one echo
  per element would blow up the A1 from a tiny source).

- 2026-08-16 (t22_param_block, gate green): the param_block node
  lands — the script-level `param(...)` parameter declaration (the
  grammar's program rule is `[using/requires] [param_block]
  statement_list`; the node sits BETWEEN the directives and the
  statement_list as a direct child of program; the same node hosts
  the param-block form of function bodies, which refuse on the
  function itself — functions are the function rung). Verified
  against live pwsh 7.6.4: with NO arguments the block leaves every
  parameter $null, EXACTLY like an undeclared variable, so within the
  v1 text-closed subset (empty argv; no script-arguments channel) the
  declaration has NO runtime effect — the lowering is ZERO statements
  (dropped), the t18-label pure-spelling precedent, byte-identical
  to the same program without the param line; parameter reads lower
  through the usual getVar slots (the t09/t10 consistent edge), so
  the executed-stdout oracle matches live pwsh by construction. The
  value-changing forms REFUSE (pinned testdata_refuse/t22_*):
  script_parameter_default (`param($a = "d")` — pwsh binds the
  default, divergent output; the assignment rung lands it) and
  attribute_list (`param([string]$a)` / `[CmdletBinding()]` /
  `[Parameter()]` — the `Param()` advanced-attribute refusal).

- 2026-08-14 (t21_null_coalesce_expression, gate green): the
  null_coalesce_expression node lands — the parenthesized twin of the
  t20 null_coalesce_argument_expression, the SAME `??` operator in a
  parenthesized command argument (`Write-Output ($x ?? "d")`; the
  two spellings parse as DIFFERENT nodes, verified against the CST;
  the bare-statement form stays REFUSED — the t17 statement-level
  precedent). Verified against live pwsh 7.6.4: the parens evaluate
  the coalesce to ONE object (`Write-Output ($x ?? "d")` prints `d`,
  `Write-Output ($x ?? 7)` prints `7` — both with $x unset), so the
  command lowers to the t20 coalesce If ALONE — zero new A1 surface:
  the t20 machinery (lowerCoalesce — the shared lowering, extracted
  when the rung landed) plus the t17 paren-chain extraction
  (parenPipelineChain, shared with the format host). The A1 If is a
  statement and the A1 has no conditional-expression node, so
  lowerCommand intercepts the element via lowerParenCoalesce instead
  of the argument-expression channel; the subset pins the paren as
  the command's ONLY element (a further argument would be a SECOND
  pipeline object — refuse > guess). The t20 refuse edges carry over
  by construction: `$true` LHS (lowerCondVar), variable RHS, chained
  `??`, cast-nested paren. The executed-stdout oracle matches live
  pwsh by construction; emission byte-identical to t20's If shape.

- 2026-08-14 (t19_merging_redirection, gate green): the
  merging_redirection_operator node lands — the pwsh stream-merge
  `N>&1` (stream N into the SUCCESS stream), the command-element
  redirection form (`Write-Output "hi" 2>&1`; the file form
  file_redirection_operator stays on the by-design refused ledger).
  Verified against live pwsh 7.6.4: `Write-Output "hi" 2>&1` prints
  hi, exit 0 — nothing in the v1 subset writes to streams 2-6, so the
  merge never changes the observable output; the executed-stdout
  oracle matches live pwsh (both print "hi") while the EMIT carries
  the pin (the A1 Redirect statement, byte-identical to the core's
  `echo "hi" 2>&1` emission — the "&N" fd-dup target the ESTree
  renderer lowers to `sh2.redirectSync` and the runtime installs as a
  shared-fd duplicate). The other operator forms REFUSE (pinned
  testdata_refuse/t19_*): `*>&1` (the A1 IrRedirect.fd is an int —
  no "all streams" fd in the contract; a core request would be
  needed) and every `X>&2` form (live pwsh 7.6.4 rejects them at
  parse time — "The 'N>&2' operator is reserved for future use" —
  while the vendored grammar over-accepts; refuse > guess).

- 2026-08-14 (t17_format_expression, gate green): the format_expression
  node lands — the `-f` .NET composite-format operator in EXPRESSION
  position, the parenthesized twin of the t14 argument_list form
  (`Write-Output ("{0}" -f "a")`; the grammar reaches this node in a
  parenthesized command argument — the t14 note's "DIFFERENT
  format_expression node" — and in a bare `"{0}" -f "a"` statement,
  which stays REFUSED: statement-level expressions are the command-only
  rung). Verified against live pwsh 7.6.4: the parens evaluate the
  format to ONE object (`Write-Output ("{0}" -f "a")` prints `a`,
  `Write-Output ("{1} {0}" -f "x","y")` prints `y x`) — the standard
  single-object echo mapping, so the argument lowers to ONE echo of the
  folded value (the t14 object-per-argument split does NOT apply: the
  paren is a single argument, not an argument list). The t17 subset
  pins the SAME all-literal constant-fold as t14 through the shared
  fold machinery (literalStringText / literalArgText / foldFormat —
  zero new A1 surface): the format string and every argument are
  literal strings / decimal integers → ONE compile-time A1 Str. In the
  paren the grammar parses the comma-list RHS as ONE
  array_literal_expression (the t14 list-split workaround does not
  apply) — the lowering collects its elements as the format's argument
  array, the pwsh semantics; `{1} {0}` pins reordering. A variable
  anywhere, a nested paren/format, a range RHS, escaped braces,
  alignment/format specifiers, a placeholder/argument count mismatch
  and any NON-format parenthesized expression (`("a")` / `($x)`) all
  REFUSE (the runtime printf-style rung is still the next milestone;
  refuse > guess).

- 2026-08-14 (t13_foreach, gate green): the foreach_statement node
  lands — `foreach ($x in $list) { B }` lowers to the A1 For statement
  `{var: x, iter: Array [ split [ getVar "list" ] ], body: B}` — the
  plan's "For over the A1 array" row, byte-identical to the core's
  `for i in $list; do …; done` emission (verified against `debashc
  file --shir`). The `in` collection is the SAME single-pipeline shape
  as the t06/t07 conditions, so it lowers through the shared
  lowerCondPipeline subset (a bare variable read; the `$true`
  automatic refusal carries over); the split wrapper is the
  word-splitting the A1 For's iter semantics implement (the estree
  renderer emits `[].concat(…)` — a bare getVar would render as
  `[].concat("")` → ONE empty item → the body would run once, a
  miscompile). Pinned for an UNSET variable: pwsh reads $null and
  iterates ZERO times; split("") is the empty list — the
  executed-stdout oracle matches live pwsh by construction (both
  print the statement after the loop, never the body; the body echo
  is structural). The foreach_parameter form (`foreach -parallel
  (…)` — concurrent iterations, the `&`-parallelism machinery)
  REFUSES, pinned testdata_refuse/t13_foreach_parallel.ps1; the
  `invocation_foreach_expression` node (ForEach-Object / the `$_`
  pipeline variable) remains the `$_` rung.

- 2026-08-14 (t12_for_condition, gate green): the for_statement node's
  for_condition field lands — `for (; $c; ) { B }`, the CONDITION-ONLY
  clause combination (the grammar admits ANY subset of the three
  clauses), lowers EXACTLY to the A1 While statement `while ($c) { B }`
  (with empty init/iter clauses a for loop IS a while loop; the same
  whileStmt shape the t06 do-while duplication emits, byte-identical
  to the core's While). The for_condition node has the same
  single-pipeline shape as while_condition, so it lowers through the
  same lowerCondition / lowerCondPipeline — the t06/t07 condition
  subset (a bare variable read, pinned for an UNSET variable: pwsh
  $null FALSY vs A1 "" FALSY, the consistent condition-position null
  edge; the body never runs on either side and the executed-stdout
  oracle matches live pwsh by construction). The other clause
  combinations REFUSE, pinned testdata_refuse/t12_for_init_iter.ps1
  (for_initializer / for_iterator — the assignment/`++`/comparison
  machinery of the plan's full for row "the C frontend's for-lowering
  (init/cond/update → while)", which lands with the assignment rung)
  and t12_for_conditionless.ps1 (`for (;;)` is an infinite loop — pwsh
  truthy vs no pinned A1 true-literal condition; the t06 `$true`
  divergence precedent: refuse > guess).
- 2026-08-13 (t11_exit, gate green): the flow_control_statement node's
  `exit` form lands — `exit 5` lowers to the A1 Exit statement with a
  bare Int code, `exit` bare to Exit with value null (lastExit), the
  bat frontend's `exit /b` precedent (all backends render it; the
  A1→ESTree renderer emits process.exit(Number(5)) — the executed-
  stdout oracle matches live pwsh by construction: both print
  "before" and stop, exit code 5). The other four keyword forms of
  the same node REFUSE, pinned `testdata_refuse/t11-t14`: break /
  continue are the A1 loop signals, and v1's only landed loop (the
  t06 do_statement) duplicates its body OUTSIDE the loop — a
  break/continue there would miscompile (the while/for rungs host
  them); return is the function rung's value channel (functions
  refuse in v1); throw is the exception model (the A1 has no
  exceptions). `exit -1` (expression_with_unary_operator), `exit $x`,
  `exit "5"` refuse (the subset pins a bare decimal integer).
- 2026-08-13 (t08_empty_statement, gate green): the empty_statement
  node lands — a lone `;` where the _statement rule expects a
  statement (this grammar parses EVERY standalone `;` as this node,
  including a trailing `;` after a pipeline). Live pwsh 7.6.4 accepts
  it as a NO-OP (verified: `Write-Output "a"; ; Write-Output "b"`
  prints a then b, exit 0). Lowering: ZERO statements — the node is
  dropped exactly like comments (the `#`-comments row below), so the
  emitted program is byte-identical to the same program without the
  `;` and the executed-stdout oracle matches live pwsh by
  construction; no A1 node is needed (a no-op has no semantics).
- 2026-08-13 (t07_if_else, gate green): the if_statement node with
  its else_clause tail lands — `if ($c) { B } else { E }` lowers to
  the A1 If node (cond/then/elsifs/else, byte-identical to the core's
  `if` emission; the else_clause is optional, a bare if lowers with
  else: []). The condition is the t06 condition subset (a bare
  variable read — lowerCondPipeline is now shared with while/do; the
  `$true` divergence refuses the same way, pinned
  `testdata_refuse/t05_if_true.ps1`). The grammar's elseif_clauses
  field REFUSES until its own rung (the A1 elsifs slot is ready but
  unpinned — `testdata_refuse/t04_if_elseif.ps1`); the while_statement
  node remains a separate uncovered construct.
- 2026-08-13 (t06_do_statement, gate green): the do_statement node
  (`do { … } while (cond)`) lands via the plan's "do-while
  duplication" — `do { B } while (C)` lowers to `B; while (C) { B }`
  (body once + the While re-check, byte-identical to the core's While
  statement shape), because the A1 DoWhile node is Perl-only in the
  ESTree renderer ("Perl-only IR statement reached the ESTree
  renderer") and the duplication is the exact-equivalent renderable
  form. The condition is pinned as a bare variable read of an UNSET
  variable (pwsh $null FALSY vs A1 "" FALSY — the condition-position
  null edge is consistent, unlike the t02 print edge); the truthy-
  automatic class (`$true` …) diverges and REFUSES, the `until`
  keyword form of the same node REFUSES (unpinned — needs the A1
  Not-cond wrap the core uses for bash `until`); both refusal pins
  live in `testdata_refuse/`. The plain `while_statement` node
  (`while ($c) {}`) remains a separate uncovered construct.
- 2026-08-13 (live pwsh oracle + t02/t03 fixes, gate green): pwsh
  7.6.4 is installed and the harness flips powershell to a LIVE native
  oracle (`native_limits_powershell` emptied; direct snap binary
  preferred over the snap-confine wrapper, mirroring go/zig — commit
  3fad6b99). The live oracle exposed two wrong records (both had been
  written to match the frontend's emission, not real pwsh): (1) t02 —
  `Write-Output` of an UNSET var prints NOTHING in pwsh ($null is
  filtered from the pipeline) vs the A1 echo's blank line; the bare
  read is dropped from the example (the null semantic stays outside
  the text-closed subset) and the braced spelling is pinned in the
  exact interpolation context. (2) t03 — `[string]"cast value"` in
  argument position prints `[string]cast value`: argument mode does
  NOT evaluate a leading type literal, so lowerCast lowers the
  argument as an expandable string (lit type text + operand) instead
  of the original identity pin (superseded — see §1 table).
- 2026-08-12 (t01_echo landed): the frontend dir went live — vendored
  wharflab/tree-sitter-powershell (`grammars/`, commit 21b365b) loaded
  via smacker/go-tree-sitter (cgo), the byte-identical A1 emitter
  (emit.go: full 13-field program, sorted keys, no HTML escaping, no
  trailing newline, core purity verdicts), the CST lowering (lower.go),
  and the REFUSE table (refuse.go). `Write-Output`/`Write-Host`/`echo`
  with string args emit the core's exec-echo Call byte-for-byte
  (verified vs `debashc --shir --raw`); `make test` is green (refusal
  pin + ingress acceptance + recorded stdout via
  `native_limits_powershell`). Also fixed in harness/frontend-stdout.sh:
  the `native[0]` unbound-variable bug for the powershell (recorded-
  expectations) case.
  NOTE (vendored-runtime quirk, recorded for the t02 rung): the smacker
  runtime does not materialize the interior text tokens of
  expandable_string_literal as children; lower.go reconstructs the
  Interpolate lit-parts from byte spans between the variable children
  (verified byte-identical for the no-variable case).

Grounding facts (verified 2026-08-10):

- `pwsh` (PowerShell Core 7.6.4) **IS installed** (2026-08-13) — the native
  executed-stdout side uses the bat precedent: `native_limits_bat`-style
  RECORDED expectations (`harness/frontend-stdout.sh` gains a
  `native_limits_powershell` list of `name.ps1|expected-stdout` entries; the
  transpiled run is compared against the record). Live-oracle wiring
  flips the gate to live native, unchanged discipline.
- `frontends/bat-sh-go` is the proven template: `bat.go` hand-rolled
  lexer/parser/emitter, `frontends/shir-emit-go/` shared A1 emitter,
  `make test` = ingress acceptance + executed-stdout (recorded) +
  refusals.
- PowerShell's object pipeline is the one genuinely new semantic: the A1 is
  text/string-typed, so v1 APPROXIMATES the pipeline as text (every
  command emits strings; `a | b` is a bash pipe) — the same approximation
  bat makes, stated explicitly rather than hidden.

## 1. Why PowerShell (and what the A1 mapping must absorb)

PowerShell is the modern Windows scripting surface bat cannot express
(objects, `$vars`, real expressions, functions with `param()`). A
powershell-sh-go closes the Windows pair the way c-sh-go closes the C pair.
The new semantics to map (each a deliberate choice, refuse > guess):

| PowerShell | A1 lowering (v1) |
|---|---|
| `#` comments | skipped |
| `;` empty statement | skipped (a NO-OP — pwsh 7.6.4 accepts a lone
  `;` and it emits ZERO statements, byte-identical to the same
  program without it; the A1 needs no no-op node, t08 pin) |
| `$x = 5` / `$x` (case-insensitive) | `Assign x` / `getVar("x")` |
| `"str $x $(expr)"` interpolation | the A1 Interpolate/template (the shell frontends already do `"$var"`); `` `n `t `` escapes |
| `'literal'` single quotes | literal string (no interpolation) |
| `$null` | `""` (the empty store value — the C frontend's NULL convention) |
| `$true` / `$false` | `1` / `0` |
| `$LASTEXITCODE` | `getVar("?")` — the `$?` channel |
| `$args[0]`, `$args` | positional reads |
| `$env:NAME` | env read |
| `+ - * / %` (numeric), `+` on strings = CONCAT | the Arith AST / a string-concat lowering (concat is the one coercion trap: `"a" + "b"` must not become numeric arith) |
| `-eq -ne -lt -le -gt -ge` | `== != < <= > >=` → the test grammar |
| `-and -or -not` | `&& || !` |
| `+= -=` etc. | compound assigns |
| `if ($c) {} elseif {} else {}` | If / else-if chain |
| `for ($i = 0; $i -lt 3; $i++) {}` | the C frontend's for-lowering (init/cond/update → while) |
| `foreach ($x in $list) {}` | For over the A1 array |
| `while ($c) {}` / `do {} while ($c)` | While / the do-while duplication — both forms landed: the plain `while ($c) { B }` lowers directly to the A1 While (t35 2026-08-14, the same whileStmt the t06 duplication's re-check and the t12 condition-only for emit), the do form via the t06 do-while duplication (`do { B } while (C)` → `B; while (C) { B }`); the condition is the t06/t07 bare-variable-read subset, pinned for an UNSET variable |
| `switch` | the A1 Case node (landed t31 2026-08-21 — the vendored grammar's switch-clause gap closed; the "clib switch lowering" the old refuse-note anticipated: `switch ($v) { 1 { … } default { … } }` → `case "$v" in 1) … ;; *) … ;; esac`, byte-identical to the core; the t31 subset pins a bare-variable / decimal-integer discriminant and decimal-integer clauses + a trailing `default`, the divergent edges (non-last default, duplicate clause values, string / bareword clause conditions, the -Regex/-Wildcard/… flags) refuse) |
| `break` / `continue` | the loop signals |
| `@(1, 2, 3)` array literal, `$a[0]`, `$a.Count` | setArray / arrayIndex / arrayLen |
| `@{ k = v }` hashtable | assoc store (or refuse v1 — bat has no dict precedent; pin one) |
| `function name { param($a, $b) … }` | A1 Function (param binding → positional; `return v` → the value channel) |
| `Write-Output x` | `echo x` |
| `Write-Host x` | `echo x` (the information stream displays by default — the pragmatic stdout mapping) |
| `Get-Content f` / `Set-Content f v` | `cat f` / write |
| `exit N` | the Exit statement (all backends render it) |
| `cmd.exe args` (external) | exec (the allowlist gate) |
| `a | b` pipeline | bash pipe (TEXT — see §1 note) |
| `-f` format operator `"{0} {1}" -f a, b` | printf-style — t14 lands the
  ALL-LITERAL constant-fold: literal format + literal args fold to ONE
  compile-time A1 Str (the argument_list form; the runtime printf
  channel is the next rung) |
| `[int]x` cast (argument position) | literal text — argument mode does NOT evaluate a leading type literal (pwsh 7.6.4: `Write-Output [int]5` prints `[int]5`, `[string]"x"` prints `[string]x`); the argument lowers as an expandable string (lit type text + operand). Identity holds only in EXPRESSION position, unreachable in v1 (assignment refuses) |

### Pinned refusals (testdata_ps1/*_refuse.ps1 — the emit must FAIL)

.NET member access (`$x.Length`, `[type]::Method`, `$x.ToString()`),
classes / `New-Object`, scriptblocks as VALUES (`$f = { … }` — foreach/if
bodies are parsed, not values), the `$_` pipeline variable (so
`ForEach-Object {}` / `Where-Object {}` refuse — the pipeline-variable
machinery is a dedicated rung), `try/catch/finally` (the exception model —
the A1 has no exceptions), splatting `@args`, `Get-*`/`Set-*` cmdlets beyond
the mapped table, providers, `Param()` advanced attributes, `-like
-match -contains` operators, `$PROFILE`-style automatic variables beyond the
mapped ones, `using` / modules, `&` call operator, background jobs.

## 2. Parser choice

**wharflab/tree-sitter-powershell** — the only maintained modern
PowerShell grammar (grammars-v4's is frozen; the CPP_PLAN §1 lesson
applies). Empirically verified 2026-08-11 by building the grammar (node
binding `tree_sitter_pwsh_binding.node`) and parsing the v1 construct
surface: **21/24 parse clean across two probe rounds** — and crucially
ALL the tokenizer-hard surface is clean: interpolated strings with
`$()` subexpressions, here-strings, backtick escapes, single-quoted
literals, `[int]::Parse` .NET interop, `@()` arrays, `@{}` hashtables,
pipelines, `1..3 | ForEach-Object { $_ }` (the `$_` rung is parser-ready
TODAY), cmdlets with named args (`Get-Content -Path f -Encoding utf8`),
if/elseif, for, foreach, comments.

Two REAL gaps, characterized precisely:
- `function name { param($a, $b) … }` — the param-BLOCK form fails to
  parse; the workarounds parse CLEAN: `function name($a, $b) { … }`
  (parens-param) and the `$args[0]` style. v1 supports functions via
  those forms and pins the param()-block form as a refuse-node.
- (The `switch ($v) { 1 { … } default { … } }` clause-block gap this
  section originally reported CLOSED with the 2026-08-10 grammar push
  — the vendored grammar parses the full switch shape and the t31 rung
  landed the "clib switch lowering" (the A1 Case node) 2026-08-21.)

The grammar is young (2★, created 2026-04) but ACTIVELY developed
(pushed 2026-08-10 — the gaps above may close fast); the empirical parse
test beats star-count as the adoption bar. The plan adds a
**parse-corpus gate**: the v1 testdata doubles as the grammar's
regression corpus — every pinned construct must re-parse clean on every
`make test`, so a grammar update that breaks a pinned construct fails
the gate instead of silently miscompiling. The Go frontend loads the
same grammar via cgo (smacker/go-tree-sitter — the binding the cpp plan
validated).

The hand-rolled tokenizer (this plan's first draft) is dropped: the bat
precedent doesn't transfer, because PowerShell's tokenizer — interpolated
strings, here-strings, backticks — is EXACTLY where the grammar is clean
and where a hand lexer breeds bugs. GLR error tolerance gives "parsed
the whole file, refused this node" (CPP_PLAN §1).

## 3. The gate

`make test` = ingress acceptance (`debashc --shir-in-estree` on the emitted
A1) + executed-stdout vs the RECORDED expectations (`native_limits_powershell`
in frontend-stdout.sh; the format is the bat precedent — the record is the
native oracle since pwsh isn't installed). `make test` also enforces the
refusals (each `*_refuse.ps1` must fail the emit).

## 4. Worker / fleet

`run_frontend_worker.sh` (the failure-driven loop) + `setup_backends.sh`
registration (`--pi-fix-frontend powershell-sh-go`). No shared-lowering
channel needed (standalone, like bat — PowerShell has no C-sibling to
borrow from; the A1 emitter is `frontends/shir-emit-go/`, shared and
read-only).

## 5. Testdata plan (t01+)

`t01_echo.ps1` (Write-Output / Write-Host), `t02_var.ps1` (`$x = 5`,
interpolation), `t03_arith.ps1` (+ - * / %, and the string-concat pin)
— landed as `t36_arithmetic.ps1` 2026-08-24 (the A1 Arith node;
ALL-LITERAL `+ - * %` in a parenthesized command argument, the
string-concat / variable-operand / `/` real-division edges pinned in
`testdata_refuse/t36_*`), `t04_if.ps1` (elseif chain), `t05_for.ps1`, `t06_foreach.ps1` (array +
`$a[0]` + `$a.Count`), `t07_fn.ps1` (parens-param function + return + call — the grammar's
param()-block gap is pinned as a refuse case), `t08_pipeline.ps1` (text
pipe), `t09_strings.ps1` (single-quote literal, `` `n `` escapes),
`t10_cmp.ps1` (-eq/-ne/-lt/-and/-or),
`t12_format.ps1` (-f operator — landed as `t14_format.ps1`,
2026-08-13: the all-literal constant-fold, argument_list form;
`t17_format_expression.ps1` lands the parenthesized format_expression
twin 2026-08-14; the runtime printf-style
rung stays on the list), `t13_args.ps1` ($args, $LASTEXITCODE), plus
a `*_refuse` set pinning every refusal-table entry. Every recorded
expectation is pinned by running the transpiled output against the record,
never against a guessed string.

## 6. Risks / milestones

- **The object pipeline is the standing risk.** Text-approximation is
  honest for v1 but silently wrong for anything that relies on objects
  (`(Get-Content f).Count`, `$x | ForEach-Object`). The v1 subset stays
  inside the text-closed world (echo/cat/pipe), and everything object-shaped
  refuses — the same line bat draws.
- **The `$_` rung** (ForEach-Object/Where-Object with the pipeline variable)
  is the first follow-up milestone — and the grammar ALREADY parses it
  (verified 2026-08-11): `$_` lowers to a per-item store var, the pipeline
  becomes a loop over the A1 array. (The `switch`-clause grammar gap that
  used to be the other watch item closed with the 2026-08-10 grammar push
  — the t31 rung landed the switch lowering 2026-08-21.)
- Milestones: (1) scaffold + the hand lexer + echo/var/arith + the recorded
  gate, (2) control flow + functions + foreach/switch, (3) the refusal table
  + worker + fleet registration, (4) the `$_` pipeline rung or pwsh-native
  flip, whichever lands first.
