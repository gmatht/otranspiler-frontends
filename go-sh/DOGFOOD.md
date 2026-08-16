# DOGFOOD — our Go frontend, translated to JS/Rust

Per TRANSLATE_ONE_APPLICATION.md: the source application is **our own Go
frontend** (`frontends/go-sh/go-sh.go` — the golib — plus
`cmd/go-sh/main.go` — the CLI); the targets are **JS (estree, executed)**
and **Rust (rendered + rustc-compiled + run)**. The oracle is the source's
native behavior (`go run`). The gates:

- `./fail-go` — Go→JS end-to-end over `testdata/*.go` (the corpus ladder).
- `./fail-go --rust` — adds the Rust target verdict.
- `./fail-go --app <file.go>` — a single app as the integration test.
- `./translate_one_application.sh mine|probe|fix|gate --lang go` — the
  playbook loop (templates in `templates/go/`).

## Status

| gate | green |
|---|---|
| corpus Go→JS (`fail-go`) | **88/88** |
| corpus Go→Rust (`fail-go --rust`) | 41/88 (rust backend `sh2.*` stubs: assoc arrays, argv, etc. — backend gap, per-target) |
| Go idiom ladder (46 templates, mined from the app + seeded) | 41/46 (4 contract boundaries + 1 documented nondeterministic) |
| app integration (the CLI, `fail-go --app`) | red — EMIT-FAIL (frontier, see below) |

### New idioms mined from the app (this pass)

From the go-sh frontend's own code (`break` ×21, `strings.Builder` ×2,
`err`-target Atoi, nil checks, return values, continue): **6 atoms went
PASS** after in-scope frontend fixes (go-sh.go): `break` → A1 Break node,
`strings.Builder` → the buffer accumulator (mirroring bytes.Buffer),
`n, err := strconv.Atoi(...)` → the `err` target skipped. Probes:
`break_loop`, `builder`, `continue_loop`, `err_check`, `nil_check`,
`return_val` — all green, in the ladder. Four constructs are contract
boundaries escalated to `core-requests/go-sh-dogfood-20260815-contract-boundaries.md`:
index+value range (`for i, s := range` — needs an index-binding For),
higher-order funcs, structs/methods, `strings.Index` position values.

**Landed this pass (probe `const_iota`, green):** `const` clauses —
Go compile-time constants. The app's own token-kind block
(`tEOF tokKind = iota` + implicit repeats) used to die in the parser
("unexpected token tokKind after expression"); the frontend now parses
`const x = expr` / `const ( specs )` and lowers each name to an Assign of
its EVALUATED value (the A1 has no const/iota node — a tokKind value is
a plain scalar). iota counts specs per block; a spec without `=` repeats
the previous expression with iota substituted; RHS supported: iota,
integer literals (±, + − × ÷ folding), string literals, and references
to earlier consts. Anything else refuses loudly (Refuse > guess).

## The app's construct footprint

`mine frontends/go-sh/go-sh.go` fires 32 of the 36 seeded templates
(capped to 16 probes). The golib's own dialect beyond the corpus: maps,
structs, methods, interfaces, type switches, error values, goroutines,
`encoding/json`, `sort`, `strings.Builder`, `fmt.Errorf` — most of which
have **no shIR representation** (the A1 is shell-flavored; a Go struct or
method call cannot be expressed). That is the (a)/(d) boundary:
translating the *golib's* JSON-emission behavior into JS would require a
contract change, queued to the core — never forked in the frontend.

## Gap classification (the dog-food work plan)

Fixes landed in this effort (in-scope surface only):

- **(b) frontend gap — nested arith nodes**: the golib's `arithBin`
  wrapped every subexpression in `{"type":"Arith"}`, but the A1 contract
  wants bare children with ONE top-level wrapper. The deserializer
  rejected `x := 1 + 2 * 3` ("unknown arith type"). Fixed in `go-sh.go`
  (`arithBin` bare + `arithWrap` at the expression boundary) — this is a
  real translation bug the dog-food mining exposed; probes + corpus green.
- **(b) probe/template fixes**: `func_decl` (complete program, not a
  wrap-able snippet), `str_prefix` (literal args), `type_switch`
  (`any`, not `interface{}`), `array_slice` (`strings.Join`, the
  supported slice form), plus the oracle wrapper's duplicate `"os"`
  import and missing `os/exec`/`strconv`/`filepath`/… detections.
  The wrapper's `os`-import detection also now covers `os.Args`/
  `os.ReadFile` (translate_one_application.sh `run_native`,
  aligned with fail-go's wrap_go).
- **(d) documented, not forced**: `go_stmt` (`go func(){…}()` + the
  immediate `fmt.Println` — native Go races; the translated JS is
  deterministic `bg|main`. The oracle itself is unstable — the probe
  stays red, documented, per Refuse > guess).

Remaining app-construct gaps (classified, queued):

- **(b) frontend gaps to grow next**: `os.ReadFile`, `os.Stdout.Write`,
  `fmt.Fprintln(os.Stderr, …)`, `os.Exit(code)`, `string([]byte)`
  conversions. LANDED this pass (probe `range_args`, green):
  `for _, a := range args` over a runtime-loaded array (`args :=
  os.Args[1:]` — the CLI's argv filter loop) now lowers to the
  `${arr[@]}` For-iter shape, ONE array-valued
  `param("slice", name, "@", "")` element that the runtime's
  forLoop flattens (the core emits exactly this for
  `for x in "${arr[@]}"` — the A1 For iter is a static element
  list whose elements are EXPRESSIONS, so the runtime-array form was
  never a contract boundary; go-sh.go parseFor). `for i, s := range`
  (index+value) still REFUSED — the index-binding For boundary (§1).
- **(a)/(d) contract boundary**: structs, methods, interfaces,
  `encoding/json`, `sort` — no A1 shape; the CLI's behavior depends on
  the golib's JSON marshaler, so the full app cannot translate until the
  contract grows. The CLI's *argument/flag/filter loop* (the parts that
  ARE expressible) becomes translatable once `os.Args`/`os.ReadFile`
  land in the frontend.

## The worker fleet (TRANSLATE_ONE_APPLICATION.md roles → processes)

| playbook role | process | scope | gate |
|---|---|---|---|
| **Core/contract worker** | `main_loop_rust.pl`, `main_loop_estree.pl`, `run_triage_worker.sh` (loop-triage-worker.log) | shared core (shir.rs/ir.rs/estree.rs/parser/), the A1 contract, `core-requests/` | cargo test --lib + the corpus gates |
| **Source-frontend worker (go-sh)** | `frontends/go-sh/run_frontend_worker.sh` (pid in loop-frontend-go-sh.pid) | `frontends/go-sh/` + `harness/` + `fail-go` + `templates/go/` | `make test` **and** `fail-go --gate` (Go→JS end-to-end) — red → `--pi-fix-frontend go-sh` (deepseek-v4-flash, xhigh) → 3 reds → `--worker-trapped` (core request) → sleep |
| **Target-backend workers** | `setup_backends.sh --run-backend-worker <lang>` (rust, go, js, c, …) + the main-loop complements | `sh2perl/backends/<lang>/` | per-backend gate; sh2.*-stub/TODO count must drop |
| **Idiom-triage worker** | `run_triage_worker.sh` | cross-product (frontend × backend × example) | FAIL-FRONTEND → `--pi-fix-frontend`; FAIL-BACKEND → core request |
| **The miner (deterministic)** | `./translate_one_application.sh mine … --lang go` | `templates/go/` (gitignored; regenerated by `dump-templates --lang go`) | — |
| **Orchestrator** | the workspace owner / this session | the ladder, the divergence forks, the acceptance runs | `./fail-go --gate` |

Restart: `./setup_backends.sh --start-frontend-workers` (frontend supervisors),
`--start-workers` (backend), `--start-triage-worker`. All workers self-enroll in
the `sh2workers` cgroup (harness/WorkerPool.pm; best-effort).

## How to drive it

```
./fail-go                                    # corpus Go→JS verdicts
./fail-go --gate                             # exit 1 on any failure
./fail-go --rust --gate                      # js+rust
./fail-go --app frontends/go-sh/cmd/go-sh/main.go
WORK=.translate-work ./translate_one_application.sh run \
    frontends/go-sh/go-sh.go --lang go --target estree --iterations 5
```

The loop converges when the mined ladder is green AND the app (the CLI,
then the golib) passes as the final integration test — the golib's full
translation is blocked on the contract boundary above, so the app-loop's
termination criterion is deliberately scoped to the expressible subset.
