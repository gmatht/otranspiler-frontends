# CPP_PLAN — a C++14 (and full-C) frontend on tree-sitter

**Status: v0.1 prototype + worker plumbing landed (2026-08-10).** The
CPP_PLAN architecture is proven concretely by `frontends/cpp-sh-go/`:

- **The split:** cpp-sh-go is the C++-only surface OVER the shared C
  lowering — it imports c-sh-go's `clib` package (include, never
  modify) and emits via `clib.Shir` (byte-equality oracle). A bounded
  desugar maps the expressible C++ surface (bool/true/false/nullptr,
  `new T[N]`/`new T` → malloc, `delete[]`/`delete` → free) and a
  keyword refusal table pins REFUSE > GUESS (templates, classes,
  std::/::, exceptions, auto, references…). `make test` = refusals +
  ingress acceptance + executed-stdout vs native g++ **+ the
  C-invariant** (c-sh-go's corpus stays green) — all green.
- **Workers:** cpp-sh-go's failure-driven worker is wired into
  `setup_backends.sh` (`--pi-fix-frontend cpp-sh-go`, `--build
  frontends`), and the c-requests channel (cpp → C) is live:
  `c-requests/` README + `--pi-fix-c-requests` (implemented by the
  c-sh-go worker, closed via `c-requests/done/`). The first real
  request is filed (bare `!<var>` test-string gap in clib).
- **Still ahead (tree-sitter):** the hand-rolled tokenizer/desugar is
  the PROVISIONAL parser. Sessions 1–4 of §6 (tree-sitter-c/cpp,
  the self-contained wasm artifact, full-C and the C++ surface) replace
  it; the split, the gate, the workers, and the request channel are the
  parts that are done.

Three commitments drive this document, each grounded in the codebase as it
is today:

1. **Parser: tree-sitter** — the only maintained modern C/C++ grammar
   ecosystem (ANTLR's is frozen at C++14; verified 2026-08).
2. **Split: one runtime, two grammars, one shared lowering** — C and C++
   share everything above the parse tree; the grammar is the only
   language-specific layer.
3. **Ownership: two workers, one shared owner, a request channel** — the
   C++ worker owns the C++ surface but never edits C-owned code; shared
   lowering changes flow through a `c-requests/` protocol (core-requests
   shape, lighter semantics).

---

## 1. Why tree-sitter (and why not ANTLR for C/C++)

The ANTLR ecosystem is a dead end for modern C++ — verified against GitHub
on 2026-08:

- `antlr/grammars-v4/cpp/` ships **CPP14 only** (even the newer
  `Antlr4ng/` subdir is a TypeScript port of the *same* CPP14 grammar).
- 86 open issues/PRs request C++17/20 in grammars-v4; **0 merged**.
- No `Cpp20.g4` exists anywhere on GitHub; no starred community ANTLR
  C++20/23 repos. The "adopt a maintained community ANTLR C++20/23
  grammar" option is a mirage — adopting means becoming the maintainer.

tree-sitter is the ecosystem that actually tracks modern C++:

| Grammar/binding | Repo | Stars | Last push | Notes |
|---|---|---|---|---|
| C grammar | `tree-sitter/tree-sitter-c` | 386 | 2026-07 | full C (VLAs, K&R, `_Generic`…) |
| C++ grammar | `tree-sitter/tree-sitter-cpp` | 445 | 2026-02 | C++14/20/23 constructs land here; used by every editor |
| official Go bindings | `tree-sitter/go-tree-sitter` | 287 | 2025-11 | cgo |
| community Go bindings | `smacker/go-tree-sitter` | 562 | 2024-08 | cgo, more mature API |

Why tree-sitter specifically, for *this* frontend:

- **Error tolerance.** tree-sitter is GLR with recovery: it produces a
  tree even over constructs it doesn't fully model. That converts
  REFUSE > GUESS from "choked at the token stream" into "parsed the whole
  file, refused this node" — the discipline the frontends are built on,
  made honest. A C++20 `requires` clause parses; the walker refuses the
  concept node cleanly instead of the lexer dying on an unknown keyword.
- **Modern coverage for free.** Designated initializers (C++20 — the one
  C++20 feature that lands *in* the expressible subset) are already in
  tree-sitter-cpp; coroutines/concepts/modules parse so they can be
  refused node-level.
- **Two grammars, one runtime.** `tree-sitter-c` + `tree-sitter-cpp` load
  into the same parser runtime; the walker switches on the language. This
  is the C/C++ split made physical.

**ANTLR is still right for the other frontends** (Go, Python, Java — where
grammars-v4 has mature, tested grammars and the merge is Go-native). C/C++
is the exception because its "official" grammar stopped at 14 and the
real maintained grammar is tree-sitter.

## 2. The wasm/cgo fork — the constraint that shapes everything

Both Go tree-sitter bindings are **cgo** (they link the C runtime). Go's
`js/wasm` target (what the busybox merged frontend builds with) has **no
cgo**. Therefore a tree-sitter frontend **cannot be merged into busybox** —
the integration used by every other frontend (bat, py, go, pl, zsh, fish).

**Decision: the C/C++ frontend ships as a self-contained native wasm
binary — the cproc pattern** (`cproc.wasm` runs via WasmRunner in both the
node CLI and the browser; the same source builds a native ELF the
otranspiler CLI spawns):

```
cpp-frontend/  (C++ source: parser glue + walker + A1 emitter)
   ├── native build  (wasi-sdk/g++ → ELF)  → otranspiler CLI process spawn
   └── wasm build    (wasi-sdk → wasm32-wasi) → shell WasmRunner (browser + CLI)
```

- The **A1 JSON contract is the shared surface**, not code: the C++ emitter
  must satisfy `shir-contract/schema.json` (+ `check_schema.py`) and the
  byte-equality oracle against `debashc --shir` on the expressible subset.
  The emitter is a few hundred lines of C++ — the price of not reusing the
  Go `shir-emit-go`.
- **Rejected alternatives:**
  - *Go + cgo walker, native only* — reuses shir-emit-go + the worker
    scaffolding, but the browser loses C/C++ source support entirely.
    Acceptable only if browser support is explicitly deferred (see §6).
  - *JS-side tree-sitter in the browser shell* — tree-sitter's own wasm
    grammars + a JS walker would host C/C++ parsing in the page, but then
    there are **two walkers** (Go and JS) to keep byte-identical — worse
    than the single C++ walker.
  - *Hand-extending CPP14.g4 in-house* — the ~week + permanent
    grammar-maintenance tax, with no upstream to absorb the deltas.

The shell wiring differs from busybox frontends: `source foo.cc` /
`otranspiler x.cc` route the source to `cpp-frontend.wasm` via WasmRunner
(or the native ELF via process spawn), capture the A1 JSON, and hand it to
`otranspilerl render` exactly as busyboxA1 does today.

## 3. The C/C++ split

**One runtime, two grammars, one shared lowering:**

```
 .c      → tree-sitter-c    ─┐
                              ├─▶ shared walker/lowering ──▶ A1 emitter ──▶ A1 JSON
 .cc/.cpp/.cxx/.hpp/.C → tree-sitter-cpp ─┘        (one owner: c-sh-go)
```

- **Grammar selection by extension** (the frontend receives `--lang c|cpp`
  mirroring the fleet convention; `.h`/.hpp pick by the containing source's
  lang, matching how the C frontend treats headers today).
- **The walker is shared.** The two grammars' node sets overlap for the
  expressible core (declarations, expressions, statements, control flow,
  function definitions); the walker branches only at the per-language
  nodes. C-only surface (VLAs, K&R definitions, `_Generic`, designated
  initializers) lowers-or-refuses under C rules; C++-only surface
  (references, `new`/`delete`, `bool`, `class`-as-`struct`,
  overloads-by-arity, designated inits) under C++ rules.
- **What lowers (the expressible subset — the same boundary the hand-rolled
  c-sh-go proves):** int/char/bool scalars, arrays, `printf`/`puts`,
  arithmetic/bitwise/comparison, `if`/`while`/`for`, plain functions,
  structs (field-by-field), pointers via `&`/`*`/`->`, `sizeof`,
  `#include`, `malloc`/`free` → the `mem.*` arena, and now: references →
  alias folding, `new`/`delete` → heap, designated initializers →
  struct-store.
- **What refuses on the AST (refusal corpus, each pinned by a testdata
  entry):** templates, class-with-methods/virtual, STL (`std::vector`,
  streams, `std::string`), exceptions, lambdas, coroutines, concepts,
  modules, `auto`, `constexpr`, operator overloads — C++ side; `_Generic`,
  `_Atomic`, complex — C side.

### Worker ownership (the "split" that matters for the process)

- **`frontends/c-sh-go/` owns:** the shared walker/lowering files, the
  tree-sitter-c wiring, the C corpus (`testdata_c/` — the existing 68
  migrated + full-C pins), the gate (`make test`), and **single ownership
  of the shared lowering**.
- **`frontends/cpp-sh-go/` owns:** the tree-sitter-cpp wiring, the C++
  walker extensions, the cpp corpus (`testdata_cpp/` — expressible +
  refusal pins), and the gate (`make test-cpp` **+ the C-invariant**: the
  C corpus must stay green — structurally enforced because the cpp worker's
  commit filter stages only cpp-owned paths).
- The cpp worker **includes but does not modify** the shared lowering: it
  calls it (same binary), and any *behavior-changing* extension goes through
  the request channel (§4). C-neutral additions (a new map, a new helper)
  live in cpp-owned files.

## 4. The requests protocol (cpp → C)

Mirror the `core-requests/` shape, with lighter semantics — this is a
planned-extension channel, not a failure trap.

**The channel:**

```
c-requests/<name>-<YYYYMMDD-HHMMSS>.md      # the request
c-requests/done/                             # closed requests (dir exists in core-requests/)
```

Request file format (core-requests/README.md, plus one field):

```markdown
# cpp: <one-line summary>

## NEED          what the shared lowering must provide
## WHY           the failing case / Unsupported reason
## MINIMAL-C-CHANGE   the smallest edit to C-owned lowering
## FAILING-CASE  the cpp testdata file + expected stdout (the ACCEPTANCE TEST)
## VALIDATION    the exact `make test-cpp` target(s) that prove it works
```

**Semantics:**

- **Non-blocking.** The cpp worker appends the request and *keeps working*
  on cpp-owned surface. No `sleeping-<name>` marker, no trap — that
  machinery is for repeated gate *failures* (cpp → core via
  `--worker-trapped cpp frontend` stays exactly as-is for those).
- **The C worker implements on its normal cycle**, with acceptance =
  **both** gates green: `make test` (C corpus — proves C didn't break)
  **and** the request's `VALIDATION` target (proves the feature actually
  works — the acceptance test lives in cpp-owned territory, which is why
  it travels with the request).
- **Close the loop:** the C worker moves the request to `done/`; the cpp
  worker's next cycle sees it, runs its full gate, and deletes its pin's
  request marker.
- **The boundary rule keeps the channel rare:** only *behavior-changing
  edits to existing C-owned code* request. C-neutral extensions ship in
  cpp-owned files (package-level state is visible to the shared code).
  If the cpp worker requests constantly, that is the diagnostic that the
  ownership line is drawn wrong — something genuinely shared lives on the
  wrong side of the split.

**Worker prompt lines (verbatim parallel of the core-requests instructions):**

- cpp worker: *"If a fix requires changing C-owned lowering behavior,
  APPEND a structured request to `c-requests/` with the testdata pin and
  exit 0. Do NOT touch c-sh-go-owned files — the c-sh-go worker implements
  c-requests."*
- c-sh-go worker: *"If `c-requests/*.md` exists, implement it; acceptance
  = `make test` AND the request's VALIDATION target both green; then move
  it to `c-requests/done/`."*

## 5. Gate & testdata

Same three-step gate as every frontend, plus the refusal half:

```
per expressible testdata file:
    parse → emit A1 → debashc --shir-in-estree accepts
    → executed stdout == recorded expectation (native_limits recorded, like bat)
per refusal testdata file:
    emit must FAIL loudly (REFUSE > GUESS pinned)
```

- `testdata_c/` — the existing 68 migrated to the tree-sitter walker
  (byte-equality vs the hand-rolled c-sh-go is the migration oracle), then
  full-C additions: VLAs, K&R definitions, designated initializers,
  compound literals, `_Generic`/`restrict` refusals.
- `testdata_cpp/` — expressible pins (references, `new`/`delete`, `bool`,
  `class`-as-`struct`, overloads-by-arity, designated inits) + the refusal
  corpus (templates, methods, STL, exceptions, coroutines, concepts,
  modules, lambdas, streams).
- The **C-invariant** is a hard line in the cpp gate: `make test-cpp` runs
  the full C corpus first and fails if anything regressed.

## 6. Sequencing

- **Session 1 — proof:** a scratch frontend (Go + cgo tree-sitter, native
  only), both grammars, a walker that emits A1 for the curated subset.
  Oracle: byte-equality vs the hand-rolled c-sh-go on the 68 corpus. This
  proves the walker/lowering shapes before any C++ investment.
- **Session 2 — the artifact:** wasi-sdk build (native ELF + wasm32-wasi),
  WasmRunner integration in the shell, `source foo.cc` wiring, `--lang
  c|cpp` dispatch. This is where the "own A1 emitter in C++" gets pinned
  by schema + oracle.
- **Session 3 — full C:** VLAs, K&R, compound literals, designated inits,
  and the C refusals. C corpus goes beyond the current 68.
- **Session 4 — the C++ surface:** references → alias folding, `new`/`delete`
  → heap, `bool`, `class`-as-`struct`, overloads-by-arity, designated
  inits, + the full C++ refusal corpus.
- **Session 5 — the process:** `c-requests/` channel, the two workers,
  leases, traps, prompts. (Can start earlier — the protocol is
  worker-scaffolding, independent of the grammar work.)

## 7. Open questions / risks

1. **Walker language — C++ vs Go+cgo.** The plan commits to a C++ walker
   (one artifact for CLI + browser, no two-walker divergence). The cost:
   the Go frontend ecosystem (`shir-emit-go`, Go worker scaffolding) is
   not reused; the A1 emitter is C++ (schema- and oracle-pinned instead).
   If browser C/C++ source support can be deferred indefinitely, a Go+cgo
   walker (native only) is the cheaper interim — but then the browser gap
   is permanent, not deferred.
2. **tree-sitter grammar fidelity.** tree-sitter-cpp is not an ISO grammar
   (it's the editor-grade grammar). The oracle anchors the expressible
   subset; refusal pins anchor the rest. Where the grammar mis-parses, the
   walker must refuse rather than guess — the corpus is the contract.
3. **`->`/`.` and templates.** tree-sitter-cpp models templates well enough
   to parse-and-refuse; `->` in the expressible subset is struct-member
   access and must NOT be confused with `operator->` — the walker needs an
   explicit guard (the C++ refusal corpus pins it).
4. **Two workers, one build tree.** If the C++ frontend is a single source
   tree, two worker processes building it need leases (the bat-sh-go
   `--leases` precedent) and path-scoped commit filters. The alternative —
   one worker with a C/C++ mode — is acceptable if the C-invariant gate
   is enforced either way.
5. **The hand-rolled c-sh-go stays until parity.** The tree-sitter frontend
   replaces it only when the migrated C corpus passes byte-identical
   against the oracle; until then both exist and the 68/68 gate guards the
   incumbent.
