# bat-sh-go frontend (dir: frontends/bat-sh-go)

Windows batch (.bat) source -> A1 shIR JSON. Workspace-side dir; no git
worktree. The "scope" is this dir + harness/* (shared test infra).

Yours (in THIS dir): the batch lexer/parser/emitter (`bat.go`), the CLI
(`cmd/bat-sh-go`), testdata, and this doc.

Shared (do NOT fork): `frontends/shir-emit-go/` (the A1 JSON emitter),
`harness/frontend-stdout.sh` (the executed-stdout gate), the core
(`sh2perl/src/shir.rs, ir.rs, estree.rs, parser/` — estree worker's).

## v1 subset (REFUSE > GUESS)

- `@echo off`, `rem` / `::` comments, `:label` / `goto label`
- `echo [text]`, bare `echo`, `echo.` / `echo:` (blank line), `echo off/on`
  (echo-control directive — no-op in the transpiled output)
- `set VAR=value`, `set "VAR=value"`, `set VAR=` (empty)
- `set /a VAR=expr` — integers, `+ - * / %`, parens, `%var%` operands
- `%var%` expansion (case-insensitive), `%%` literal, `%1`..`%9`, `%*`,
  `%errorlevel%` -> `$?` (getVar("?"))
- `if [not] A==B (cmd) [else (cmd)]` — else only with the parenthesized
  form (real cmd requires the else on the closing-paren line); multi-line
  `( ... )` blocks supported
- `for %%v in (word list) do cmd|(block)` — `%%v` in the body is the
  loop-var read
- `cmd1 & cmd2` statement separators

Deliberately NOT in v1 (refuse loud, documented as worker items):
- `exit /b` — the ESTree renderer's stmt lowering has `unreachable!` for
  IrStmt::Exit (core gap; queue a core-request)
- `call`, `setlocal`/`endlocal`, `shift`, `pause`, `start`, `pushd`/`popd`,
  `set /p`, `if defined/exist/errorlevel`, `for /l /f /d /r`, `^` line
  continuation, `|` pipes, delayed expansion `!var!`, `%cmdline%`

## Semantics notes

- Batch is case-insensitive; all var names and labels are lowercased in
  the ShIR.
- `%var%` -> shell-flavored getVar reads; `if A==B` conds are adapted to
  the A1 test-call text (`%v%` -> `$v`, quoted sides kept).
- Batch `set` values are strings; `%x%` in a value becomes an Interpolate.
- Positionals `%1`..`%9` have NO closing `%` (batch form).

## Gate

`make test` — for every testdata file: (1) the emitted A1 must be accepted
by the core's deserializer (`debashc --shir-in-estree`); (2) the executed
stdout (A1 -> ESTree -> node) must match the RECORDED expectation in
`harness/frontend-stdout.sh` (`native_limits_bat` — cmd.exe is
Windows-only, so the native side is recorded, not executed).

## Otranspiler

`.bat` source extension is wired in `otranspiler/main.go` (sources map +
langOf); `otranspiler file.bat out.js --run` executes via the estree
runner.
