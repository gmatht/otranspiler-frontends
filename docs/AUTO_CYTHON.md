# Python → Cython: feasibility, scope, and a plan

Status: **Stage 0/1 started.** A first *sound* slice ships as `py2cy`
(`frontends/py-sh-go/cmd/py2cy`, pass in `autocython.go`): conservative typed
pure-Python-mode Cython with a cython/CPython parity oracle. See §12a.

2026-09-15.

Question: *can we make a `python2cython` translator? Would it be easy? Could we
support all Python that Cython supports?*  This document is the answer; no code
exists yet.

---

## TL;DR

- **The name is the trap.** Cython already compiles plain Python, so
  "translate Python into Cython" is the *identity* — a pointless program.
  What is worth building is the part a human does by hand: **auto-annotate
  Python with the `cdef`/typed declarations that make Cython fast.**
  `python2cython` should mean *automatic typed Cython*, nothing less.
- **It is a backend, not a frontend.** We already have Python → A1 shIR
  (`frontends/py-sh-go`) and the shared analyses (`var_types`, ranges,
  int-arrays, counted loops, reductions, the i64 / `__int128` / GMP tiers).
  The missing arrow is **A1 shIR → Cython** — a renderer
  (`otranspilerl --target cython`). The house rule *"frontends parse;
  backends render"* puts this squarely in the backend column.
- **Easy?** Three different sizes:
  - *semantics-preserving passthrough* (emit the source, Cython compiles
    it): **days**, and near-useless on its own;
  - *auto-typed renderer for the current py-sh-go subset*: **weeks**, and
    mostly wiring facts we already compute;
  - *all Python that Cython supports*: a **different, larger** project that
    only becomes cheap if we stop re-parsing Python ourselves and run our
    annotation pass over **CPython's `ast`** (which is Cython's own
    frontend). Then coverage = Cython's, by construction.
- **We already own the yardstick and the golden outputs.**
  `py-sh-go/profile_example/bench_cython.sh` already compares CPython, pure
  Cython, hand-typed Cython, and py-sh-go→C on the same workloads, and
  `bench/cython/{rolling_hash,bignum,io}_typed.pyx` are exactly the
  hand-written typed files an auto-translator must learn to emit.
- **The measured reality that shapes the design:** *pure* Cython is
  **slower than CPython** on every shape we tested (0.7–1.0×); typed Cython
  wins only after hand-typing (and a hand-written GMP FFI block for bigint),
  and even then pays the full **~57 ms libpython startup** that py-sh-go→C
  (~1 ms) does not. So the value of a translator is precisely the typing,
  and its natural niche is the places where handing semantics to CPython
  beats re-implementing them: **arbitrary bigint, dynamic objects, and
  correctness on Python the C subset will not accept.**

---

## 1. What "python2cython" could mean (four candidates)

| # | Interpretation | Verdict |
|---|---|---|
| a | Source text → Cython source, *as a compiler frontend in the same sense as py-sh-go* | **Backend, not frontend.** We already have Python→A1; the target is a renderer. |
| b | Emit the input unchanged (Cython compiles Python) | Trivial, and worthless: it just invokes Cython. |
| c | Emit **pure-Python-mode** Cython (annotations, still valid Python) | The sweet spot: source stays runnable, types come from A1. |
| d | Emit **auto-typed** Cython (`.pyx` `cdef` / memoryviews / GMP FFI) | The real goal; where the speed is. |

The deliverable is **(d), delivered through (c)** — auto-typed output in
pure-Python-mode by default (see §5), with traditional `.pyx` as an option.

---

## 2. Why this is a backend, and what we already have

The pipeline is already in place; only the last arrow is missing:

```
app.py ──(py-sh-go)──▶ A1 shIR ──(otranspilerl --target c)────▶ C ──▶ tcc
                                  └─(otranspilerl --target ???)─▶ Cython
```

Everything to the left of the fork is shared. What the C renderer already
computes and a Cython renderer would **reuse**, not rebuild:

| Fact / mechanism | Where it lives | Cython equivalent |
|---|---|---|
| conservative Python type verdicts (`Int`/`Str`/`Any`) | `shir.rs::analyze_var_types`, A1 `var_types` | `cdef long long` / `cdef str` / leave as Python `object` |
| integer range + effective-width analysis | `analyze_var_ranges`, `effective_widths` | `int`/`uint*_t` vs `long long` vs object |
| counter-exact loop sizing, counted `for` recovery | `counted-while-forinit`, `counted-arith-forinit` | `for i in range(N)` with `cdef` counter |
| int-array detection + reduction aggregates | `analyze_int_arrays`, `analyze_reduce_arrays` | typed memoryview / `long long*` + a reduction loop |
| bigint detection + the tiered grower | `analyze_bigint_vars`, `tiered_i64`, `c_profile` widths | GMP FFI (`cdef extern from "gmp.h"`), exactly like `bignum_typed.pyx` |
| profile-guided widths / capacities | `c_profile.rs`, `SH2_PROFILE_IN` | typed-memoryview length / `cdef` width choice |
| CUDA candidacy / mask plan | `cu_candidacy` | `prange` + `nogil` (a later stage) |

This is the whole argument for the backend framing: **we are not writing a
Python parser or a semantics engine; we are writing another renderer for an
IR that already carries the facts.** Cython then supplies the runtime
semantics (refcounting, exceptions, bigint via the FFI we emit) that the C
path re-implements.

### Golden outputs already exist

`py-sh-go/profile_example/bench/cython/` holds hand-written typed Cython for
the bench shapes — the exact artifacts the renderer should reproduce:

- `rolling_hash_typed.pyx` — `cdef long long i; cdef long long h = 0` over a
  counted loop. This is *only* the annotation the C renderer already infers.
- `bignum_typed.pyx` — a hand-written `cdef extern from "gmp.h"` block
  (`mpz_init`/`mpz_mul_ui`/`mpz_fdiv_ui`) because **Cython has no big-int**.
  Our renderer can emit this block mechanically from the bigint verdicts.
- `io_typed.pyx` — `char **` + `realloc`/`strdup` list upkeep, deliberately
  matching what py-sh-go→C emits for a growable list.

`bench_cython.sh` already builds and *parity-checks* all three, so the
acceptance oracle for annotation is pre-built.

---

## 3. Why not "just use Cython"? (the measured case)

From `docs/PY_BENCH.md` / `bench_cython.sh` (see the doc for method):

- **Cython (pure) is 0.7–1.0× CPython** on every shape — compiling untyped
  Python buys nothing and can cost.
- **Cython (typed)** wins, but only after a human writes the declarations;
  for `bignum_mul` that means hand-binding GMP.
- **py-sh-go → C** on the same workloads: `rolling_hash` ≈ **33×** CPython,
  `sum_squares` ≈ **31–45×** (with a native vector **and an exact
  `__int128` aggregate** that typed Cython cannot express — the typed
  column is `n/a`), `bignum_mul` ≈ **2×**, `app.py` I/O ≈ **2.3×**.
- Startup: Cython `--embed` links libpython → **~57 ms floor**; the
  transpiled C binary → **~1 ms**.

So the honest niche for a Cython target is **not** "beat the C path on hot
integer kernels" — the C path already does, and without a runtime. It is:

1. **Semantics coverage.** Cython runs essentially all of Python; the C
   subset refuses what it cannot prove. A Cython target is the safe,
   always-correct fallback for a program the C backend rejects.
2. **Bigint and dynamic objects.** Where the C path must build tiers and
   GMP plumbing, Cython can bind GMP or simply keep a Python object.
3. **Library/extension integration.** A `.pyx` is importable; a
   transpiled `main()` is a process.

That framing matters: the project is **not** "replace Cython", it is
"**make the auto-typing we already do emit a second, semantics-complete
target**".

---

## 4. Is it easy? — a staged plan with honest sizes

Each stage is independently useful and independently shippable.

### Stage 0 — passthrough + pure-Python-mode shell (days)
`--target cython` that emits the original source (or the A1 reconstructed at
the statement level) with a `# cython:` header. It *compiles* and is
byte-identical to CPython by construction. Value: pins the A1→Cython
round-trip, the CLI shape, and the CI oracle before any typing exists.
**Risk: none. Refuse nothing** — Cython accepts the input.

### Stage 1 — auto-type the py-sh-go subset (weeks)
Emit the types the C renderer already infers, for the constructs py-sh-go
accepts:

- scalar `Int` with a *proved* range → `cdef long long` / `cdef int` /
  `cdef unsigned …`; unproved → leave as Python `object` (correct, no win);
- counted loops → `cdef` counter, `for i in range(N)`;
- int lists + `sum`/`min`/`max` → a typed array (memoryview or `long long*`
  + length) and a native reduction;
- bigint → the GMP FFI block (Stage 1b), reproducing `bignum_typed.pyx`;
- **every construct outside the subset is refused** in the current
  "REFUSE > GUESS" style, so Stage 1 is honest about its envelope.

This is the first genuinely valuable milestone, and its oracle is byte
parity on `frontends/py-sh-go/testdata/*.py` plus a `bench_cython.sh`-style
timing comparison against the hand-written `*.pyx`.

### Stage 2 — wide coverage via CPython's `ast` (a project, not a sprint)
The current limiter is **our frontend subset**, not Cython. Rather than grow
py-sh-go into a full Python parser, take the *other* input path:

```
source.py ──(CPython ast, via a thin bridge)──▶ full-fidelity AST
           ──(annotation planner over our shIR facts)──▶ typed Cython
```

Now **coverage = Cython's coverage**, because every construct we cannot
type is emitted as plain Python and Cython handles it. The work is the
bridge + an annotation planner; the analyses are the ones we have.

### Stage 3 — the accelerators Cython offers and C does not (optional)
`prange` + `nogil` for the CUDA-candidacy shapes, typed memoryviews for
NumPy-adjacent code, `cdef class` for hot objects. Pure upside, but later.

| Stage | What | Size | Oracle |
|---|---|---|---|
| 0 | passthrough / pure-Python-mode shell | days | compiles; CPython parity |
| 1 | auto-type the py-sh-go subset | weeks | testdata parity + typed-`.pyx` timing |
| 1b | bigint → emitted GMP FFI | days (inside 1) | `bignum_typed.pyx` parity |
| 2 | CPython-`ast` ingestion + annotation planner | 1–2 months | full CPython test slice + parity |
| 3 | `prange`/memoryviews/`cdef class` | open-ended | shape-specific benches |

---

## 5. Output surface: three ways to be Cython

1. **Traditional `.pyx`** — `cdef long long h`, `cdef extern from "gmp.h"`.
   Matches the hand-written bench files exactly; the clearest target for
   Stage 1.
2. **Pure-Python mode `.py`** — `cython.declare(...)` / `@cython.locals(...)`
   / `@cython.cfunc`. The file **stays valid, runnable Python**: the
   fallback interpreter is the same file. This is the recommended default.
3. **Sidecar `.pxd`** — augment an untouched `.py` with declarations in a
   `.pxd`. Keeps the source byte-for-byte pristine; best if the input must
   stay the user's file.

Recommendation: **mode 2 by default, mode 1 available** (it is the most
legible and the one the existing `.pyx` oracle validates), mode 3 later.
The A1 `var_types` map is the natural source for every declaration.

---

## 6. Soundness hazards (and how they map to what we already do)

Auto-typing Python is where correctness is won or lost. Each hazard has an
existing analogue in the C backend, which is why this is *bounded* work:

| Hazard | Cython behaviour | Our mitigation (already implemented for C) |
|---|---|---|
| **Unbounded `int`** → C `long long` | wraps silently (UB) | prove a range, or **tier**: i64 mirror + overflow check → `__int128` → GMP (`tiered_i64`, `spec_growth_vars`) |
| **`%` sign** (Python floor-mod vs C truncation) | truncates | emit `((a % m) + m) % m`, already done for the C path |
| **`is` identity of inferred literals** | a `cdef double` inference can break `is` | *Cython's own documented limitation*; only type when `is`/`id` is not used (a liveness check we can do on the IR) |
| **bigint** | no native type | emit the GMP `cdef extern` block (Stage 1b) or keep `object` |
| **heterogeneous lists/dicts/str** | Python objects | type only when the element type is proved (int-array analysis); otherwise `object` |
| **exceptions across C frames** | Cython propagates them | free, but `finally`/`except` over C-typed locals needs care in the planner |
| **`exec`/`eval`/metaclasses/monkey-patching** | mostly supported; `inspect`/frames differ | pass through as Python; never annotate through them |
| **startup** | `--embed` ≈ 57 ms libpython | document it; prefer extension-module mode where the caller already has a Python |

The governing rule is unchanged: **an annotation is a proof, not a guess.**
Every declaration is emitted only when the core has *proved* it; anything
else is emitted as plain Python and left exact-but-slow. That is how a
partial annotator stays semantics-preserving.

---

## 7. "Could we support all Python that Cython supports?"

**Yes — but only by not writing a Python frontend.**

Cython 3 targets CPython 3 semantics and states it "aims for full Python
compatibility". Its *permanent* deviations are small and documented
(cython.readthedocs.io, *Limitations*):

1. nested tuple argument unpacking — Python-2-only, already gone;
2. `inspect` does not treat Cython functions as functions;
3. stack frames: fake tracebacks, no locals / `co_code`;
4. identity-vs-equality for inferred literal types (`is` on inferred
   `double`/`None`).

So "all Python Cython supports" ≈ CPython 3 minus four footnotes we inherit
by choosing Cython as the target. The real constraint is **our** input
envelope:

- Today `py-sh-go` is a **subset** frontend and refuses loudly outside it.
  Building "all Python" **into py-sh-go** would mean re-implementing a
  CPython-compatible parser — wasteful and forever behind CPython.
- The cheap path is Stage 2: **consume CPython's `ast` directly** (the same
  starting point Cython uses) and run our *annotation planner* over it,
  keyed by the facts our shIR analyses already produce. Untyped constructs
  pass through; typed ones are proved. Then:
  - **coverage = Cython's** (we add nothing Cython cannot compile), and
  - **speedup = the subset we can prove** (which is exactly the honest
    contract, because untyped Cython is not faster than CPython anyway).

A useful corollary: we should *not* claim "all Python **faster**". Cython
itself doesn't make untyped Python faster (measured: 0.7–1.0× CPython).
The claim is "all Python *correct*, the provable subset *fast*".

---

## 8. Recommended first milestone

**`otranspilerl --target cython` (pure-Python-mode) for the py-sh-go subset**,
with:

1. `py-sh-go` → A1 → Cython, in pure-Python mode (`cython.declare`), for
   scalars, counted loops, int lists/reductions, and bigint (GMP FFI).
2. **Refuse** (loudly, in the existing style) any construct the annotator
   cannot prove — the *output* is then plain Python for that site.
3. Acceptance:
   - compiles with `cython`/`cythonize`;
   - **byte-identical stdout to CPython** on `frontends/py-sh-go/testdata/*.py`
     (reuse `profile_example/check_cpython_parity.sh` as the harness);
   - timing compared against the hand-written `bench/cython/*.pyx` via
     `bench_cython.sh`, on the existing shapes;
   - a **refusal test** per unprovable construct (the "never guess" guard).
4. CI gate: the same oracle as the C path, plus a `cython --version` guard.

Sketched output for the first golden case (`rolling_hash.py`), pure-Python
mode:

```python
# cython: language_level=3, boundscheck=False, cdivision=False
import cython

@cython.locals(i=cython.longlong, h=cython.longlong)
def main():
    h = 0
    for i in range(2000000):
        h = (h * 31 + i) % 1000000007
    print(h)
```

— i.e. exactly the declarations in `bench/cython/rolling_hash_typed.pyx`,
generated rather than hand-written.

---

## 9. Open questions / decision points

1. **Where does it live?** Design here (frontends repo); implementation as a
   renderer in `otranspilerl`/`sh2perl` next to the C backend, so it shares
   the A1 ingress and the analyses. (Alternative: a small standalone
   frontend-repo tool that drives `py-sh-go` + a local annotator, but then
   it cannot reuse the core facts.)
2. **Pure-Python mode vs `.pyx` as the contract?** Recommend pure-Python
   mode (source stays runnable) with `.pyx` as a legibility option.
3. **Bigint policy:** emit GMP FFI by default, or prefer Python `int` and
   only FFI when a profile says the value is hot? The C path already answers
   this for its target; mirror it.
4. **Extension module vs `--embed`:** do we own a host, or emit an
   importable `.pyx` and let the user's Python embed it? This decides the
   startup story and the I/O framing.
5. **Stage-2 bridge:** in-process `libpython` call (a C-API bridge), a
   subprocess that prints `ast.dump`, or a Go/Python parser? The first is
   fastest, the second simplest and most portable.
6. **Does a Cython target earn its maintenance?** The C path wins on hot
   integer kernels; Cython wins on coverage/bigint/dynamic objects. If the
   answer is "only for programs the C subset rejects", scope the milestone
   to exactly that fallback role.

---

## 10. Scope and home: a diagonal accelerator, not a neutral backend

The ecosystem is an **any2any matrix**: *frontends* parse a source language to
A1, *backends* render A1 into a target language. Cython looks like it belongs
in the backend column. It does not, and the reason is worth stating plainly.

### 10.1 Cython's runtime *is* CPython's

A neutral backend re-expresses the shIR's semantics in the target. Cython has
no semantics of its own: it **is** CPython with static typing bolted on. So:

- **Python → Cython** keeps the source semantics *for free* (CPython) and adds
the one thing that makes it fast (types). This is the **diagonal**: source and
target share a runtime.
- **Perl → Cython** (or shell → Cython) is not *impossible* — you can render
any semantics through Python objects — but it is **pointless**: you would pay
Python's dynamic dispatch to emulate a language you could compile straight to
C. The neutral Python-object target already exists (`--target python`);
Cython-ing it buys nothing.

`perl2cython` is meaningless in the way the question means it: Cython's value
is **diagonal and cannot be generalised**.

### 10.2 Two families, not one

| family | direction | examples | coupling |
|---|---|---|---|
| **neutral targets** | any source → target | C, ESTree/JS, Perl, Python, Go, Rust, Zig, Java, PowerShell, sh | via A1 (the contract) |
| **same-language accelerators** | source → its own typed form | **Python → Cython**, (Python → Numba/mypyc), (bash → typed bash) | via *that* source's AST + a type proof |

Accelerators are inherently **pair-specific**: Cython only accepts Python;
Numba only Python; `typeset -i` only bash. They are the diagonal of the matrix
and belong with the **source** language, not the target set. (There is also a
*faithful* diagonal — shell → sh, Perl → Perl — but that is a different axis:
semantics-preserving round-trip, not acceleration.)

### 10.3 Consequence for the interface

A diagonal accelerator must take **Python source (or CPython's `ast`)** as its
primary input, not A1:

- taking A1 caps coverage at our frontend subset, destroying the very
  reliability that makes a Cython target worth having (delegating semantics to
  CPython);
- the A1 facts (`var_types`, ranges, int-arrays, bigint, tiers) stay useful as
  an **optional fact source** for the provable subset — consult them where they
exist, fall back to "plain Python object" where they do not.

The honest contract is therefore *Python in → typed Cython out + a refusal
manifest*, validated by CPython parity and the hand-typed `.pyx` goldens — **not**
"A1 in → Cython out" in the neutral backend set.

### 10.4 Where it should live (and whether it is a submodule)

Constraints from the rest of the project:

- `sh2perl` is the **neutral core**, single-owner, and must never reference a
  source-specific tool (the one-way rule; AGENTS).
- New artifacts are **separate repos coupled by a contract, pinned by commit**
  (`sh2runtime`; the `-O4` split-off; `otranspiler-frontends` at D3).
- `otranspiler-frontends` is already mounted as `sh2perl/frontends` (D3), and
  its charter is "frontends parse; they do not optimize".

| option | verdict |
|---|---|
| neutral `--target cython` in `otranspilerl` | **no** — falsely advertises any2any, needs a source gate; the value is diagonal |
| a renderer in `sh2perl` | **no** — the core stays neutral and source-agnostic |
| a tool inside `otranspiler-frontends` | **plausible** — it is source-side; widens that repo's charter to "parse + same-language accelerate" |
| its **own repo** (`autocython` / `py2cy`) | **recommended once it must be released/versioned**; no upstream, so it is a *creation* like `otranspiler-frontends` |
| a **submodule of `sh2perl`** | **no** — violates the one-way rule and bloats the neutral core |
| a **submodule of the `-O4` repo** (or of `otranspiler-frontends`) | only if that repo must *build* it in-tree; otherwise pin it by SHA in CI (the `sh2runtime` pattern) |

Low-friction path:

1. **Stage 1** — add it under `otranspiler-frontends` (sibling of `py-sh-go`),
   explicitly as a *source-family accelerator*, with the CPython-parity +
   `bench_cython.sh` oracle. No new repo, no submodule.
2. **Stage 2** — the CPython-`ast` planner, with its own Cython-version pin and
   release cadence: promote it to its **own repo**, and mount it as a submodule
   only in whichever repo needs to produce it (e.g. the `-O4` repo, so that a
   `python-O4 --emit-cython` ships with the driver). Its dependency on the core
   is a **pinned git dependency**, one-way.
3. **Never** place it in `sh2perl`.

Topology at Stage 2 (if it is its own repo):

```
sh2perl                       neutral core (submodule; knows nothing of it)
  └── frontends/              = otranspiler-frontends (source-side, D3)

autocython                    source-side same-language accelerator (own repo)
  ├── reads Python source / CPython ast        (required)
  └── reads sh2perl A1 facts, pinned git dep   (optional; provable subset)
```

### 10.5 Bottom line

- **Yes, limit it to Python → Cython.** It is a *same-language accelerator*;
  keeping the neutral any2any matrix neutral is a feature, not a gap.
- **It is a backend-shaped thing with a source-language contract** — so it
  lives source-side, consumes Python, and (optionally) borrows the core's facts
  one-way.
- **Not a submodule of the core.** A separate repo coupled by its output
  contract (typed Cython + refusal manifest), pinned by commit; mounted as a
  submodule only where a consumer must build it in-tree.

---

## 11. Control flow: `goto` and dual loops

**Cython has no `goto` — true, and irrelevant here: our dual loops do not use
`goto`.**

The speculative dual arm (`sh2perl/src/c_backend.rs::try_spec_loop`) emits
ordinary structured control flow — and the only `goto` anywhere in the C
backend is a word in the C reserved-keyword sanitizer list:

```c
if (!ovf) {                        /* fast i64 arm; sticky overflow flag  */
    while (cond_fast) { … }        /* ovf |= __builtin_*_overflow(…)       */
}
if (ovf) {                         /* exact replay from the entry snapshot */
    restore(entry);
    while (cond_exact) { … }       /* GMP / Python-int arm                 */
}
```

The *designed* versioned loop is the same story one level up: a shIR→shIR
transform (`While` → guarded two-`While`, `DUAL_LOOPS.MD` §1.2) — two plain
loops, no jumps. `DUAL_LOOPS.MD` never mentions `goto`.

Cython supports every primitive above (`if`, `while`, `break`, boolean flags).
The only C-specific pieces are the overflow builtins and `__int128`, both
replaceable:

- overflow checks → Cython's `overflowcheck` directive, or a small
  `cdef extern` to `__builtin_{add,sub,mul}_overflow`;
- `__int128` fast tier → `long long` in the fast arm, with the replay arm as
  Python `int` (exact by construction — no `__int128` needed at all).

And `DUAL_LOOPS.MD` §1.1 already makes the **arm form renderer-local** ("guard
form (branch vs dispatch table vs outlined function) … the backend knows its
profile"), so the Cython renderer is not obliged to mirror the C emission.

Two clean Cython forms:

1. **Same shape** — the `if (!ovf) / if (ovf)` two-arm loops above.
2. **Outlined** (arguably *nicer* than the C form): `cdef` the body once and
   call it from the fast arm and the replay arm — no duplicated body, no
   control-flow trickery.

Crucially, because Cython's exactness comes for free from Python `int` and
objects, the dual loop there is a **pure cost optimisation**: v1 can emit a
single exact arm (Python `int`) and add versioning only if `bench_cython.sh`
shows it pays. So this is a **v2 optimisation deferral, not a capability gap** —
and when we do want it, the workaround is the structured form we already emit.

For completeness: `goto` exists in the ecosystem only as **A1 input** (a C
source's `Goto`), and the shared ingress rewrites it to structured flow
(`restructure_goto_only`) before any backend — of any target — sees it.

---

### 11.1 Side effects: the replay must be pure (and is checked)

The two "dual loop" mechanisms have **different** safety requirements, and it
is important not to conflate them:

| mechanism | shape | side effects | why |
|---|---|---|---|
| **Speculative replay** (`c_backend.rs::try_spec_loop`) | fast arm runs, entry state restored, exact arm **replays** on overflow | **must be side-effect-free** | the body can execute **twice** |
| **Entry-guarded versioned loop** (`shir.rs::dual_version_while`, default-off `SH2_DUAL_LOOPS`) | one guard at entry picks **one** arm | safe; runs **once** | no replay — the guard selects a whole-loop version |

The replay gate is real and conservative. `spec_collect` admits only:

- `Assign` with a **scalar target and a pure-scalar RHS** (`expr_is_pure_scalar`:
  `Arith`/`Str`/`Int`/`Var`/`Ident`/`Bool` — **no `Call`, `Capture`, `Index`,
  `Interpolate` or `Ext`**);
- a bare `Expr(Arith(_))`;
- nested `If`/`Block`/`While`/`DoWhile`/`For`/`ForInit` with `Arith` conditions
  and recursively clean bodies;
- **everything else → `_ => return false`** (I/O, `exec`/`echo`, captures,
  array writes, `Output`, `Return`, `Die`, `SetChildError`, …).

So the replay arm never contains an observable effect other than the scalar
variables it snapshots and restores. (It is *stricter* than necessary: even a
provably-pure call is refused.)

**Audit finding (fixed while writing this doc).** The gate had one hole: the
*structured* array-store path rejects a flattened target (`t.var.contains('[')`,
`speculative_loop_rejects_flattened_array_store`), but the *bare-arith* path did
not — `((a[0]++))` arrives as `IrStmt::Expr(IrExpr::Arith(ArithAst::IncDec {
var: "a[0]" }))`, and `spec_collect_targets` admitted the baked index name as if
it were a scalar. An array write is a side effect (the replay would duplicate
it), and the arm would snapshot the mangled `a_0_` ident (undeclared → compile
error). Fixed by rejecting `var.contains('[')` in `spec_collect_targets` too;
pinned by `spec_collect_rejects_baked_array_index_in_bare_arith`.

**Consequence for the Cython target.**

- If we emit the **replay** form, we must reuse this predicate — and *not* copy
  it. It currently lives, C-local, in `c_backend.rs`; a Cython renderer that
  wants replay should consume a **shared core predicate** (`body_is_pure_scalar`)
  so the eligibility (soundness-critical) is proved once and the arm *form*
  stays renderer-local (per `DUAL_LOOPS.MD` §1.1).
- Prefer the **entry-guarded / outlined** form whenever a range is provable: it
  is side-effect-safe and is the canonical "versioned loop". Replay exists
  precisely for the case where no range is provable (an unbounded Collatz
  chain) — so when we do speculate, the pure-body gate is mandatory, not
  optional.

---

## 12a. Implementation status (first slice)

`py2cy` (`frontends/py-sh-go/cmd/py2cy`, pass in `autocython.go`):

- parses with the merged ANTLR grammar (full Python 3, incl. the walrus
  operator), then emits **pure-Python-mode** Cython: a
  `# cython: language_level=3` header, `import cython`, a
  `cython.declare(<name>=cython.longlong, ...)` line for the proved scalars,
  and the **unmodified** source (so the file stays valid CPython);
- **levels** (`--no-opts`, `--simple-opts-only`, default *full*) — all
  behaviour-preserving, trading cleverness for surprise: none emits no
  declarations, simple proves only i64 literals + literal-bounded counters,
  full runs the interval analysis. **`--gmp` rewrites bigints to GMP**
  (`cdef extern from "gmp.h"` + `cdef mpz_t` + `mpz_*`/`gmp_printf`), which
  is `.pyx`-only and needs `-lgmp`; it declines back to the exact
  pure-Python output whenever it cannot rewrite a construct exactly. Off
  (the default), bigints stay exact Python ints (~1× CPython, no faster);
- **output mode** (`--py` default, `--pyx`): `--py` is pure-Python mode
  (a `.py` valid under both CPython and Cython, `cython.declare`); `--pyx`
  emits traditional Cython (`cdef long long a, b` at module and function
  scope, no `import cython`). `--gmp` implies `--pyx`. `--pyx` is faster
  where scalars dominate (`rolling_hash` 0.081 vs 0.107 s) and is the form
  the hand-written goldens use;
- **usage safety**: a typed variable forces its whole expression into C, so
  any typed name in an expression that is not provably i64-safe (wrap-prone
  `+ - *`, shifts, bitwise) is refused as well — `x = 2**63-1; y = x + 1`
  leaves *both* exact. A value used with `is`/`is not` or `id()` is never
  typed (a C value has no Python identity). `/`, `//`, `%` stay safe because the emitted header
  does **not** set `cdivision=True` (Cython's default gives Python
  semantics); `coverage/semantics-parity.sh` pins all of this;
- **function scope**: each `def`'s locals are proved with the same analysis,
  and a `cython.declare(...)` line is inserted at the top of the body (after
  a docstring). Parameters are never declared (a parameter may be any object
  at the call site). Function-local declarations beat module-level ones
  (`rolling_hash` in a function: 0.09 s vs 0.14 s);
- the lexer synthesises CPython's end-of-input NEWLINE, so a file whose last
  line has no trailing newline (or ends in whitespace) parses;
- **soundness is a sound i64 interval analysis** (`autocython_ranges.go`):
  a scalar's abstract value is an interval or ⊤; arithmetic is interval
  arithmetic with **any overflow → ⊤**; `a % m` with `m > 0` and a provably
  **non-negative** `a` is `[0, m-1]` (Python floor-mod equals C truncation
  only there); a `for x in range(<int literals>)` counter is bounded by the
  endpoints; a `for` body is iterated to a fixed point with union-widening
  (capped → ⊤); a `while` body and `try`/`match`/`async` subtrees widen
  everything they assign to ⊤; `if`/`elif`/`else` join the branch states.
  Everything not proved stays a Python object. Pinned refusals: t101's
  growing `s = s + 4000000000000000000`, a negative-lhs `%`/`//` (Python vs
  C), i64 overflow in `+`/`*`, and a conditional `try`/`match` assignment;
- gate: `make py2cy-test` — `autocython_test.go` (the proof/refusal boundary)
  plus `coverage/py2cy-parity.sh` (annotate → `cython --embed` → stdout must
  equal CPython; 22/22 on the t01–t1x slice, t101 included).

Measured on `bench/rolling_hash.py` the analysis proves both `h` and `i`:
**0.12 s vs 0.74 s CPython and 0.93 s pure Cython**, with identical stdout.

**Next:** int lists/reductions (typed memoryview / `long long*`) and the
bigint GMP FFI (`bignum_typed.pyx`); then the annotation planner over
**CPython's `ast`** with the shIR facts as an optional input (§7, §10.3) so
coverage becomes Cython's.

---

## 13. Benchmarks (reproducible)

`coverage/py2cy-bench.sh` measures the **annotation pass** on the bench
shapes — the same files `bench_cython.sh` uses — at their own fixed N, so
the numbers are comparable run-to-run without calibration. Reproduce:

```sh
cd frontends/py-sh-go
REPS=5 ./coverage/py2cy-bench.sh
```

Requires `cython`, `python3-config` and (for the `--gmp` row) `gmp.h`. Every
implementation is parity-checked against CPython *before* it is timed; the
reported time is the best of `REPS`.

One capture (`REPS=4`, `gcc -O2`, Cython 3.0.8):

| shape | impl | time(s) | vs CPython |
|---|---|---|---|
| `rolling_hash` (range 2e6) | CPython | 0.492 | 1.00× |
| | Cython (pure) | 0.587 | 0.84× |
| | py2cy (default, `--py`) | 0.107 | 4.60× |
| | **py2cy --pyx** | **0.081** | **6.10×** |
| | hand-typed `.pyx` | 0.051 | 9.59× |
| `sum_squares` (range 2e6) | CPython | 0.455 | 1.00× |
| | Cython (pure) | 0.475 | 0.96× |
| | **py2cy (default)** | **0.290** | **1.57×** |
| | py2cy --pyx | 0.296 | 1.53× |
| `bignum_mul` (while 1e5) | CPython | 0.493 | 1.00× |
| | Cython (pure) | 0.518 | 0.95× |
| | **py2cy --gmp** | **0.165** | **2.99×** |
| | py2cy --pyx (no GMP) | 0.480 | 1.03× |
| | hand-typed `.pyx` | 0.159 | 3.09× |

Reading it:

- **Typing is the whole win.** Pure Cython is ≤~CPython on every shape
  (0.84–0.96×); the same source plus py2cy's declarations is 1.5–6.1×.
- **`--pyx` (cdef) beats `--py` (cython.declare)** where scalars dominate:
  0.081 vs 0.107 s on `rolling_hash`; the hand-typed golden still leads
  (0.051 s) because it hand-writes the tightest loop. On `sum_squares` the
  two modes tie (the list is untyped either way).
- **`--gmp` vs `--pyx` on bigint** (0.165 vs 0.480 s ≈ 3× vs 1×) shows
  precisely what the GMP transform buys: without it the bigint is a Python
  `int` and the annotation is a no-op for the hot variable.
- **Partial typing shows up honestly.** `sum_squares` proves only the
  counter `i`: `xs` stays a Python list (no typed `list[int]` in pure-Python
  mode) and the exact sum needs `__int128`, which Cython cannot express —
  hence ~1.5×, not 30×.
- All of these **link libpython** (`cython --embed`): short programs carry a
  ~57 ms startup the `py-sh-go → C` path (~1 ms) does not.

Caveat: absolute times move ~2× run-to-run on a loaded shared box; the
ordering and the ratios are the stable result. Timing is informational (the
repo's testing policy) — the parity oracles (`coverage/py2cy-parity.sh`,
`coverage/gmp-parity.sh`, and `make py2cy-test gmp-test`) are the gate.

## 12. References

In-tree:

- `frontends/py-sh-go/` — the Python frontend (Python → A1 shIR), subset, `testdata/`.
- `frontends/py-sh-go/profile_example/bench_cython.sh` — the Cython yardstick (pure + typed + py-sh-go C).
- `frontends/py-sh-go/profile_example/bench/cython/{rolling_hash,bignum,io}_typed.pyx` — the hand-typed golden outputs.
- `frontends/py-sh-go/profile_example/README.md` (Part 3) and the workspace `docs/PY_BENCH.md` — the measured results.
- workspace `docs/PYTHON-O4.md` — the Python→C→CUDA driver and the analyses a Cython renderer would reuse.
- `shir-contract/schema.json` — the A1 node/field contract.
- `plan.md` — the frontends/backends division of labour ("frontends parse; the core optimizes; backends render").

Cython:

- *Pure Python Mode* — cython.readthedocs.io/en/latest/src/tutorial/pure.html
- *Language Basics* (type declarations) — …/src/userguide/language_basics.html
- *Limitations* (the four permanent deviations) — …/src/userguide/limitations.html
- *Using Parallelism* (`prange`) — …/src/userguide/parallelism.html
