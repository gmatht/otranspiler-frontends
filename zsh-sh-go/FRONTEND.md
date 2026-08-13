# zsh-sh-go frontend (dir: /nvme/ai/sh2loop/frontends/zsh-sh-go)

Workspace-side dir; no git worktree. The "scope" is this dir +
harness/* (shared test infra).

Yours (in THIS dir): the lexer, parser, emitter, and any tests
specific to this frontend.

Shared (do NOT fork): frontends/shir-contract/, frontends/check_contract.py,
frontends/equiv.py, frontends/test_pipe.py, frontends/plan.md,
frontends/cxx-rust-adequacy.md — these are cross-frontend contracts
and tests, maintained centrally. Also: the core
(src/shir.rs, src/ir.rs, src/estree.rs, src/parser/) is single-owner
(the estree worker during the lowering phase).

Worker: /nvme/ai/sh2loop/frontends/zsh-sh-go/run_frontend_worker.sh

## Implementation (2026-08-06 rewrite)

main.go + lowering.go + analysis.go are a hand-rolled port of the core
frontend (parser, lowering, A2 var-type analysis), byte-identical to
`debashc --shir FILE --raw` on the full testdata/ language-ladder corpus
(t01-t52). The v1 stub (echo + NAME=VALUE only) is gone.

zsh-specific coverage beyond the POSIX-shaped core of the corpus:

- `[[ ... ]]` / `(( ... ))` conditions (test/let lowering)
- `(( expr ))` statements (`exec let`)
- C-style `for (( init; cond; step )); do …; done` → the rich A1 `ForInit`
  node (byte-identical to the core's CStyleFor lowering: ';'-split
  trimmed header, let-able Assign/IncDec init/step → structured
  `Assign{Arith}` stmts, cond → `exec let` (or `Int(1)` for `((;;))`),
  `runs:false`; a non-let-able part keeps the whole construct on the
  opaque `cstyleFor` call with the raw header text, spacing preserved)
- bare `$a[2]` array indexes (→ `arrayIndex`) and `${s[2,3]}` / `$#a`
- `f() {` function bodies (whitespace before the brace)
- heredocs (`cat <<EOF`, `<<-`, `<<<`, quoted delimiters)
- `export X=v`, `local y=5`, positional `$1`, `$?`, multi-assign

`make test` is the BYTE-EQUALITY ORACLE vs the core (the A1 contract);
the shir-emit-go package (frontends/shir-emit-go/) is the shared emitter.

## A1 round-trip limits (zsh constructs with no bash-shaped A1 form)

The A1 contract is bash-shaped; constructs whose zsh tokenization differs
from bash's cannot round-trip byte-identically:

- **`$#a` (array length).** The core's bash tokenizer reads `$#a` as the
  positional count `$#` followed by a literal `a` (Interpolate of
  getVar("#") + "a") — correct bash, wrong zsh. The A1 has no fused
  "length of this variable" token for that spelling, so the executed
  output would be "0a". The corpus therefore tests array length with
  `${#a[@]}` (→ `arrayLen(a)`, supported end-to-end; zsh-native).
  `$#name`/`${#name}` in SOURCE still parse byte-identically to the core;
  they just keep bash's tokenization.

Runtime support added to harness/sh2-namespace.mjs for the zsh-emitted
A1 shapes the bash corpus doesn't produce:

- `getVar("#name")` (from unquoted `${#s}`): string length for scalars,
  element count for arrays/assocs.
- `arrayIndex(name, "a,b")` (from `${s[2,3]}` on a scalar): 1-based
  inclusive substring range (bash bad-subscript cases stay empty for
  single keys).
- `test(...)` with a dangling unary flag (from `[[ -n $x ]]` where `$x`
  expanded away): the missing operand is the empty expansion (`-n ""`
  → false, `! -n ` → true).
- `split(x)` in zsh mode (estree-gen dispatches the emitter's inline
  split shape through `sh2.split`): zsh NEVER field-splits unquoted
  expansions (SH_WORD_SPLIT off by default), so the whole value is one
  field — even when empty (zsh iterates once with ""; bash zero times).
  bash/posix keep the whitespace field-split, byte-identical to the
  inline lowering.

**Array base (PLAN v12).** zsh arrays are 1-based while the A1 contract
is canonical 0-based — the frontend NORMALIZES subscripts at emit
(`zshIndexKey`: positive literal `n` → `n-1`; comma-range `a,b` →
`param("slice", name, a-1, b-a+1)`; negative indices are base-invariant
and stay; dynamic indices pass through) for reads, `${s[2,3]}` slices,
and `a[n]=v` / `a[n]+=v` element WRITES (the index is baked into the
assignment's var name — the runner's setVar/assign are language-blind
0-based). Executors/backends never see the source language; only the
estree runner's `_setLang("zsh")` (split no-op) remains. Byte-equality
with the core (whose bash parse cannot match the normalized emit) is
waived for array-subscript files (harness/zsh-subscript.ere);
executed-stdout is the semantic anchor. `${#x}` / `${#a[@]}` length
forms have no subscript and stay byte-compared.
