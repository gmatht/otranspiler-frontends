# PLAN_POWERSHELL_F — a PowerShell frontend on the bat precedent

**Status: PROPOSAL (no code yet).** This document proposes
`frontends/powershell-sh-go` — a PowerShell (.ps1) source -> A1 shIR JSON
frontend, architected as the sibling of `frontends/bat-sh-go` (Windows
scripting; workspace-side dir, no git worktree, hand-rolled Go lexer/parser,
the recorded-expectations native gate).

Grounding facts (verified 2026-08-10):

- `pwsh` (PowerShell Core) is **NOT installed** on this box — the native
  executed-stdout side uses the bat precedent: `native_limits_bat`-style
  RECORDED expectations (`harness/frontend-stdout.sh` gains a
  `native_limits_powershell` list of `name.ps1|expected-stdout` entries; the
  transpiled run is compared against the record). Installing pwsh later
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
| `[int]x` cast | identity (the C `(int)` precedent) |

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
