# powershell-sh-go frontend (dir: frontends/powershell-sh-go)

PowerShell (.ps1) source -> A1 shIR JSON — **the bat sibling**
(workspace-side dir; no git worktree; the object pipeline is a stated
TEXT approximation for v1; see PLAN_POWERSHELL_F.md).

**Status: WORKER-IMPLEMENTED, t01 landed (gate green).** The parser is
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
- Comments, comment-only files (empty Program), and the REFUSE table
  (refuse.go): anything outside the subset errors loudly — including
  .NET member access (`$x.Length`), assignment, `|` pipelines, unknown
  commands, named parameters (all pinned in `testdata_refuse/`).

The v1 subset and the refusal table are PLAN_POWERSHELL_F.md. The next
construct (t04_var: assignment + `$var` interpolation) lands on the
next RED pin; the string-interpolation machinery it needs is already
in place (lower.go reconstructs string parts from byte spans — the
smacker runtime does not materialize the interior text tokens of
expandable_string_literal as children, verified against the core).

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/.
