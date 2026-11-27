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

## v0.8 additions — unit enums & match

- UNIT-variant enum values lower to qualified tag strings
  (`Color::Red` -> `"Color::Red"`); the Ty tracker gains `Enum(name)`.
- `match` with unit-variant arms (incl. or-patterns `A | B` and `_`
  fall-through) lowers to nested Ifs over string-equality tests on a
  hoisted scrutinee (`__sh2mN <- <scrutinee>`); statement / let /
  assign / fn-tail positions all supported. Guards and payload
  (tuple/struct) patterns refuse — payloads need record storage.
- equality tests dispatch numeric `-eq`/`-ne` vs string `=`/`!=` by
  PROVEN operand type (string literals, unit variants, Str/Enum-typed
  vars); unknown types stay numeric (no behavior change).
- KNOWN LIMITATION documented: A1 function params bind into the SHARED
  global store, so a param name that collides with a caller's variable
  clobbers it (shell semantics; per-function locals are a core-side
  lift analysis). t49 avoids the collision and comments it.

Pinned by t49_enum_match.rs.

## v0.9 additions — clone & AtomicBool statics

- `x.clone()` on ANY receiver -> the CloneDeep drop-in node (core:
  shir_nodes/clone_deep.node; JS structuredClone). Sound by the Clone
  bound itself: clone never aliases, so a deep copy is exact for every
  store value.
- `static NAME: AtomicBool = AtomicBool::new(v)` — typed statics persist
  across per-function scopes; `new` ctor -> the initial value;
  `.load(Ordering::..)` -> plain variable read; `.store(v, ..)` ->
  assignment (single-threaded runtime: exact).
- bool operands in equality tests stringify exactly ("true"/"false")
  under `=`/`!=`; PROVEN-bool bare variables lower as conditions
  (`$b = "true"`); unproven variables still refuse.
- If-condition method calls hoist to temps (If evaluates once — exact);
  While conditions do NOT hoist (per-iteration semantics) and keep
  refusing calls.
- MILESTONE: 4/30 full-file passes (debug.rs joined via AtomicBool).

## v0.10 additions — array storage & Vec::is_empty

- vec![..] / [a, b] assignments STORE via the setArray builtin — a
  NATIVE JS array in the variable (a plain Assign stringifies to
  "10,20,30"; t42/t44 were unobservably wrong before this).
- Vec::is_empty() folds to length==0 in conditions via the hoisted
  param("len", name) read; str::is_empty() is the EXACT equality
  `$s = ""` (no encoding questions).
- DISCOVERED CORE BUG (filed rust-frontend-20260824-param-len-arrays.md):
  the core folds param("len", arr) to String(getVar(name)).length, and
  getVar returns the SCALAR view of an array var => ${#arr} prints the
  first element's length on the JS tier (pure-bash repro included).
  Vec::len() therefore still REFUSES.
- contains(&x) also refuses: no exact builtin read for array membership.

Pinned by t51_len_isempty.rs.

## v0.11 additions — file-mod expansion (gated)

- `mod x;` FILE declarations now resolve against sibling sources and
  splice their items recursively — so cross-module calls into a file's
  OWN submodule tree resolve (`transforms.rs` -> `builtin.rs`).
- GATED SPLICE: a module is included only when it lowers cleanly ON ITS
  OWN (self-check via this binary). A module that refuses contributes
  nothing sound — the entry's calls into it refuse loudly instead.
  Whole-crate merging was tried and REVERTED: it made every file inherit
  the crate's deepest gap (0/30).
- Known limitation: cross-crate calls into NON-lowerable modules
  (`crate::transforms::builtin::fallback_builtin_to_exec` from the four
  backend files) still refuse until those modules pass individually.

## v0.11 additions (cont.) — Vec/array for-loops

- `for x in v` / `v.iter()` / `v.iter_mut()` lower to an index
  while-loop with arrayIndex element reads; the bound is ALWAYS a
  variable (the while-condition grammar reads bare numbers as
  positionals — $3!). Known literal lengths assign the constant;
  unknown ones use the ${#v} param read (miscounts for arrays until
  the param-len core request lands).
- Ty::Arr now carries the ELEMENT type (Vec<T> annotations, vec!
  literal first-element inference, slice types) and the KNOWN length
  of literal-built arrays; loop vars inherit it so `x.method(..)`
  dispatches on T.
- scalar deref reads (`*n`) erase to the value itself.

Pinned by t52_for_vec.rs.

## v0.12 additions — iterator-chain desugar (.iter().map(F).collect())

- `let D = SRC.iter().map(F).collect();` (assignment position too)
  desugars to accumulator + hoisted ${#SRC} bound + index while-loop +
  per-element setArrayAppend — all existing A1 shapes. F is either an
  INLINE CLOSURE (param bound to the element read, body evaluated in
  place — captures read from the shared store, exact for our subset)
  or a registered fn name (per-element fnValue dispatch).
- Result typing: closure body Ty / fn return Ty propagate to D.
- KNOWN GATE GAP: the executed-stdout oracle still fails for these
  programs because EVERY array read-back goes through getVar's scalar
  view (the param-len core request). t53 lives in testdata-pending/
  with instrumentation proving all appends fire correctly; it promotes
  automatically once that request lands.
- Source length must be PROVEN from a literal-built array (the
  param fallback miscounts); otherwise refuses.

## v0.13 additions — multi-step iterator pipelines

- chains generalize: SRC.iter()[.map(F)]*[.filter(P)]*[.take(N)]*.
  collect() desugars to the accumulator loop; each element flows
  through the steps in order:
  - map: closure inline or registered-fn fnValue dispatch, result
    rebinds a per-iteration holder temp
  - filter: predicate must be PROVEN bool (JS "" is falsy but Rust
    values never are — truthiness guessing would diverge); skips via
    Continue on a hoisted bool temp
  - take(N): literal N caps the known bound
- comparisons/logic now infer Ty::Bool (filter predicates and
  value-position booleans type-resolve)

Pinned by t54_iter_chain.rs.

## v0.14 additions — lib-tree gated splice + verdict cache + lifetime erasure

- expansion now ALSO walks lib.rs's module TREE with the same gated
  splice (clean-alone modules only) — cross-crate calls into CLEAN
  modules resolve; calls into dirty ones refuse at the site. The
  earlier 0/30 disaster cannot recur: dirty modules never join.
- clean-lowering verdicts are disk-cached (mtime-keyed, /tmp) and
  child checks run in SINGLE-FILE mode (RUST_FRONTEND_NO_EXPAND=1),
  breaking the check-recursion cycle; census runs in ~20s.
- LIFETIME generics erase unconditionally (`fn f<'a>(x: &'a T)` IS
  `fn f(x: &T)` at runtime); type/const generics + where clauses still
  refuse.
- const-array membership: `.contains(&x)` on a literal const slice
  (`const BUILTINS: &[&str] = &[..]`) lowers to an exact OR-equality
  chain over the known contents in the test grammar.

Pinned indirectly via transforms/builtin.rs lowering cleanly
(69-entry builtins table). Remaining blockers there: slice patterns
against core IrExpr::* variants (meta-level).

## v0.15 additions — tuples & fn-items as values

- tuple literals `(a, b)` store as native arrays via setArray;
  `t.N` reads on tracked array vars lower to the exact arrayIndex
  builtin (getVar's scalar view would misread). `.N` on untracked
  receivers refuses.
- fn items used as VALUES (`transforms::g`, or a bare registered `g`
  not shadowed by a variable) lower to their REGISTERED name string;
  indirect calls through tracked Fn-typed vars resolve to that name at
  compile time and dispatch fnValue/fnCall. Sound: the only operation
  on a fn value is an indirect call, which dispatches by name.
- Ty gains Tuple(arity) and Fn(name); inference covers annotations,
  tuple-literal shapes, and non-shadowed fn paths.

Pinned by t55_tuples_fnvals.rs.

## v0.16 additions — .enumerate() / .rev() / tuple-destructuring params

- chain steps generalize further: `.enumerate()` pairs (position,
  element) with the position being THE LOOP INDEX (never materialized
  as a nested array); `.rev()` flips visitation (position =
  bound-1-idx); both compose with map/filter/take in any order.
- closure params destructure TUPLES: `.enumerate().map(|(i, x)| ...)`
  binds i <- position, x <- arrayIndex(src, position) directly.
- filter/params ADOPT the holder's proven type (`|b| *b` derefs
  through; comparisons infer Bool).
- KNOWN GATE GAP (same param-len request): accumulated-array read-backs
  still go through getVar's scalar view, so t56 waits in
  testdata-pending/ with t53.

## v0.17 additions — Option<T> layer

- Ty::Opt(elem): Option<T> annotations/fields map to the store's
  None≡"" convention (None IS "" — exact whenever no code distinguishes
  Some("") from None; the c-sh-go null→"" idiom, and what getVar's
  unset→"" already does).
- Option method family on proven-Opt receivers: as_deref(),
  unwrap_or_default(), unwrap_or("") / unwrap_or_else(empty) — all
  collapse to the plain variable read. unwrap_or(non-empty) refuses
  (branching not representable in value position).
- Some(v)/None constructors: Some(v) IS v; None IS "". Bare and
  qualified forms both accepted.
- Method-call RESULT types now infer (as_deref keeps Opt-ness,
  unwrap_or takes the default's type) so chains compose.
- infer_ty: comparisons/logic -> Bool (from v0.13), deref passthrough.

Pinned by t57_option.rs (self-contained struct + params + locals).

## v0.18 additions — .skip(K) windows

- `.skip(K)` joins the pipeline steps: the hoisted element read offsets
  by K (`arrayIndex(src, pos + K)`); with `.take(N)` the window is
  exactly K..K+N. Composes with map/filter/enumerate/rev.
- Instrumented verification: appends carry exact values; all remaining
  oracle divergences for array-collecting chains trace to ONE core
  issue — getVar's scalar view of array vars under the ${#acc} fold
  (param-len request, evidence extended). t58 in testdata-pending/.

## v0.19 additions — `?` propagation & registered-fn return types

- `let x = expr?;` — Option propagation under None≡"": an EMPTY value
  returns "" from the enclosing fn (Exit); otherwise x binds the
  unwrapped value. Exact when callers treat "" as None (same
  convention). Requires the enclosing fn to return Option (rustc
  enforces).
- Registered fns now carry their DECLARED RETURN TYPES (cx.fn_rets):
  call results type-resolve (`echo(1).unwrap_or("")` chains work;
  `.collect()` result typing was already via closure/fn inference).
- Pinned by t60_try_option.rs.

Refused loudly (testdata/*_refuse.rs — the emit MUST fail, t13–t23):
borrows `&x`, user functions, `String::from`/method calls, `match`,
`vec!`, `eprintln!`/`dbg!`, floats, `{:?}`/named/width format specs,
`loop`, indexing, dynamic range bounds, any other item/macro/statement.

## v0.20 additions — `if let`, `matches!`, `push`, and the dogfooding scoreboard

**The dogfooding measurement.** `harness/rust-dogfood.sh` runs this
frontend over every `.rs` under `sh2perl/src/` (the v0.2 goal: "parse ALL
the Rust in sh2perl's src/") and reports the lowered/refused split plus a
first-blocker histogram, taken from a content-hashed snapshot of the tree
so a concurrent editor cannot make the score jump mid-run.
Baseline: **14/208 (6.7%)** files lower end-to-end, 194 refused.

**The finding that matters more than the number.** An instrumented probe
(catch_unwind at the per-function funnel) shows **~8460 blocked
functions across ~24 blocker classes, long-tailed** — match patterns
1821, nested item declarations 795, unknown call 754, non-identifier
`let` 704, method on receiver 682, method on string 529, for-iterator
374, match guard 322. Each refused FILE carries 10–49 of them.
**Consequence: no single fix moves the whole-file count.** A fix is
judged by the blocker CLASS it removes and the frontier it exposes, not
by files gained. Measured over this change: `condition expression` fell
13→5 in the first-blocker histogram and the `if-let` sites behind it
became reachable, while the whole-file count stayed 14/208 and the
function count stayed 693/9153. `--min` on the scoreboard is therefore a
REGRESSION tripwire; **the histogram is the progress metric.**

Added (each pinned by testdata t62/t63, each verified stdout-exact
against native `rustc` by `harness/frontend-stdout.sh`):

- **`if let`** — `if let Some(x) = e { .. } else { .. }` binds x and runs
  then iff the scrutinee is not the None marker; `if let None = e` (which
  syn parses as a BINDING ident — in pattern position `None` is the
  absent variant, not a fresh binding) tests the marker directly; `_`
  likewise. Works in statement, condition, `else if`, AND
  value-returning-tail position (`if_value_node` gained an `out` slot so
  the binding hoists where both branches can see it). The scrutinee
  hoists to a temp so it evaluates ONCE.
- **`matches!(v, PAT)`** in BOTH value and condition position, lowered
  through the SAME `arm_cond`/`unit_variant_pat` conversion the match
  lowering uses (so the two can never disagree). The subject must be a
  plain variable — the test grammar reads `$var`, and re-evaluating a
  call subject would run it twice (not exact) — with its own refusal
  message. Or-patterns parse via `parse_multi_with_leading_vert`.
- **`arr.push(v)` / `s.push_str(&p)`** in statement position —
  `setArrayAppend(arr, [v])` and `s = concat(s, p)` (a new `str_concat`
  helper emitting the A1 `Interpolate` shape). Both require a plain
  variable receiver; `push` additionally requires a proven array.
- **`Some(v)` / bare `None` typing** (`infer_ty`) — carried over from the
  in-flight tree state; without it the option methods collapse only for
  literals. `t61_let_opt.rs` depends on it.

**Two exactness bugs this surfaced, both fixed:**

- A bare `None` was lowering to `getVar("None")` — reading an unrelated
  variable by accident. It is the absent marker and now emits the empty
  string on the constructor path AND the bare-path route.
- The `if let` condition was being written UNQUOTED (`$v = ""`). An
  unquoted empty expansion vanishes under word-splitting, so the runtime
  saw `[ = "" ]` and the branch never fired. The quoted form
  (`"$v" = ""`) is what bash emits, for exactly this reason. Caught by
  the executed-stdout oracle, NOT by inspection — the oracle, not the
  emit, is the source of truth.

Refused loudly (new pins t64/t65): `if let Some(<non-name>)`
(destructuring), and `matches!` on a compound subject.

## v0.21 additions — the backend dogfood frontier

Four subset holes that stopped the DOGFOODING walk early, all exact lowerings
(no guessing):

- **`|=` / `&=` on a PROVEN bool** are Rust's logical OR / AND
  (`BitOr`/`BitAnd` on bool), and they are NOT short-circuiting — the RHS
  always evaluates once, so hoisting it into a temp is the exact order.
  The result is the usual value-position `test` (a native JS boolean). A
  bitwise lowering would be silently wrong (the store's `"true"`/`"false"`
  coerce to NaN → 0 through JS `|`), and a NON-bool receiver refuses:
  i64 bitwise semantics are not the JS 32-bit `|`/`&` and the store is
  string-typed (REFUSE > GUESS). This was the first blocker for **24**
  `src/transforms/*.rs` files (`changed |= recurse(s);`) — the backend
  lowering pipeline itself.
- **Lifetime-only generic impl targets erase** (`impl<'a, 'r> T<'a, 'r>`,
  `impl Trait for T<'_>`, `impl<'a> Walker<'a>`), exactly like the v0.14
  function-level lifetime erasure: a lifetime has no runtime
  representation. Type/const generics still refuse. This unblocked the
  first hole in `rust_backend.rs` and `python_backend.rs`.
- **Match literal / binding / `_` arms.** Integer literals lower to
  `$t -eq N`; string and bool literals to quoted test equality; `A | B`
  joins with `-o`; bare `None` tests the None≡"" marker; a plain binding
  arm (`other => …`) binds the hoisted scrutinee and is the
  irrefutable fall-through, as is `_` (both only in last position —
  rustc's unreachable-arm rule). The always-true guard `1 -eq 1` keeps
  the one-node shape for an irrefutable-only match. Payload/struct
  patterns still refuse (record storage).
- **Primitive associated constants** (`i128::MAX`, `u64::MAX`, `u8::MIN`, …)
  fold to their EXACT decimal text. The store is decimal-string-typed, so
  the positive i128/u128 range survives verbatim; an arithmetic use beyond
  i64 refuses at its own site. (The ESTree executor still parses a
  numeric-looking A1 `Str`, so the pin stays on positive out-of-i64
  values; the frontend's A1 carries the exact string either way.)

**Scoreboard movement (the histogram is the metric, not the pass count).**
The first-blocker classes removed: `binary-operator statement` 24 → 0,
`impl of generic/qualified type` 8 → 0, `path expression` 2 → 0. As the
v0.20 note predicts, the whole-file count does not move (each refused file
carries 10–49 blockers); the frontier does — e.g. `rust_backend.rs` now
stops at an item-level macro, `python_backend.rs` at `.lines()` iteration,
and the transforms files at `&mut` borrows / payload matches.

Pinned by t16 (promoted from its old refusal pin), t66–t69 (executed-stdout
vs native rustc), and t70/t71 (the refusal boundaries: non-bool bitwise
compound assign, payload match patterns).

## v0.22 additions — environment reads and Option presence

`.lines()` and `&mut` are the obvious next examples, but neither is exactly
representable in the store yet: a split/line array has a RUNTIME length (the
A1 array reads need a proven length; `param("len")` is the scalar view), and
`&mut` writes through an alias a value store cannot express. The highest-value
EXACT class actually on the frontier is the environment read that gates the
transform pipeline: `env::var`/`env::var_os` appears **195** times in the
tree, and the first-blocker class was "method on receiver" mostly from
`.unwrap_or_default()` / `.is_ok()` / `.is_some()` on those reads.

- `std::env::var("NAME")` / `env::var_os("NAME")` lower to
  `getVar("NAME")`, whose runtime lookup falls back to `process.env`. The
  Result/Option shape is `Ty::Opt(Str)`; `unwrap_or_default`,
  `unwrap_or("")` and `as_deref` (already supported) collapse to the read.
- `is_some`/`is_ok` are `getVar(..) != ""`; `is_none`/`is_err` are
  `== ""` (an A1 `BinOp`, the same marker test as `if let Some(x)`).
- Exactness preconditions are ENFORCED, not assumed: the name must be a
  string LITERAL, a plain store identifier (getVar parses `#`/`[...]`), and
  no program binding anywhere may share the name (getVar reads the store
  before the environment). `var` vs `var_os` differ only for non-UTF-8
  values, which the string store cannot hold. Presence uses the documented
  None ≡ "" convention: a set-but-empty env var reads as absent, exactly
  as `Some("")` already does (t73/t75 pin the two refusals).

**Scoreboard, one frozen source for both binaries (hash `422a3436f88b0ceb`,
254 files):**

```
LOWERED                14 / 254   (unchanged)
method on receiver     22 -> 19
call to unknown        67 -> 68
compound subject        1 -> 2
macro invocation        0 -> 1
```

The pass count is unchanged because each refused file still carries several
blockers — the first-blocker histogram is the frontier metric. Seven files
moved past their first blocker: the four `transforms/*_lift` /
`text_ops_planner` A/B gates now stop at `.split(',').any(..)` (a DYNAMIC
split, i.e. the array-length core request), `perl_generator_fixed` at
`eprintln!`, `echo_return` at a cross-module call, and `str_chop_cursor` at
`matches!` on a compound subject.

Pinned by t72 (executed stdout: set/unset reads in value and condition
position, `is_ok`/`is_err`) and t74 (Option presence predicates), with the
refusal boundaries t73 (computed env name) and t75 (env name shadowed by a
program binding).

## v0.28 additions — exact compile-time integer `as` casts

`as` was the *named* expression refusal, and its biggest site class is a
compile-time fact, not a semantics wall: when the operand is a CONSTANT and
the value fits the target type's range, Rust's integer `as` yields the same
value, so the store's exact decimal text IS the answer. The first blocker of
`src/c_backend.rs` (the flagship backend) was `const VER_M: i128 =
i64::MAX as i128;` — pure constant folding.

Lowered (all exact, no guessing):

- **A constant cast to an integer type** — the operand may be an integer
  literal (i128-parsed, so u64/u128 literals survive), a parser-level
  negative, a paren, a tabled `const`, a primitive assoc const
  (`u8::MAX`), or another constant cast. The value is range-checked
  against the target type's own range and, when it fits, emitted verbatim.
  Nesting composes left to right (`(u8::MAX as u16) as i64`).
- `const_int`/`const_int_i128` fold the same casts, so `const A: i128 =
  i64::MAX as i128;` lands in the int-const table and later constant uses
  (conditions, other casts) see the value.
- `infer_ty` types a cast to an integer type as `Int` (a float/pointer
  cast types as Unknown instead of pretending).

Refused loudly (t83/t84):

- a cast of a **non-constant** expression — a runtime value can wrap, and a
  wrapped u64 has no decimal-store representation, so a guessed truncation
  would be silently wrong;
- a constant cast that would **wrap** (`300 as u8`), named as such;
- a cast to a non-integer type (float/pointer), its own message.

One runtime note for future pins: the A1 carries the exact decimal text even
above 2^53, but the ESTree executor parses a numeric-looking value as a JS
number, so PRINTING `i64::MAX` through the JS path loses digits (caught by
the stdout oracle on the first draft of t82). The pin therefore stays inside
the double-exact range; the cast lowering itself is exact either way.

**Scoreboard movement (the histogram is the metric).** The first-blocker
class `expression kind `cast (`as`)`` is gone from the histogram; the
whole-file count stays 17/254, as the v0.20 note predicts. Frontiers moved:
`src/c_backend.rs` 7120 → 145 (`condition operand`), `src/cuda_backend.rs`
and `src/vulkan_backend.rs` still `.chars()` iteration, `fish/sh/zsh/rust_backend`
still `lazy_static!`, and the three `go/perl/zig_backend` files still stop at
`fallback_builtin_to_exec`, whose body needs `&mut`.

Pinned by t82 (executed stdout vs native rustc) and the refusal boundary
pins t83 (wrapping constant) / t84 (non-constant).

## v0.29 additions — the first COMPLETELY lowered backend file

The dogfood mission's first concrete win: **`src/vulkan_backend.rs` (334
lines) now lowers END TO END** — zero refusals, A1 accepted by the core,
ESTree rendered, JS evaluated. It is the first `src/*_backend.rs` file to
reach LOWERED. It was chosen by *measured frontier*, not by size: after
v0.28 the 13 `*_backend.rs` files stood at `c_backend.rs` 145,
`cuda_backend.rs` 8, `vulkan_backend.rs` 3 — the smallest path to a
finished file.

Classes lowered on the way, each exact:

- **Code-point iteration** — `for c in s.chars()` and `for line in
  s.lines()` materialise `strChars`/`strLines` into a temp array (the
  runtime carries Rust's semantics: Unicode SCALAR values, and `lines()`
  with `\r\n` stripped and no trailing empty line) and run the ordinary
  index loop over it.
- **`.chars()` / `.lines()` in VALUE position** — the same ops; `.next()`
  snapshots the sequence once, reads its length, and yields element 0 or
  the absent marker `""`.
- **`String::push(char)`** — a concat: rustc types the argument as `char`,
  so it is exactly one store element.
- **`is_ascii_alphanumeric` / `is_ascii_digit` on a char** — an anchored
  ASCII class (`globMatch`). The receiver is ONE char, both sets contain
  both cases and `[0-9]` has no case, so the answer is Rust's for every
  input. NOTE the A1 call order is `(value, pattern)`; the runtime
  signature is `globMatch(pattern, value)` and the emitter swaps.
- **`Vec<scalar>::contains`** — the ordered array cannot answer
  membership, so every `push` also maintains a parallel ASSOC SET
  (`<v>__set`) and membership is a key lookup folded into the store's
  text `"true"`/`"false"`. Exact because `push` is the only Vec mutation
  this frontend admits and a whole-variable REASSIGNMENT of such a Vec
  REFUSES (t89) rather than leaving a stale set.
- **`iter().enumerate().map(|(i, n)| (K, V)).collect()` into a map** —
  snapshot the source's elements, then one `assocSet` per element.
- **`for (i, x) in v.iter().enumerate()`** — the tuple pattern binds the
  loop index and the element.
- **`Option::copied`/`cloned`/`unwrap`** — identities (the store has no
  reference layer; a `None` `unwrap` panics in Rust, so there is no
  defined stdout to model). **`unwrap_or(d)`** is a conditional over the
  receiver: a variable, or a PURE store read hoisted once into a temp;
  anything with a side effect keeps its own refusal (t90).
- **`if` in VALUE position** (a `let` initializer, a `format!` argument) —
  a branch body is a STATEMENT LIST, so it lowers to a statement `If`
  writing a temp, emitted through a new pending-statement buffer that
  `lower_block` drains immediately BEFORE the statement that reads the
  temp (source order preserved; exactly one branch runs, as in Rust).

Three exactness bugs found by the pins, all fixed or documented:

1. The A1 **`Ternary` node is Perl-only IR** — the ESTree renderer panics
   on it. Value-level conditionals are therefore statement `If`s.
2. The emitter inlines a store read as `sh2.vars.x ?? ""`, which STRINGS
   an array: materialising an array variable must go through
   `arrayItems(x)` (a copy), never `getVar(x)`, or a 3-element source
   arrives as `"aa,b,cc"` and a loop runs once.
3. **`BTreeMap` iteration order is not modelled**: the store's assoc
   arrays iterate in INSERTION order, rustc iterates a `BTreeMap` in KEY
   order. Range-for over a map is exact for an insertion-ordered map
   (`HashMap`-style use) but not for a `BTreeMap`. This is a pre-existing
   runtime/representation limit, documented here instead of tuned away:
   no pin iterates a `BTreeMap`, and `vulkan_backend.rs` only *builds* one
   (`ext_idx`) — it never iterates it.

One robustness fix this surfaced: the method-dispatch table's struct/enum
receiver arms were `unreachable!`. A hoisted `if` condition can legitimately
reach them, and `src/c_backend.rs` then PANICKED instead of refusing; both
arms are now honest refusals ("method ... on enum receiver"), so every corpus
file still REFUSEs loudly rather than crashing (checked: zero `internal
error`/`panicked` lines across the 254-file run).

Scoreboard: **LOWERED 17 → 18 / 254**; `src/vulkan_backend.rs` moves from
3 first-blockers to 0. The frontend-stdout gate is **61/81 match, 0 FAIL,
20 SKIP** (was 57/75). Pinned by t85–t88 (executed stdout vs native
`rustc`) and the refusal boundary pins t89/t90.

## v0.30 additions — cuda_backend.rs frontier: 7 → 5, two registry bugs

The next-smallest backend file after v0.29 was `src/cuda_backend.rs`
(7 blocked units, first `assignment target` at line 214). Two of those
units were not Rust-idiom gaps at all — they were **registry bugs** in the
frontend that also silently weakened every other file:

1. **Self-referential types were dropped as `ambiguous`.** `ty_from_type`
   classifies a path by looking its name up in the enum/struct registry, so
   the field `body: Vec<CuSeqStmt>` inside `enum CuSeqStmt` typed as
   `Unknown` on the first registration pass and as `Arr(Enum)` on a later
   one (the same declaration is registered from the entry file, from the
   flattened mod tree and from the crate-wide declaration index). Two
   differing shapes for ONE declaration read as a real name collision, so
   `register_enum` removed the enum and marked it ambiguous — every
   `CuSeqStmt::While { .. }` pattern then refused with `struct pattern
   path`. A name is a declaration fact, so `predeclare_item` /
   `predeclare_types` now declare every enum and struct NAME (recursing
   into inline mods) before ANY shape is computed, in all three funnels.
   Resolution is order-independent; genuine collisions (same name, two
   different declarations) still go ambiguous and still refuse.
2. **`#[cfg(test)]` items with doc comments were not dropped.** The
   predicate required EVERY attribute to be `cfg(test)`, but `///`
   arrives in `attrs` as `#[doc = "..."]`, so a documented test helper
   looked like a semantic attribute and refused (`semantic attribute on a
   function`). `cfg_test_only` now accepts inert metadata (`doc`, `test`,
   `macro_export`, `allow`, `inline`, `deprecated`) beside the gate. The
   drop is applied at every funnel that can resurrect a test-only item:
   `flatten_items` (the flat item list), `defined_names` (call resolution),
   `collect_calls_in_items` (the demand set), the demand-pull extraction,
   `index_items` (the crate declaration index) and `predeclare_item`.
   This is exactly rustc's non-test behavior — such an item does not exist
   in the build we compile — and any call to one keeps refusing as an
   unknown function instead of reaching a missing definition.

`src/cuda_backend.rs` now clears `seq_lane_targets` and `undeclared_regs`
(the `#[cfg(test)]` register guard plus its `#[cfg(test)] mod tests`, which
has `#[test]` children and a `use super::*`). The remaining five units are
ALL the same wall, and it is a real one: `arith` / `divmod` / `seq_stmt`
(`*tmp += 1`, `*qlab += 1`), `seq_stmt`'s callers and `shir_to_cu_compute`
(`arith(..., &mut tmp, &mut used, &mut vlines)`) and `shir_to_cu_reduce`
(`for st in &spec.seq_prelude` then `seq_stmt(..., &mut qlab)`). The file's
whole API is `tmp: &mut u32`, `used: &mut BTreeSet<String>`, `out: &mut
Vec<String>`; the value store has no reference layer, so mutation through
a borrow keeps its own refusal (`&mut` borrow / `assignment target`).
`join` on an array receiver and `is_power_of_two` are the next first-blockers
beyond that, and neither can finish a unit while `&mut` is refused
(`divmod` hits `*tmp += 1` right after `is_power_of_two`, `shir_to_cu_compute`
hits `&mut` arguments right after `join`), so they stay refusals rather
than half-classes.

Measured (same repo state, binaries built from `17e7a5a` and from this
tree, `--census` over the 14 `src/*_backend.rs` files):

| file | before | after |
|---|---|---|
| `c_backend.rs` | 609 | 608 |
| `cuda_backend.rs` | 7 | **5** |
| `fish_backend.rs` | 164 | 164 |
| `glsl_backend.rs` | 90 | 90 |
| `go_backend.rs` | 125 | 125 |
| `java_backend.rs` | 67 | 64 |
| `lint_backend.rs` | 16 | 16 |
| `perl_backend.rs` | 49 | 49 |
| `python_backend.rs` | 140 | 140 |
| `rust_backend.rs` | 358 | 358 |
| `sh_backend.rs` | 70 | 70 |
| `vulkan_backend.rs` | 0 | 0 |
| `zig_backend.rs` | 63 | 63 |
| `zsh_backend.rs` | 78 | 78 |
| **total** | **1836** | **1830** |

Whole-tree dogfood unchanged at **LOWERED 18/254 (7.1%)** — `vulkan_backend.rs`
still `rc=0`, `cuda_backend.rs` still refuses, zero `internal error` /
`panicked` lines. Frontend-stdout gate **63/84 match, 0 FAIL, 21 SKIP**
(was 61/81, 0 FAIL, 20 SKIP). Pinned by t91 (self-referential enum struct
patterns, executed stdout vs native `rustc`) and t92 (documented
`#[cfg(test)]` helper + test mod dropped), with refusal pin t93 (`cfg`
that is not `cfg(test)` still refuses — it selects code for a build
configuration we cannot know).

### Known inexactness found while pinning t91 (CLOSED in v0.31 below)

A **struct or enum value does not survive a free-function parameter
boundary**: `fn get(p: &P) -> i64 { p.x }` + `get(&P{x:5})` prints `0`
where rustc prints `5`, and an enum argument makes `match` fall through to
`undefined`. The parameter prologue binds `param <- getVar("i+1")`, which
the emitter inlines as `String(sh2.positional[i] ?? "")` — text. Arrays
(`arrayLen`/`arrayItems` by name) and `&self` method dispatch are fine;
only record payloads are stringified. This is PRE-EXISTING and orthogonal
to lowering: it does not change a single refusal, but it means LOWERED is
not a fidelity claim for a file whose functions pass structs/enums (e.g.
`vulkan_backend.rs` calls `arith(&st.value, &ext_idx)`), which is why no
pin in this suite passes a record across a call. **v0.31 closes this** from
the frontend side (the parameter-prologue fix below); the reproducer there
is now a pin.

## v0.31 additions — record arguments cross the call boundary

LOWERED is now a fidelity claim for a file that passes structs/enums
between functions. The reproducer from v0.30 prints rustc's answer:

```rust
struct P { x: i64 }
fn get(p: &P) -> i64 { p.x }
fn main() { let p = P { x: 5 }; println!("{}", get(&p)); }   // 5 (was 0)
```

Cause: the parameter prologue emitted `param := getVar("1")`, and a
literal-digit `getVar` is a **text-mode** read — the core inlines it as
`String(sh2.positional[0] ?? "")`. That `String()` is right for every shell
value and destroys a record (the callee received `"[object Object]"`).

Fix (frontend only, no core change): a **record-typed** parameter
(`Ty::Struct`, `Ty::Enum`, and `Ty::Opt` of either) is bound through
`record_param_bind`, which hands `getVar` the index as an *arithmetic*
value instead of a literal string. That keeps the call generic —
`sh2.getVar(1)` — and the runtime answers it with the positional **exactly
as the caller passed it**: the same text every primitive gets (argv entries
are text anyway) and the object itself for a record. Every other parameter
keeps today's text read byte for byte: primitives, arrays, maps,
`Option<scalar>`, shared references to primitives and `&self` method
receivers (`self` is still bound by the receiver branch's own read, so
method dispatch is untouched). `Option<record>` moves too — its `None` is
the store's empty value, where a text read and a value read agree — because
leaving it behind would have kept the same silent wrongness for half the
record shapes.

Shapes covered, each its own pin and each executed stdout-exact against
native `rustc`:

| pin | shape | rustc stdout |
|---|---|---|
| t94 | struct arg by `&` | `12` |
| t95 | enum arg by `&` (payload variants) | `9`, `20` |
| t96 | struct field reads through the param (loop bound + condition) | `3 2 1`, `y=2` |
| t97 | enum MATCHED through the param (payload + unit) | `move 2 3`, `quit`, `peek 9 8` |
| t98 | two record args in one call | `309`, `102` |
| t99 | record param passed onward (three hops) | `42` |
| t100 | `Option<record>` param (`Some` + `None`) | `5`, `0` |

One record-passing shape genuinely stays unsupported and keeps a refusal
pin: **`&mut` record** (t101 — `fn bump(p: &mut P)`, refused as `` `&mut`
borrow (mutation through a borrow is not representable) ``), for the same
reason `&mut` scalars refuse: the value store has no reference layer.
Tuple structs are unaffected either way (their constructors refuse before
any argument crosses a call).

Measured, baseline binary built from `f54a335` vs this tree:

- record reproducer: `0` (struct) / `undefined` (enum) → **`5`** / payload exact;
- frontend-stdout gate: **63/84 match, 0 FAIL, 21 SKIP** → **70/92 match, 0 FAIL, 22 SKIP**
  (the 7 new matches are t94–t100; the extra SKIP is the t101 refusal pin);
- `--census` over the 14 `src/*_backend.rs`: **1830 → 1830** blocked units,
  every file byte-identical in count (`c_backend.rs` 608, `cuda_backend.rs` 5,
  `vulkan_backend.rs` 0, …) — no unit changed status, so nothing that used to
  lower "thanks to" the stringified read now refuses;
- whole-tree dogfood: **LOWERED 18/254**, and the LOWERED *set* is identical
  file-for-file; `vulkan_backend.rs` still `rc=0`, zero `internal error` /
  `panicked` lines. Note `vulkan_backend.rs` is exactly the case that
  improves: it calls `arith(&st.value, &ext_idx)` with a record payload.

The `&mut` wall that keeps `cuda_backend.rs` at 5 blocked units is unchanged
(see v0.30): `*tmp += 1`, `*qlab += 1` and `&mut` arguments have no exact
form in the value store, so `join`/`is_power_of_two` still stay refusals
rather than half-classes.

## v0.32 additions — `Self::m` associated-function calls

Inside an `impl` block, `Self::helper(..)` names exactly the item
`Type::helper` names: the impl's own associated fn, registered under the
mangled `Type_helper`. The resolver only knew the explicit spelling, so it
looked up `Self_helper`, which no impl ever registers — every
associated-function helper call was an `unknown function` refusal.
`c_backend.rs` spells `Self::` on 511 lines, `python_backend.rs` on 274,
`rust_backend.rs` on 234.

This is name resolution, not a new lowering path. `call_target` resolves
`Self::m` against the enclosing impl's type (`Cx::self_ty`, set by
`function_def` and cleared for free fns), and `infer_ty` gives a
`Self::m(..)` call the registered def's declared return Ty. The emitted
call is byte-identical to the `Type::m` spelling; no runtime shape
changes.

| pin | shape | native stdout |
|---|---|---|
| t102 | `Self::helper` from a `&self` method, bound to a `let` | `43` |
| t103 | `Self::helper` in TAIL position, String result | `336` |
| t104 | `Self::helper` from a receiver-less associated fn, two args + field args | `14`, `3` |

Refusal pins:

- t105 — `Self::missing(..)` with no registered `Type_m` stays
  `call to unknown function`: resolution never guesses a target.
- t106 — an **enum value receiver** (`w.rank()` where `w: Width`) stays
  refused. Enum method dispatch would bind `self` into the shared store,
  and the limitation documented in v0.8 (a callee's parameter names
  clobber the caller's variables) makes a method that calls another
  method silently wrong: the reproducer
  `Width::U8.widen(Width::U64)` printed `u64 u8` against rustc's
  `u64 u64`. Until per-function locals land, enum receivers stay out;
  struct receivers keep exactly the path they took before.

Measured against `ae27361`:

- frontend-stdout gate: **70/92 match, 0 FAIL, 22 SKIP** -> **73/97 match,
  0 FAIL, 24 SKIP** (t102-t104 match, t105/t106 are the new refusal pins);
- `--census` over the 14 `src/*_backend.rs`: **1830 -> 1815** blocked
  units — `c_backend.rs` 608 -> 602, `rust_backend.rs` 358 -> 352,
  `python_backend.rs` 140 -> 139, `go_backend.rs` 125 -> 124,
  `zig_backend.rs` 63 -> 62, every other file unchanged (no `Self::`
  refusal is left in the corpus);
- whole-tree dogfood: **LOWERED 18/254**, first-blocker histogram
  unchanged (the backends' first blockers are other classes: enum
  receiver, `&mut`, item macros), zero `internal error` / `panicked`
  lines, `vulkan_backend.rs` still `rc=0`.

## v0.33 additions — the item-macro / global-state class

The largest remaining first-blocker cluster was the one thing every
backend does at the top of its file: declare mutable program state.

```rust
lazy_static::lazy_static! {
    static ref NAMES: std::sync::Mutex<HashSet<String>> = Default::default();
    static ref NOCASEMATCH: std::sync::Mutex<bool> = std::sync::Mutex::new(false);
}
static COUNT: AtomicUsize = AtomicUsize::new(0);
thread_local! { static DEPTH: Cell<i64> = Cell::new(0); }
```

Three separate holes produced it: `ty_from_type` had no arm for the
interior-mutability wrappers, so `Mutex<T>` was `Unknown`; `Mutex::new`,
`Cell::new` and `AtomicUsize::new` were unknown call targets; and
`static`/`lazy_static!`/`thread_local!` refused at the item. Every access
to the state then refused as well, so each file stopped on line ~16-25.

### The lowering: erase the wrapper, keep the state

JS has one thread, so `Mutex`/`RwLock`/`Cell`/`RefCell` are vacuous and
an atomic is a plain number. The *state* is real, and the A1 store is
exactly a global mutable slot, so:

- `Ty::Cel(inner)` — `Mutex<T>`, `RwLock<T>`, `Cell<T>`, `RefCell<T>`
  (the wrapper erased, `T` kept); `Arc<T>`/`Rc<T>` are transparent;
  `AtomicUsize`/`U64`/`U32`/`I64`/`I32`/`U8`/`I8`/`AtomicBool` map to
  `Int`/`Bool`.
- `Ty::Guard(inner)` — what `lock()`/`read()`/`write()`/`borrow()`/
  `borrow_mut()` yields; `unwrap()` peels it, so `*S.lock().unwrap()`
  has the static's type.
- A static's lifetime IS a store global: the declaration emits one
  initializer statement at program start (`static_init_stmt`), never a
  per-call re-init. `static` items, `lazy_static!` (`static ref`) and
  `thread_local!` (`static`) all register the same way; the macros are
  parsed for their items and have no other effect.
- Storage per type: `Set`/`Map` statics use the **named associative
  store** (`assocEmpty`/`assocSet`/`assocGet`/`assocDel`/`arrayLen` —
  the same store a `let mut m = HashSet::new()` uses), `Vec` statics a
  native array, scalar statics their store slot. `Default::default()`
  is the type's zero value (`0`, `false`, `""`, empty assoc).
- `let g = S.lock().unwrap();` binds a **handle on the static**, not a
  copy: `cx.guards` maps `g -> (S, T)` for the function's scope, so a
  write through `g` is visible to the next `lock()` exactly like Rust.

### The operations (each one Rust's own answer, not an approximation)

| rust | lowered to |
|---|---|
| `S.lock().unwrap().insert(k)` | `assocSet(S,k,"1")`, value = "was it absent" |
| `S.lock().unwrap().remove(k)` | read, `assocDel(S,k)`, value = previous |
| `contains` / `contains_key` | `assocGet(S,k) != ""` |
| `len` / `is_empty` | `arrayLen(S)` / `arrayLen(S) == 0` |
| `clear()` | the assoc reset (empty store) |
| `get(k)` | `assocGet(S,k)` (the `None ≡ ""` convention) |
| `*g = v`, `*g += e`, `*g` | store assign / arithmetic update / store read |
| `g == x`, `g.get()` | store read in a test / in arithmetic |
| `a.load(o)` / `a.store(v,o)` | store read / store write |
| `a.fetch_add(e,o)`, `fetch_sub`, `swap` | read into a temp, update, value = OLD |
| `C.with(|c| c.get())` | store read |
| `C.with(|c| c.set(v))` | store write |
| `C.with(|c| c.replace(v))` | evaluate `v`, read old, write, value = old |

The `Ordering` argument of an atomic has no observable effect with one
thread (Rust constrains only cross-thread order), so it is dropped where
rustc can observe nothing. Nothing else is guessed.

### Kept refused, with the cause

- **A static collection has no value form** (pin `t110`): `.clone()` of a
  `Mutex<HashSet<..>>` static would need a deep copy of a *named* assoc
  under a new name, and `.iter()` order is the store's insertion order,
  not the set's — a HashSet has no order Rust can observe. Both refuse
  (`clone/iter of a collection`) instead of producing a second store.
  `Mutex<Option<T>>.take()`/`.as_ref()` chains refuse for the same
  reason: an option-of-collection would need a copy.
- **`include!(concat!(env!("OUT_DIR"), ..))`** in
  `render_ext_estree/mod.rs` stays refused: the included code is
  generated at build time and is not in the source, so there is nothing
  to lower. That file keeps its 2 blocked units.

### Two correctness fixes this required

- `static` initializers used to be lowered with plain `value_expr`,
  which is why `Mutex::new(0)` was an *unknown function* refusal even for
  a legal static; they now go through `static_init_stmt`.
- A function's **tail expression** dropped its hoisted statements, so an
  atomic RMW in tail position read the temp before writing it (the
  counter silently kept its old value). `function_def` now drains
  `pending` before the `Return`.

`const` items also fold integer expressions now (`1 << 53`, bit ops,
checked), which removed the blocker that stood after this class on the
JS path.

### Measured

Gate (`harness/frontend-stdout.sh rust`, native `rustc` vs A1→ESTree→JS):

| pin | shape | native stdout |
|---|---|---|
| t107 | atomic statics: `fetch_add`/`fetch_sub`/`swap`/`store`/`load` in `main` and in a tail slot | `0 3 5 100 5` |
| t108 | `Mutex<i64>`/`Mutex<bool>`: alias handle, `*g += n`, `*g = v`, `*g` read, `*g == v`, `if *g` | `5 7 on seven` |
| t109 | `thread_local!` `Cell<i64>`: `with(\|c\| c.set(c.get()+1))`, `replace`, `get` | `2 10 42` |

t110 is the refusal pin above. Gate: **73/97 match, 0 FAIL, 24 SKIP** ->
**76/101 match, 0 FAIL, 25 SKIP**.

`--census` over the 14 `src/*_backend.rs`: **1815 -> 1801** blocked units
— `fish_backend.rs` 164 -> 159, `sh_backend.rs` 70 -> 66,
`zsh_backend.rs` 78 -> 76, `rust_backend.rs` 352 -> 349, the other ten
unchanged. No file's first blocker is `lazy_static!`/`thread_local!`/
`Mutex::new`/`AtomicUsize::new` any more; they now stop on other classes
(fish/sh/zsh on nested item declarations, rust on a condition operand,
c on an enum receiver).

The JS path itself moved the most: `shir.rs` refused at
`Mutex::new` (line 25) in whole-program mode and now refuses at
`unwrap_or` (line 45); `estree.rs` is unchanged (93, the
iterator-length class).

Whole-tree dogfood: **LOWERED 18/254**, `EXIT=0`, no `internal error`
and no `panicked` line, and no `lazy_static!`/`thread_local!`/
`Mutex::new`/`AtomicUsize::new` first blocker anywhere in the tree.

What is *not* proven by execution: a `lazy_static!` set static cannot be
compiled by the gate's rustc (no crates are available to it), so the
collection branch is pinned by composition — the assoc primitives
(`t81`) plus the handle layer (`t108`) — while the runnable pins cover
atomics, `Mutex` scalars and `thread_local!` `Cell`. A candidate pin that
re-locks a `Mutex` while the guard is alive deadlocks native rustc and
was removed for that reason, not because the lowering failed.

## v0.34 additions — the Option/Result combinator algebra

The JS path stopped at `unwrap_or` (shir.rs line 45, whole-program) and the
census showed the whole combinator surface as the largest cluster behind
it: `unwrap_or` 32, `map_or` 17, `map` 15, `and_then` 21, `or_else` 3,
`unwrap_or_else` 3, `ok_or` 4 across the tree. They are lowered as ONE
value algebra, not a text substitution.

### The value model (unchanged, now used everywhere)

`Some(v)` / `Ok(v)` **IS** `v`; `None` / `Err(_)` **IS** the absent marker
`""`. So a combinator is a *branch on presence*, and presence is the
store test `"$x" != ""`.

### Evaluation order (the part that must not be guessed)

Rust's combinators differ in WHEN the argument runs, and the lowering
follows each one:

| form | argument timing | lowering |
|---|---|---|
| `unwrap_or(d)` | eager (it is an argument) | `d` hoists BEFORE the branch |
| `map_or(d, f)` | `d` eager, `f` only when present | `d` hoists, `f` in the then-branch |
| `unwrap_or_else(f)` | lazy, only when absent | `f` in the ELSE branch |
| `or_else(f)` | lazy, only when absent | `f` in the ELSE branch |
| `map(f)` / `and_then(f)` / `is_some_and(f)` / `ok_or(e)` | only when present | `f` in the then-branch |
| `unwrap_or_default()` | the TYPE's zero (0 for `Option<i64>`) | a constant |

`branch_value` is the helper: it records the pending length before each
branch, hoists the branch's own statements into that branch, and returns
the temp both branches store. An argument that would run eagerly is
lowered BEFORE `branch_value` is entered, so a side effect in `d` happens
whether or not the value is present — as in Rust.

Closures are INLINED with their parameter renamed to a fresh store temp
(`RenameIdent` over syn's `visit_mut`), so a closure body can never
capture or clobber an outer binding of the same name. A `move`/`async`
closure, a pattern parameter, a body that rebinds its parameter, and a
body containing `?` all stay refused (the parameter would become a
different variable, or the `?` would bind to the caller's function).

`Result<T, E>` maps onto the same model as `Ty::Opt(T)` (Ok ≡ Some,
Err ≡ absent) — the convention `?`/`ok()` already assumed. A unit-ish
`Ok(())` stays `Ty::Unknown`: both `Ok(())` and `Err(..)` are `""`, which
is exactly what the store can express.

### `any`/`all` in VALUE position

`.iter().any(P)` / `.all(P)` used to lower only as a `let` initializer or
an `if` condition; in a match-arm body or a call argument they refused
(`method `any` on array receiver`). They now lower through the same
bounded loop, hoisting its setup + iterations into `pending` when the
result is read by the enclosing statement. Refusals kept inside the
class: a function POINTER as the predicate (`any(Self::f)` — a fn item is
not a closure in this IR — pin `t116`), a tuple-pattern parameter, and a
parameter that shadows a store binding.

### Two bugs this exposed (both fixed)

- **A function body dropped its hoisted statements.** `lower_block`
  drains `pending` before each statement, but the function-body loop
  called `lower_stmt` directly — so `let a = opt.unwrap_or(d)` inside a
  fn WITH parameters assigned `a` from a temp that was never set (it read
  `null`). The gate caught it (t57). `lower_fn_stmt` now performs the same
  drain for function bodies.
- **`unwrap_or_default()` returned the absent marker** instead of the
  type's zero, so `Option<i64>` printed `""` where Rust prints `0`.

### Kept refused, with the cause

- **A `Result`'s ERROR payload is not representable** (pin `t114`).
  `unwrap_or_else(|e| ..)`, `.err()`, `unwrap_err`, `map_err`, `ok_or_else`
  with a payload consumer, and `Err(e)` patterns all observe the error
  value; the store carries Err as the absent marker and nothing else, so
  they refuse rather than silently dropping the payload. `is_ok_and`,
  `unwrap_or`, `map`, `and_then`, `flatten` and `ok()` (which DISCARD the
  error) do lower. `ok_or(e)` is refused too (pin `t115`): it answers a
  `Result` whose error payload has no place in the store, and evaluating
  `e` for its side effects and then dropping it is only sound while
  nothing observes it — which the frontend cannot see.
- **`strip_prefix`/`strip_suffix` values stay refused** (pin
  `t113_strip_affix_refuse`). The affix lowering is a conditional
  EXPRESSION and the ESTree renderer has no conditional expression (only
  branch statements); it also reads the affix length through a `length`
  METHOD call, which JS renders as `p.length()` (a property there). Even
  fixed, Rust's strip is BYTE-based while JS slicing counts UTF-16 code
  units, so a length-based slice is a DIFFERENT string for non-ASCII
  input — a silent mistranslation, not a lowering. Typing the result
  `Option<&str>` (which is what it is) was implemented, measured, and
  reverted: it turned a clean refusal into IR the JS renderer cannot
  render.
- Combinator chains whose closure body is a statement block, that rebind
  the closure parameter, or that contain `?`, and iterator laziness in
  `.map(..).collect()` pipelines, refuse as before.

### Measured

Gate (`harness/frontend-stdout.sh rust`, native `rustc` vs
A1->ESTree->JS), pins `t111`/`t112` executing byte for byte:

| pin | shape | native stdout |
|---|---|---|
| t111 | every combinator on `Option` and `Result`, present and absent, incl. `map_or`, `and_then`, `or_else`, `unwrap_or_else`, `unwrap_or_default`, `is_some_and`, `is_ok_and`, `flatten` | `5 9 7 1 10 0 6 0 true false true false -1 8 5 0 5 42 4 -2 hi` |
| t112 | `any`/`all` in value position (call argument), strings and ints, empty collections | `true false true true false true` |

`t113_strip_affix_refuse`, `t114_option_err_payload_refuse`,
`t115_ok_or_refuse` and `t116_fn_ptr_quantifier_refuse` are the refusal
pins (measured: `ok_or` and a fn-item predicate both refuse, `flatten`
lowers and matches `rustc` on `4 -2`). Gate: **76/101 -> 78/107 match, 0 FAIL, 29 SKIP**.

`--census` over the 14 `src/*_backend.rs`: **1801 -> 1795** blocked units
(`c_backend.rs` 602 -> 599, `fish_backend.rs` 159 -> 158,
`glsl_backend.rs` 90 -> 89, `python_backend.rs` 139 -> 138; the other ten
unchanged). The cluster shrank much further than the total, because many
of these units now die on the NEXT wall instead of the combinator one:
combinator-shaped refusals 110 -> 97 across the 14 files, `any`/`all`
refusals 27 -> 11.

The JS path itself (this is the target): `shir.rs` **802 -> 759**, first
whole-program blocker `unwrap_or` (line 45) -> `trim_start_matches`
(line 185); its combinator-shaped refusals **102 -> 42** (shir.rs +
estree.rs) and `any`/`all` refusals 36 -> 22. `estree.rs` **93 -> 92**
(the iterator-length class still leads). There is no `js_backend.rs`
anywhere in the corpus — the JS path IS `shir.rs`, `estree.rs`,
`render_ext_estree/mod.rs` and `shir_json*.rs` / `bin/shir_render.rs`;
`render_ext_estree/mod.rs` stays at 2 (`include!` of generated code).

Whole-tree dogfood (`harness/rust-dogfood.sh`, 254 files): **LOWERED
18/254, EXIT=0**, no `internal error` and no `panicked` line — unchanged
from v0.33, and here is exactly why, measured rather than assumed:

- Tree-wide, no file's FIRST blocker is a combinator any more except two,
  and both are refused BY RULE, not by omission: `bc.rs` stops at
  `ok_or_else(|| format!(..))` — a LAZY payload constructor, refused with
  `ok_or` because the closure's value is the error payload (pin `t115`);
  `grep.rs` stops at `grep_boolean(call).map(NativeEmit::Cond)` — a
  FN ITEM as the closure argument (pin `t116`). All other combinator
  refusals in the tree are below some earlier blocker in the same
  function.
- The seven files closest to a flip (`bin/{c,java,perl,python,sh}_backend`,
  `bin/sh2glsl`, `bin/shir_to_c`, one unit each) all stop at
  `env::args().collect()`. The JS runner models process arguments
  (`--name`/`--args` set `$0`/`$1..`), but there is no A1 op that reads
  them as an ARRAY, so `collect()` on `std::env::args()` has nothing to
  lower to. Refused rather than synthesised: a wrong argv array would
  silently change every driver's behaviour.

What is *not* proven by execution: `map_or`'s eager-default rule and the
lazy `or_else` branch are proven by the pins' stdout (an eager default
with a side effect is covered by `t90_unwrap_or_impure_refuse`, which
keeps the *impure* default refused), but no pin observes the ORDER of two
side effects inside a combinator chain — the argument-eagerness rule is
enforced structurally (arguments lower before the branch) rather than
measured.

## v0.35 additions — the process argument vector as a value

The seven files closest to a full lowering (`bin/{c,java,perl,python,sh}_backend`,
`bin/sh2glsl`, `bin/shir_to_c`) all stopped at `env::args().collect()`: the IR
had no expression that names the process argument vector, so a program could
not read its own argv and no pin could exercise one.

### The node (core, `src/shir_nodes/argv.node`)

`Argv` is a fieldless IR expression: the process argument vector as ONE
array value, element 0 = the program name as invoked (`sh2.argv0`), then the
arguments in OS order (`sh2.positional`). The ESTree renderer emits
`[sh2.argv0].concat(sh2.positional)` — a FRESH array, so a later write to
the caller's binding cannot reach the runtime's positional store (a shell
`shift`/`set --` rebinds `sh2.positional` and must not be able to rewrite a
Rust program's snapshot). The value is immutable input: argv is fixed
before `main`, so reading it twice in one run yields the same elements —
there is no aliasing or lifetime question to model.

### The frontend lowering

`std::env::args()` / `env::args()` is typed `Vec<String>`'s element type
(`Ty::Arr(Str)`) and lowered to `Argv`; `collect()` on it binds that value
directly (identity, no loop, no copy step), so `len`, indexing and
iteration over the result are the ordinary array operations. `first`/`last`
answer `Option<&elem>` — the element, or the absent marker when the array
is empty, with the length read ONCE and the index guarded so neither end
can be overrun. `str::is_empty()` is exact for every encoding (a string is
empty iff its stored text is `""`), and a map/set `is_empty()` now answers
in VALUE position too (its length is the same runtime count).

### Proven by execution

`t117_argv` — collects argv, prints its length, iterates it and counts:
with **no** arguments the gate sees `1 true 1` on both sides; with
identical arguments fed to both sides
(`estree-runner.mjs … --name <binary> --args a b` vs `<binary> a b`) both
print `3 true a b 3`, so argument CONTENT flows through the A1→ESTree→JS
path in OS order. The gate pin deliberately prints nothing that includes
argv[0], because a compiled binary's path and the runner's `--name` are
different strings by construction; that is a property of the harness, not
of the lowering.

`t118_edge_accessors` — `first`/`last` on a non-empty and an empty vector
plus `is_empty` on `""` and on a non-empty `String`: `a bb none none true false`.

### Kept refused, with the cause

- **`as_bytes()` stays refused** (pin `t119`). It is the largest remaining
  string cluster, and it is not a typing problem: a Rust `&str` is a BYTE
  view while the store's text is a JS string of UTF-16 code units, so
  `bytes[i]` and `bytes.len()` differ from the code-unit read for every
  non-ASCII input — exactly the inputs the corpus has (latin-1 fixtures
  included). The byte view needs an IR representation of its own.
- `env::args_os()` (OS strings, no text model), `env::args().nth(K)` and
  a `.skip(K)` chain in a `for` loop stay refused for now: `nth`/`skip`
  consume the iterator in Rust, and the store's array is a snapshot — the
  consuming forms need an explicit cursor, not a silent reuse.
- Everything refused in v0.34 stays refused for the reasons written there
  (Result error payloads, `ok_or`/`ok_or_else`, fn-item predicates,
  `strip_prefix`/`strip_suffix` values).

### Measured

Gate: **78/107 -> 80/110 match, 0 FAIL, 30 SKIP** (`t119_as_bytes_refuse`
is the new refusal pin; `t117`/`t118` execute and match native `rustc`
byte for byte).

`--census` over the 14 `src/*_backend.rs`: **1795 -> 1793** blocked units
(`c_backend.rs` 599 -> 598, `zig_backend.rs` 62 -> 61); the JS path
`shir.rs` **759 -> 758** and `estree.rs` **92 -> 92** (unchanged, see
below). The two lost units are files whose whole function turned green
from the empties/edges family; the near-flip files' first blockers moved
on (`eprintln!` in five of them, `nth`/`skip`/`expect` in the rest) but
their next blocker is a `debashl` crate call (`Parser::new`,
`std::fs::read`, `crate::shir::ast_to_ir`) that no frontend can lower, so
their unit count is 1 either way.

`estree.rs` did not move this turn, and the reason is measured rather than
assumed: every short unit in it has a SECOND blocker behind the argv/
iterator one — `ast_to_estree`/`ast_to_estree_json` (5 lines each) call
`crate::shir::ast_to_ir`, the `matches!`-with-guard family
(`is_ident_expr`, `is_sh2_call`, `is_last_exit_member`) needs a payload
FIELD read that the enum model does not carry (a variant with a payload is
not stored as a tag string), and `is_positional_name` is the
`strip_prefix` value refused in v0.34.

Whole-tree dogfood (`harness/rust-dogfood.sh`, 254 files): **LOWERED
18/254 — unchanged, and no file flipped this turn**, `EXIT=0`, no
`internal error` and no `panicked` line. The measured reason, not a
guess: lowering a file needs EVERY function in it to lower, and each of
the near-flip files has exactly one refusing function (`main`) whose
remaining blockers are, in order after argv: `eprintln!` (c/fish-go
style drivers), `.nth(1)`/`.skip(1)`/`.expect(..)`, and then a `debashl`
crate call — `Parser::new(..)`, `std::fs::read(..)`,
`crate::shir::ast_to_ir(..)` — which the frontend cannot resolve at all,
so those units stay at 1 whether argv lowers or not. What DID move is the
first blocker on every one of them (from `env::args().collect()` to the
next honest wall), which is the frontier the next class should attack.

## v0.36 additions — stderr as a typed output channel

`stderr` had no representation in the IR at all: the shell frontends reach it
through `>&2` and the runtime's diagnostics, but a batch frontend's program
could not write there, so the five drivers whose first blocker was
`eprintln!` had no honest path forward.

### The node (core, `src/shir_nodes/err_line.node`)

`ErrLine { value }` is an IR expression whose value is a formatted string; the
ESTree renderer emits `sh2.warn(value)` — the namespace member the shell path
already uses for `>&2`, which writes `String(v ?? "") + "\n"` to stderr and
leaves stdout untouched. Rendered code may only call whitelisted `sh2.<name>`
members, so a raw `process.stderr.write` was not an option: the channel has to
go through the namespace. The operand is built by the SAME formatter as a
stdout print (`format_value`: literal segments, `{}`, `{name}` captures, with
width/precision specs refused), so a diagnostic line cannot silently use a
different rendering than the print next to it. The trailing newline is part of
the node's contract, not the caller's string.

### The frontend lowering

`eprintln!` lowers to `ErrLine(<formatted value>)`. `eprint!` stays refused
(pin `t122`): it is the same channel WITHOUT the newline, the only stderr
member always appends one, and adding a newline the source does not emit would
be a behaviour change, not a lowering. `io::stderr().write_all(..)` and
`write!(io::stderr(), ..)` stay refused (pin `t123`): the first writes BYTES
(a byte view the store does not have) and both return a `Result` the program
may inspect. `stderr().lock()` stays refused with the same byte/Result reason.
`{:?}` on a non-proven integer stays refused (Debug is a different renderer),
identically on both channels.

`std::process::exit(N)` also landed in this pass (it is the next blocker in
four of the five drivers): the A1 `Exit` statement is exactly this call
(`process.exit(Number(N))`), the status is its only observable, and value
position cannot occur because the call's type is `!`. `nth(K)` on the
argument snapshot builds on v0.35: a CONSTANT `K` is a guarded index over a
fresh array (length read once, element only when `K < len`, absent marker
otherwise) — there is no iterator position to consume. A non-constant `K`,
and any consuming `skip(K)` chain bound to a variable, stay refused: those
need an explicit cursor, which is a different model, not a silent index.

### Proven by execution, both streams

`t120_stderr` interleaves `println!` and `eprintln!` (a literal, an
interpolated `String`, and a final line). Native `rustc` and the JS run agree
BYTE FOR BYTE on both streams: stdout `start|ok|`, stderr
`usage: dump_ast <script.sh>|reading file.sh|done|`.

`t121_argv_nth_exit` is the full channel test: `args().nth(1)` with a
`match`, `eprintln!` and `exit(2)` on the missing path, `println!` on the
present one. With NO argument both sides print nothing on stdout, write
`usage: need a name\n` to stderr and exit **2**. With one argument
(`estree-runner.mjs … --args alpha` vs `<binary> alpha`) both print
`name: alpha` on stdout, nothing on stderr, and exit **0**. The exit status is
compared as well as the two streams, so "it printed the usage line" is not
mistaken for "it took the failure path".

This turn also fixed an ORDERING bug the new pins exposed: a `match`
scrutinee whose value needs hoisted temps (a guarded index, a branch value)
had its binding statement emitted BEFORE the temps it reads, so
`match args().nth(1) { .. }` matched on the temp's initializer instead of the
element. `match_value_node` now evaluates the scrutinee first, flushes its
hoisted statements, and only then emits the binding — the general rule for
every caller of `value_expr`.

### Measured

Gate: **80/110 -> 82/114 match, 0 FAIL, 32 SKIP** (`t120`/`t121` execute and
match native `rustc` on stdout AND stderr with identical argv; `t122`/`t123`
are the new refusal pins).

`--census` over the 14 `src/*_backend.rs`: **1793 -> 1793, unchanged**, and
this is measured rather than assumed: no unit in those files first-blocks on
`eprintln!`/`eprint!`/`process::exit`/`nth`/`skip`, so the new capabilities
complete no unit there. The units that DO first-block on `is_empty` are all
`match`-payload bindings (`ArithAst::Var(n)`) whose next refusal is
`n.chars().all(..)` — an enum payload field read the model does not carry —
so lowering their `is_empty` would move the blocker without completing them.
JS path: `shir.rs` **758 -> 758** and `estree.rs` **92 -> 92** (unchanged);
`render_ext_estree/mod.rs` 2. Quoted proof lines and the reason are in the
report; the per-file first-blocker table is reproducible with
`for f in $(find /tmp/src-base/src -name '*.rs'); do ./target/release/rust-frontend --shir "$f"; done`.

Whole-tree dogfood (`harness/rust-dogfood.sh`, 254 files): **LOWERED 18/254,
EXIT=0**, no `internal error` and no `panicked` line. What moved is the
frontier: five drivers' first blocker went from `eprintln!` to
`std::process::exit` — which this turn lowered — and their next walls are
`.nth(1)` (now lowered for a constant), `.skip(1)` (cursor model, refused),
`.expect(..)`, the `debashl` crate calls (`Parser::new(..)`,
`std::fs::read(..)`, `crate::shir::ast_to_ir(..)`), so their unit count is 1
either way and no file can flip.

### The per-file structural proof (whole tree, 254 files)

`DOGFOOD_BLOCKERS.tsv` records, for every one of the **236 still-coded**
files, the refusing source LINE and the first blocker verbatim (regenerate
with `find /tmp/src-base/src -name '*.rs' | xargs -P8 -n1 <run
'rust-frontend --shir F'>`). Its measurement, not its assertion:

- **57 of the 236** first-block on a callee that does not exist in this
  frontend, i.e. a `::`-qualified path with no symbol table behind it:
  - `render_ext_expr/handlers/*.rs:7` — **23 files**, each stopping on
    `super::all_nodes::<node>` (the generated handler modules call a module
    this frontend never resolves);
  - `render_ext_estree/handlers/*.rs:7..12` — **10 files**, stopping on
    `crate::shir::expr_to_estree_pub` (`address_of`, `affix_strip`,
    `clone_deep`, `cutset_trim`, `deref`, `element_read`, `regexp_find`,
    `spread`, `type_assert`, plus `cgo_call.rs:10 crate::estree::str_lit`);
  - the four backend drivers and `zig_backend.rs:180`
    (`crate::transforms::builtin::fallback_builtin_to_exec`), the five
    `pipeline_native/capabilities/*.rs:22..49` (`crate::ir::exec_word_args`,
    `crate::ir::ir_expr_to_perl`), and the one-off crate calls:
    `lexer.rs:431 Token::lexer`, `lint_backend.rs:39` and
    `shir_passes/mod.rs:122 PassContext::default`,
    `transforms/planner.rs:45 Capabilities::default`,
    `shir_nodes/enc.rs:12 Value::Array`, `shared_utils.rs:23 fs::read`,
    `shir_passes/strip.rs:79 std::mem::take`,
    `shir_passes/interval.rs:59` and
    `shir_passes/list_profile/census.rs:105 crate::shir::analyze_var_ranges`,
    `transforms/arith_identity.rs:101 crate::shir::source_lang`,
    `transforms/echo_return.rs:76 crate::shir_passes::scc::build_call_graph_stmts`,
    `transforms/test_lowering.rs:83 crate::shir::walk_lastexit_liveness`.
- **3 files** stop on a bare call to a function defined in the same file
  (`ir_expr_to_perl` x2, `census`), which a local symbol table would resolve.
- **176 files** stop on a language feature, not a callee; the top classes
  are `method `X` on receiver` (20), `&mut` borrow (14), `assignment
  target` (10), `nested item declarations` (10), `condition
  expression`/`negated condition expression` (9+9), `binary operator in
  arithmetic` (9), `condition operand` (8).

A file flips only when EVERY function in it lowers, so a crate-callee file
cannot flip from any frontend-side typing work: the symbol is simply not
there. That is the measured reason LOWERED is 18/254 and not a claim that
"stderr did not help".

The JS path is measured the same way, with the census lines quoted verbatim
(columns are TAB separated):

```
CENSUS	fn	lang_has_exit_status	unsupported Rust: method `trim_start_matches` on string receiver (no proven JS-equivalent yet) (line 185)
CENSUS	fn	set_source_lang	unsupported Rust: method `with` on interior-mutable receiver (no proven JS-equivalent yet) (line 201)
CENSUS	fn	ast_to_estree	unsupported Rust: iterator source length unproven (param-len arrays core request) (line 315)
```

So `shir.rs` stays at 758 and `estree.rs` at 92 because their first units
refuse on a FEATURE (`trim_start_matches`, an interior-mutable `.with(..)`, a
`Vec::with_capacity(n)` length the length model does not track) and the next
wall is `crate::shir::ast_to_ir` — none of it stderr-shaped, so none of it
moved.

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
