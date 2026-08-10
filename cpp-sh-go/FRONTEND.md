# cpp-sh-go frontend (dir: /home/llm/sh2loop/frontends/cpp-sh-go)

C++14 source -> A1 shIR JSON — **the C++-only surface over the shared
C lowering** (CPP_PLAN.md: one runtime, two grammars, one shared
lowering; include but never modify).

Workspace-side dir; no git worktree. The "scope" is this dir +
harness/* (shared test infra).

## The split (CPP_PLAN §3)

- `main.go` — the C++-only surface: a provisional tokenizer (the
  tree-sitter parser replaces it when the full-C/C++ grammar lands), the
  C++-only keyword REFUSAL table, and a bounded DESUGAR onto C:
  - `bool`/`true`/`false`/`nullptr` → `int`/`1`/`0`/`0`
  - `new T[N]`/`new T` → `malloc(N * sizeof(T))`/`malloc(sizeof(T))`
  - `delete[] p`/`delete p` → `free(p)`
- The shared lowering is **c-sh-go's `clib` package, imported as a Go
  module — never modified here.** Extensions to C-owned behavior go
  through `c-requests/` (see the channel at the workspace root).
- Emit is `clib.Shir` — the A1 JSON is byte-equivalent to the C
  frontend's on the expressible subset (the oracle).

## v0.1 subset (REFUSE > GUESS)

Expressible (each pinned by a testdata_cpp/*.cc entry):
- everything c-sh-go proves (printf, int/char arith, if/while/for,
  functions, structs, pointers, malloc/free…) — `//` and `/* */`
  comments included
- `bool`/`true`/`false`/`nullptr`
- `new T[N]` / `new T` (heap) and `delete[] p` / `delete p`

Refused loudly (testdata_cpp/*_refuse.cc — the emit must FAIL):
templates, classes (use `struct` for data-only), `std::`/`::`,
`->`, exceptions, `auto`, `constexpr`, coroutines/concepts (C++20),
references `int&`, `new` with constructor args or pointer types,
`delete` of a non-identifier.

## Gate

`make test` = the cpp gate (refusals + ingress acceptance via
`debashc --shir-in-estree` + executed-stdout vs native g++) **AND the
C-invariant** (the c-sh-go corpus stays green — the hard line, CPP_PLAN
§5). `make test-cpp` runs just the cpp gate; `make test-c-invariant`
just the C-invariant.

## Worker

`run_frontend_worker.sh` — failure-driven (mirror of c-sh-go's): gate
`make test` every iteration; green → commit in scope; red → pi
(`setup_backends.sh --pi-fix-frontend cpp-sh-go` — whose prompt says to
append to `c-requests/` instead of touching C-owned code); 3 fails →
trap via `core-requests/`.

Shared (do NOT fork): frontends/shir-contract/,
frontends/shir-emit-go/ (the A1 emitter, used read-only through clib),
frontends/plan.md, frontends/CPP_PLAN.md, frontends/cxx-rust-adequacy.md.
Also the core (sh2perl/src/*.rs) is single-owner (the estree worker).
