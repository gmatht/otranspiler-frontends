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

## v0.3 additions — user functions

- multiple `fn` items: non-main fns lower to A1 `Function` statements
  (the c-sh-go protocol), emitted BEFORE the main body in source order
  (runtime def-before-use). Params bind positionally at fn entry
  (`p_i <- getVar("i+1")`); value-returning calls dispatch `fnValue`,
  statement-position (void) calls dispatch `fnCall`; explicit
  `return e;` lowers to the native A1 `Return`.
- tail expressions: a semi-less final expression of a value-returning
  fn returns its value (`fn twice(n: i64) -> i64 { n * 2 }`). Control-
  flow tails of value-returning fns REFUSE (if/match-as-value is a later
  phase — refuse > silently dropping the value).
- no-main sources are legal (library-style definition-only files): empty
  program body.
- inert attributes dropped on fns (`#[test]`, `#[macro_export]`, doc/
  allow/inline/deprecated — compile-time-only metadata); any other
  attribute refuses (cfg/cfg_attr select code).
- STILL refused: calls inside arithmetic (A1 arith has no call operand;
  hoisting to temporaries comes with if-as-expression), generics,
  receivers/impl blocks, closures.

Pinned by t35_functions.rs; t14_fn_refuse moved to the generic-fn
refusal boundary.

## v0.4 additions — declarations & associated functions

- struct / enum / union / type-alias / trait ITEMS accepted as
  declarations (no runtime effect by themselves; every USE — struct
  literal, variant path, trait dispatch — refuses at its own site)
- inline `mod` blocks flattened into the item list (namespace nesting is
  invisible to the lowered names); file `mod x;` contributes nothing;
  `#[cfg(test)]`-gated subtrees dropped entirely (test-harness-only code
  does not exist in a normal build)
- `macro_rules!` definitions dropped (unknown invocations still refuse);
  other item-level macros refuse (lazy_static! etc. creates state)
- inherent AND trait impl methods lower as mangled `Type_method`
  Function defs; calls resolve `Type::method(...)` through the mangling.
  This guesses nothing: every RECEIVER path (&self syntax) refuses until
  the method-contract tranche lands.
- STILL refused: method receivers/field access, unknown call targets
  (`String::from`, mod-path calls like `math::triple`), closures

Pinned by t36_decls.rs + t37_method_refuse.rs. Corpus movement:
11/30 -> 17/30 files past ALL structure into expression-level gaps
(17x method-call/field-access expressions, 4x receivers, 3x unknown
call targets, 1x if-tail-value, 1x {:?}, 1x lazy_static, 1x wasm_bindgen).

## v0.5 additions — value-position booleans, methods, borrows, arrays

- comparisons / `&&` / `||` / `!` in VALUE position lower to `test`
  calls (the py-sh-go CompareE convention — the runtime returns native
  JS booleans, printing like Rust's bool `{}`). Operands are ints /
  int-vars; string comparison refuses (test grammar is numeric).
- SOUNDNESS FIX: a bare boolean VARIABLE as a condition now REFUSES
  (`$b` alone parses as a nonempty-string/file test — silently wrong).
- minimal type tracker (`Ty`: Int/Str/Bool/Arr/Unknown) seeded from
  param annotations, let annotations and literal shapes; per-function
  scope; a re-`let` re-infers. This is what keeps method lowering from
  guessing.
- string methods with PROVEN 1:1 JS equivalents on Str receivers:
  contains->includes, starts_with->startsWith, ends_with->endsWith,
  trim/trim_start/trim_end, to_lowercase/to_uppercase (A1 MethodCall,
  rendered NATIVE JS by the core). Everything else refuses.
- shared `&T` borrows erase to value snapshots (exact: no concurrent
  mutation is possible through the original while a shared borrow
  lives); `&mut` refuses; obsolete t13 pin removed.
- `vec![..]` and `[a, b]` literals -> native A1 Array literals.

Pinned by t39 (refuse), t40, t41, t42, t43 (refuse), t44. Corpus
movement: 17/30 -> 18+/30 files deeper into the expression frontier;
remaining first-gaps: struct literals x4 (needs field-access contract),
unproven methods x6 (iterators: collect/chars/iter...), receivers x4,
unknown call targets x3, if-tail-value, {:?}, lazy_static!,
wasm_bindgen, `?`, tuple.

## v0.6 additions — receiver methods & field reads

- inherent impl methods with receivers lower via the mangled
  `Type_method` def: `self` binds from the FIRST positional
  (`self <- getVar("1")`); all three receiver forms share one exact
  lowering (JS objects are references: `&mut self` writes through —
  true; `&self` can't mutate — true; by-value moves are rustc-checked).
- method dispatch: `obj.m(args)` on a Struct-typed receiver ->
  fnValue("Type_m", [obj, args...]); the Ty tracker resolves struct
  types from declarations, params and struct-literal lets.
- field reads `p.x` -> the FieldRead drop-in node (core side:
  shir_nodes/field_read.node + per-backend handlers; merged to main).
- FIELD READS IN ARITHMETIC hoist their pure reads into fresh
  temporaries (`self.n * 2` -> `__sh2f1 <- self.n; return __sh2f1*2`) —
  exact snapshot semantics, backend-neutral.
- KNOWN GAP (core request rust-frontend-20260823-record-storage.md):
  STORING a record (`let c = Point{..}`) stringifies at the core's
  store-write path (`sh2.vars.c = String({...})`), so t45/t46 live in
  testdata-pending/ until the estree worker lands the one-line fix.
  Reader-only structs work the moment it lands; mutating methods
  (`self.n += k`, field WRITES) need a contract shape after that.

Pinned by t46_methods.rs (moved to pending with t45).

## v0.7 additions — if-as-expression

- `let x = if c {A} else {B}` / `x = if ..` bind x in EVERY branch
  (exact: rustc guarantees all paths produce the value); fn TAILS return
  per-branch (`Return` in then + deepest else); else-if chains recurse
  into nested Ifs. Branch statements lower normally first.
- branch type inference: first provable branch value's Ty.
- match-as-value / enum variants / write! remain refused (they need the
  enum-representation + Display tranches — see the ast_words/ir.rs
  probes in ROADMAP).

Pinned by t48_if_value.rs. Frontier after v0.7: iterator machinery x9,
match-on-enums x5 (needs enum repr + write!), record .clone() x5,
`?` x2, singletons (write!, lazy_static, wasm_bindgen, generic impl,
{:?}, Token::lexer iterators, as_bytes).

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
