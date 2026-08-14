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
- `set /a VAR=expr` — integers, `+ - * / %`, parens, unary minus
  (`-5`, `-(1+2)`), `%var%` operands, `%%` literal (modulo)
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
- `for /f ["delims=X tokens=1[,N...]"] %%v in (FILE | "literal line" | literal words | 'cmd') do …`
  (v1.1) — line iteration with field tokens (read-builtin lowering; the
  single-token case adds a discard var since read's last var gets the
  remainder). `in ("string")` is cmd's quoted-literal form — the WHOLE
  string is one line, tokenized by delims (bare `(word word)` is the
  frontend's literal-words extension; real cmd treats those as
  filenames). `skip=`/`eol=`/`usebackq` and token sets not starting at 1
  refuse. NOTE: the estree runtime caches read buffers per source, so two
  for /f loops over the SAME file in one program see the second as empty
  (runtime gap — use distinct files).
- `> file` / `>> file` / `N>` / `N>>` redirects (v1.1) — the word-list
  scan wraps the exec in a Redirect stmt. The glued cmd forms (`>file`,
  `echo text>file` — cmd ECHOES the space before a spaced `>`, so the
  no-space form is the portable one) are handled, batch `\` path
  separators normalize to `/` for the POSIX targets, and the `nul` device
  maps to `/dev/null` (how batch silences `copy`/`move` status lines).
- batch builtin -> POSIX command mapping (v1.1): copy->cp, del/erase->rm,
  type->cat, move/ren/rename->mv, rd->rmdir, md->mkdir, dir->ls, where->
  which, xcopy->cp, ver->uname, find->grep -F, cls/title->no-op; common
  flags translated (dir /b->-1, del /q->-f, /y->-f, ...). `robocopy SRC
  DST /S|/E` -> `rsync -a SRC/ DST`; `/MIR|/PURGE` -> `rsync -a
  --delete SRC/ DST`; no recursion flag -> `rsync -a --exclude='*/'
  --include='*' SRC/ DST` (top-level files only); `/MOV|/MOVE` ->
  `rsync -a --remove-source-files` (/MOVE leaves the emptied source
  dirs); `/L` -> `rsync -an`; file filters -> rsync --include/
  --exclude sets; `/XD dirs` / `/XF files` -> `--exclude=...`;
  `/LOG:file` -> `--log-file=file` (log format differs). robocopy's
  verbose status output is silenced with `>nul`; retry/wait/thread and
  output-quiet flags are dropped; unknown options refuse. Batch `ren OLD
  newname` resolves the bare destination into OLD's dir. NOTE: the estree
  runner's exec allowlist derives from the SOURCE text — scripts using
  mapped commands should mention the posix names in a comment.
- `cmd1 & cmd2` statement separators — split on the first top-level `&`
  (outside double quotes); cmd ECHOES the trailing space before a spaced
  `&`, so `echo a&echo b` is the portable form

## v1.2 additions (landed 2026-08-14)

- `cmd1 && cmd2` / `cmd1 || cmd2` — the A1 `BinOp And/Or` conjunction
  shape (byte-identical to the core's `a && b` / `a || b` emission, probed
  with `debashc --shir`). Both operands must be plain single commands
  (no redirects/blocks/pipelines — refuse > guess); `a & b && c` mixes
  correctly (`b && c` folds, `a` runs first). The estree renderer lowers
  And/Or to a lastExit check, the sh renderer to `&&`/`||`.
- `a | b` pipelines — the A1 `pipeline` Call (the core's `a | b` shape).
  Each stage must be one plain command; `for /f %i in ('a | b')` keeps its
  pipe inside the single quotes (the /f command delimiter).
- `shift` — the core's own `shift` is an exec Call of the shift builtin;
  the estree runner shifts the CALL-scoped positional array (a
  `call :label` pushes a fresh scope), so the canonical arg-loop
  (`:next / if -%1-==-- goto :eof / echo %1 / shift / goto :next`) works.
  The `-%1-==--` positional test lowers via `-\$1-==--` (positionals now
  convert in if-conditions too).
- `ren *.cxx *.cpp` — the EXTENSION-CHANGE pattern form. Both arguments
  must match `*.EXT` (a single leading `*`, then `.EXT`, no further
  wildcards, no dir prefix, no quotes); anything else refuses. Lowers to
  `for f in <SH2GLOB *.cxx>; do mv "$f" "$(basename "$f" .cxx).cpp"; done`
  — basename strips the LAST suffix, cmd's exact rule (`a.cxx.cxx` ->
  `a.cxx.cpp`, where a first-occurrence replace would diverge).
- `find "text"` mapping fix: the leading quoted search term is stripped
  of its quotes (cmd strips them building the argv) — `find "beta"` ->
  `grep -F beta`, not `grep -F "beta"` (which matched the quote chars).
- Core fixes that landed with v1.2 (in the sh2perl submodule):
  (1) `fix_control_flow` now runs on the A1-ingress path
  (`--shir-in-estree`), and its return-conversion is keyed on LOOP-BODY
  arrows only — a bare `return` inside a whileLoop body arrow exited the
  callback and the loop spun forever (bat t51 exposed it; bash's own
  `if c; then return; fi` in a loop had the same latent bug); frontend
  VALUE-returning arrows (zig `__fn_f`, the py ArrayComp IIFE, C fnValue)
  keep native returns via the `sh2.functions.set` / loop-helper
  recognition. (2) `handle_bare_goto`'s backward splice used pre-drain
  indices and panicked for any non-empty loop body — the batch
  `:loop ... goto loop` idiom is the first real backward-bare-goto user.

Deliberately NOT in v1/v1.2 (refuse loud, documented as worker items):
- `call other.bat`, `setlocal`/`endlocal`, `pause`, `start`,
  `pushd`/`popd`, `set /p`, `for /d /r`, `for /f` with skip=/eol=/usebackq
  or token sets not starting at 1, delayed expansion `!var!`, `%cmdline%`,
  inline `^` escapes (only the end-of-line continuation form), `ren`
  patterns beyond `*.EXT` (a `?` pattern, a mid-name `*`, a dir prefix).

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
