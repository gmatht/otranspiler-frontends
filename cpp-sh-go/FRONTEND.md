# cpp-sh-go frontend (dir: /home/llm/sh2loop/frontends/cpp-sh-go)

C++14 source -> A1 shIR JSON — **the C++-only surface over the shared
C lowering** (CPP_PLAN.md: one runtime, two grammars, one shared
lowering; include but never modify).

Workspace-side dir; no git worktree. The "scope" is this dir +
harness/* (shared test infra).

## The split (CPP_PLAN §3)

- `parser.go` — **tree-sitter-cpp IS the parser**: the whole file
  parses (GLR error tolerance), and a whitelist walker REFUSES any
  NAMED node outside the expressible set (node-level refusal,
  REFUSE > GUESS — `template_declaration`, `class_specifier`,
  `qualified_identifier`, `reference_declarator`, `placeholder_type_specifier`,
  `try_statement`, `->`, syntax errors — with kind + line).
- `main.go` — the C++-only surface: a tokenizer demoted to C-text
  reconstruction for clib (string/char/comment/preprocessor-safe), and
  a bounded DESUGAR onto C:
  - `bool`/`true`/`false`/`nullptr` → `int`/`1`/`0`/`0`
  - `new T[N]`/`new T` → `malloc(N * sizeof(T))`/`malloc(sizeof(T))`
  - `delete[] p`/`delete p` → `free(p)`
  - `stripSwitchDefaultBreak` — a switch's default arm trailing
    `break` is dropped from the token stream before clib sees it: the
    shared switch lowering keeps it (only case-arm breaks are
    stripped), and an A1 Break outside a loop is an uncaught BREAK
    signal in the ESTree runtime (t15_switch DIFF). Redundant in the
    if-chain the switch lowers to; stripped at the token level so only
    breaks that bind to the switch are touched.
- The shared lowering is **c-sh-go's `clib` package, imported as a Go
  module — never modified here.** Extensions to C-owned behavior go
  through `c-requests/` (see the channel at the workspace root).
- Emit is `clib.Shir` — the A1 JSON is byte-equivalent to the C
  frontend's on the expressible subset (the oracle).

The wasm packaging (CPP_PLAN §2: a self-contained tree-sitter wasm
binary for browser C/C++ source support) stays deferred; this is the
native Go + cgo tree-sitter adoption (CPP_PLAN Session 1 shape).

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

## JS-target dogfood (TRANSLATE_ONE_APPLICATION)

The cpp frontend's Go source is a dogfood target of the go-sh frontend
(the Go→JS pipeline): `./fail-go --app
frontends/cpp-sh-go/cmd/cpp-sh-go/main.go` — the CLI transpiles Go→JS
end-to-end and the translated JS reproduces the native no-args behavior
(usage → stderr, exit 2). The CLI is a thin wrapper (os.Args filter,
os.ReadFile, cppshgo.Shir call, os.Stdout.Write) — the same construct
set the go-sh CLI's app gate exercises.

The LIBRARY (`main.go` + `parser.go`) is **explicitly bounded for the
JS target** — its full transpilation + testdata_cpp reproduction is
blocked on four boundaries, none forkable in the frontend (Refuse >
guess):

1. **struct values** — `type tok struct{ kind, text string }`, 17
   `tok{...}` composite literals, 36 `t.kind`/`t.text` field reads
   (mostly on array elements: `toks[i].text`). The A1 has no struct
   value shape — category-1 A1-EXTENSION, core request
   `go-sh-structtype-20260814-054454` (the dotted member-key contract).
   The go-sh frontend erases the struct TYPE decl (compile-time only)
   but refuses the VALUE forms.
2. **byte semantics** — `src[i]` byte access: Go bytes are integers
   (the lexer's `c >= '0' && c <= '9'` digit test needs the ASCII
   VALUE), the A1 is string-flavored. A char-string lowering is
   faithful for `c == ' '` equality but silently mis-lowers the digit
   test (evalArith("a") = 0 passes `-ge 0`), so the construct stays
   REFUSED (Refuse > guess — no silent mis-lower).
3. **cgo tree-sitter** — `parser.go`'s `treeCheck` links
   tree-sitter-cpp via cgo; cgo cannot transpile to JS. The whitelist
   refusal is a parser concern that stays native (the CPP_PLAN §2 wasm
   packaging is the deferred resolution).
4. **clib dependency** — `clib.Shir` (c-sh-go, ~5k lines) is the shared
   C lowering, a separate Go module; the A1 emission depends on it.
   Transpiling it is its own dogfood effort (c-sh-go's), not the cpp
   surface's.

Landed in the go-sh frontend for the cpp surface's expressible parts
(probes t96/t98): `var m = map[K]V{...}` map literals (with bool
values), struct type decl erasure, `[]T` types, boolean switches
(`switch { case cond: }` → if-else chain), arithmetic comparison
operands (`"$((i+1))" -lt "$n"`), and De Morgan `!(a && b)`.

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
