# src/ coverage roadmap — rust-frontend vs sh2perl/src

Baseline census (2026-08-23, `cargo build --offline --bin census && ./target/debug/census ../../s2p.rust/src/*.rs`):
all 30 files refuse at the first non-main item today. Aggregate idiom
counts over the corpus (top entries):

── items ──
  1570  fn
  1058  fn.ret_type
   706  impl.fn
   292  enum.variant.unit
   188  enum.variant.tuple
   131  use
    90  struct
    83  mod
    75  enum.variant.named
    63  const
    63  impl
    51  enum
    39  static
    30  fn.generics
    26  impl.trait
     5  impl.other_item
     3  type_alias
     1  trait
     1  trait.fn
── statements ──
  5109  expr_stmt
  4583  let
   271  let.else
── exprs ──
 31000  path
  6483  lit.str
  6196  call
  5268  bin
  5031  block
  3135  if
  2414  lit.int
  2343  if.no_else
  2169  ref
  1594  field
  1259  match
  1208  lit.bool
  1170  lit.char
  1112  return.val
  1095  unary
  1068  index
   984  method.emit
   928  closure
   824  method.to_string
   815  struct_lit
   706  other_expr
   697  for
   695  method.push
   692  method.len
   651  qualified_path.Box::new
   619  assign
   556  method.iter
   531  try_?
   451  tuple
   404  method.clone
   397  method.contains
   397  method.is_empty
   383  range..
   344  method.push_str
   343  method.collect

## Phased plan (each phase = frontend subset growth + testdata pins, gate green)

1n. DONE v0.14 — lib-tree GATED splice (clean modules only, mtime-cached
    single-file verdicts), lifetime-generic erasure, const-slice
    contains -> OR-eq chains. builtin.rs lowers cleanly; the four
    backend files still refuse on it (slice patterns vs IrExpr::*).
1l. DONE v0.12 — iterator-chain desugar (.iter().map(closure|fn).
    collect()) via accumulator + index loop + setArrayAppend; closures
    inline, fn-names dispatch fnValue; result typing propagates.
    BLOCKED at oracle: getVar scalar view (param-len request). t53
    pending.
1m. DONE v0.13 — generalized iterator pipelines (map*/filter*/take*
    over .iter(), proven-bool filters with Continue skip, take-capped
    bounds); Ty::Bool inference for comparisons (t54).
1k. DONE v0.11b — Vec/array for-loops (index while-loop + arrayIndex,
    hoisted var bound, element-type tracking incl. literal lengths),
    scalar deref erasure (t52).
1j. DONE v0.11 — file-mod expansion with gated splice (clean modules
    only; whole-crate merging tried + reverted). Last-segment call
    resolution for crate paths; struct field-type tracking (field
    receivers type-resolve). 4/30 holds.
1i. DONE v0.10 — setArray-native array storage, Arr::is_empty fold,
    Str::is_empty equality; SECOND core bug filed (param-len arrays:
    ${#arr} scalar-view miscount). t51 pins.
1h. DONE v0.9 — CloneDeep drop-in node (any-receiver .clone()),
    AtomicBool statics (typed statics map, new/load/store), bool test
    operands + proven-bool conditions, If-condition call hoisting
    (t50 pins; debug.rs full pass -> 4/30).
1g. DONE v0.8 — unit enums + match: tag-string variant values, match ->
    if-chain over string-eq on a hoisted scrutinee (or-patterns/_),
    typed =/!= test dispatch; param-clobber limitation documented.
    Frontier: payload-variant enums (needs record storage), iterator
    machinery x9, record clone x5.
1f. DONE v0.7 — if-as-expression (branch-duplicated binding / per-branch
    Return; t48). Frontier now: enum matches + write! (x5), iterators x9,
    record clone x5, `?` x2 — all design-level tranches.
MILESTONE (v0.6, same day): first FULL-FILE passes — ast.rs, lib.rs,
    variable_analysis.rs emit + ingress clean. 3/30.
1e. DONE v0.6 — receiver methods (Type_method mangling + self positional),
    FieldRead drop-in node (core), arith field-read hoisting, std
    constructors next. CORE REQUEST filed: record storage stringification
    (rust-frontend-20260823-record-storage.md) blocks t45/t46 in
    testdata-pending/.
1d. DONE v0.5 — value-position booleans (test-call convention), soundness
    fix for bare bool-var conditions, minimal Ty tracker, proven str-method
    whitelist via A1 MethodCall, shared-borrow erasure, vec!/array literals
    (t39-t44). Frontier: struct literals + field access (drop-in node work),
    iterator machinery, receivers.
1c. DONE v0.4 — declarations: struct/enum/type/trait items, mod flattening,
    cfg(test) subtree drops, macro_rules defs, impl methods under Type_method
    mangling with Type::method call resolution; receivers still refuse
    (t36/t37 pin it). Corpus: 17/30 past structure.
1b. DONE v0.3 — user functions: multi-fn programs, positional params,
    fnValue/fnCall dispatch, native Return, tail-expression returns,
    no-main library files, inert attrs. t35 pins it. Corpus movement:
    0/30 -> 11/30 files past the structural wall; remaining first-gap
    census: 19x non-fn items (struct/enum/impl/mod/macro_rules),
    8x expression kinds (method calls/field access), 2x qualified call
    target (String::from etc.), 1x {:?} format spec.
1. DONE v0.2 — program structure basics: use / const / static / typed lets /
   bool literals (t31–t34).
2. user functions: multi-fn programs; params -> A1 positional protocol,
   value returns via capture (mirror c-sh-go fnValue conventions; probe
   the core first). struct/enum/impl items accepted as DECLARATIONS.
3. types layer: String/&str unification, Vec<T>, Option<T>, slices,
   references erased at lowering boundaries.
4. expressions: comparisons as values, short-circuit &&/|| in value
   position, if/match as EXPRESSIONS, field access, indexing, casts.
5. std methods by frequency (census order): to_string, push, len, iter,
   clone, contains, is_empty, push_str, collect, chars, starts_with...
6. match statements + patterns (lit/or/wild/range first; struct/tuple
   patterns later).
7. iterators/closures: for-in over Vec/&[T], .iter().map()/filter() as
   generic shIR->shIR transform opportunities (shared across frontends).
8. format! machinery beyond {} ({:?}, width, named args), vec!, assert!,
   panic!/unwrap paths.
