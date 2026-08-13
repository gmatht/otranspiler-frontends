# powershell-sh-go frontend (dir: frontends/powershell-sh-go)

PowerShell (.ps1) source -> A1 shIR JSON — **the bat sibling**
(workspace-side dir; no git worktree; the object pipeline is a stated
TEXT approximation for v1; see PLAN_POWERSHELL_F.md).

**Status: WORKER-IMPLEMENTED, t07 landed (gate green).** The parser is
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
- Comments, comment-only files (empty Program), and the REFUSE table
  (refuse.go): anything outside the subset errors loudly — including
  .NET member access (`$x.Length`), assignment, `|` pipelines, unknown
  commands, named parameters (all pinned in `testdata_refuse/`).

The v1 subset and the refusal table are PLAN_POWERSHELL_F.md. The next
construct (assignment + `$var` interpolation, the elseif chain) lands
on the next RED pin; the string-interpolation machinery it needs is
already in place (lower.go reconstructs string parts from byte spans —
the smacker runtime does not materialize the interior text tokens of
expandable_string_literal as children, verified against the core).

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/.
