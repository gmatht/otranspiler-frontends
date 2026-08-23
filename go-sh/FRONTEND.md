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

---

## Drop-in shIR nodes for the no-A1-shape constructs (2026-08-23)

The four headline REFUSE gaps above now lower to GENERIC ext nodes
declared in the core's `shir_nodes/*.node` DSL (build.rs-discovered —
new node + one handler file per construct, zero edits to shared core
beyond the ingress/lift dispatch wiring). Node names are deliberately
non-Go so Zig/Java/etc. frontends can reuse them; any Go-specific
residue lives in THIS frontend's lowering, documented inline.

**Supported now (testdata t100–t103, executed stdout == native go run):**
- Type assertion `x.(T)` → `TypeAssert{expr, kind}` — checked
  passthrough; `kind` ∈ sh2.typeOf vocabulary (string|int|float|bool|
  array) derived by `goTypeKind` (pointer marks strip, slices/variadic →
  array). Comma-ok `v, ok := x.(T)` assigns v ← TypeAssert and lowers ok
  as a typeof comparison branch pair (`"$ok"="true"` gates conditions;
  bare Bool-var conds supported). Named types (`x.(*StrE)`) still refuse:
  their dynamic kind is not knowable at this layer.
- Address-of `&x` / dereference `*p` → `AddressOf`/`Deref` pair.
  SNAPSHOT semantics on value-only backends (reads pass through);
  reference-capable backends may render true references. WRITES through
  a pointer refuse loudly. Nil checks keep working (`p == nil` ⇔ empty).
- Variadic spread `f(args...)` → `Spread{expr}` (ESTree
  SpreadElement), valid in direct call-arg position over ARRAY-typed
  vars. Callee side: a variadic decl param (`parts ...string`) splices
  the tail positionals into a real array at function entry
  (`setArray(parts, ${@@:N})`), so reads/len/index/Join all see a
  genuine array. Variadic func LITERALS still refuse.
- Composite literals in EXPRESSION position (`return map[string]any{…}`)
  → `MapLiteral{keys[], values[]}` (parallel lists), read back via
  `ElementRead{coll, key}` (computed coll[key]) with literal string
  keys. TRANSIENT values only: assigning/returning a whole dict to a
  name keeps the assocSet path or refuses (the capture protocol is
  string-typed). Positional struct literals (`{"a", b}` map values)
  still refuse.
- `fmt.Sprintf(varFormat, args...)`: dynamic-format printf capture (the
  runtime evaluates verbs at run time). Caveat: shell printf interprets
  backslash escapes in the FORMAT — Go's Sprintf does not — so var
  formats must be escape-free.

**Still-open gaps (each refuses with "unsupported …"):** named-type
assertions; pointer writes; struct VALUES/member access on values
(`prog.Imports`, positional struct literals); computed array indexes
(`src[p.pos]`); user-fn calls in condition position (bool-returning
predicates need a status-return protocol); comparisons in word
position; zero-value reads; unsupported stdlib constructors.

Corpus state (frontends/coverage/parser-coverage.sh): 125 .go files →
EMIT 103 (82.4%, was 99/121 = 81.8%), REFUSE 22, PARSE-ERR 0, CRASH 0 —
no previously-passing file regressed; every refusal is at a named
construct.

---

## Self-hosting push: computed indexes, struct values, predicates (2026-08-23b)

**Supported now (verified vs native go run):**
- Computed indexes: `src[i]`, `arr[i+1]` (arrays via evalArith
  subscripts; strings via ${s:$i:1} single-element reads — Go bytes =
  chars for the ASCII corpus), indexed writes `a[i] = v`, computed
  slice bounds `src[start+1 : i]`
- Struct VALUES via the generic OBJECT STORE runtime helpers
  (sh2.objNew/objGet/objSet/list*/map* in harness/sh2-namespace.mjs):
  `&T{...}` / `T{...}` allocate an object whose id is an opaque string;
  pointers ride the echo/capture value-return protocol with TRUE
  reference semantics (aliasing/mutation preserved); field access is
  objGet chains; layouts captured from `type X struct{...}` decls
- Methods (`func (r T) m(...)`) lower as subs with the receiver as $1
  (an object id); call sites pass it explicitly
- Predicate calls in condition position (`if isIdentStart(c) {`) —
  bool-returning subs signal via EXIT STATUS (`return <cmp>` lowers to
  sh2.return 0/1); bare Bool-var conditions gate on "$v"="true"
- Bare `switch {` (switch-true) → chained If; bare `for {}` →
  While(true); `for i, v := range xs` (index+value, incl. list-object
  fields); multi-value returns via RESULT SLOTS (__ret_fn_N);
  comma-ok map reads (`v, ok := m[k]`); strings.TrimSpace/Split/
  LastIndex/Index helpers; dynamic bytes.Buffer writes

**Self-hosting status:** go-sh now parses its own source past line ~1300
of 3880 before refusing. Remaining walls (each needing design work):
maps-in-structs coherence between the assoc-array and object-store
models; defer/recover; interface{} boxing of object ids; the JSON emit
library (external package, stubbed). Full-file emit NOT yet achieved.

Corpus unchanged: EMIT 103/125, REFUSE 22, PARSE-ERR 0 — zero
regressions.
