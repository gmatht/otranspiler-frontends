# New Frontends: What We Need From the Core, and What We Can Build Now

Status: **session 1 — contract tooling + first (Python) frontend.** The core's
ShIR JSON export (asks A1–A6) already landed in sh2perl (commits b73fe63,
5108cb9, 5f0445f); this plan is built on top of that, not waiting for it.

## 0. The architecture (agreed in discussion)

```
  shell source (or other source lang)
        │
        ▼
  NEW FRONTEND (any language)          ── parse + faithful semantics only
        │  emits language-neutral ShIR JSON (the A1 contract)
        ▼
  debashc core                         ── semantics-preserving shared passes
        │     + attach every provable fact (var_types, purity, const…)
        ▼
  annotated ShIR JSON / per-backend lowering
        │
        ▼
  BACKENDS (perl, estree/js, c, c++, go…) ── trust what they can, render,
        own target-specific lowering + runtime
```

Division of labor:
- **Frontends parse; they do not optimize.** The optimizer attaches facts.
- **Shared passes are semantics-preserving for every backend** — so nothing
  ever needs to negotiate or veto (no hint protocol; annotations are the
  negotiation surface).
- **Target-dependent decisions live in the backend**, never in the core.

## 1. What we already have from the core (verified 2026-08-04)

| Ask | What | Where |
|---|---|---|
| A1 | Full language-neutral ShIR JSON export: every `IrStmt`/`IrExpr` node, deterministic, compact JSON, keys alphabetically sorted | `src/shir_json.rs`, `debashc --shir <file>` |
| A2 | Conservative type verdicts (`IrType {Int, Str, Any}`) serialized as `var_types` (lifted vars only; `Any` = absent) | `shir.rs::analyze_var_types`, `shir_json.rs` |
| A3 | Purity classification per call (`PureCpu`/`Emulable`/`Fs`/`Spawn`/`Control`) | `shir_json.rs::call_purity` (+ `SYNC_BUILTINS` in `shir.rs`) |
| A4 | `sh2.*` namespace spec (machine-readable) | workspace `harness/sh2-namespace.json` (consumer-owned) |
| A5 | Contract docs | `sh2perl/docs/backend-*-core-needs.md`, `estree-contract.md`, `arith-contract.md`, `backend-universal-contract.md` |
| A6 | C-keyword hygiene (reserved-name escaping) | core |

The ShIR JSON **is** the frontend contract. It already works end-to-end:
`debashc --shir` is a standalone consumer of the same `ast_to_ir` every
backend uses, so a frontend that reproduces it byte-for-byte is provably
faithful without touching the core.

## 2. How to update the core (ordered, minimal, additive)

Design principle: **frontends parse; the core optimizes and attaches facts.**
Most of this work is *publishing what already exists*, not building new
semantics. All changes are additive — backends ignore what they don't
consume. Sequencing is dependency-ordered; nothing before 2.2 is blocking.

### 2.1 Contract versioning + schema pin (cheap, do first)
- Add `"contract_version": 1` to the Program JSON.
- The authoritative schema stays `shir_json.rs`; the workspace copy is
  `frontends/shir-contract/schema.json` (keep in sync;
  `check_schema.py` validates corpus output against it — 0 violations/70).
- Any consumer (frontend, backend, validator) fails loudly on version
  mismatch instead of silently mis-emitting.

### 2.2 ShIR JSON deserializer — the load-bearing item
- serde derives on the IR types (only `IrType` derives today) so the A1
  JSON round-trips structurally.
- `shir_json → IrProgram` reader with **ingress validation**: schema
  conformance, node whitelist, no unknown shapes (mirror of the ESTree
  structural gate).
- CLI: `debashc --shir-in <file.json>` → optimize → `--estree`/`--perl`/…
  — the "pipe" architecture closes: **frontend output can be executed by
  any backend.** Prerequisite for the `O(F(S)) == C(S)` oracle (2.3) and
  for executing non-shell frontend output at all.

### 2.3 `--shir-unoptimized` export — pin the raw boundary
- Today `ast_to_ir` runs `optimize_stmts` inline and the export attaches
  var_types/purity. Add a raw mode: pre-`optimize_stmts`, pre-annotations.
- Then three comparisons are possible, and error attribution is clean:
  - `F(S)_raw == C(S)_raw` — frontend vs core frontend, raw (the
    frontend's semantic output)
  - `O(F(S)) == C(S)` — frontend through the REAL optimizer equals the
    reference export (the architecture's oracle; needs 2.2)
  - `C(S)_raw` vs `C(S)` — the optimizer's own footprint ("frontend
    parsed wrong" vs "optimizer changed it")

### 2.4 Publish A3 purity as data (the A4 namespace JSON)
- `SYNC_BUILTINS` + `call_purity` live in Rust; publish as machine-readable
  JSON so frontends **derive, not copy** (pysh.py embeds a mirror today).
  The callee whitelist / structural gate consumes the same file.

### 2.5 Source positions — the ONLY semantic extension
- Optional `loc: {line, col}` on statements (side table or inline;
  expressions inherit the enclosing statement's position).
- Populated by the parser; **propagated through `optimize_stmts`** (folded
  nodes keep their origin); serialized in A1.
- Why: (a) `$LINENO`/`$BASH_LINENO`/trap-ERR are shell SEMANTICS (corpus:
  064_23_complex_error_handling_traps.sh) — a faithful transpiler must
  emit the ORIGINAL line numbers, not generated ones; (b) debugging
  generated code is a universal backend need (Perl/C `#line`, JS source
  maps). Passes the "universally useful + not derivable + additive" test
  — the filter that rejected quote-style fidelity.

### 2.6 Explicit non-extensions (do NOT add to the IR)
- **Quote-style preference:** `StrStyle` capacity already exists; the
  neutral path keeps normalizing plain words to `DoubleQuoted`. Target
  idiom wins (Perl double-quoting a no-interpolation string is a
  perlcritic smell; Python PEP 8 prefers double). Source-style fidelity is
  a backend renderer policy reading the existing Str-vs-Interpolate shapes.
- **Comments / cosmetic fidelity:** same reasoning; the RawText/verbatim
  bridge covers round-trip needs.
- **A second frontend-IR for general-purpose languages:** the shell-domain
  ShIR is the contract; a Python/Perl/C-shaped IR would split the
  ecosystem (rejected — see §8.5 for how consistency nets handle the
  expressible intersection instead).

## 3. What we can build now, without modifying the core

| Piece | What | Status |
|---|---|---|
| `shir-contract/schema.json` | hand-authored node/field schema from `shir_json.rs` | this session |
| `shir-contract/check_schema.py` | corpus coverage checker: every emitted node must fit the schema | this session |
| `py-sh/` (pysh.py) | first frontend: shell-subset parser + A1/A2/A3 emitter, byte-identical to `debashc --shir` on its subset | this session |
| `equiv.py` | equivalence harness: diff frontend vs `debashc --shir` per file (oracle = byte equality); `--strip-annotations` mode validates the semantic core without A2/A3 | this session |
| Tier A canonical formatters | per-backend-language format(input) == format(output) consistency nets — cheapest, no frontend needed | next |
| go-frontend/ | ANTLR Go frontend (the discussed architecture) — contract tooling above is language-neutral and reusable | next |
| consistency frontends | per backend language (Python → C → C++, defer Perl) covering the expressible core only; feed §8 nets | next |

## 4. py-sh subset (v1)

Supported: comments, blank lines, `;` separators, `name=value` assignments
(bare/single/double-quoted values, `$var` refs), simple commands with bare /
single-quoted / double-quoted words and `$var` refs, `VAR=x cmd` env
prefixes. Unsupported (fail loudly with `Unsupported`): redirects, pipes,
`&&`/`||`, `if/while/for/case/function`, `$( )`, `$(( ))`, `${ }`, backticks,
arrays, `$1`/`$@` positional refs, tests. The corpus gate will grow the
subset; every new construct is added by first pinning the core's exact
`--shir` shape for it.

## 5. Word-shaping rules discovered by pinning the core (the contract's teeth)

The core's lowering is subtle — this is what "faithful" means:

- bare word → `Str(text, "DoubleQuoted")` (unquoted normalizes to DQ style)
- single-quoted word → `Str(text, "DoubleQuoted")`
- word *starting with* a double quote → `Interpolate([lit …])` even all-literal
- word with `$ref`:
  - exactly one ref, nothing else (`$x`, `"$x"`) → `Call("getVar", …)` directly
  - mixed (`a$x`, `"a$x"`, `$x$y`) → `Interpolate([lit|expr …])`
- mid-word double quotes (`a"b"c`) → quotes stripped, merged into `Str`
- assignment value uses the same word shaping (`x=1` → `Str("1")`,
  `x="a b"` → `Interpolate`, `x=$y` → `getVar`)
- `VAR=x cmd` → assignments folded into the exec `env` Object arg (3rd),
  NOT emitted as `Assign` stmts
- command word is always `Str(cmd, "DoubleQuoted")`; exec args are
  `Array([…])`; purity: `Emulable` iff cmd ∈ SYNC_BUILTINS (getVar → Emulable)
- `var_types`: numeric lift (every source parses as i64 or is `getVar` of a
  lifted var); string lift (every source is a literal `Str`/all-lit
  `Interpolate` or `getVar` of a lifted var); env-prefix vars excluded;
  names sorted; only lifted vars listed
- JSON: compact, `sort_keys`, raw UTF-8 → byte-identical to serde_json

## 6. Risks / guardrails

- **The ESTree worker is live** (main_loop_estree.pl, staging
  `sh2perl/src/estree.rs` + `harness/*`): we read the core, never write it;
  new files live only under `frontends/`; nothing in `harness/` is touched.
- **The corpus gate is the oracle** — for frontends, `debashc --shir` byte
  equality on the supported subset is the gate; when the core gains the
  shir-JSON→backend path, the same JSON runs the behavioral gate.
- **Never guess a shape**: every new construct is added by pinning the
  core's actual output first (this is how the v1 rules above were derived).
- Determinism: frontend output must be byte-stable run-to-run (sorted keys,
  no set-iteration nondeterminism).

## 7. Learnings (filled in after building)

**Session 1 build result (2026-08-04):** `frontends/` landed with the contract
tooling + first frontend + oracle. Corpus: **13/13 supported examples
byte-identical to `debashc --shir`; 0 FAIL; 514 honestly unsupported
(fail-loud); 4 core-skip** (core can't parse them either). Schema checker:
**0 violations over 70 diverse examples** (functions, nested cases,
pipelines). Tests: 10/10.

### L1 — Byte-identical JSON is easy once you know serde's rules
serde_json uses a BTreeMap → **keys always alphabetically sorted**;
compact (`{,}` separators); raw UTF-8. Python matches byte-for-byte with
`json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False)`.

### L2 — The word-shaping rules in §5 were wrong in places; probing pinned them
- **unquoted `\X` → `X`** (backslash dropped): `\$x`→`$x` (NO expansion),
  `a\ b`→`a b` (escaped space joins the word), `\'y`→`'y`, `\q`→`q`.
  `\<newline>` in UNQUOTED context → newline KEPT (no continuation!).
- **dq: `\\`→`\`, `\$`→`$` (no expansion), `\`+newline → line
  continuation (both dropped); other `\X` kept raw** (`\"`, `\``, `\n`,
  `\q`).
- **`\"` inside a MULTILINE dq word behaves differently from single-line**
  (core's multiline path processes it; single-line keeps raw) → refused,
  fail-loud, until the rule is understood.
- **Assignment values use the same `starts_with_dq` shaping as words**: `x=1`→
  `Str`, `x="a b"`→`Interpolate`, `x=$y`→`getVar`, `VAR=x cmd` → env Object.

### L3 — The A2 type analysis has surprising source-acceptance rules
- **string lift accepts ANY Interpolate** as a string-literal source
  (`dest_root=$emmccheck'p3'` → `Str`) — interpolation is string-typed by
  construction, not just all-lit parts.
- **multiline Str values are NOT lifted** (value containing `\n` → not a
  string-literal source) — the core's `var_types` stays empty for them.
- `getVar(lifted)` sources inherit the referenced var's verdict in BOTH lifts
  (`x=1; y=$x` → y:Int; `s="a"; t=$s` → t:Str).
- env-prefix vars (`VAR=x cmd`) are excluded, not assignment sources.

### L4 — Purity (A3) is derivable, not mysterious
`exec` purity = `Emulable` iff cmd ∈ SYNC_BUILTINS (42 names, mirrored in
pysh.py) else `Spawn`; `getVar` → `Emulable`. The frontend embeds the table;
when the A4 namespace JSON is published, frontends derive from it instead.

### L5 — Refuse > guess: the v1 posture turned 22 FAILs into honest "unsupported"
The first corpus run had 22 FAILs from half-understood constructs (keywords
`if`/`[[` mis-parsed as commands, `let`/`declare`/`eval` with arith semantics,
dq-escape context rules). A refuse-list (compound keywords + store/arith
builtins) made them fail-loud: 22 FAIL → 0 FAIL, 513 honest unsupported.
**Every construct enters the subset by pinning the core's exact shape first
(probe → implement → oracle-confirm).**

### L6 — Robustness lessons
- Subprocess timeouts + a fail-loud guard on non-advancing cursors caught two
  infinite loops (`|`, `<`, `<<` operators that `parse_line` returned on but
  `compile_` didn't consume).
- **Measure with exact bytes** (Python heredoc), never through outer-shell
  quoting — my first dq-escape probe was mangled by bash single-quote
  processing and produced wrong rules.
- The ESTree worker edits `shir.rs` live (its staging is `src/*` + `harness/*`):
  we read the core, never write it; new work lives only under `frontends/`.
  Within this session the A1/A2/A3 shapes were stable (the A1–A6 commits
  predate it); re-pin when the core moves.

### L7 — What this proves for the architecture
A frontend CAN be built and validated against the core's ShIR JSON contract
without touching the core: parse → emit → byte-diff oracle. The oracle is the
frontend's gate (like the behavioral corpus is the backends'), and
`equiv.py --strip-annotations` compares the semantic IR alone (A2/A3 are
core-attached annotations in the future architecture). Next: grow the subset
(redirects, pipelines, if/test, arith, `${}`, case, functions — each pinned
first); port the emitter to Go/ANTLR (contract tooling is language-neutral);
core deserializer to close the loop (plan §2.1).

---

## 8. Testing strategy (the pyramid) — agreed in discussion

For every source/target language pair, four layers. The bottom layers are
**execution-free** (arbitrary untrusted input is safe — no running the
output); the top layer is the only semantic anchor and requires curated,
trusted, side-effect-free examples.

| Layer | Input space | Executes? | Catches |
|---|---|---|---|
| 1. crash/consistency fuzz (frontend+generator) | full, incl. malformed | no | panics, hangs, malformed output, refusal-path bugs |
| 2. round-trip / idempotence / canonical-form nets | full, syntactically valid | no | frontend↔backend disagreement, non-idempotence, verbatim-bridge violations, non-idiomatic rendering |
| 3. oracle hierarchy (`F==C`, `O(F)==C`) | frontend subset ∩ corpus | no | frontend lowering fidelity (shell family) |
| 4. curated behavioral equivalence | small, trusted, side-effect-free | yes | actual semantic correctness vs the reference impl (bash, CPython, …) |

### 8.1 Layer 2 details — per-language consistency nets
For each language L with an idiomatic source-rendering backend:
- **Tier A (cheapest, no frontend):** canonical-form round-trip —
  `format(L → shIR → L) == format(input)` with a per-language formatter
  (black/gofmt/clang-format). Catches non-idiomatic/divergent rendering.
- **Tier B (consistency frontend):** full parse-tree round-trip —
  `ANTLR(L → shIR → L)` vs `ANTLR(input)`; enforces (a) **idempotence**
  `emit(parse(emit(parse(x)))) == emit(parse(x))`, (b) **verbatim-bridge
  fidelity** (refused constructs byte-identical), (c) canonical equality.
- **Scope:** the nets only mean something on the expressible intersection
  (the simple imperative core); the rest is verbatim-trivial or refused.
  Consistency frontends are deliberately partial.
- **ESTree caveat:** nets apply only to backends that render idiomatic
  source (Perl today; Python/C/C++ when those backends exist). The ESTree
  leaf emits runtime-dispatch JS — excluded; the net is a forcing function
  for idiomatic backend rendering.

### 8.2 The anchoring rule (judge one side only if the other is anchored)
- Shell: the core's backend is anchored behaviorally (vs bash) → `F==C`
  judges frontends. New languages: **anchor the backend behaviorally first**
  (output vs the reference impl on a curated corpus), THEN round-trip nets
  judge the frontend lowering.
- **Consistency ≠ correctness:** two self-consistent errors cancel. Layers
  1–3 are regression/crash/coverage nets, never semantic proofs; layer 4 is
  the only semantic anchor.

### 8.3 What the tests need from the core
- `--shir-in` (2.2) for `O(F)==C` and for executing frontend output.
- `--shir-unoptimized` (2.3) for error attribution.
- `loc` (2.5) for line-number semantics + debugging.
- `contract_version` (2.1) so every consumer fails loudly on drift.

### 8.4 Sequencing
1. Tier A formatters per backend language (cheapest consistency value).
2. `--shir-unoptimized` + `--shir-in` in the core (needs a coordinated
   commit — pause/coordinate with the ESTree worker).
3. Consistency frontends in difficulty order: **Python** (demo, py2py
   thread) → **C** (cheap grammar) → defer **C++** (very hard) and
   **Perl** (pure-ANTLR infeasible — context-dependent lexing; best
   semantic fit, hand-rolled parser if ever).
   *C++ is no longer deferred: see `CPP_PLAN.md` (tree-sitter-based
   C/C++ frontend, separate from the ANTLR fleet).*
4. Curated behavioral corpora per backend language (layer 4) as backends
   land.

### 8.5 What consistency nets tell us about the IR
Each frontend's supported subset empirically maps the IR's expressible
intersection for its language; the round-trip proves the subset round-trips
faithfully. This is the answer to "how universal is the IR?" — measured per
language, not asserted.

## 9. Learnings from the design discussion (session 2)

- **L8 — The oracle is only as strong as its anchor.** `F(S) == C(S)` judges
the frontend only because the core's backend is separately anchored
behaviorally (vs bash). For a new source language there is no `C`, so
round-trip tests judge the pair (or the backend, under a trust assumption)
— anchor the behavioral side first, then the structural tests mean
something.
- **L9 — "Unoptimised" vs "optimised" conflation.** The session's oracle
compares F against C's final export (which includes `optimize_stmts`
effects + annotations). v1 is benign (constant folding inert; dead-assign
conservative) but the honest formulation is `O(F(S)) == C(S)` once the
deserializer lands; `--shir-unoptimized` pins the raw boundary.
- **L10 — Execution-free testing is the only safe way to cover arbitrary
input.** Behavioral tests require curated, known-safe, side-effect-free
inputs (the shell corpus is safe only because it's curated). Crash-fuzz +
round-trip nets cover the full input space at zero execution risk;
consistency ≠ correctness, so they complement, never replace, the curated
behavioral layer.
- **L11 — Line numbers are semantics, not metadata.** `$LINENO`/`$BASH_LINENO`
/ trap-ERR require emitting the ORIGINAL source lines — the strongest
argument for `loc` (2.5). Quote-style fidelity failed the same test:
available in shell/Python/Perl but entangled with per-language semantics
(semantic in shell/Perl, cosmetic in Python, char-vs-string in C), so the
preference doesn't transfer and `StrStyle` capacity suffices.
