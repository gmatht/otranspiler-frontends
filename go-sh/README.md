# go-sh

The Go port of the [shell-subset frontend](../py-sh/pysh.py), emitting the
same **ShIR JSON (A1 contract)** that `debashc --shir` produces. Mirrors
every pinned rule from `frontends/plan.md` (L2 word shaping, L3
var_types, A2/A3 annotations, A4 purity).

## Status

**Scaffold — not yet compiled or run in this environment** (no `go`
toolchain available). The code is a line-by-line port of `pysh.py` and is
intended to be byte-identical to the core frontend on the v1 subset. When
a Go toolchain is present:

```sh
cd frontends/go-sh
go build -o go-sh .
./go-sh --shir testdata/t01_echo.sh --raw > /tmp/g.json
debashc --shir testdata/t01_echo.sh --raw > /tmp/c.json
diff /tmp/g.json /tmp/c.json   # should be empty (byte-identical)
```

The same byte-equality oracle (`frontends/equiv.py`) and full-pipe test
(`frontends/test_pipe.py`) apply unchanged once the binary is built.

## Why hand-rolled, not ANTLR

The plan discussed an ANTLR-based Go frontend. The scaffold here is
**hand-rolled** (lexer + parser + emitter, no parser generator) for three
reasons:

1. **No ANTLR toolchain in the scaffold environment** (and no `go` either).
   The semantics are the hard part; a parser generator only helps with the
   syntax.
2. **Shell word expansion is context-sensitive**, and ANTLR's lex-then-parse
   pipeline is a poor fit. The hand-rolled scanner does single-pass
   segment analysis (matches `pysh.py`).
3. **The emitter is a mechanical port of the proven `pysh` reference**, so
   the grammar matters less than mirroring the pinned lowering rules.

To port to ANTLR later: extract the scanner/parser into an ANTLR4
grammar (`Bash.g4`), generate the Go parser, and re-attach the emitter
(`internal/shir/` in this scaffold).

## Layout

- `go-sh.go` — single-file program: scanner, parser, emitter, var_types,
  purity, CLI. Mirrors `pysh.py` 1:1.
- `go.mod` — module declaration (no external deps).
- `testdata/` — curated v1 examples (mirror of `frontends/tests/`).
- `sync-builtins.sh` — regenerate the embedded `SYNC_BUILTINS` from
  `sh2perl/data/sh2-builtins.json` (the A4 namespace; the JSON is the
  source of truth — the sh2perl lib test `a4_sync_builtins_matches_rust`
  asserts the two stay in sync).
- `Makefile` — `make build`, `make test`, `make sync-builtins`.

## Pinned rules (must match pysh + the core)

- **Word shaping (L2):** unquoted `\X` → `X` (drop backslash, `\<newline>`
  kept as newline); dq `\\`→`\`, `\$`→`$`, `\`+newline → line
  continuation (dropped), other `\X` raw; `\"` inside *multiline* dq
  refused.
- **Type analysis (L3):** numeric lift = every source parses as i64 or
  `getVar`-of-lifted; string lift = `Str` (not multiline) or any
  `Interpolate` or `getVar`-of-lifted; env-prefix vars excluded.
- **A3 purity:** `exec` = `Emulable` iff cmd ∈ `SYNC_BUILTINS` else
  `Spawn`; `getVar` → `Emulable`.
- **A1 contract:** `contract_version: 1` on Program; same node vocabulary
  and key ordering as `shir_json.rs` (Go's `encoding/json` sorts map keys
  alphabetically, matching serde_json's BTreeMap output).

## Refuse > guess

v1 refuses: pipe/redirect/`$(( `/`${`/backtick/positional/array/case/arith
builtins (`let eval declare typeset local readonly export unset read shift
source trap set exec test time .` `[[`)/compound keywords (`if while for
case function ...`). The corpus gate grows the subset; every new
construct enters by pinning the core's exact `--shir` shape first.
