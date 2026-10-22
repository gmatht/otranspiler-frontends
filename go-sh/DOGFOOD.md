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

### FIXED (2026-10-15, sixth pass): every struct layout was correct; the layout SLICE was not

The `T{...}` refusal (`line 113: expected ), got {` → `unknown field kind in
struct token`) is now GREEN. The earlier passes all pursued "struct layouts
come out empty" and measured `mapGet(structs,"tok") == ""`. That reading was
a MEASUREMENT TRAP: the empty string was the value of a *different*
container (`structFieldTypes`) being watched, not `structs`. Verified by
dumping `p.structs` directly — it held `tok→"kind,text"` all along.

Five independent defects, each confirmed by probe and gated at
`fail-go` 124/124:

1. **`getVar` returned a positional for a digit-named var**
   (`harness/sh2-namespace.mjs`). The `^[1-9]$` positional arm ran BEFORE
   the var store, so a digit-named Go temp read back as `$2`. The var store
   is now consulted first (bash precedence: `2=x; echo $2` prints x).
2. **`argToWord` routed a plain shell array through `listItems`.** The
   documented unscoped-`paramSlice` leak marked a same-named LOCAL as a
   slice param; the read site now trusts the CURRENT element typing
   (`varTypes[rn]=="Array"` + not an any-list ⇒ not a slice param), so
   `append(fields, names...)` splices via `arrayItems`.
3. **`num_add` for every `+`/`-`.** `parseAdd` records `e.op`, but
   `arithNumCalls`'s `case "add"` ignored it — so `p.toks[p.pos-1]` lowered
   as `p.pos+1`. parseTypeDecl then read the token AFTER the type name and
   registered the layout under the key `"{"` instead of `"tok"`. This was
   THE blocker for the literal: `-` now emits `num_sub`.
4. **A slice value written into a container lost its slice-ness.**
   `exprToWord` reads an Array-typed var as `getVar(name)` (element 0); the
   map-element-write site now emits `arrayItems(name)` when the RHS is an
   Array-typed var.
5. **`range` over a statically-empty, runtime-loaded array.** `names :=
   []string{}` then `names = append(...)`: `p.arrays[name].elems` is empty,
   so the For iterated `[]` and the body never ran. An Array-typed var with
   empty static elems now falls through to the runtime
   `param("slice", name, "@", "")` read.

Plus the last link, which was a RUNTIME defect, not a frontend one:
`p.structs[t.text]` is a `map[string][]string`, and the map VALUE (a slice)
was destroyed on assignment. `setVar` space-JOINED array values, so
`arrayToList(name)` — which reads the array store — saw `[]`, and the slice
crossed the `parseStructLit(typeName, layout)` call boundary as an empty
list (`listLen` 0 ⇒ the keyed-field loop never ran ⇒ "unknown field").
`setVar` now keeps an Array value in the array store AND the joined scalar
in the var store, so `${arr}` stays bash-faithful while `arrayToList`
recovers the items. Frontend side: `registerAssignType` and the comma-ok
map-read path register `layout` as Array (`registerVar` is first-write-wins,
so the mark is written to `p.varTypes` directly).

Result: the translated frontend now resolves `token{kind: …}` — `arg2` is a
real list id and the keyed-field lookup succeeds. The self-emit advances to
a NEW frontier (`unsupported call "" in word position`), i.e. past the
struct-literal stage entirely.

### SEVENTH pass (2026-10-15): the shared-slot class, and the new frontier

`fail-go` 124/124 and `make test` 124/124 throughout. Native self-emit OK
(5.34 MB). Two independent blockers fixed, and one new class found.

**1. `structFields` was missing from the struct-name prescan.**
`prescanFuncNames` pre-registered `structs`/`structFT`/`structRaw` but not
`structFields` — the very map the composite-literal lowering tests for
KNOWN-ness (`if _, ok := p.structFields[cname]; !ok`). A struct declared
later in the file was therefore present in three maps and absent from the
fourth, so `T{...}` fell through to the unknown-type path, the `{` was
swallowed, and the parse derailed with an empty-callee call in
`parseAssignStmt` (`unsupported call "" in word position`). Fixed.

**2. Local slice/map declarations shared ONE global shell slot.**
`var x []T` and `x := []T{}` lower to `setArray("x", …)` with every read
lowering to the same bare name, so the NAME is the storage. This file
declares `words` in 17 functions, `out` in 11, `args` in 6 — the later
function's reset wiped the earlier one's items. Concretely, `parseArgs`
returned an EMPTY arg list (`parseArgs 45->58 len=0`) because another
function's `args` had clobbered the slot, which is what derailed the whole
parse.

Fixed with `scopeLocalStoreName`: a LOCAL slice/map declaration mints a
function-unique store name (`words__7`), aliased through `varAlias` so
reads and writes follow via `resolveVar`, unwound by `endFuncLocalScope()`
at each function-exit restore site (decl path and func-literal path). A
slice PARAM is never renamed (it rides the positional protocol), and the
type-switch guard check was narrowed so a scoped local is not mistaken for
a guard.

Site fixes that followed: `append`'s destination, the `:=` plain-array and
any-list branches, and the `:= []T{}` literal branch all now resolve the
store name. **Verified effect:** 139 unique slots emitted, and the
`parseArgs` empty-arg-list derailment is gone.

**3. `for i := range <any-list>` (index-only) had no branch.**
The value form (`for _, t := range tgts`) worked; the index-only form fell
through to `for-index range needs an Array var…`. Added a `listLen(id)`
case. Probes `tg5.go`/`tg7.go`/`tg2.go` (plain func, `make([]T,…,0)`,
method-with-receiver) now parse.

**New frontier (open):** `expected ), got {` at the `token{kind: tNL,
line: …}` append inside `lex`'s `case c == '\n':` — `structs["token"]`
reads back `""` in the translated run. Root-caused one level: the declared
`names := []string{}` in `parseTypeDecl` emits `setArray` (a shell array)
while the same name's `append` emits `listPush` (a list id), so the field
accumulator stays empty. The append's classification calls `anyListVar`
with an OBJECT ID (`anyListVar("obj#982362") => true`) instead of a name —
`rhs.args[0].name` evaluates to an obj-store reference in the translated
run. That is the next thing to fix; the decl and the append must agree on
one storage model per scoped name.

### EIGHTH pass (2026-10-15): the scalar/let slot class and closure scoping

`fail-go` 124/124, `make test` 124/124, native self-emit OK (5.36 MB).
**503 unique store slots** now emitted (was 139).

**4. The scalar/`let` channel had the same shared-slot defect.**
`x := p.parseExpr()` lowered to a **file-level store slot**, so every
function's `arg`, `e`, `out`, `w`, `names`, `layout` collided. This file has
`arg := p.parseExpr()` in **8** functions. Concretely, in `parsePrimary`
`arg` was found holding the **parser object** (`anyListVar("obj#679241")`)
because another function had written it last — so `arg.kind`, `arg.name`
and every derived read were garbage, and the append classification flipped
the accumulator to `listPush` while the declaration had emitted `setArray`.

The general `:=` tail now routes through `scopeLocalStoreName` like the
slice paths.

**5. Nested func literals must INHERIT the enclosing scope (and top-level
names must not be scoped at all).**
Two regressions the scoping introduced, both caught by the corpus:

- `t106_iife_closure` — a closure mutating an outer local. Reset of the
  per-function alias set at func-literal entry severed the closure from the
  enclosing function's slots. A literal now *extends* the name set and
  unwinds only what it minted (symmetric save/restore of
  `localAliases`/`fnLocalAliases`/`fnLocalAliasOrder`).
- `t42_global_mut` — `g := 0` at top level then a closure writing `g = 1`.
  The closure's write was scoped to `g__1` while every read outside still
  resolved to the global `g`, so the mutation was silently lost. Added
  `topLevelNames`: a name bound at top level keeps its bare store name
  inside every nested closure.

**A `goto` did not survive translation — FIXED 2026-10-15 (6305338e3).**
The first cut of the append gate used `goto appendPlain`; the A1 has no
goto node of its own, so it lowered to `sh2.unsupported("goto
appendPlain")` and killed the run, and it was rewritten as a plain
conditional. That workaround is no longer needed for the forward-jump
shapes: `restructure_goto` now lowers `for { switch { … default: goto L }
L: rest }` (this frontend's own `goto matched` scan) and `for { … goto L };
L: rest` correctly. The bug was never in the A1 — the frontend emitted
`Label`/`Goto` correctly and `go-sh --shir` has carried a `Goto` arm since
2026-08-07 — it was in the shared goto-elimination pass, which dropped the
enclosing loop for gotos reached through a `Case`/`If` branch and skipped
the tail guard when the label sat inside the loop. See `FRONTEND.md`
§"Next frontier" for the mechanism and the new tests.
**Backward gotos and jumps across loop levels still refuse**, so keep
writing straight-line code where a workaround is cheap.

**Storage-model agreement.** Added `localModel` (per scoped slot:
array/list/map) so a declaration's chosen model overrides a stale
same-named mark from another function, plus a `varTypes[...]=="Array"`
fallback. The declaration and its appends must agree; the remaining
mismatch is recorded below.

**Open (root-caused, not yet fixed):** in the full `go-sh.go`, some
`names := []string{}` sites still emit `setArray` (shell array) while their
`append` emits `listPush` (list id), so `parseTypeDecl`'s field accumulator
stays empty and `structs["token"]` reads back `""` (the `expected ), got {`
frontier at line 113's `token{kind: tNL, …}`). A minimal reproduction of the
same shape (`names := []string{}` inside an `if`, then `append`) is
CORRECT in isolation, so the trigger is a same-named `names` in an earlier
function of THIS file re-marking `anyLists["names"]` after the declaration
recorded `localModel["names__N"]`. The next step is to make the append read
the model through the SAME key the declaration wrote (the scoped slot), and
to clear `anyLists[bare]` at scoped-declaration time for every `:=`/`var`
slice form — not just the ones already patched.

### NINTH pass (2026-10-15): the struct-layout chain is CLOSED

`fail-go` 124/124, `make test` 124/124, native self-emit OK.
**506 unique store slots** emitted; **zero** `listItems`-on-shell-array
splices remain (was 2 at the last count).

**The last link: the scoped slot was never TYPED.** Every `x := []T{...}`
literal path minted a function-unique store name and cleared the stale
marks, but registered `varTypes` only under the **bare** name. `argToWord`'s
spread gate, the `for i := range x` bound and the append model check all
consult `varTypes[resolveVar(name)]` — i.e. the SCOPED slot — so a plain
shell array came back untyped and took the LIST paths:

    fields__134 = append(fields__134, names__137...)
      -> setArrayAppend("fields__134", [...listItems(getVar("names__137"))])

`names__137` is a shell array (`setArray`), so `listItems` read the object
store and spliced `[]`. `parseTypeDecl`'s field accumulator therefore
stayed empty, `p.structs["tok"]` was `[]`, and `token{kind: tNL, …}`
refused with `expected ), got {`. Now:

    setArrayAppend("fields__134", [...arrayItems("names__137")])

The fix registers the scoped slot in all three `:=`/`var` slice-literal
branches (`parseVarSpec`, `parseAssignStmt`'s plain-array and `[]T{...}`
branches): `registerVar(stn,"Array")` + `varTypes[stn]="Array"` +
`localModel[stn]="array"`, and clears the bare-name marks so a same-named
any-list from another function cannot re-flip the decision.

**The translated frontend is now executing, not refusing.** The frontier
moved from a parse REFUSAL to a runtime failure:

| stage | outcome |
|---|---|
| before this pass | `failf: expected ), got {` (refused at lex's `token{…}`) |
| after | runs **922 s**, then `RangeError: Invalid array length` (12 GiB heap) |

So the struct-literal blocker is gone and the remaining work is genuinely
execution-side: a computed array length goes out of range somewhere in the
translated run. The next step is to locate that site — it is a real bug in
the translated code path, not a refusal.

Recorded here so the next pass starts from execution, not from the layout
question: `p.structs` is populated correctly (`token -> kind,text,raw,line`,
`parser -> 57 fields`), `arrayItems` is the plain-array splice and
`listItems` the list-id splice, and the choice is keyed on the SCOPED slot's
`varTypes`/`localModel`.

### TENTH pass (2026-10-16): the self-hosted lexer runs — the infinite loop is gone

The ninth pass left the translated frontend hanging (`922 s`, then a 12 GiB
`RangeError`). It was not a runaway allocation. **Four scoped-store defects
in the frontend made the self-hosted frontend loop forever**, all of the
same class: a declaration, a write and a read resolving to *different*
store names, because `targets[0]` holds the BARE parsed name while
`resolveVar`/`paramName` return the function-unique alias that
`scopeLocalStoreName` mints (`j` -> `j__396`).

Measured on the same pipeline, translated frontend vs native `go run`:

| input | stock translated | fixed translated |
|---|---|---|
| `package main` | MATCH | MATCH |
| `func f() {}` | **HANG** | MATCH |
| `func main() { x := 1 }` | **HANG** | ERR (unsupported expr) |
| `j := 0; j++` | **HANG** | ERR (unsupported expr) |
| three more probes | **HANG** | ERR / DIFFER |

Four of six previously-hanging inputs now terminate, two byte-identically.

**1. `j++` / `j--` wrote the bare name** (`parseAssignStmt`). The loop
variable of `funcLitAhead`'s
`for j < len(p.toks) && p.toks[j].kind == tNL { j++ }` was incremented at
`j` while the condition read `j__396`, so it never advanced. Fixed by
scoping the target through `scopeLocalStoreName` (idempotent, and returns
the bare name outside a function, so the C-style ForInit step lowering —
which already passes resolved names — is unaffected).

**2. `name := lit` emitted `local <bare>=<lit>`** (the `:=` literal
early-return). The declaration wrote the bare slot while every read used
the alias: `lex`'s `i := 0` initialised `i` against reads of `i__4`, so
`for i < len(src)` never advanced and **the lexer itself spun**. Six sites
in this file.

**3. `x += expr` wrote the bare name** — including the `i += len(op)`
operator-length advance, whose `arith` text is a bare name too. `i` never
moved, so the scan re-read the same byte; the multi-char operator table
`{..., "++", "--"}` therefore never matched and `:=`/`++` came out as the
*two* puncts `:` `=` / `+` `+`. That then mis-parsed as two statements and
died in `parseAssignStmt` with "unsupported statement starting with =".

**4. `for _, v := range <array>` iterated the bare name.** The range base
was looked up as `p.arrays[rv.name]`/`p.varTypes[rv.name]` and its iter
word emitted `param("slice", rv.name, "@", "")` — all bare — while the
declaration had stored under `multiOps__2`. The loop iterated an empty
array and its body never ran (this is what actually broke `lex`'s
operator table *after* fix 3).

Verified: native frontend still emits **byte-identical A1 for 122/124
corpus files**; the two that differ (`t41_local_var`, `t96_typed_params`)
differ ONLY in the scoped name now being used consistently, and both still
produce identical program output. `fail-go` 124/124, `make test` 124/124
with the fixed binary, and the 123/124 stdout-match corpus check is
unchanged from stock (the one EMPTY, `t61_while_read.go`, needs stdin).

### Next frontier (execution-side, now that the loop is gone)

The translated frontend reaches `exprToWord` with an EMPTY expr at
`parseAssignStmt` and fails with `unsupported expression ""`. The token
stream for `x := 1` is correct (`x` `:=` `1` NL EOF, kind 2/7/3/1/0), so
the failure is in the statement dispatcher: it consumes the whole input and
then tries to lower a token it never read. That is the next single blocker
— it gates every `:=`-bearing input, which is most of `go-sh.go`.

### ELEVENTH pass (2026-10-16): the `:=`-blocker is CLOSED — the translated frontend parses Go

The tenth pass left `parseAssignStmt` reaching `exprToWord` with an empty
expr (`unsupported expression ""`) — it gated every `:=`-bearing input,
i.e. most of `go-sh.go`. Root-caused and fixed. Five independent
scoped-store defects, all one class again: a write or a *type
registration* keying on the bare parsed name while every read resolves
through `resolveVar` to the function-unique alias.

**1. `registerVar` marked every name as a top-level global.** `outer`
means "bound at TOP LEVEL", and it is load-bearing twice: `scopeLocalStoreName`
keeps a global's bare name, and the `:=` scoping branch is *skipped* for
one. Setting it unconditionally meant the FIRST `registerVar("e", …)`
anywhere — `e := p.parseOr()` in parseExpr, via registerAssignType — made
every later function's `e := …` skip scoping and declare the BARE slot
while the body's reads used the alias. In parsePostfix that split `e` in
two: the declaration wrote bare `e`, the loop read `e__N`, and the final
`return e` echoed an empty module local, discarding parsePrimary's value.
Now `outer` is set only outside a function.

**2. The single-target call-capture path assigned the bare name.**
`e := p.parsePrimary()` is a CALL rhs, so it takes the capture path
(not the `:=` literal branch) — which emitted `assignStmt(name, capture)`
with the bare `name`. Now resolved through `scopeLocalStoreName` before
the type registration, list marks and both capture shapes, so all four
agree on one slot.

**3. The multi-target path registered the bare name** (`tg`). Same split
for `sub, ok := p.spreadAppendPart(tmp, a)`.

**4. `captureReturnTypeText` and the prescan collected every IDENT as a
return position.** For `(stmts []map[string]any, ok bool)` that gave six
entries (`stmts/map/string/any/ok/bool`) for two positions, so call sites
indexed `retIds[0]` as the NAME `stmts`. A new shared
`returnPosBases(toks, lo, hi)` splits a result clause on top-level commas
and takes each position's type base, dropping a leading named binding
(`stmts []T` → `T`) and hiding bracket-nested arguments (`map[string]any`
→ `map`). Its first cut was O(n²) per part and ran once per `func` at
prescan time — 1,048,573 calls / 113 s and a `Map maximum size exceeded`
on `func f() {}`; it is now a single pass.

**5. Grouped/named slice returns lost their slice-ness.** The prescan only
detected `retRaw` STARTING with `[]`, so `([]T, error)` and
`(stmts []T, ok bool)` were typed scalar and a spread of the result
refused `variadic spread needs an array-typed var`.

### Measured (same pipeline, translated frontend vs native `go run`)

| input | tenth pass | eleventh pass |
|---|---|---|
| `package main` | MATCH | MATCH |
| `func f() {}` | MATCH | MATCH |
| `func main() { x := 1 }` | ERR `unsupported expression ""` | **MATCH** |
| `func main() { x := 42 }` | HANG | **MATCH** |
| `j := 0; j++` | ERR | **MATCH** |
| `func main() { name := "world" }` | HANG | ERR (next blocker) |
| `func f() {}` (bodies) | DIFFER (`stmts: ""`) | DIFFER (next blocker) |

**5 of 7 probes now byte-identical to native** (tenth pass: 2 of 6, the
rest hanging or panicking).

Safety: the native frontend's A1 is **byte-identical for all 124 corpus
files** (0 differing), `fail-go` 124/124, `make test` 124/124, and the
executed-stdout check is unchanged from stock (123 pass / 1 empty — the
stdin-reading test). So every fix here is behavior-preserving on the
existing corpus while unblocking the self-host path.

### Next blocker (single, precise)

`parsePostfix` still returns empty for a **string-literal** RHS
(`name := "world"`): `parsePrimary` returns the `str` object correctly in
isolation and `e__N` is demonstrably set to the object id, but the
function never reaches its `return e` — it panics inside the loop body
(whereas the same shape with a NUMBER rhs, `x := 42`, now works). Same
"value lost between the echo and its reader" family; start from
`parsePostfix`'s `{`-branch (`callName(e)` / `structFields` lookup).

Second: function bodies still assemble to `stmts: ""` (the statement-list
return shape) even when the body is empty — see `func f() {}`.

Third, performance: the translated frontend takes 14 s on a 9-line file
(dominated by the `failf` unwind path), so corpus-sized runs need the
per-file budget raised, not read as hangs.

### Pass 12 (2026-10-23): the seventh-pass source committed, and the build fix

The seventh pass above was committed only as this document — the source
side (`scopeLocalStoreName`, `localSeq`/`localAliases`/`localModel`,
the scoped `topLevelNames`) stayed in the worktree, uncommitted, while
the worker sat on provider quota walls. It is now committed as-is, no
edits, after re-verifying it on the gates.

Verified (all three green):

| gate | result |
|---|---|
| `make test` (A1 ingress + executed stdout) | **125/125** |
| `fail-go --gate` (Go→JS end-to-end) | **125/125** |
| `fail-go --app frontends/go-sh/cmd/go-sh/main.go` | **GREEN** |

- **Probe `t119_local_slot_isolation` pins the shared-slot class.** Two
  functions each declaring `var words []string`, the caller reading it
  after a call. Pre-fix the callee's reset is visible in the caller
  (`a 2 B`); post-fix `a 1 A`, byte-identical to native `go run`. The
  probe FAILS against HEAD of `go-sh.go` and passes after, so it is a
  real regression test, not a pass-through.
- **Self-emit**: `./go-sh --shir go-sh.go` emits 5.43 MB and the core
  deserializer accepts it (`--target estree` → 18.3 MB JS); HEAD's emit
  is also accepted, so the gain is semantic (slot isolation), not
  ingress.
- **Build fix (the reported failure)**: `make build` died with
  `make: /snap/go/current/bin/go: No such file or directory`
  (Error 127) — the hardcoded snap path, on a box whose Go is
  `/usr/bin/go` (apt golang-go 1.22.2). Same class py-sh-go fixed in
  57245668; `GO` now takes the first of the snap path and
  `command -v go`, and resolves to `/usr/bin/go` under a minimal
  `PATH=/usr/bin:/bin`. The `pir failed (rc=5)` lines in the worker log
  were never compile errors — they are HTTP 429 quota walls.
