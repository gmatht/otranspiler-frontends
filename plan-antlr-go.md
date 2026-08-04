# Plan: ANTLR + Go frontends (supersedes the earlier Rust plan)

**Status:** The earlier `plan-antlr-rust.md` proposed Rust frontends
("match the core's language"). The user correctly challenged that:
the frontends don't have to match the core's *language* — they have
to match the **generated parsers' target language** and the **A1
contract**. The official grammars in `grammars-v4` are tested in Go
(and others); the `antlr4-go` runtime is the most mature ANTLR4
binding; the existing `go-sh/` worker scaffolding is already Go. **Go
is the cleaner choice.** This document supersedes the Rust plan —
same architecture (ANTLR + official grammars + shir-emit), Go
implementation throughout.

## 0. Why Go (not Rust) for the frontends — the corrected framing

- **The boundary is the A1 shIR JSON, not the core's Rust types.** The
  frontends emit the contract; the core consumes it (deserializer +
  ESTree/Perl backends). The frontends cross the language boundary at
  the JSON. They don't import the core's Rust types — the contract is
  language-agnostic. So "match the core's language" was an
  over-correction; the right alignment is "match the generated
  parsers' target" + "match the worker scaffolding."
- **The official grammars are Go-first.** `grammars-v4` includes Go
  in its default test target list, and many grammars
  (`golang/`, `python/`, `javascript/`, `cpp/`, `java/`, `c/`, `rust/`,
  etc.) have a generated-Go subdir with a ready-to-import Go parser
  (e.g., `golang/Go/go_parser_base.go` imports
  `github.com/antlr4-go/antlr/v4`). Using Go means: take the grammar,
  generate the Go parser, **import it directly as a Go package**,
  walk the parse tree with a Go listener that emits shIR. The
  pipeline is Go-native end-to-end.
- **The existing `go-sh/` is already Go** (hand-rolled). Upgrading it
  in place to ANTLR-Go (same directory, same language, same worker
  scaffolding, swap parser+emitter) is the lowest-friction change.
  The Rust plan would have required a new crate, a Cargo workspace,
  and a new worker — more work for the same outcome.
- **Rust is still a valid target** (it's in `test.sh`'s default list,
  and 6 grammars officially test in Rust: `c`, `zig`, `abb`, `gql`,
  `maml`, `sysml-v2`). The Rust plan's *architecture* is right; its
  *implementation language* was the wrong call. We can still do
  Rust later if a reason emerges (e.g., a future backend wants to
  share types with a Rust frontend) — the plan is language-agnostic
  in structure.

## 1. The right architecture: ANTLR + Go + a shared shir-emit package

**Pipeline per frontend:**
```
official .g4 (grammars-v4/{python,golang,...}/<Lang>Lexer.g4 + <Lang>Parser.g4)
  │
  ▼  antlr4 -Dlanguage=Go -package <lang>  (or trgen -t Go)
generated lexer + parser (Go, uses github.com/antlr4-go/antlr/v4 runtime)
  │
  ▼
hand-written Go listener/visitor (walks the parse tree)
  │
  ▼
emits the A1 shIR JSON (byte-identical to the core's shir_json.rs)
```

**Why this is right:**
- **Uses official grammars** — no hand-rolled lexers/parsers. The
  `.g4` files are mature, maintained, and tested in Go (and other
  targets) by the upstream CI.
- **Uses ANTLR** — the parser-generator approach the plan called for.
- **Uses Go** — matches the generated parsers' primary target, the
  `antlr4-go` runtime, the existing `go-sh/` worker, and the
  upstream's Go test path.
- **The emitted shIR is the same A1 contract** every backend consumes.
  The emitter (listener) is hand-written Go but it's the same
  emitter logic as the current hand-rolled frontends — a few hundred
  lines per frontend, modeling the same v1 shell-flavored subset
  (assignment, if/while/foreach, system/exec/qx, $var/$ENV, string
  interpolation, heredocs, comments, shebang; refuse package/use/
  sub/bless/->/regex/etc.).

## 2. The shir-emit package (the deferred #4, in Go)

The Go listener needs to **build shIR values** (the same A1 program
shape that `sh2perl/src/shir_json.rs` emits). Two options:

**(a) Hand-written `Emit(program *ShirProgram) ([]byte, error)` per
frontend.** Each frontend constructs an in-memory `ShirProgram`
struct (Go) and a hand-written `Emit` serializes it to the A1 JSON
in the exact byte order the contract requires (sorted keys via Go
`encoding/json` with `json.Marshal` on a `map[string]any`, which
sorts keys). This is the same pattern as the current
`pysh.py`/`go-sh.go` emittters (hand-built `json!`-equivalent in Go).
**Pro:** trivial; no extra dependency. **Con:** per-frontend
duplication of the contract shape.

**(b) A shared `shir-emit-go/` package** (a single Go module /
package) with a canonical `ShirProgram` type and `Emit` function.
All frontends import it. **Pro:** single source of truth for the A1
JSON shape (the lockstep-drift problem the deferred #4 was meant to
solve, now solved in Go). **Con:** one more Go module to maintain.

**Recommendation: (b).** It's the right time to do #4 properly — in
Go, where the frontends live. The Rust deferred #4 (serde-derive in
`sh2perl/src/ir.rs`) is **further deferred** or becomes a Go
equivalent (a hand-written `Emit` in `shir-emit-go/` achieves the
same "single source of truth" goal, in the language that matters for
the frontends). The core (`sh2perl/src/*.rs`) is unchanged.

## 3. Per-language plan

| Frontend | Grammar source | Go target officially tested? | Plan |
|---|---|---|---|
| **Go** (`go-sh/` upgraded in place) | `grammars-v4/golang/GoLexer.g4` + `GoParser.g4` | Yes (Go in default list) | **First.** Replace the current hand-rolled Go frontend with ANTLR-Go. Same directory, same worker, swap parser+emitter. Use the official `golang/examples/` as test inputs. **Lowest-friction change** — the only first frontend that is an *upgrade in place* of existing code. |
| **Python** (`py-sh-go/`) | `grammars-v4/python/python3/Python3Lexer.g4` + `Python3Parser.g4` | No (Python3 target only) | Use official `.g4`; generate Go parser; write Go test suite that parses `python/python3/examples/`. Replaces the old `py-sh/` (Python, now in `~/junk/`). |
| **Perl** (`perl-sh-go/`) | **None in grammars-v4** (no `perl/` dir) | n/a | **Gap.** Options: (i) find a third-party ANTLR4 Perl grammar on GitHub; (ii) **write a custom `.g4`** for the shell-flavored Perl subset (the v1 constructs from the prior `perl-sh.py` attempt); (iii) defer (lower priority per the earlier shell-family-first recommendation). A full Perl grammar is famously context-sensitive and ANTLR-incompatible; a **subset** grammar is tractable. |
| **POSIX sh / bash** (`posix-sh-go/`) | **None in grammars-v4** (no `bash/`, `shell/`, `posix/`, `zsh/`, `fish/`, `sh/`, `powershell/`) | n/a | **Gap — highest value after Go/Python.** Same options. The shell-flavored subset is small and well-defined. A **custom `.g4`** informed by the POSIX shell grammar (there are existing EBNF/yacc grammars for bash; porting one to ANTLR4 is the right call). This is the core's product scope. |
| **zsh, fish, busybox** | None | n/a | Future; same custom-grammar pattern. |

## 4. Integration with the sh2perl core (unchanged from the Rust plan)

- **The listener/emitter produces the same A1 JSON** as the current
  hand-rolled frontends. The deserializer
  (`sh2perl/src/shir_json_in.rs`) accepts it; the ESTree backend
  consumes it; the Perl backend consumes it (`ir_to_perl`); the
  corpus gate (`fail`/`fail-estree`) compares against bash. **No
  core change needed** for the first frontend.
- **The per-worktree/per-frontend worker** (`setup_backends.sh
  --start-workers`, with `pi` + deepseek-v4-flash + automatic key
  rotation) **adapts unchanged**: each
  `frontends/<lang>-go/run_worker.sh` calls `setup_backends.sh
  --wait`, runs `go build && go test`, commits within scope
  (`frontends/<lang>-go/` + harness/*), and on failure invokes `pi`
  (opencode-go + deepseek-v4-flash) scoped to the frontend's dir.

## 5. The verification layer (replaces the Python `~/junk/` layer)

The hand-rolled Python verification (`equiv.py`, `test_pipe.py`,
`check_contract.py`, `shir-contract/check_schema.py`, now in
`~/junk/`) was built around the hand-rolled frontends. The Go
frontends need a new verification layer — **as `go test` in each
frontend module**. The Python files in `~/junk/` are the reference
for what the tests should do:

- **Byte-equality oracle** (replaces `equiv.py`): `go test
  ./internal/equiv` — for each `testdata/t*.sh`, run
  `debashc --shir <file> --raw` (the core) and
  `<frontend-go> --shir <file> --raw` (the new frontend), assert the
  two JSON outputs are byte-equal (the core #1 fix means `--shir
  --raw` has no trailing newline; the Go frontend does the same).
- **Roundtrip / ingress acceptance** (replaces `test_pipe.py`):
  `go test ./internal/roundtrip` — emit via the frontend, ingest
  via `debashc --shir-in-estree` (or `--shir-in-perl` for Perl),
  assert the deserializer accepts every emit (no ingress failures).
- **Contract-version consistency** (replaces `check_contract.py`):
  `go test ./internal/contract` — assert the core's emitted JSON
  has `contract_version: 1` and the frontend's emitted JSON has
  `contract_version: 1`, and they match.

## 6. Bootstrap steps (concrete, ordered)

**Toolchain (one-time):**
1. Install the **ANTLR4 tool** (Java-based generator):
   `antlr4` (the standard CLI). The `grammars-v4` repo uses the
   C#/.NET Trash toolkit for grammar-level validation, but the
   `.g4` files are the same; we can use `antlr4` directly or `trgen`.
2. Get the **Go ANTLR4 runtime**: `go get
   github.com/antlr4-go/antlr/v4` (the runtime; the `golang/Go/`
   generated parser imports it).
3. (Optional) Install Trash toolkit for grammar-level validation:
   `dotnet tool restore` + `trgen`/`trparse`. Useful but not
   required — our own `go test` is the real validation.

**Per-frontend bootstrap (`go-sh/` upgraded in place — the
template — do this first):**
1. `cd frontends/go-sh && go mod init github.com/gmatht/sh2loop/frontends/go-sh`
   (or use the existing module). Add `github.com/antlr4-go/antlr/v4`.
2. Copy the official grammar: `cp ~/sh2loop/grammars-v4/golang/GoLexer.g4
   GoParser.g4 frontends/go-sh/grammars/`.
3. Write a `build.go:generate` directive (or a Makefile target) that
   invokes `antlr4 -Dlanguage=Go -package go -o generated/
   grammars/GoLexer.g4 grammars/GoParser.g4` (regenerate on grammar
   change).
4. Write the hand-written **Go listener/visitor** (walks the parse
   tree, builds the shIR program, calls the shared
   `shir-emit-go.Emit(program)` to serialize to the A1 JSON).
   ~300-500 lines, modeled on the current `go-sh.go` emitter logic
   (which already produces byte-equal output for the v1 corpus).
5. Write the CLI: `go-sh --shir <file> [--raw]`.
6. Write the `go test` suite (byte-equality, roundtrip, contract).
7. Write `frontends/go-sh/run_worker.sh` (the scoped worker:
   `go build && go test` → commit within scope → on test failure,
   `setup_backends.sh --pi-fix frontend go-sh`).
8. Verify: `go test ./...` in the frontend module → byte-equal on
   the v1 corpus (the existing `go-sh/Makefile` testdata is ready).
9. `setup_backends.sh --start-workers` (the existing scoped worker
   for `go-sh` picks it up — just with the new ANTLR backend).
   Confirm 13/13 corpus byte-equal.

**Then replicate for Python** (`py-sh-go/`): same pattern, official
`python/python3/Python3Lexer.g4` + `Python3Parser.g4`, new Go module
in `frontends/py-sh-go/`, Go test suite against
`python/python3/examples/`.

**Then the shell family** (`posix-sh-go/`, etc.): custom `.g4` for the
shell-flavored subset. **Highest value** — the core's product scope.

**Then Perl** (`perl-sh-go/`): custom `.g4` or third-party.

## 7. Honest scope and order

Multi-session work (matching the per-weekend scope of prior turns):

- **Session A (this weekend, if you want):** upgrade `go-sh/` in place
  to ANTLR-Go. The pipeline proof. The template for all subsequent
  frontends. **Lowest friction** — it's an in-place upgrade of
  existing code, same directory, same language, same worker. ~one
  focused day of work. Includes the `shir-emit-go/` shared package.
- **Session B:** `py-sh-go/` (replicate the template; official grammar
  available).
- **Session C:** `posix-sh-go/` (custom `.g4` for the shell subset).
  Highest value — the core's product scope.
- **Session D:** `perl-sh-go/` (custom `.g4` or third-party).
- **Ongoing:** zsh, fish, busybox, etc.

## 8. What this replaces (the `~/junk/` layer is dead code)

- `py-sh/pysh.py`, `perl-sh/perl-sh.py` — replaced by `py-sh-go/`
  and `perl-sh-go/` (Go, ANTLR).
- `go-sh/` (current hand-rolled Go) — upgraded in place to
  ANTLR-Go (same dir, same language, better parser).
- `equiv.py`, `test_pipe.py`, `check_contract.py`,
  `shir-contract/check_schema.py` — replaced by per-module `go test`
  (byte_equality, roundtrip, contract_version).

The move to `~/junk/` was the right prep step — it cleared the deck
so the ANTLR-Go architecture is the only path forward. Nothing there
needs to be restored; it's all replaced by the plan above.
