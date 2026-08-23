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
