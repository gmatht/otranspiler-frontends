# go-sh frontend (dir: /nvme/ai/sh2loop/frontends/go-sh)

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

Worker: /nvme/ai/sh2loop/frontends/go-sh/run_frontend_worker.sh

---

## Type-position erasure & explicit value gaps (2026-08-21)

The parser now consumes the full Go TYPE grammar via a balanced
`skipType()` (ident/pkg.T/`*T`/`[N]T`/`[]T`/`map[K]V`/chan/func/
parenthesized/struct/interface/variadic). Go types are compile-time
only and have zero runtime statements, so parse-and-erase IS the
faithful drop-in lowering (the contract already pinned for empty
interfaces, scalar aliases, and generic type params):

**Supported (erased):**
- `type Name struct{…}` / `type Name interface{…}` (incl. method sets)
  / `type (...)` groups — top-level declarations emit nothing
- parameter types: `[]T`, `[N]T`, `*pkg.T`, `...T`, `map[K]V`,
  `func(…) …`, qualified names; grouped specs `(a, b T)`
- result types incl. multi-value lists `(T, error)` (funcs + literals)
- `var ( … )` groups with any type position erased
- generic if-init assignments `if x := e; cond {` (init lowers as an
  ordinary assignment; only the cond gates the branch)
- multi-value `return a, b` → multiple echo words (a shell sub returns
  via stdout — the same channel shell functions use for several values)
- bool literals (`true`/`false`) in value position → their textual form
  (fmt prints bools as true/false — byte-faithful under echo), incl.
  bool-valued map literals (the allowedKinds/syncBuiltins idiom)
- calls to DEFINED subs as RHS: `q, _ := f(args)` → `q=$(f args)`
  capture (single non-_ target)

**Explicit gaps (each refuses loudly with "unsupported …", never
mis-lowers) — corpus classification REFUSE:**
- type assertions `x.(T)` (comma-ok included): the A1 is dynamically
  typed with no tags to test; the false branch would be unreachable in
  a mis-lowering
- address-of `&x` / dereference `*p`: no storage locations in the A1
- variadic spread `f(args...)`: a word is one value; no drop-in spread
- composite literals in EXPRESSION position (`return map[string]any{…}`,
  struct-literal map values): whole dict/struct values have no A1 shape;
  maps exist only as named assoc-arrays mutated by assocSet
- struct-field member access on values (`prog.Imports`, `l.src[i]`):
  members are not variables in the A1
- comparisons in word position (`return c == '_' || …`): conditions are
  If/test shapes, not echoable words
- zero values: an unassigned var read or absent-key map read yields ""
  where Go gives the type's zero value (assocGet is a fixed 2-arg
  contract shape — a default-aware read would be a core change)
- unsupported stdlib constructors (sitter.NewLanguage,
  regexp.MustCompile, sync.Once.Do, …) in word position

Corpus state after this change (frontends/coverage/parser-coverage.sh):
121 real .go files → EMIT 99 (81.8%), REFUSE 22, PARSE-ERR 0, CRASH 0 —
100% parser engagement (every file either fully lowers or is refused at
a named construct).
