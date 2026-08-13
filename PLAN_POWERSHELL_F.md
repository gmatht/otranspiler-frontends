# PLAN_POWERSHELL_F — a PowerShell frontend on the bat precedent

**Status: IN PROGRESS — t12 landed (gate green).** The
proposal is implemented as described; revision history below.

## 0. Revision history

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
| `while ($c) {}` / `do {} while ($c)` | While / the do-while duplication |
| `switch` | refuse-node (the grammar cannot parse switch clause blocks yet — the if/elseif chain covers the dispatch pattern; the clib switch lowering is ready when the grammar closes the gap) |
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
| `-f` format operator `"{0} {1}" -f a, b` | printf-style |
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
- `switch ($v) { 1 { … } default { … } }` — switch clause blocks fail;
  a genuine grammar gap, so `switch` is a refuse-node for v1 (the
  if/elseif chain covers the common dispatch pattern meanwhile).

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
interpolation), `t03_arith.ps1` (+ - * / %, and the string-concat pin),
`t04_if.ps1` (elseif chain), `t05_for.ps1`, `t06_foreach.ps1` (array +
`$a[0]` + `$a.Count`), `t07_fn.ps1` (parens-param function + return + call — the grammar's
param()-block gap is pinned as a refuse case), `t08_pipeline.ps1` (text
pipe), `t09_strings.ps1` (single-quote literal, `` `n `` escapes),
`t10_cmp.ps1` (-eq/-ne/-lt/-and/-or),
`t12_format.ps1` (-f operator), `t13_args.ps1` ($args, $LASTEXITCODE), plus
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
  becomes a loop over the A1 array. The `switch`-clause grammar gap is the
  other watch item (the grammar pushed 2026-08-10 — it may close fast).
- Milestones: (1) scaffold + the hand lexer + echo/var/arith + the recorded
  gate, (2) control flow + functions + foreach/switch, (3) the refusal table
  + worker + fleet registration, (4) the `$_` pipeline rung or pwsh-native
  flip, whichever lands first.
