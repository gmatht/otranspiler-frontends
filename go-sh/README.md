# go-sh

**Go source → shIR JSON (A1 contract).** A hand-rolled lexer + recursive-descent
parser for the v2 Go subset described in the header of `go-sh.go`, lowering to
the exact A1 node shapes the core frontend produces for the equivalent shell
construct. The contract (`sh2perl/src/shir_json.rs`) is the source of truth.

This started as a line-by-line Go port of the Python shell frontend
(`../py-sh/pysh.py`); the 2026-08-06 rewrite replaced that scanner/parser with
a Go-source parser. Only the emitter rules (word shaping, var_types, purity)
are still shared with `pysh`.

## Build and gate

```sh
cd frontends/go-sh
make build      # go build -o go-sh ./cmd/go-sh
make test       # 1) A1 ingress acceptance  2) executed-stdout oracle
```

`make test` has two halves, both over `testdata/*.go`:

1. **Ingress acceptance** — every example must emit non-empty A1 JSON that the
   core deserializer accepts (`otranspilerl-cli - --target estree`). Byte
   equality with the core is not the signal here: the core consumes *shell*,
   and this frontend consumes *Go*.
2. **Executed stdout** (`harness/frontend-stdout.sh go`) — the Go source's
   native output must match the output of the emitted A1 run through the
   estree reference. This is the real semantic gate.

The corpus is **Go source only**. A `.go` fixture that is not valid Go belongs
to another frontend's corpus (`posix-sh-go` owns the shell examples); the
triage table in `harness/triage.sh` declares this frontend's corpus as `go`,
so a stray non-Go file in `testdata/` shows up as a phantom
FAIL-FRONTEND-EMIT on every sweep.

## Layout

- `go-sh.go` — the frontend: scanner, parser, lowering/emitter. `package
  golib`, so it doubles as a library.
- `cmd/go-sh/main.go` — the CLI wrapper (`--shir FILE --raw`).
- `testdata/` — the Go-source corpus (the gate's and triage's input).
- `Makefile` — `build`, `test`, and the A1 oracle bootstrap.
- `sync-builtins.sh` — regenerate the embedded `SYNC_BUILTINS` from
  `sh2perl/data/sh2-builtins.json` (the A4 namespace source of truth).
- `FRONTEND.md` — the supported subset and every explicit refusal.
- `DOGFOOD.md` — progress running the Go frontend over the Go codebase.

## Refuse > guess

A construct outside the subset refuses loudly with `unsupported …`; it is never
mis-lowered. `FRONTEND.md` carries the current refuse list. The corpus gate
grows the subset, and every new construct enters by pinning the core's exact
`--shir` shape first.
