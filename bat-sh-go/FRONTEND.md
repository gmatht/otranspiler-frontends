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
- `if [not] defined VAR` / `if [not] exist FILE` / `if [not] errorlevel N`
  (v1.1) — mapped to `[[ -n $var ]]` (the estree test tokenizer expands
  `$var` but treats `${...}` as literal — `-n` is the mappable form;
  an empty-but-set var counts as NOT defined, a batch difference) /
  `[[ -e FILE ]]` / `[[ $? -ge N ]]`
- `for %%v in (word list) do cmd|(block)` — `%%v` in the body is the
  loop-var read
- `for /l %%v in (start 1 end) do …` (v1.1) — the A1 `Range` iterable,
  unit step only; other steps refuse
- `^` end-of-line continuation (v1.1) — joins the next non-empty line;
  comments are exempt; inline `^` escapes refuse
- `call :label [args]` (v1.1) — subroutines: called labels are extracted
  into A1 `Function` stmts (registered before the main flow; the estree
  fnCall binds the args to %%1..%%9 via the positional array, the sh
  renderer emits real `name() { ... return }` functions). `goto :eof` ->
  Return inside a subroutine / Exit at top level. Distinct from `goto`
  (jump-with-return vs jump): goto targets stay inline as Goto/Label —
  and ONLY goto-target labels emit a Label stmt; fall-through markers
  (nothing jumps to them) are dropped as no-ops, keeping the body inline
  so batch's label fall-through semantics hold (t15 pins this — a
  function model would lose the fall-through code). `call other.bat`
  refuses (v1.1: only `call :label`).
- `exit /b [N]` — the direct IrStmt::Exit statement (all backends render
  it: estree -> process.exit, sh -> exit, perl -> exit; the estree arm
  landed 2026-08-09)
- `for /f ["delims=X tokens=1[,N...]"] %%v in (FILE | literal words | 'cmd') do …`
  (v1.1) — line iteration with field tokens (read-builtin lowering; the
  single-token case adds a discard var since read's last var gets the
  remainder). `skip=`/`eol=`/`usebackq` and token sets not starting at 1
  refuse. NOTE: the estree runtime caches read buffers per source, so two
  for /f loops over the SAME file in one program see the second as empty
  (runtime gap — use distinct files).
- `> file` / `>> file` / `N>` / `N>>` redirects (v1.1) — the word-list
  scan wraps the exec in a Redirect stmt.
- batch builtin -> POSIX command mapping (v1.1): copy->cp, del/erase->rm,
  type->cat, move/ren/rename->mv, rd->rmdir, md->mkdir, dir->ls, where->
  which, xcopy->cp, ver->uname, find->grep -F, cls/title->no-op; common
  flags translated (dir /b->-1, del /q->-f, /y->-f, ...). Batch `ren OLD
  newname` resolves the bare destination into OLD's dir. NOTE: the estree
  runner's exec allowlist derives from the SOURCE text — scripts using
  mapped commands should mention the posix names in a comment.
- `cmd1 & cmd2` statement separators

Deliberately NOT in v1 (refuse loud, documented as worker items):
- `call other.bat`, `setlocal`/`endlocal`, `shift`, `pause`, `start`,
  `pushd`/`popd`, `set /p`, `for /d /r`, `for /f` with skip=/eol=/usebackq
  or token sets not starting at 1, `|` pipes, delayed expansion `!var!`,
  `%cmdline%`

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
