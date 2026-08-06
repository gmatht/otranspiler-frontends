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
- bare `$a[2]` array indexes (→ `arrayIndex`) and `${s[2,3]}` / `$#a`
- `f() {` function bodies (whitespace before the brace)
- heredocs (`cat <<EOF`, `<<-`, `<<<`, quoted delimiters)
- `export X=v`, `local y=5`, positional `$1`, `$?`, multi-assign

`make test` is the BYTE-EQUALITY ORACLE vs the core (the A1 contract);
the shir-emit-go package (frontends/shir-emit-go/) is the shared emitter.
