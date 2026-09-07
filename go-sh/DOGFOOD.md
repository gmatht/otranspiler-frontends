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
| corpus Go→JS (`fail-go`) | **98/98** |
| corpus Go→Rust (`fail-go --rust`) | 41/88 (rust backend `sh2.*` stubs: assoc arrays, argv, etc. — backend gap, per-target) |
| Go idiom ladder (47 templates, mined from the app + seeded) | 42/47 (4 contract boundaries + 1 documented nondeterministic) |
| app integration (the CLI, `fail-go --app`) | **green** — the no-args oracle (usage → stderr, exit 2) is reproduced by the translated JS |
| cpp CLI integration (`fail-go --app frontends/cpp-sh-go/cmd/cpp-sh-go/main.go`) | **green** — the cpp frontend's CLI transpiles Go→JS and reproduces its no-args behavior |

### New idioms landed (this pass — the app gate)

**The CLI (`cmd/go-sh/main.go`) now transpiles Go→JS end-to-end** and the
translated JS reproduces the native no-args behavior (usage message to
stderr, `os.Exit(2)`). The `fail-go --app` gate is green. Landed
constructs (all probe-verified in the ladder, t93–t95):

- **bool literals**: `raw := false` lowers to `raw = "false"` — a
  LITERAL, never a `$false` var read (the old lowering read a var named
  "false", which the runtime resolves to ""). `true`/`false` are
  keywords, not vars (probe `t93_bool_literal`).
- **bare-var conditions**: `if raw` / `if !raw` on a bool var → the
  `"$raw"="true"` test (the Not twin wraps it in `!`). Go conditions
  are bools, so any bare-var condition IS a bool var (probe
  `t93_bool_literal`).
- **multi-target assign from a single call**: `out, err :=
  golib.Shir(src)` — a call returning (value, error) lowers to
  `out = $(Shir "$src")` (the function-call capture shape; the A1 has
  no cross-package calls, so a package-qualified callee lowers to a
  shell sub of its last name component), the error target dropped
  (mirroring strconv.Atoi/os.ReadFile). DOCUMENTED APPROXIMATION: the
  callee is never executed in the app's no-args path (the CLI exits
  before reaching it), so the gate verifies parse + valid A1 + dead
  code, not the call's semantics — the golib's full translation stays
  the (a)/(d) contract boundary below.
- **`err.Error()`**: the error value's message → the error var's value
  (the A1 has no error objects; an error return is dropped at the call
  site, so err is a plain var — never assigned in the expressible
  subset, the error branch is dead code).
- **`os.Stdout.Write(x)`**: the raw byte write WITHOUT the trailing
  newline echo adds → `printf "%s" "$x"` (the A1 has no raw-write
  node). The `[]byte{...}` byte-slice literal form (the CLI's
  `os.Stdout.Write([]byte{'\n'})` newline terminator) decodes its char
  elements to the literal bytes (probe `t94_stdout_write`).
- **empty array literal**: `a := []string{}` — the A1 Array.elements
  must marshal to `[]`, never `null` (the core's deserializer rejects
  null elements; shir_json.rs always collects a Vec) (probe
  `t95_empty_array`).
- **`os.Exit(N)` value type**: the A1 Exit value is a full expr —
  `{"type":"Int","value":N}` (IrExpr::Int), NOT the arith "Num"
  sub-node (shir_json_in.rs rejects "Num" at the expr boundary).

**Harness (`fail-go`)**: the app oracle now runs in the go-sh module
context — the golib import resolves only through go.mod replace
directives (a dependency's own replaces are ignored; the main module
must repeat them), and the app is BUILT (not `go run`, which wraps any
non-zero exit as rc=1 + "exit status N"). The verdict now requires
stdout AND exit code to match the oracle (the app's `os.Exit(2)` is
part of its observable behavior); node's rc is captured directly (a
pipeline's rc is the last command's — `tr` masked it).

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

**Landed this pass (probe `append_var`, green):** `s = append(s, v)` with
a VARIABLE element — the CLI's argv filter (`filtered = append(filtered,
a)`, main.go:20, the loop that used to die "append elements must be
literals (v2)") and the golib's `tas = append(tas, item)`. The A1
`setArrayAppend` element contract is an EXPRESSION (shell `arr+=(x)`
lowers to split(getVar(x)) in shir.rs), so the literal-only refusal was
a frontend self-restriction, not a contract boundary: var elements now
lower through exprToWord (`getVar` after resolve) — one element per
appended VALUE, no field-splitting (Go semantics). Probe `append_var`,
green; the CLI's frontier moved past line 20 to `fmt.Fprintln(os.Stderr,
…)` (main.go:24).

**Landed this pass (probe `fprintln_stderr`, green):**
`fmt.Fprintln(os.Stderr, …)` — the CLI's usage-message construct
(main.go:24). Fprintln writes space-separated operands + "\n" to fd 2;
the frontend now lowers it to the A1 fd-dup Redirect shape the core
emits for `echo … >&2` (`Redirect{fd:1, mode:"w", target:"&2"}` — the
runtime dups fd 1 onto fd 2, so echo's stdout writes land on stderr),
reusing the Println operand-separation logic (extracted to
`printlnWords`). Probe `fprintln_stderr`, green; the wrapper's `os`
import detection gained `Stderr` (fail-go wrap_go +
translate_one_application.sh run_native). The CLI's frontier moved past
line 24 to the next documented gap (`os.Exit(2)`, main.go:25).

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

- **(b) frontend gaps**: LANDED this pass (the app gate went green):
  `os.ReadFile` (statement + if-init forms), `os.Exit(code)`,
  `string([]byte)` conversions, `os.Stdout.Write` (var + `[]byte{...}`
  literal forms), bool literals + bare-var conditions, multi-target
  assign from a single (value, error) call, `err.Error()`, empty array
  literals — see the Status section. `for i, s := range` (index+value)
  still REFUSED — the index-binding For boundary (§1).
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
