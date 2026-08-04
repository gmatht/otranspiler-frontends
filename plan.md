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

## 2. What we still need from the core (future, do NOT block on)

1. **A shir-JSON → backend path (deserializer).** Today `--shir` is
   export-only (`shir_json.rs` hand-builds; `ir.rs` derives no Deserialize).
   Frontend output currently validates against the core frontend but cannot
   be *executed* by a backend without going through the Rust AST. Needed for
   the pipe architecture to close: `shir-json → IrProgram` + a structural
   gate on ingress (schema + whitelist, mirroring the ESTree gate).
2. **Pinned machine-readable purity table.** `call_purity` +
   `SYNC_BUILTINS` live in Rust; a frontend must mirror them (this session
   embeds a copy). Publishing them as the A4 namespace JSON (workspace
   harness) makes every frontend derive, not copy.
3. **Schema versioning.** The A1 JSON needs a version field + schema so
   frontends can fail loudly on drift instead of silently mis-emitting.
4. **Optional:** `debashc_optimize_shir` C-ABI export (debashcl.wasm
   precedent) for embedding, once the JSON pipe proves itself.

None of these block starting; they block *finishing* (executing frontend
output through backends).

## 3. What we can build now, without modifying the core

| Piece | What | Status |
|---|---|---|
| `shir-contract/schema.json` | hand-authored node/field schema from `shir_json.rs` | this session |
| `shir-contract/check_schema.py` | corpus coverage checker: every emitted node must fit the schema | this session |
| `py-sh/` (pysh.py) | first frontend: shell-subset parser + A1/A2/A3 emitter, byte-identical to `debashc --shir` on its subset | this session |
| `equiv.py` | equivalence harness: diff frontend vs `debashc --shir` per file (oracle = byte equality); `--strip-annotations` mode validates the semantic core without A2/A3 | this session |
| go-frontend/ | ANTLR Go frontend (the discussed architecture) — next session; contract tooling above is language-neutral and reusable | next |

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
