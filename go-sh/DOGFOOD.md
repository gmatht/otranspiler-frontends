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
| corpus Go→JS (`fail-go`) | **109/109** |
| corpus Go→Rust (`fail-go --rust`) | 41/88 (rust backend `sh2.*` stubs: assoc arrays, argv, etc. — backend gap, per-target) |
| Go idiom ladder (47 templates, mined from the app + seeded) | 42/47 (4 contract boundaries + 1 documented nondeterministic) |
| app integration (the CLI, `fail-go --app`) | **GREEN** (2026-09-09): `fail-go --app frontends/go-sh/cmd/go-sh/main.go` passes — oracle and JS both print the usage path and exit 2 with no args. The last blocker was the EXTERNAL-UNIT call `out, err := golib.Shir(src)` (main.go:68): `golib` is an import alias for another compilation unit, so neither the defined-sub nor the PACKAGE-MODE clause applied. Landed: import alias→path recording (`parseImportSpec`/`importAlias`; `_`/`.` bind nothing) + `isExternalAlias` (dot in the first path segment = non-stdlib) admitted in `callTargetName`, the assign-path word builder, and the word-position call path — the call lowers to the bare sub name with the multi-return positional distribution (DOCUMENTED APPROXIMATION: resolves only when the callee's unit is linked in; executed absent-sub calls fail at runtime, but the app's no-args path exits before it). Probe `t99_external_unit.go` pins it. The golib's OWN full translation (11k lines, maps/structs/methods) stays the (a)/(d) contract boundary — the `--app` gate covers the CLI unit only |
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

## Self-hosting run: the translated frontend on its own source (2026-10-14)

The dogfood taken literally: transpile `go-sh.go` with the frontend, run
the resulting JS under node, and use it to parse Go source. The oracle is
the native binary's own A1.

```
# 1. native self-emit → ESTree → JS (the translated frontend)
./go-sh --shir go-sh.go --raw > /tmp/self.a1.json
$CLI - --target estree < /tmp/self.a1.json > /tmp/self.estree.json
node -e 'import("./harness/estree-gen.mjs").then(m => …)'   # emit .mjs
# 2. drive it: call the translated Shir() on an input
node .dogfood/tfrontend.mjs <input.go> <out.json>           # workspace/.dogfood
# 3. oracle
./go-sh --shir <input.go> --raw > <native.json>             # byte-compare
```

### Result

| measurement | value |
|---|---|
| translated golib runs under node | yes (13k lines → 5.2 MB A1 → 18 MB ESTree) |
| translated `Shir()` on small inputs | byte-identical to native |
| corpus ladder (`testdata/*.go`, 124 files) | **41 byte-identical / 83 differ** |
| native self-emit of `go-sh.go` | succeeds (5,225,223 B A1, ingress OK) |
| translated self-emit of `go-sh.go` | **not yet** — the statement-list assembly still loses statements |

Comparison is with keys sorted and numeric strings normalized, so the two
known marshalling artefacts (Go's `encoding/json` sorts map keys; the
translated ints stringify as `"1"`) are excluded and the 83 are real
divergence. The dominant classes are `stmts: ""` (statement-list assembly,
e.g. the multi-assign `Block` return echoing a plain object) and
unsupported constructs the *translated* golib trips over that the native
one handles (map-typed params in value position, etc.).

### Bugs this exposed (all fixed — see the commit for details)

The value of the exercise: every one of these was invisible to the
existing corpus, which only ever exercised the NATIVE frontend.

1. **Reassigned params read stale positionals.** `n = n+1; return n`
   returned the argument. Fixed with a body prescan + entry prologue +
   store reads, and `params` omitted on shadowed functions.
2. **Assoc element writes dropped entirely.** `m[k] = v` vanished for
   every key shape including literal keys; now lowered to `assocSet`,
   with `delete(m,k)` → `assocDel`.
3. **Escaped rune literals decoded to the backslash.** `'\''` was 92, not
   39 — so the lexer's own quote handling was wrong once translated.
4. **`append(dst, src...)` nested instead of spliced** — the spread var
   fell through `case "var"` and was pushed as one element, which is what
   made `parseTopLevel` return an empty statement list.
5. **Runtime value-channel coercion** (`harness/sh2-namespace.mjs`): three
   sites `String()`-ed an A1 builder value into `"[object Object]"`.

## Larger inputs: how far the translated frontend gets (2026-10-14)

Beyond the corpus ladder, the translated frontend was run on the big
real-world Go files in this repo (the sibling frontends' own sources).
Two findings, both about **agreement** rather than raw capability:

| input | native | translated |
|---|---|---|
| `frontends/fish-sh-go/fish-sh-go.go` (2453 lines) | REFUSE at 295 (`p.lex.nextTok`) | emits an EMPTY program, rc 0 |
| `frontends/posix-sh-go/main.go` (3631 lines) | REFUSE at 349 (comparison operand `call`) | emits an EMPTY program, rc 0 |

So on large inputs the translation does **not** dogfood the frontend: it
runs (no crash, ~35–52 s per file) but diverges on the error path.

### The error-path class (root cause)

`Shir` reports failure via `run`'s panic recovery, and the CLI checks it:

```go
stmts, err := p.run()
if err != nil { ... return nil, err }     // Shir
out, err := golib.Shir(src)
if err != nil { fmt.Fprintln(os.Stderr, ...); os.Exit(2) }   // cmd/main.go
```

Under translation that chain is broken at two points:

1. **`if err != nil` on an error value lowers to the STATUS test**
   (`"$?" -ne 0`) — the A1 has no error objects (documented: an error
   return is dropped at the call site). Faithful to the design, but it
   means the error must arrive as a failing status.
2. **`run`'s recovery does not fail the status.** Its catch emits
   `PANIC:` to stderr and `printf %v` into a *variable*, then returns
   success — so the status test is false, `Shir` proceeds with nil
   stmts, and `Emit` produces an empty program.

Native prints the panic and exits **2**; the translated frontend prints
the panic (to stderr) and **succeeds** with an empty program. A caller
that checks the exit code — as `fail-go` does — sees "translated to an
empty program", not "refused".

`cmd/go-sh/main.go`'s `os.Exit(2)` is additionally unreachable in the
translation for the documented external-unit reason: `golib.Shir` is a
call into another compilation unit ("resolves only when the callee's
unit is linked in"), so there is no error value to branch on.

### FIXED (2026-10-15): the recovered panic now fails the status

The diagnosis above was too pessimistic — this needed no contract change.
The A1 already expresses failure two ways, both used by this frontend:
the status-return protocol (`sh2.return` sets `lastExit`) and the `Exit`
statement. What was missing was one emission: an error-returning
function's deferred recovery must leave a FAILING STATUS, since the
caller's `if err != nil` is compiled to the status test `"$?" -ne 0`.

`fnReturnsError` now matches result-list ELEMENTS (not a substring — the
substring form fired for every signature that merely mentioned error, and
stamped `Return 1` onto ordinary returns, which zeroed the self-emit).
Pair-verified: `run() (int, error)` gets the failing Return;
`plain() int` with the same defer/recover shape does not.

Translation ladder, before and after:

| | identical | differ | no-emit |
|---|---|---|---|
| before | 38 | 83 | 0 |
| after | **41** | 66 | **17** |

The 17 are cases that used to fabricate an empty program (counted as
divergence) and now correctly decline — so the "differ" number got smaller
*and* more honest. No file regressed from identical.

### COMPLETE (2026-10-15): the error return now exits non-zero

The remaining half is fixed. `return nil, err` emits a value-channel write
whose lowering RESETS `lastExit` to 0 (a correct successful-return rule),
and that reset ran *after* `sh2.return` had set the failing status — so
the caller still saw success. The failing status is now published AFTER
the value emission (setLastExit(1), then the return).

The rule also had to be narrowed: only a MULTI-SLOT return is an error
return. A single-slot `return Emit(prog)` delegates to a callee whose own
`(T, error)` results are the callee's business; counting it as "our error
is non-nil" made every success path fail (the translated golib exited 1 on
valid input) — caught by checking a known-good input, not just the error
case.

| input | native | translated |
|---|---|---|
| valid Go | rc=0 | **rc=0** |
| unparseable Go | rc=2 | **rc=1** (non-zero) |

The translated frontend also no longer fabricates an empty program
(`reachedEmit=false`), and the ladder is unchanged and honest:

| | identical | differ | refusing |
|---|---|---|---|
| now | 41 | 66 | 17 |

Gates: `fail-go` 124/124, `make test` 124/124.

## The remaining frontier, root-caused (2026-10-15)

The 17 refusals and the self-emit failure at `go-sh.go:113` reduce to ONE
cause: **the translated frontend cannot parse `T{...}` struct literals,
because struct layouts come out empty.**

Chain, each link verified by probe:

1. `parseTypeDecl` builds its field lists with a multi-target short
   declaration of slice literals:
   `fields, ftypes, fraws := []string{}, []string{}, []string{}`.
2. In the translated run the field loop's append
   (`fields = append(fields, names...)`) is called with **zero elements**,
   so `fields`, `ftypes` and `fraws` all end up empty arrays while
   `sh2.setArray`/`setArrayAppend` themselves behave correctly
   (`assocNames=false arrays=true`, and a direct call with elements
   populates the array normally).
3. Empty layouts mean `p.structs["tok"]` is empty, so the
   `if layout, ok := p.structs[t.text]` arm in `parsePrimary` misses and
   `tok{...}` falls through to a plain var — the parser then dies at the
   `{` (`expected ), got {`).
4. That is exactly self-emit line 113,
   `toks = append(toks, token{kind: tNL, line: line})`, and it accounts for
   the struct-shaped refusals (t104/t108/t116/t95).

### LANDED: the slice-param spread store (2026-10-15, third pass)

`append(dst, src...)` with src a slice PARAM emitted
`listItems(getVar("src"))` — but a param carries its list id POSITIONALLY,
so that read is `""` and the splice appends nothing. Fixed (commit
"go-sh: a slice-param spread must read its list id POSITIONALLY"):
`anyListReadWord` now covers slice params and the spread call site uses
it, with a store-read fallback so a nil word can never reach the A1 (a
null child arg is rejected at ingress and loses the whole program).

Verified: the slice-param A1 is now `listItems(getVar("1"))`; ladder
unchanged (41 / 66 / 17); fail-go 124/124.

The self-source chain is NOT yet fixed by this — the name that empties
`fields` in `parseTypeDecl` reaches a different lowering, and further
probes surfaced a second class (`unsupported call "" in word position`).
Both need their own pass.

### ROOT CAUSE FOUND: `paramSlice` is not function-scoped (2026-10-15, fourth pass)

The empty struct layout traces to a **scope leak**, and it is a clean
one-line finding with a non-trivial fix:

`p.paramSlice` is written while parsing a parameter list
(`go-sh.go:2351`) and is **never reset per function**. The function-scope
save/restore in `parseFuncLit` and the decl path covers `fnParams`,
`fnParamOrd`, `fnLocals`, `inFunc` (and `paramShadow`) — but NOT
`paramSlice`.

Consequence: a slice param in ANY earlier function permanently marks that
NAME as a slice param. A same-named LOCAL in a later function then takes
the param lowering — `listItems(getVar(name))` (the positional protocol)
instead of `arrayItems(name)` (the shell-array read) — which reads `""`
and makes the splice append **nothing**.

That is exactly `parseTypeDecl`:

```go
names := []string{}                 // a LOCAL, in a later function
names = append(names, ...)
fields = append(fields, names...)   // -> listItems(getVar("names")) -> []
```

so `p.structs["tok"]` stays empty, `T{...}` is not recognised, and the
parser dies at the `{` — self-emit line 113.

Both attempted repairs were reverted, each for a measured reason:

1. **reset `paramSlice` at function entry** — breaks parameter PARSING
   itself, because `paramSlice` is read while the list is being built
   (`params = append(params, names...)` at `go-sh.go:2380` then sees
   `names` unmarked; self-emit dies at 2380).
2. **gate the read on `fnParams`** (a name is a slice param only if it is a
   param of the current function) — breaks the same phase for the
   mirror-image reason: during parameter parsing `fnParams` is not yet
   populated (self-emit dies at 2372).

The correct fix is real scope modelling rather than a guard: `paramSlice`
must be saved/restored around function bodies, while remaining visible
during that function's OWN parameter parsing. A dedicated
`paramParsing` phase flag (true while the param list is being consumed)
is the smallest shape — mark/read freely while parsing params, and gate
reads on the current function's params for the body.

Both wrong turns are recorded so the next pass does not repeat them; the
tree is left at the last verified commit with fail-go 124/124.

### Third attempt, and the missing piece (2026-10-15, fifth pass)

A `paramParsing` phase flag was added (set while a parameter list is being
consumed, consulted by the readers) so that `paramSlice` stays usable
during parameter parsing but is ignored for same-named locals in a body.
It had **no measurable effect**, and the reason is now known:

**a new field on `parser` does not reach the translated run.** The A1
carries `paramSlice` only as an `objGet` field-NAME string
(`"paramSlice"` passed to `objGet`), never in an `objNew` field list, and
adding `paramParsing: false` to the `p := &parser{…}` initialiser did not
make it appear. Every gate that reads it therefore reads `undefined` and
takes the false branch — a silent no-op, not an error.

So the sequence is:

1. the parser object's field list is built by `newstructStmts`, which
   iterates the struct's **layout** (`p.structs[stype]`) —
2. which is populated by `parseTypeDecl` —
3. which is exactly the function broken by the unscoped `paramSlice`.

The "field list" problem is therefore DOWNSTREAM of the same root cause,
not a second blocker. The single thing to establish next is how a parser
OBJECT gets its field set at all when the layout is empty (empirically,
the baseline A1 shows the fields are read by name and the object is still
usable, so something populates it outside `newstructStmts`), and then to
thread the phase flag through that path.

Four candidate repairs were measured and reverted; none is landed.

### The append, traced one level deeper (2026-10-15, second pass)

The emitted JS for that line is:

```js
sh2.setArrayAppend("fields", [...sh2.listItems(sh2.getVar("names"))]);
```

`listItems` reads the OBJECT store, but `names` is a `[]string` —
`isAnyListElem("string")` is FALSE, so plain-string slices ride the
**shell array store**, not list ids. So `listItems(getVar("names"))` reads
nothing and the append receives `[]`. Two candidate repairs were tried and
both were reverted:

- routing the read through `anyListReadWord` (param-aware) — needed a
  fallback added immediately after, because for a non-list name it
  returned nil and the A1 then carried a JSON `null`, which the ingress
  rejects outright (`ext-child.args[0]: not an object`, whole program
  lost);
- keying the spread branch on the destination only — correct in
  isolation (native A1 for `out = append(out, names...)` became
  `listItems(getVar("1"))` for a slice param), but the self-source path
  still emitted the old shape, so it did not fix the target.

Net effect of both: gate stayed 124/124, ladder unchanged — so they were
churn, not progress, and were reverted to keep the tree verified.

The real fix is a single decision in the spread lowering: **splice a
plain-array source from the array store** (`arrayItems`), and a list-id
source from the list store (`listItems`), chosen by `isAnyListElem` of the
source's element type. That choice must be verified against the ladder AND
the self-emit, because a wrong splice rule silently empties every struct
layout.
