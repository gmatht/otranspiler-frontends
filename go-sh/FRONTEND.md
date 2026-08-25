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

**Self-hosting status:** go-sh now parses its own source past line ~2158
of 3880 (goto/Label, empty-init and optional-cond/post C-style for,
ContainsRune, forward struct prescan all landed) before hitting the
pointer-out-param wall (`*out = append(*out, ...)`). Remaining walls
(each needing design work): pointer-out-param writes (needs an
indirection/box model or source refactor), maps-in-structs coherence
between the assoc-array and object-store models, defer/recover stubs,
interface{} boxing of object ids, cross-file package references
(cpp-sh-go's treeCheck lives in parser.go — single-file frontend cannot
resolve), the JSON emit library (external package, stubbed). Full-file
emit NOT yet achieved.

Corpus unchanged: EMIT 103/125, REFUSE 22, PARSE-ERR 0 — zero
regressions.

---

## cpp-sh-go package coverage + parser-bug fixes (2026-08-24)

The goal: every Go source file the CPP frontend contains/uses accepted
by this parser without PARSE-ERR; remaining gaps are named refusals,
never silent mis-lowerings. State after this pass:

**Corpus-wide (frontends/**/*.go, 125 files): 103 EMIT / 22 REFUSE /
0 PARSE-ERR.**

Parser bugs fixed (each was a hard parse error before):
- strconv statement-position family: `Atoi/ParseInt/ParseFloat/ParseBool`
  plus the inverse `Itoa/FormatInt` — ParseInt's extra args consumed;
  non-literal args lower through strAtoi/strItoa call words; only
  base-10 Atoi folds literals (ParseInt's base can reinterpret digits).
- LOCAL type declarations (`type dotTgt struct{…}` inside a func body)
  — extracted parseTypeDecl, valid in statement position too.
- Index-only range `for i := range X` over Array-typed vars and list
  fields — counted ForInit over the length (range-over-int literal arm
  extended; two-value form stays a contract boundary).
- Two-value range over a COMPUTED container (`for i, f :=
  range p.structs[typeName]`) — container word evaluates natively.
- Var-slice range (`for _, a := range args[1:]`) — single flattened
  param("slice", name, lo) iter element.
- `len(x.field)` inside arithmetic (hasObjectRead + arithNumCalls route
  through num_* calls), compound element writes (`m.f[k] += v` for
  string-valued containers; numeric elements refuse), member elements in
  slice literals, strings.HasPrefix/TrimPrefix/TrimSuffix over non-var
  operands (strHasPrefix call word / AffixStrip ext node).
- PACKAGE MODE: `go-sh --shir f1.go f2.go … | dir` concatenates the
  package's files before parsing (prescan resolves cross-file refs);
  package-qualified calls `pkg.F(...)` resolve when F is in the source.

New core drop-in: **AffixStrip** (shir_nodes/affix_strip.node +
render_ext_expr/render_ext_estree handlers) — single-occurrence affix
strip with literal (non-glob) semantics. The `${s#p}` param-op path
stays for metachar-free patterns; glob-metachar patterns (`[]`, `[]`
would silently no-op in the shell) route here. Verified end-to-end:
frontend → debashc --shir-in-estree → estree-runner matches `go run`,
and the Perl render matches native semantics.

cpp-sh-go package status (all three .go files, zero parse errors):
- cmd/cpp-sh-go/main.go + main.go + parser.go together: REFUSE at
  `clib.Shir(...)` — external module (c-sh-go clib, cgo/tree-sitter
  behind it); its implementation is outside any frontend's source set.
- parser.go alone: REFUSE at `sitter.NewParser()` /
  `cpp.GetLanguage()` — github.com/smacker/go-tree-sitter is a cgo
  binding; no faithful shell/JS lowering exists (Refuse > guess).

These are the complete remaining non-parse blockers for the CPP
frontend file set: both are external-library boundaries where any
lowering would be a mis-lowering.

---

## Three-class refusal disposition (2026-08-24, goal pass 2)

The 33 refusing files from the sweep were triaged into the three
classes below. Frontend fixes landed this round; the rest are routed
or documented as specified.

### Class 1 — external cgo bindings → PASSED TO THE C FRONTEND
Constructs whose only executable semantics live in native code
(tree-sitter via cgo, cross-module `*lib.Shir` entry points, opaque
node-pointer methods). These are **not lowered** — every refusal names
the exact construct and line; translation of programs that need them
goes through the C frontend path (native-only execution). Files:
cpp-sh-go/{main.go,parser.go,cmd/main.go}, c-sh-go/cmd,
fish/perl/zig/bat cmd CLIs (`*lib.Shir`),
coverage/ts-node-gap/main.go + powershell-sh-go/cmd/* +
powershell-sh-go/refuse.go (sitter.NewLanguage/NewParser, n.Content,
root.Content), busybox/main.go (function-value fields `clib.Shir` et al.
— first-class funcs are themselves a contract boundary).

### Class 2 — Go stdlib with runtime state → new shIR nodes where faithful
| construct | disposition |
|---|---|
| strings.Trim(s, cutset) | **CutsetTrim** drop-in node (shir_nodes/cutset_trim.node + render_ext_expr/render_ext_estree handlers): Perl `s/^[..]+//`-pair with \Q\E quoting; JS scan/slice IIFE; Rust trim_matches. Verified vs `go run`. |
| strings.TrimLeft/TrimRight(s, cutset) | **CutsetTrim side field** (optional_string: "left"/"right"/absent=both, commit 2a90a1b): Perl conditional s/// substitutions, Go backend strings.TrimLeft/Right, ESTree handler skips the corresponding scan loop. go-sh lowers through CutsetTrim with side; accepts SLICE operands (s[0:i]) via exprToWord. Oracle-verified vs `go run` on all three sides (JS byte-identical; Perl render correct). |
| element writes on container fields (`m.f[k] = v`, `l[i] = v`) | **bracket-write path repaired** (commit ab7020f2): unconditional second `expect("]]")` refused every plain single-index write; key now lowers via exprToWord (raw expr node rejected by ingress); map-typed field literals allocate map OBJECT refs; sh2.objNew('map') allocates kind:'map'. Oracle-verified vs `go run`. go-sh SELF-PARSE advanced 1425→3460; zig-sh-go 942→1296. |
| member-list slice family (`x.out[lo:hi]`, copy/truncate/spread-back) | **listSlice + listExtend runtime helpers + ListRef var flavor**: `x.f[lo:hi]` → listSlice(objGet(id,f), lo, hi) (new list ref, exclusive hi); copy idiom `upd := append([]T{}, x.f[m:]...)` assigns the ref (typed ListRef so len() → listLen); `x.f = append(x.f, src...)` spreads via listExtend chains; strings.Join over member slices → joinSep. Oracle-verified vs `go run` (tail-copy+truncate+join round-trip). zig-sh-go advanced 577→942. |
| sync.Once.Do(fn) | frontend lowering: guard-var idiom (`$__once_x != "1"` gates assign+body) — no node needed; verified once-only execution end-to-end |
| os.Stat | named-target if-init arm: `if info, err := os.Stat(E); err == nil && info.IsDir()` → `-d E`; `err == nil`/`err != nil` → `-e`/`! -e`; var paths supported. Full FileInfo structs stay refused (no faithful A1 shape) |
| regexp.MustCompile | JUSTIFIED REFUSAL: first-class regex objects have no A1 value representation; per-method lowerings (MatchString etc.) would need pattern-object transport |
| json.Marshal / json.NewEncoder(...).Encode | JUSTIFIED REFUSAL: encoding an object-store composite requires traversal machinery in every backend's object model (and HTML-escape options); not faithfully expressible as a pure expr node |

### Class 3 — fixable frontier constructs fixed this round
- struct TAGS (backquoted metadata) skipped in struct layouts
  (shir-emit-go's exported Program fields)
- map-typed PARAMETERS register as assoc maps → `m["key"]` lowers to
  assocGet (c-sh-go's isBreakStmt)
- byte reads on STRING fields (`l.src[l.pos]`) in words AND conditions —
  SubStrExtract single-char read (fish/zsh lexers)
- strings.ContainsRune(set, rune(c)) membership condition (zig lexer);
  byte(x)/rune(x)/string(x) conversions are identity reads
- fmt.Fprintf(os.Stderr, ...) → fd-dup-wrapped printf (%v→%s:
  error/string operands render identically)
- multi-target assignments with index RHS no longer mis-parsed as
  comma-ok (`td, ntPath := os.Args[1], os.Args[2]`)
- os.Args[i] element reads → positional `${@:k:1}` / `$0`

### Remaining frontier (each refuses loudly, needs contract design)
- calls inside returned boolean expressions (`return isIdentStart(c) ||
  ...`): predicate subs signal via EXIT STATUS; their verdicts cannot
  ride as BinOp values without an agreed bool-value protocol (a ladder
  prototype interacted badly with the statused-test comma form — see
  git history before re-attempting). Affects posix/zsh/py-sh-go mains.
- strings.ReplaceAll with COMPUTED replacement (`` s = ReplaceAll(s, c,
  `\`+c) ``) — param-op needs literals; would need a strReplaceAll
  runtime function (harness-side addition, not a node).
- map-typed dynamic type assertions (`x.(map[string]any)`) and pointer-
  named assertions (`x.(*StrE)`): the TypeAssert kind vocabulary has no
  map/object member and struct ids stringify, so typeof cannot
  distinguish them.

---

## Refusal-elimination pass 1 (2026-08-24, goal pass 3)

### Landed: And/Or condition splitting + dual-channel predicate returns
- **splitBoolCondIf**: `if A && B`/`A || B` conditions (and predicate
  `return <bool tree>`) desugar into NESTED ifs before lowering. This
  fixes a REAL correctness bug: BinOp And/Or renders with shell STATUS
  semantics (`(l, lastExit===0 ? r : false)`), which reads stale `$?`
  when the leaves are native JS comparisons — pure-Go predicate bodies
  (`c == '_' || (c >= 'a' && c <= 'z')`) mis-branched on stale state.
- **Dual-channel predicate returns**: each arm of a predicate's bool
  tree ECHOES its verdict ("true"/"false" — Go's bool printing, the
  VALUE channel capture-based callers read) AND Returns 0/1 (the
  STATUS channel exec-condition callers branch on).
  `fmt.Println(isIdStart('x'), isIdStart('+'))` → `true false`,
  matching `go run`.

### Discovered contract boundary (blocks further composition)
Nested predicate calls compose POORLY under dual-channel: every sub in
the chain echoes to the SHARED stdout, so an outer capture collects the
inner subs' verdicts concatenated with its own (`isIdentChar('!')`
calling `isIdentStart` yields polluted captures). Composing calls as
BinOp operands needs ONE of:
(a) exec-status-only conds + echo ONLY at the outermost sub (requires
    outermoseness knowledge or a compiler-pass rewrite), or
(b) a runtime value-channel for booleans (fnValue-style dispatch with
    "true"/"false" strings and a Bool-typed A1 word shape), or
(c) inline expansion of simple predicate bodies at call sites.
Decision escalated — see FRONTEND.md history; until landed,
calls-inside-returned-boolean-expressions keep refusing loudly
(`unsupported comparison in word position`, posix/zsh/py-sh-go mains).

### Also still open (unchanged from pass 2)
ReplaceAll-with-computed-replacement (needs sh2.strReplaceAll harness
function); dynamic type assertions (map/object kind vocabulary);
os.ReadDir; regexp.MustCompile; json.Marshal/NewEncoder.

---

## Refusal-elimination pass 2 (2026-08-24, goal pass 4)

### Landed
- **Flag-based bool-tree lowering** (`boolTreeStmts`): And/Or trees in
  conditions AND predicate returns evaluate each leaf into a flag var
  (`$__bt_N`), gated by `$flag ==/"!=" "true"` tests — Go's exact
  short-circuit semantics, immune to the statused And/Or render.
  Verified: nested predicate probes match `go run` byte-for-byte.
- **Status-capture value calls**: bool-returning subs called in VALUE
  position lower as `capture(Arrow[ If(exec sub){echo true}else{echo
  false} ])` — verdict via $? channel, no stdout pollution between
  nested calls. Registered via `p.boolFuncs` at predicate-return parse.
- **Bare-var conditions** (`if ok {`) → `-n "$ok"` truthiness test.
- **Value-returning subs as comparison operands** (`p.peek() == '$'`)
  → capture(word) compared natively (condOperandA1WordInner call case).
- **`<buf>.Len()` / `len(struct.field)`** → strLen/listLen over the
  buffer/objGet word.
- **Single comparisons as boolean VALUES** (`push(..., c == '"', ...)`)
  → native A1 BinOp Eq/Ne/Lt/... (exprToWord binop case).
- **regexp.MustCompile + FindString/MatchString**: patterns tracked
  (`p.regexpVars`); FindString lowers to the new **RegexpFind** drop-in
  node (JS `(TEXT.match(new RegExp(PAT)) || [""])[0]`; Perl `do{my $m;
  ($T=~/(P)/)&&($m=$1); $m//""}`), MatchString → sh2.regexMatch.
  Verified vs go run ("abc9def" case).

### Portable JS verification ✓
harness/browser-shim.mjs: executes the SAME compiled prog.mjs against
a shimmed `process` global (stdout captured, exit intercepted, no fs)
— the emitted program's only Node surface is the injected `process`
object. Verified: nested-predicate program produces byte-identical
stdout under estree-runner (Node) and browser-shim.

### Remaining frontier (pass-4 sweep) — each with its BLOCKING DECISION named
| refusal | files | blocking decision |
|---|---|---|
| sitter.NewLanguage/NewParser, root/n.Content, root.NamedChild, n.StartPoint | coverage/ts-node-gap, powershell-sh-go/{main,refuse,lower}.go | CGO-PATH C-backend handlers: the CgoCall node lands shIR faithfully; the C backend must render the tree-sitter api.h family (ts_parser_new/ts_parser_set_language/ts_node_*) AND the generated build needs -ltree-sitter linkage — both pending |
| *lib.Shir(...) entry points | cpp/c/fish/perl/zig/bat cmd CLIs | MULTI-FRONTEND REGISTRY: cross-module value transport needs an agreed runtime registry (busybox-style combined binary registers each frontend's Shir; standalone builds refuse) |
| json.Marshal / json.NewEncoder(...).Encode | shir-emit-go/emit.go, powershell-sh-go/emit.go | JSON-ENGINE: encoding object-store composites requires per-backend traversal over the obj model (JS: walk _objStore → JSON.stringify-compatible; Perl: JSON::PP) — node design pending |
| json.Unmarshal(data, &v) | coverage/ts-node-gap/main.go | same JSON-ENGINE decision, reverse direction (populate the obj store from parsed JSON) |
| type assertions to map[string]any / *StrE | c-sh-go/main.go, posix/zsh lowering.go | TYPE-KIND VOCABULARY: TypeAssert kind lacks map/object; struct ids stringify so typeof cannot discriminate — needs sh2.objType(id) runtime + vocab extension |
| unsafe.Pointer(...) | coverage/ts-node-gap/main.go | routed via CgoCall where prefixed; bare conversions need pointer-repr semantics — CGO-PATH |
| strings.ReplaceAll(s, c, computed) | fish-sh-go/fish-sh-go.go | RUNTIME FN: needs sh2.strReplaceAll(text, old, newFn-result) harness function (computed replacement cannot be a ${s//} literal) |
| filepath.Glob(pattern) | go-sh/cmd/go-sh/main.go | GLOB-LIST: glob expansion to a newline-joined list exists via GLOB_MAGIC internals — needs a word-position lowering |
| os.ReadDir(dir) + e.Name()/e.IsDir() | go-sh/cmd/go-sh/main.go | DIR-STREAM: entries are objects (Name/IsDir); needs a dir-listing primitive carrying the parent dir for IsDir re-tests, or a names-only documented variant |
| comparison in word position | py-sh-go/main.go | BOOL-COMPOSITION residue: and/or trees WITH user-call leaves now lower via boolTreeStmts; the remaining shape composes a call leaf with string-compare operands in one BinOp — needs the leaf-as-value protocol decision |
| condition: call (some shapes) | posix/zsh/py analysis+cmd files | predicate calls in non-If positions (switch guards, for conds) — per-position cond lowering |
| range over a non-var | go-sh/go-sh.go:3187 | range generalization |
| map key must be literal | zig-sh-go/main.go | computed assoc keys need a key-eval lowering |
| expected assignment operator, got "func" | perl-sh-go/main.go:496 | FUNCTION VALUES (`f := func(){…}` as var): first-class functions are a contract boundary (fnValue registry exists in runtime; frontend parse+lowering pending) |
### CGO-PATH node sketch (designated route, not yet landed)
```
node CgoCall        # any cgo-bound call: constructor/method/free-fn
tag "CgoCall"
kind expr           # stmt variant wraps Expr
field target: string   # e.g. "sitter.NewParser", "node.Content"
field args: exprs
field recv: optional_expr
```
C backend: renders the real C calls (#include <tree-sitter.h>, ...) so
cgo-bound PROGRAMS become C-backend-only builds — faithful execution,
zero mis-lowering. JS/Perl backends render `sh2.cgoUnsupported(target)`
(a loud runtime error, never silence). Frontend routes sitter/*-lib
calls there instead of refusing at parse time.

### Pass-4 addendum: found-during-verification issue
`v, ok := userFn()` (multi-value return from a USER function): the
RESULT-SLOT capture path assigns ok = whole captured stdout ("val true")
instead of splitting per-slot, and v goes unassigned in the generated
JS for some shapes — a silent mis-lower candidate flagged for next
session's first fix (investigate __ret_N slot splitting vs echo-word
joining; likely fix = one echo PER return value inside the Arrow).

### Pass-5 addendum: multi-return slot protocol FIXED (2026-08-24)
`v, ok := userFn()` now lowers as: hidden temp = capture(exec f args);
each target reads `listGet(strSplit($temp, " "), i)` — Go's per-position
value semantics, no stdout pollution, no runtime __ret support needed.
Verified vs go run (`ok: val`). strSplit/listGet are JS-path runtime
functions; the Perl backend renders the object model as bare calls
pending a Perl object-runtime preamble (pre-existing gap for ALL
object-model programs — exposed, not caused by this change).

### Pass-5 sweep state
go-sh/go-sh.go self-parse frontier advanced to line 3227
(`for _, a := range args[0].args` — member-of-index range target needs
deeper type inference). All other refusals unchanged from the pass-4
table. Gates: fail-go 103/103, make test 103/103.

---

## Pass-9 status summary (2026-08-24)

Sweep: 136 files (excluding debug artifacts), 103 EMIT / 33 REFUSE / 0 PARSE-ERR.
Refusal inventory: coverage/refused-go-sh-20260824-pass9.txt

### Cumulative lowerings landed across this goal's passes
SplitN (param ops), ContainsAny/Compare/TrimLeft/TrimRight/TrimSpace,
IndexByte/IndexRune/Index, ReplaceAll (computed), buf.String()/len(field)
in arithmetic, bare-var truthiness conditions, value-subs as comparison
operands, compound field assigns (+ = on string fields and nested element
fields), pointer receivers, type-switch pointer-type patterns, computed
map keys, multi-return slot protocol, per-position return typing,
function-literal assignments, PACKAGE MODE (multi-file concatenation),
bare-return-in-switch-case parse fix, ContainsRune membership, fmt.Fprintf
to stderr, os.Args[i] positional reads, os.Stdout.Write, regexp
MustCompile tracking, SplitN param-op lowering, local type declarations,
bare-switch or-chain flag-based lowering.

### ACTIVE REGRESSION (found 2026-09-01, NOT frontend-caused)

`if <bool var> { return X }` + fallthrough return misrenders: the If's
test-call cond collapses at ESTree render time to `String("") !== ""`
(always false) — f(true)/f(false) print "no"/"no" instead of yes/no.
Reproduces identically on BOTH trees' debashc binaries with identical
shir input (the shir is CORRECT: cond=test("-n \"$ok\"")); the collapse
happens in shir.rs render/fold machinery. Suspect: concurrent worker's
"text_ops: construct-normalisation transforms" commit 44c350a (touched
shir.rs). fail-go stays green because the snippet corpus lacks this
shape. Needs a bisect against 44c350a^ (that parent has an unrelated
compile error — seq_range_for duplicate — fix that first).

### Remaining refusals by category (each with named blocking decision)

**CGO-PATH → C frontend** (10 files): sitter.NewLanguage/NewParser,
root/n/sp.Content methods, root.NamedChild, n.StartPoint, unsafe.Pointer,
*lib.Shir cross-module entry points (c/fish/perl/zig/bat), clib.Shir.
BLOCKING DECISION: CgoCall node landed (estree/Perl loud-fail); C-backend
native rendering needs tree-sitter api.h mapping + -ltree-sitter build
linkage + a C-runtime preamble for each affected program. This is the
designated route — not implementable in JS/browser by design.

**JSON engines** (4 files): json.Marshal/Unmarshal/NewEncoder over the
object store. BLOCKING DECISION: needs per-backend traversal of the obj
model (JS: walk _objStore → JSON.stringify-compatible; Perl: JSON::PP) —
node design pending.

**Per-file lowerings** (~15 files): type assertions to *StrE /
map[string]any (TypeAssert kind vocabulary); strings.Join with computed
separator (joinSep runtime fn landed, frontend adoption pending);
strings.ReplaceAll with computed args (strReplaceAll runtime fn landed);
os.ReadDir (dir-stream primitive); filepath.Glob (glob-list lowering);
function values (`f := func(){…}` as var + fnValue dispatch); compound
field assigns on cross-function vars; bare call conditions in remaining
shapes; range over member-of-index; map key must be literal (computed
assoc keys landed but some shapes remain).
