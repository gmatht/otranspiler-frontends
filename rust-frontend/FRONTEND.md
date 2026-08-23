# rust-frontend (dir: /home/llm/sh2loop/frontends/rust-frontend)

Rust source -> A1 shIR JSON (the sh2perl frontend contract). The first
Rust-hosted frontend in the fleet (syn-based — stable crates.io parser,
no nightly, no rustc-dev; the rustc_parse/rustc_private path was
rejected per `frontends/cxx-rust-adequacy.md` + the syn-vs-tree-sitter
comparison: typed AST mirroring rustc, zero toolchain churn).

## v0.1 subset (REFUSE > GUESS)

Expressible (each pinned by a testdata/*.rs stdout example, t01–t12):
- `fn main()` only — no other items, no attrs, no async/unsafe/const,
  no args/return type (anything else REFUSES)
- `let`/`let mut` integer and string bindings; `let x;` deferred-init
  declarations are DROPPED (Rust guarantees assignment before use, the
  native oracle enforces it — the c-sh-go `int i;` pattern)
- plain assigns `x = e;` and compound `x += e;` / `-=` / `*=` / `/=` /
  `%=` (lowered to `x = x OP e` — the c-sh-go compound shape)
- integer arithmetic `+ - * / %` (the ESTree renderer truncates `/`),
  literals incl. hex/bin/octal and `_` separators (normalized to decimal
  — the store is decimal-string-typed), unary minus (`-x` -> `0 - x`)
- if/else, `else if` chains (lowered to a NESTED If in the else
  position — the shell frontend's elif shape, never the `elsifs`
  field), while, `for i in 0..N` / `0..=N` — LITERAL bounds only (A1
  Range is INCLUSIVE: `0..N` -> `{start:0, end:N-1}`, `0..=N` ->
  `{start:0, end:N}`)
- print!/println! with `{}` placeholders -> `exec printf` with `%s`
  (safe for the string-typed store — ints and strings both print
  correctly); literal `%` -> `%%` (the runtime printf would misread a
  lone `%`); `{{`/`}}` -> `{`/`}`; placeholder count must equal arg
  count; `println!` appends the newline
- conditions: comparisons == != < <= > >= (-> the `test` string
  grammar `-eq -ne -lt -le -gt -ge`), `&&`/`||` (-> `-a`/`-o`, binding
  order matches Rust), `!`, parens; operands are variables or integer
  literals
- bare `return;` lowers to the A1 `Exit` statement (it ENDS the program —
  not a no-op; regression t25) and bare `x;` (no-op) is dropped

## v0.2 additions (the src/ coverage push, tier-1 = JS/ESTree)

The goal: parse ALL the Rust in sh2perl's `src/` (30 files, ~109k lines).
Architecture stance (per the core owner):
  - the frontend emits NATURAL Rust semantics in shIR — no shell-shaped
    encodings, no JS-specific ones either; conversion to a concrete
    backend is the CORE WORKER's job or a GENERIC shIR->shIR transform;
  - where shIR lacks a node Rust needs, it is added as a DROP-IN file
    (`sh2perl/src/shir_nodes/*.node`) so build.rs generates the bindings —
    never by editing shared core files (merge-conflict discipline);
  - tier-1 validation is the executed-stdout oracle vs native rustc
    through the ESTree/JS path (`make test`).

Added (each pinned by testdata t31-t34):
- `use` items — dropped (type-checker-only names, zero runtime effect)
- `const NAME: T = e;` / `static NAME: T = e;` items — immutable globals,
  lowered to assignments that run BEFORE main; integer consts join a
  const table and are FOLDED at compile time wherever the A1 grammar
  demands a literal (range bounds `for i in 0..LIMIT`, test operands).
  Const folding is semantics-preserving compiler work, not an encoding.
- `let x: i64 = e;` type annotations (Pat::Type) — erased (the store is
  dynamically typed); deferred-init typed lets still dropped
- boolean literals as VALUES (`let b = true;`) — the A1 `Bool` expr,
  rendered by the core to a native JS boolean (prints like Rust `{}`);
  literal-bool CONDITIONS fold to always-true/false numeric tests

Refused loudly (testdata/*_refuse.rs — the emit MUST fail, t13–t23):
borrows `&x`, user functions, `String::from`/method calls, `match`,
`vec!`, `eprintln!`/`dbg!`, floats, `{:?}`/named/width format specs,
`loop`, indexing, dynamic range bounds, any other item/macro/statement.

## Gate

`make test` = refusal pins + ingress acceptance (`debashc
--shir-in-estree` accepts the emitted A1) + the executed-stdout oracle
(`harness/frontend-stdout.sh` `rust`): native `rustc` compile+run vs
the A1->ESTree->JS run, normalized stdout match. Native compile with the
real toolchain is the source of truth — the frontend only ever sees
valid Rust (invalid input is rejected before the oracle runs).

Load hardening: the worker's `make test` holds the workspace
`.gate.lock` flock (same serialization backend workers use — commit
7edd76e) so the gate never races another worker's cargo build, and the
harness's rust native path retries a starved/OOM'd `rustc` compile
(2026-08-12 14:49: t01's native side came back empty on a concurrent
build, transpiled side correct, rest green). Oracle-tear hardening:
both oracle phases (ingress acceptance + stdout harness) run a
`snapshot-debashc.sh`-verified copy of debashc taken once per gate run,
never the shared binary — a concurrent estree-worker relink used to
read as a misleading "FAIL (emit is not valid A1 shIR)" and escalate
to pi as a build failure (2026-08-15, core round 177255ec; the
zsh-sh-go t71_var_name_mods precedent, 2026-08-12 20:55).

## Worker

Not yet registered (prototype). The fleet pattern is failure-driven
(`run_frontend_worker.sh` -> pi via `setup_backends.sh
--pi-fix-frontend <name>` -> commit/stash -> trap/escalate); registering
rust-frontend in `setup_backends.sh` is a follow-up.

## Layout & discipline

- `src/main.rs` — the whole frontend (parse + walk + A1 emit; ~700
  lines). A1 node constructors byte-match the core's `--shir` shapes
  (probe the core first, never guess).
- `testdata/` — t01–t12 positive (each pins one construct), t13–t23
  refusal pins.
- Offline build: `cargo build --offline` against the workspace's cached
  registry (pinned syn 2.0.119 / serde_json 1.0.151 / proc-macro2
  1.0.107+span-locations).

Shared (do NOT fork): frontends/shir-contract/, frontends/shir-emit-go/,
frontends/plan.md, frontends/cxx-rust-adequacy.md (the adequacy verdict
this subset lives inside), harness/frontend-stdout.sh (the `rust` lang
case). The core (sh2perl/src/*.rs) is single-owner (the estree worker).

## Honest scope (the adequacy verdict)

cxx-rust-adequacy.md: shIR cannot carry Rust beyond ~3/40 features
(types, borrows/lifetimes, traits, enums/patterns, closures, async,
generics). This frontend is a **source-to-shell filter for the
expressible subset** — every construct outside the subset REFUSES
loudly, nothing is smeared. The v0.1 subset is the shell-flavored
fragment of Rust (int state machines that print); the classic `for i in
0..N` + if/while + println program shape. It is a research/prototype
surface, not a Rust-to-anything compiler.
