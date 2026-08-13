# powershell-sh-go frontend (dir: frontends/powershell-sh-go)

PowerShell (.ps1) source -> A1 shIR JSON — **the bat sibling**
(workspace-side dir; no git worktree; the object pipeline is a stated
TEXT approximation for v1; see PLAN_POWERSHELL_F.md).

**Status: WORKER-IMPLEMENTED, t17 landed (gate green).** The parser is
wharflab/tree-sitter-powershell (vendored under
`grammars/tree-sitter-powershell/`, loaded via smacker/go-tree-sitter
cgo) — the plan's choice, empirically verified. The emitter produces A1
shIR JSON **byte-identical to the core frontend** (`debashc --shir
--raw`) for the supported subset: sorted keys, the full 13-field
program shape, no HTML-escaped `<>&`, no trailing newline, and the
core's purity verdicts (echo → Emulable).

Landed surface (each construct lands with a pinned `testdata/` example,
validated by `make test` = refusals + ingress acceptance +
executed-stdout vs LIVE `pwsh` (7.6.4, direct snap binary — the
harness prefers /snap/powershell/current/opt/powershell/pwsh over the
snap-confine wrapper, mirroring go/zig); the old hand-authored
`native_limits_powershell` records were removed 2026-08-13 when pwsh
was installed — the live oracle is the reference, and the first gate
immediately exposed two wrong records (t03 [string] cast prints the
type name; Write-Output of an unset var prints nothing, not an empty
line) — now the worker's fixes, not records):

- t01_echo: `Write-Output "…"` / `Write-Host "…"` / `echo …` → the
  core's exec-echo Call; string args lower exactly like the core's
  double-quoted strings (Interpolate with lit/expr parts, single-quoted
  and barewords → Str DoubleQuoted).
- t02_braced_variable: `${name}` — the braced spelling of a variable
  read; braces are pure spelling, both forms lower to the same
  getVar slot. Pinned in the exact interpolation context ("a ${foo} b"
  → "a  b"): a BARE `Write-Output ${foo}` of an unset var prints
  NOTHING in pwsh ($null is filtered from the pipeline) while the A1
  echo of an empty store value prints a blank line — that null
  semantic stays outside the v1 text-closed subset (refuse > guess).
- t03_cast: `[type] operand` (cast_expression) in COMMAND-ARGUMENT
  position lowers as an expandable string: lit type text (`[string]`)
  + the operand — pwsh argument mode does NOT evaluate a leading type
  literal, so `Write-Output [string]"cast value"` prints
  `[string]cast value` (live pwsh 7.6.4; the original identity pin was
  written against a guessed record and is superseded — identity would
  only hold in expression position, unreachable in v1). The
  unary-operator forms of expression_with_unary_operator (`-not` /
  `!` / `++` / `--`) and object-shaped operands (member access, `@()`
  arrays) still REFUSE.
- t04_invocation_operator: `&` / `.` before a whitelisted command
  name (command_invocation_operator + command_name_expr): the
  operator lowers away (pure invocation spelling, the ${}-brace
  precedent of t02; the emit is byte-identical to the un-prefixed
  form) — verified against live pwsh 7.6.4. `& $cmd` / `. ./file.ps1`
  REFUSE (the command_name_expr text is not in the whitelist).
- t05_concatenated_argument: the concatenated_command_argument node —
  adjacent quoted/bareword/variable pieces with no whitespace
  (`pre"mid"post`, `$x"b"`, `2"a"`). Live pwsh 7.6.4 argument-mode
  tokenization: an UNQUOTED head absorbs every following piece (ONE
  argument), so the pieces merge exactly like the core's
  adjacent-word folding (adjacent lits merge; a variable →
  Interpolate; all-literal → Str — byte-identical to the core's `echo
  pre"mid"post` / `echo $x"b"`). A QUOTED head REFUSES: pwsh
  terminates the argument there and writes one pipeline OBJECT per
  leading quoted string (`Write-Output "a"b` prints "a" then "b"
  on separate lines), and the multi-object output is outside the v1
  single-object echo mapping (the t02 null-edge precedent: refuse >
  guess). `"a""b"` is NOT this node (the doubled quote is an
  escaped quote inside ONE string); backtick escape_character and
  `$(…)` pieces inside a concatenated argument still REFUSE.
- t06_do_statement: the do_statement node — `do { … } while
  (cond)` (do + statement_block + `while` keyword + `(`
  while_condition `)`). Live pwsh 7.6.4: the body runs ONCE, then the
  condition re-checks; the plan's lowering is "the do-while
  duplication" (PLAN_POWERSHELL_F.md §1): `do { B } while (C)` →
  `B; while (C) { B }` — the body once, then the While re-check,
  byte-identical to the core's While statement shape (the A1 DoWhile
  node is Perl-only in the ESTree renderer — "Perl-only IR statement
  reached the ESTree renderer" — so the duplication keeps the
  construct on the renderable node; the equivalence is exact). The
  condition is a bare variable read, pinned for an UNSET variable:
  pwsh reads $null (FALSY) and the A1 store reads "" (FALSY) — the
  condition-position null edge is CONSISTENT (unlike the t02 PRINT
  edge, which stays outside the subset). Truthy pwsh automatics in a
  condition (`$true`, `$PID`, …) DIVERGE (pwsh truthy → infinite
  loop vs A1 "" → falsy) — `$true` REFUSES, the `until` keyword form
  of the same node REFUSES (unpinned; needs the A1 Not-cond wrap),
  both pinned in `testdata_refuse/`.
- t07_if_else: the if_statement node with its else_clause tail — `if
  (cond) { B } else { E }` (the plan's "If / else-if chain" row,
  PLAN_POWERSHELL_F.md §1): the A1 If node, byte-identical to the
  core's `if` emission (cond/then/elsifs/else, sorted keys, no runs
  field). The condition is the t06 condition subset — a bare variable
  read, shared with while/do via lowerCondPipeline (so the `$true`
  divergence refuses the same way, pinned in
  `testdata_refuse/t05_if_true.ps1`); the else_clause is OPTIONAL (a
  bare `if ($c) { B }` lowers with else: []); the grammar's
  elseif_clauses field REFUSES until its own rung (the A1 elsifs
  slot is ready but unpinned — `testdata_refuse/t04_if_elseif.ps1`).
  Pinned for an UNSET variable condition: pwsh $null (FALSY) and A1
  "" (FALSY) both take the else branch — the executed-stdout oracle
  compares the transpiled run against live pwsh (both print "no").
- t08_empty_statement: the empty_statement node — a lone `;` where
  the _statement rule expects a statement (the grammar parses EVERY
  standalone `;` as this node, including a trailing `;` after a
  pipeline). Live pwsh 7.6.4 accepts it as a NO-OP (verified:
  `Write-Output "a"; ; Write-Output "b"` prints a then b, exit 0).
  Lowering: ZERO statements — the node is dropped exactly like
  comments (the plan's `#`-comments row; the A1 has no no-op node and
  needs none), so the emitted program is byte-identical to the same
  program without the `;` and the executed-stdout oracle matches live
  pwsh by construction. The nil return is skipped by lowerStatementList
  and covers block bodies too (lowerBlock shares the path).
- t09_expandable_bareword: the expandable_bareword node — a variable
  immediately followed by unquoted literal text with no whitespace
  (`$foo-bar`: the grammar's variable + a generic_token tail; the
  tail's first char cannot be `.` / `$` / `[` / `{` / a quote, so
  `$foo.txt` is member_access and `$foo2` is ONE variable token —
  this node is exactly the bareword-tail twin of the t05
  variable-headed concatenation `$x"b"`). Live pwsh 7.6.4
  argument-mode tokenization (verified 2026-08-13): the variable
  expands and the tail is literal — with foo UNSET, `Write-Output
  $foo-bar` prints `-bar` (the argument starts with `$`, so `-bar` is
  NOT parsed as a parameter) and with `$foo = "abc"` it prints
  `abc-bar`; the braced spelling `${foo}-bar` parses as the SAME node
  (the t02 brace precedent — braces are pure spelling, both name the
  same getVar slot). The pieces lower exactly like the core's
  adjacent-word folding for bash `echo $foo-bar` (byte-identical:
  Interpolate [expr getVar("foo"), lit "-bar"]) via the same
  mergeConcatParts fold as t05 — the transpiled run prints "-bar" on
  both sides (the A1 store reads "" for foo: the tail makes the
  argument an expandable STRING, not the bare-$null t02 print edge,
  so the unset-variable oracle is CONSISTENT).
- t10_here_string: the expandable here-string (@"…"@ — the grammar's
  expandable_here_string_literal, a string_literal body): the
  multi-line twin of the t01 double-quoted string. Live pwsh 7.6.4
  (verified 2026-08-13): the opening @" must END its line — the
  content starts after the first newline — and the newline(s) right
  before the closing "@ are NOT part of the string (the closing
  delimiter is `(\r?\n)+"@`). Variables interpolate exactly like a
  double-quoted string — an UNSET variable reads $null and
  interpolates as EMPTY text (verified: prints "a  b"), CONSISTENT
  with the A1 store's "" (the t09 argument-string edge, unlike the
  bare-$null t02 print edge) — so the body lowers through the same
  byte-span reconstruction as the t01 strings (Interpolate with
  lit/expr parts, byte-identical to the core's `echo "a $foo b\nc"`
  emission), with the here-string boundary math (the open/close
  newline runs) in place of the quote stripping. Backtick
  escape_character text REFUSES — pinned in
  `testdata_refuse/t06_here_string_backtick.ps1`: pwsh processes `x
  escapes inside @"…"@ (backtick-n is a real newline — verified), but
  the vendored runtime does not materialize them as named children,
  so the byte-span reconstruction would emit them literally — a
  silent miscompile (the t05 backtick precedent: refuse > guess). The
  literal here-string (@'…'@ — verbatim_here_string_characters) is
  the sibling of a later rung, still a loud REFUSE.
- t11_exit: the flow_control_statement node's `exit` form — `exit 5`
  → the A1 Exit statement, `exit` bare → Exit with value null
  (lastExit). Live pwsh 7.6.4: `exit 5` terminates the script with
  status 5, the statements after it never run (verified: "before"
  prints, "after" does not, exit code 5). The value lowers as a bare
  Int code (pipeline → pipeline_chain → unary_expression →
  integer_literal) — the bat frontend's `exit /b N` precedent
  (`{"type":"Exit","value":{"type":"Int","value":5}}`); the
  A1→ESTree renderer emits process.exit(Number(5)), so the
  executed-stdout oracle matches live pwsh by construction (both
  print "before" and stop; the second echo never runs). `exit -1` /
  `exit $x` / `exit "5"` REFUSE (the subset pins a bare decimal
  integer). The other four keyword forms of the same node REFUSE,
  pinned in `testdata_refuse/t11-t14`: break and continue are loop
  signals whose only v1 loop host (the t06 do-while duplication)
  lowers the body OUTSIDE the loop — emitting them would miscompile
  (the while/for rungs host them); return is the function rung's
  value channel (functions refuse in v1); throw is the exception
  model (the A1 has no exceptions).
- t12_for_condition: the for_statement node's for_condition field —
  `for (; $c; ) { B }` — the CONDITION-ONLY clause combination (the
  grammar admits ANY subset of the three clauses). With empty
  init/iter clauses a for loop IS a while loop, so the lowering is
  exactly that: `for (; $c; ) { B }` → the A1 While statement
  `while ($c) { B }`, byte-identical to the t06 do-while
  duplication's While shape (the same whileStmt). The for_condition
  node has the SAME single-pipeline shape as while_condition
  (verified against node-types.json), so it lowers through the same
  lowerCondition / lowerCondPipeline — the t06/t07 condition subset:
  a bare variable read. Pinned for an UNSET variable (pwsh $null
  FALSY vs A1 "" FALSY — the consistent condition-position null
  edge): the body NEVER runs on either side, the statement after the
  loop prints on both (the body echo is structural — a wrongly-run
  body would DIFF). The other clause combinations REFUSE, pinned
  `testdata_refuse/t12_for_init_iter.ps1` (the initializer/iterator
  clauses are the assignment/`++`/comparison machinery of the plan's
  full for-lowering row, PLAN_POWERSHELL_F.md §1 — lands with the
  assignment rung) and `t12_for_conditionless.ps1` (`for (;;)` is an
  infinite loop — pwsh truthy vs no pinned A1 true-literal condition;
  the t06 `$true` divergence precedent: refuse > guess).
- t13_foreach: the foreach_statement node — `foreach ($x in $list) {
  B }` → the A1 For statement (the plan's "For over the A1 array"
  row): `{var: x, iter: Array [ split [ getVar "list" ] ], body: B}`,
  byte-identical to the core's `for i in $list; do …; done` emission
  (the iter pipeline has the SAME single-pipeline shape as the t06/t07
  conditions, so the `in` collection lowers through the same
  lowerCondPipeline subset: a bare variable read; the split wrapper is
  the word-splitting the A1 For's iter semantics implement — WITHOUT
  it a bare getVar would render as `[].concat("")` → one empty item,
  a miscompile). Pinned for an UNSET variable: pwsh iterates $null
  ZERO times and split("") is the empty list — the executed-stdout
  oracle matches live pwsh by construction (the body echo is
  structural, a wrongly-run body would DIFF; the interpolation is the
  t09/t10 consistent edge). The foreach_parameter form (`foreach
  -parallel (…)`) REFUSES — concurrent iterations, a different
  execution model — pinned `testdata_refuse/t13_foreach_parallel.ps1`.
  (`invocation_foreach_expression` — `ForEach-Object`/the method form
  — is the `$_` pipeline-variable machinery, a separate rung.)
- t14_format: the format_argument_expression node — the `-f` .NET
  composite-format operator inside an argument_list
  (`foo("{0} {1}" -f "a","b")`; the grammar reaches this node ONLY
  there — a parenthesized `Write-Output ("{0}" -f "a")` is the
  DIFFERENT format_expression node, landed as t17, and a bare `-f` in
  argument position is a command_parameter, still refused). Live pwsh
  7.6.4 (verified 2026-08-13): the head argument and the
  parenthesized value(s) are SEPARATE pipeline objects (`Write-Output
  foo("{0} {1}" -f "a","b")` prints `foo` then `a b` on two lines),
  so the command lowers to ONE echo statement PER OBJECT — the A1
  echo joins its own args with spaces on one line, which would
  miscompile the object-per-argument reality (the t05 quoted-head
  refusal stays: that multi-object shape is a tokenizer artifact,
  this one is the explicit parens). The t14 subset pins an
  ALL-LITERAL format: the format string and every argument are
  literal strings / decimal integers, so `LHS -f args` folds to ONE
  compile-time A1 Str — the grammar parses the comma-list RHS as the
  format's RHS plus following argument_expression elements of the
  enclosing list, while pwsh takes the whole comma-list as the -f
  argument ARRAY, so the lowering collects [RHS] + the following
  elements (the pwsh semantics); bare `{N}` placeholders substitute
  the N-th argument, `{1} {0}` pins reordering. A variable anywhere
  (`"{0}" -f $x` — pinned `testdata_refuse/t14_format_var.ps1`; the
  runtime printf-style rung is a later milestone), escaped braces,
  alignment/format specifiers (`{0:D2}`), a placeholder/argument
  count mismatch, a plain literal argument list (`foo("x")`), an
  empty `foo()` and an argument after the parens all REFUSE (refuse >
  guess).
- t17_format_expression: the format_expression node — the SAME `-f`
  .NET composite-format operator in EXPRESSION position, the
  parenthesized twin of t14 (`Write-Output ("{0}" -f "a")`; the
  grammar reaches this node in a parenthesized command argument — the
  t14 note's "DIFFERENT format_expression node" — and in a bare
  `"{0}" -f "a"` statement, which stays REFUSED: statement-level
  expressions are the command-only rung). Live pwsh 7.6.4 (verified
  2026-08-14): the parens evaluate the format to ONE object —
  `Write-Output ("{0}" -f "a")` prints `a`, `Write-Output ("{1} {0}" -f
  "x","y")` prints `y x` — the standard v1 single-object echo
  mapping, so the argument lowers to ONE echo of the folded value
  (the t14 object-per-argument split does NOT apply: the paren is a
  single argument, not an argument list). The t17 subset pins the
  SAME all-literal fold as t14 via the shared fold machinery: the
  format string and every argument are literal strings / decimal
  integers → ONE compile-time A1 Str. In the paren the grammar parses
  the comma-list RHS as ONE array_literal_expression (the t14
  list-split workaround does not apply) — the lowering collects its
  elements as the format's argument array, the pwsh semantics; `{N}`
  placeholders substitute the N-th argument, `{1} {0}` pins
  reordering. A variable anywhere (`"{0}" -f $x`), a nested
  paren/format, escaped braces, alignment/format specifiers
  (`{0:D2}`), a placeholder/argument count mismatch and any NON-format
  parenthesized expression (`("a")` / `($x)`) all REFUSE (the runtime
  printf-style rung is a later milestone; refuse > guess).
- Comments, comment-only files (empty Program), and the REFUSE table
  (refuse.go): anything outside the subset errors loudly — including
  .NET member access (`$x.Length`), assignment, `|` pipelines, unknown
  commands, named parameters (all pinned in `testdata_refuse/`).

The v1 subset and the refusal table are PLAN_POWERSHELL_F.md. The next
construct (assignment + `$var` interpolation, the elseif chain, the
runtime printf-style `-f` rung) lands on the next RED pin; the
string-interpolation machinery it needs is already in place (lower.go
reconstructs string parts from byte spans — the smacker runtime does
not materialize the interior text tokens of expandable_string_literal
as children, verified against the core).

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/.
