# PLAN_JAVA_F — a Java frontend: viability, language, architecture

**Status: DECISION SESSION (2026-08-16) — analysis only, no code written.**
This document records a discussion that walked the Java-frontend question
end-to-end: viability of a defined Java subset on the A1 contract, the
existing Java→JS tool landscape, the implementation-language decision
(including the wasm/cgo fork and the clib-sharing question), and the
dual-transport architecture for Rust frontends. It supersedes no plan
(no Java frontend exists yet); it is the decision record to build from.

Grounding facts (verified this session): the fleet gates are c-sh-go
103/103, py-sh-go 77/77, go-sh 86/86, cpp-sh-go 10/17 (v5.1 note);
the java *backend* (`sh2perl/src/java_backend.rs`) gates 3/616; the python
*backend* (`backends/python` worktree, `src/python_backend.rs`) gates
0/539 (all stubs); otranspiler wires `.py` on BOTH the source and target
sides but the target ingress flag `--shir-in-python` is NOT wired into the
core CLI (only `--shir-in-perl` / `--shir-in-estree` / `--shir-in-sh` exist
in `cli/src/lib.rs`).

## 0. TL;DR verdict

A Java frontend is viable **as a defined subset** — "immutable-value Java":
static methods + primitives + immutable strings + records, no heap-aliased
mutable objects, no dispatch, exceptions refused (or the v24 A1 `Try`
node). The three arguments that upgrade the ceiling from "Java-flavored C":

1. **UB is neutralized by subset definition** — c-sh-go already documents
   "no UB, bounded pointers"; Java's fixed-width wrap is the same problem,
   equal footing.
2. **Immutable strings are a headwind *removed*** — Java subset code only
   ever REBINDS a String (`s = s + x` → fresh value); the alias-mutation
   class of C `char*` bugs (c-sh-go's mem-slice/dynamic-position machinery,
   v20) is impossible by construction.
3. **The closed, spec-defined JDK surface is a structural advantage** —
   a whitelisted JDK slice is bounded by construction (no FFI; JNI/FFM/
   reflection refusable as a complete set; `Runtime.exec`/`ProcessBuilder`
   is the single well-defined seam — the same `bash -c`-shaped seam the
   runtime already has). Contrast: C links anything, Python `import`
   reaches native extensions, shell's every-external-command IS an FFI
   (the `check_qx.pl` allowlist exists for that reason).

The wall that survives all three: **heap-aliased mutable objects**.
Records / immutable value classes (final fields, constructor-once,
generated `equals`) render onto nodes the contract already has
(`Object`/`Json`/assoc arrays) and are the defined middle tier. Exceptions:
refuse, or take the Python-shaped `Try` (try-with-resources / multi-catch /
finally all refusable).

## 1. Where Java sits against the existing Java→JS tool landscape

Three incumbent families, all JS/WASM-only:

- **Source transpilers**: GWT / J2CL (full Java + JRE emulation, Closure
  output), JSweet (Java→TypeScript).
- **Bytecode compilers**: TeaVM, Bck2Brwsr (`.class` → JS/WASM, AOT).
- **JVM-in-browser**: CheerpJ, DoppioJVM (carry the whole JDK).

Our path (Java source → A1 → ESTree → sh2runtime JS) is a source
transpiler with a **shell-shaped IR**. On the JS axis the incumbents win
decisively: full Java semantics, whole-program optimization (GWT's DCE,
J2CL's Closure passes), mature tooling. Our path's defensible reasons are
ALL off the JS axis: multi-target (Java→perl/c/go/python/zig/sh has no
incumbent at all), one runtime executes many sources, refusal-beats-JRE-
emulation, and provable fidelity for the subset (executed-stdout oracle
vs `javac`+`java`, the corpus discipline).

**Idiom spectrum** (how idiomatic is their JS?): idiom is bought with
fidelity. TeaVM/CheerpJ — N/A or compiler-shaped; GWT — famously
anti-idiomatic by design (size/speed, `-style PRETTY` bolted on); J2CL —
moderately idiomatic, Closure-flavored (`goog.module`, annotations);
JSweet — the ONLY idiomatic one, and it achieves it by shrinking Java to a
JS-shaped subset (candies map JDK → JS stdlib, `@js` escapes, features with
no JS-idiomatic form refused). That is exactly our move — except we shrink
toward a **bash-shaped** subset, so our JS output is idiomatic relative to
the `sh2.*` runtime contract, not vanilla JS. The typed paths already emit
near-native JS (`| 0`, `Math.imul`, `>>> 0`, BigInt — c-sh-go's
C-executed ESTree gate proves the shapes); the `sh2.*` call sites are the
bash-semantics leakage. Idiom is not a stated pipeline goal (fidelity is
the gate); making the Java frontend emit "clean path" JS is a real
engineering choice, not a given.

## 2. Implementation language — the decision record

Candidates evaluated against: parser ergonomics for Java, fleet
consistency, wasm packaging, and (for Java specifically) the thinness of
clib reuse.

| Option | Parser | wasm | Fleet fit | Verdict |
|---|---|---|---|---|
| Go handrolled | C-size proven (c-sh-go) | busybox `GOOS=js` | default | fine, but handroll is more work than C's (Java's expression layer is bigger) |
| Go + tree-sitter (cgo) | tree-sitter-java | **impossible** (CPP_PLAN §2: Go wasm has no cgo) | default | the wasm fork kills it for packaging |
| C++ + tree-sitter (cproc) | tree-sitter-java | wasi-sdk → wasm32-wasi, native ELF + wasm | exception architecture | works, but a C++ A1 emitter forfeits the Go emit helpers |
| JS (web-tree-sitter / ANTLR4ng / handrolled) | **best of the options** | **free** — JS is the browser; grammar wasm loads directly | Node already a hard fleet dep (estree-runner.mjs, frontend-stdout.sh) | strongest for browser endgame + velocity |
| Rust (dual-transport) | tree-sitter official bindings (`cc` native), syn precedent (rust-frontend) | native `cc` fine; debashc.wasm needs C toolchain → wasm32-wasi at release | in-core link = one binary | the only path to the one-binary release |

Key wasm fact (CPP_PLAN §2, verified): both Go tree-sitter bindings are
cgo; Go's `js/wasm` and `wasip1` targets have no cgo; a tree-sitter
frontend therefore cannot be merged into the busybox Go wasm. JS dissolves
this fork entirely (web-tree-sitter's native format IS wasm); Rust handles
it natively (grammar C compiled by `cc`) but the wasm release build needs
the wasi C toolchain (the cproc pattern in Cargo form).

**The i64 serialization rule (JS path)**: A1 `Int` nodes serialize as JSON
numbers (`json!({"type":"Int","value": i})`, i64) and the core ingress
requires `as_i64()`. JS Numbers cannot represent i64 past 2^53 — a JS
frontend must emit `Int` nodes with a hand-rolled emitter writing the exact
digit string (the core's serde_json parses the number token exactly).
Rule: don't `JSON.stringify` the A1.

## 3. Porting clib — concluded: the wrong frame

`clib` (c-sh-go/main.go, 5069 lines) decomposes: the C lexer/parser (the
bulk — **useless for Java**, which shares no token surface with C), the A1
emit helpers (`st()`/`call()` maps, ~50 lines, re-specifiable), the
C-shaped folding table (Java needs a different table: String/Math/Integer),
sizeof/malloc folding (C-specific). cpp-sh-go reuses clib because C++ shares
C's token surface — the whole trick is "demote the tokenizer to C-text
reconstruction". Java has none of that.

**clib is not the shared asset — the A1 contract + core analyses are.**
Evidence: the lowering is already distributed — Go clib (parse/emit) +
`harness/outparam_to_returns.py` (395 lines of Python, post-emit out-param
transform) + Rust core (typed lowerings, printf folds, analyses) + JS
runtime (`harness/sh2-namespace.mjs`, printf/typed-arg emulation). The
fleet already has two independent emitters (Python, Go) held honest by the
byte-oracle; a JS emitter would be a third — that is the pattern working.
The anti-drift mechanism is the byte-oracle + `check_schema.py`
(0/70 violations), NOT code sharing. "Port clib to JS" ≈ porting 5% of it;
"port clib to Rust" = the in-core-frontend architecture (see §4), whose
blocker is the boundary discipline, not the language.

## 4. Dual-transport architecture (Rust frontends, JSON shims for dev)

Proposal: frontends as **Rust libraries linked into the core for release**,
with **JSON shims for development**. Endorsed with three rules:

1. **The `Frontend` trait is the seam**: `fn parse(&str) -> Result<IrProgram,
   String>`. The JSON CLI (dev/gate) and the in-core linker both call it —
   never two implementations. The JSON path is `parse()` → `shir_json`,
   nothing more.
2. **The gate stays on the JSON path — always.** Both oracles are
   contract-level (byte-equality vs core `--shir` for shell-dialect
   frontends; executed-stdout vs native for C/Java/Python). Plus a release
   smoke test asserting transport identity: in-process emit == JSON-path
   emit byte-identical (the `O(F(S)) == C(S)` oracle of plan.md §2.3,
   generalized to the transport boundary).
3. **The crates stay external and pinned** (the sh2runtime model): own
   repos/CI running the JSON gate; the core's `Cargo.toml` pins the rev.
   Release = a pin bump, not core commits. Otherwise "linked for release"
   quietly becomes "the core owns all frontends" — the exact thing the
   `frontends/` boundary prevents.

**The dependency cycle — the structural prerequisite**: a Rust frontend
producing `IrProgram` must depend on the crate that defines it (debashl),
but the release link is core → frontend. The fix: factor `IrProgram` +
`shir_json` out of `src/ir.rs` into a standalone published crate (e.g.
`a1-contract`) that debashl, the frontends, and the backends all consume —
a core-touching refactor, but a net positive (the contract as a
first-class versioned crate is the architecture's stated ideal). JS
requires none of this — the Rust migration touches the core; the JS path
touches nothing.

**The dev-shim win is the strongest half**: it decouples frontend
iteration from core rebuilds — the documented operational pain (c-sh-go
FRONTEND.md v5.2: a concurrent core rebuild starved/OOM-killed every gate
run, 172s vs 41–53s). The release link is optional packaging (one binary,
no emitter drift); it is not a capability unlock — the JSON transport
remains a perfectly viable permanent contract.

## 5. Rust vs JS migration — an endgame bet

The deciding axis is **is the release artifact one Rust binary, or is the
browser a first-class target?**

- **Rust** is the only path to the one-binary release (in-process link is
  Rust-only; no Rust JS engine worth embedding). Costs: the `a1-contract`
  factoring, full re-implementation per parser, rustc build costs. Wasm
  story stays uniform (`debashc.wasm` is already Rust).
- **JS** is free for the browser endgame and iteration (no compile; the
  oracle is JS; Node is already a fleet dependency). Costs: process
  boundary at release (Node stays a runtime dep — one-binary foreclosed),
  the i64-emit rule, drift policing stays on the byte-oracle.

**Do not migrate the working fleet for its own sake**: zsh/fish/bat/posix/
powershell/go-sh are Go, gated green, transport cost nil. The decision is
about the NEW frontends, made once: if "debashc does everything, one
binary" is the release story → Rust, now (write the Java frontend once as a
dual-transport crate; writing it in JS now and migrating later is writing
it twice). If the browser is a first-class target → JS with web-tree-sitter.
If neither is binding → the language is a footnote; Go (status quo) is
fine, and the Frontend trait + JSON gate is the only thing worth
standardizing.

## 6. Where Python sits in otranspiler (the two-sided precedent)

Python is the only language wired on BOTH sides of otranspiler, and it
demonstrates the full loop pattern the Java frontend would follow:

- **Source** (`input file.py`): `sources[".py"] = "frontends/py-sh-go/
  py-sh-go"` — a Go program (3465 lines, handrolled Python-subset parser →
  A1 JSON), the reference frontend whose emit shapes c-sh-go mirrors,
  gated 77/77.
- **Target** (`output file.py`): `targets[".py"] = "flag:python"` → would
  invoke `debashc --shir-in-python`, but that ingress flag is NOT wired in
  the core CLI (only `--shir-in-perl` / `--shir-in-estree` / `--shir-in-sh`
  exist) — otranspiler hits the "target %q not wired" error. The renderer
  (`sh2perl/src/python_backend.rs`, in-process, bypasses JSON) lives in the
  `backends/python` worktree, unmerged; its gate is 0/539 (all stubs) — the
  weakest backend in the fleet (c 30/539, rust 23/539, java 3/616).

The lesson: frontends are external processes speaking JSON; backends are
Rust-in-worktrees consuming the same contract; the core's merged ingress
arms are the wiring seam. A Java frontend ships as the same shape:
external parser → A1 JSON → (eventually, if the Rust/dual-transport
architecture lands) an in-process crate behind the `Frontend` trait.

## 7. Decisions & open questions

Decisions (this session):

1. A Java frontend is viable as an immutable-value defined subset;
   idiomatic OO (mutation, dispatch, collections) is refused.
2. The Java→JS comparison is the weakest possible framing for our path;
   the value is Java→anything (multi-target), not Java→JS.
3. Don't port clib to JS or Rust — re-specify the small A1-emit library
   per frontend; the shared asset is the contract + core analyses.
4. The dual-transport architecture (Frontend trait + JSON gate + external
   pinned crates) is the right shape IF the Rust path is chosen.
5. Rust vs JS is an endgame bet resolved by the release story, not by
   capability; don't migrate the working Go fleet.

Open questions:

- Is the release artifact one Rust binary (→ Rust, dual-transport) or is
  the browser a first-class target (→ JS, web-tree-sitter)?
- Who owns the `a1-contract` crate factoring (a core-request? the estree
  worker? — it touches `src/ir.rs` + `shir_json.rs`, the shared core)?
- For the Java subset: records-only middle tier, or refuse classes
  entirely in v1? Exceptions refused, or the v24 `Try` node from day one?
- Does `String` `==` on the subset mean `.equals()` (semantic lie) or is
  `==` on String-typed operands refused (honest)?
