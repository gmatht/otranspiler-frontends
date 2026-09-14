# profile_example — one Python app, many workload shapes, different transpiled C

A self-contained demonstration of **profile-guided transpilation** through
this workspace's pipeline:

```
app.py  --(py-sh-go)-->  A1 shIR  --(otranspilerl --target c)-->  C
```

The whole point is in the last arrow: the *same* A1 shIR renders to
*different* C depending on a runtime profile measured on the target
workload. The mechanism is `docs/PROFILING.md` (SH2_PROFILE /
SH2_PROFILE_IN), implemented in `sh2perl/src/c_profile.rs` and wired
into `sh2perl/src/c_backend.rs`.

## The one application

`app.py` is a tiny log classifier: read stdin line by line, route each
line to `errors` (the line mentions `ERROR`) or `ok`. It builds two
growable lists whose lengths depend on the workload.

It is deliberately restricted to the py-sh-go v1 subset so the demo does
not depend on unrelated gaps: `while line != "":` (the
`for line in sys.stdin:` lowering has a pre-existing off-by-one),
substring containment instead of `len()` arithmetic, and no `int()`
/ f-strings.

## The workloads (different shapes)

`benchmarks/gen.sh` regenerates the fixed corpus (`sh gen.sh`). The
shapes differ in **count**, **ERROR ratio**, and **line length**:

| workload          | lines | errors | ok  | shape |
|-------------------|------:|-------:|----:|-------|
| `empty.log`       |     0 |      0 |   0 | no input |
| `quiet.log`       |     6 |      0 |   6 | short, no errors |
| `few_errors.log`  |     4 |      2 |   2 | tiny mixed |
| `long_lines.log`  |     8 |      3 |   5 | few long lines |
| `mixed.log`       |    40 |     20 |  20 | half errors |
| `error_heavy.log` |    64 |     60 |   4 | error-dominated |
| `many.log`        |   300 |     30 | 270 | large, mostly ok |

Both lists are *growable vecs* in C (`char **errors`, `char **ok` with
`_len`/`_cap`), so their observed maximum length is exactly what the
profile records and what the consumer pre-sizes.

## Run it

```sh
cd frontends/py-sh-go/profile_example
./run_profile_demo.sh                 # all workloads
./run_profile_demo.sh benchmarks/many.log benchmarks/few_errors.log
```

Prerequisites (all in-tree):
- `frontends/py-sh-go/py-sh-go` (`make build`)
- `otranspilerl/target/debug/otranspilerl-cli` (`cd otranspilerl && cargo build --bin otranspilerl-cli`)
- a C compiler, `python3` (oracle), and `harness/profile-merge.py`

## What the script does

For the emitted A1 it renders three C builds:

1. **baseline** — plain render (no profiler env). This is byte-identical
   to the render before the profiler existed (all probes are behind
   `SH2_PROFILE`, the A/B firewall).
2. **profiled** — rendered with `SH2_PROFILE=1`, compiled, and run
   against one workload. On exit it dumps
   `out/<workload>.profile.json` (`schema` 1): per-site loop trips,
   branch-arm counts, value-magnitude buckets, and array lengths.
3. **guided** — the *same* A1 re-rendered with
   `SH2_PROFILE_IN=<profile>`. The observed `len_max` of each list
   becomes its advisory initial capacity; the still-present growth check
   means a wrong/stale profile costs a `realloc`, never correctness.

Then it checks that baseline, profiled, and guided stdout all agree with
each other and with `python3 app.py`, merges all profiles with
`harness/profile-merge.py` (max/union) to show a safe cross-shape build,
and runs a **guard test**: a build guided by the tiny `few_errors`
profile is run against `many.log` (far past the observed caps) and must
still match the baseline.

Generated artifacts land in `out/`:
- `*.profile.json` — the measured shape per workload
- `<workload>.guided.c` — the C that profile produced
- `<workload>.diff.txt` — the profile plus a baseline-vs-guided diff

## The result

```
workload          lines  errors_max    ok_max prealloc caps (guided)             guided C
empty                 0           0         0 (none)                             6882006e3da975e9
error_heavy          64          60         4 errors=60,ok=4                     4c680796ce546773
few_errors            4           2         2 errors=2,ok=2                      3735817d0b74dbd8
long_lines            8           3         5 errors=3,ok=5                      704685a42a3e3b7e
many                300          30       270 errors=30,ok=270                   227bc2b52b64c9a8
mixed                40          20        20 errors=20,ok=20                     1d545faa2afa15d3
quiet                 6           0         6 ok=6                                7cda6e9f4256a6bb

MERGED(all 7)         -           -         - errors=60,ok=270                   edabd6b4ba72bb9c
merged build reproduces every workload's baseline stdout

guard: stale_guard.guided (profiled on few_errors) on many.log -> errors=30 ok=270  (matches baseline)
```

Reading the table:

- Each workload yields a **different guided C hash** — the difference is
  the `_sh_prealloc_*` block. `out/quiet.diff.txt` and
  `out/error_heavy.diff.txt` show it directly:
  ```c
  --- baseline.c
  +++ quiet.guided.c
  +static void _sh_prealloc_ok(void) { ok_cap = (size_t)6; ... }
  +__attribute__((constructor)) static void _sh_prealloc_init(void) {
  +  _sh_prealloc_ok();
  +}
  ```
  `error_heavy` instead gets `_sh_prealloc_errors` cap 60 **and**
  `_sh_prealloc_ok` cap 4.
- `empty` has no observed length, so its guided C is **byte-identical**
  to the baseline (same hash `6882006e…`) — the `None` path is the
  pre-profiler render.
- `quiet` only pre-sizes `ok` (errors was never observed, so it has no
  site cap) — the profile shapes *which* allocations are pre-sized, not
  just how big.
- `MERGED(all 7)` takes the per-list maximum (`errors=60`, `ok=270`),
  so one binary covers every workload with no regrowth on any of them;
  merge is max/union only — a profile can never shrink a bound.
- The guard run proves the advisory framing: the `few_errors` caps
  (`2`/`2`) are far below `many.log`'s `30`/`270`, the grow path fires,
  and the output still matches.

## Where the code lives

- `sh2perl/src/c_profile.rs` — schema-1 `ProfState`/`ProfileIn`, the C
  probe preamble, exit dump, merge-consultable `cap_for`, and the
  `prealloc_block` emitter.
- `sh2perl/src/c_backend.rs` — the wiring: `prof_site` (positional ids
  in render order), probes for loops (`emit_while_loop`, native range
  `for`), branch arms (`IrStmt::If`), numeric scalar stores (mags), and
  array appends/literals (lens), plus the profile/prealloc preamble
  splice and the observed-cap pre-sizing at the vec declaration.
- `sh2perl/tests/profile_e2e.rs` — golden counts, default-off
  cleanliness, determinism, and source-hash tracking.
- `harness/profile-merge.py` — merge with schema/hash validation and
  max/union semantics.

Every probe is gated on `SH2_PROFILE` and every consumer on
`SH2_PROFILE_IN`, so the default render is unchanged (the A/B
firewall). See `docs/PROFILING.md` for the design; Phase 2 (this demo)
is the exact-cap pre-sizing. Phase 1 metrics (`growth_events`,
per-call arg-type bitmaps, reduction execution bits) and Phase 3
(assertive mode with widen guards) are future work.

---

# Part 2 — profile-selected integer widths (u32 / wide)

`app.py` / `run_profile_demo.sh` exercise the *allocation* half of the
profile (array lengths → pre-sized vecs). `app_grow.py` /
`run_width_demo.sh` exercise the *type* half: the `mags` sites (value
magnitude buckets) select the C integer tier, the assertive
`SH2_ASSUME_OBSERVED_WIDTHS` of `docs/DUAL_LOOPS.MD` §4.

## The one application

`app_grow.py` reads N lines and doubles `x` N times, so the workload
shape decides the magnitudes:

```python
n = len(lines)
x = 1
i = 0
while i < n:
    x = x * 2
    i = i + 1
print(x)
```

The default (no-profile) build types both `x` and `i` as `long long`,
which is **wrong past N=63** (`2**130` truncates). The profile is what
makes it exact.

## Why the profiler needed fixing first

The first version of this demo could not see the middle tier at all: the
*instrumented* build was the default one (`long long x`), so `x`
**wrapped** at 2^63 before the probe ran, and every large workload
reported the same saturated bucket (`8`). A profile that cannot
distinguish "fits 64 bits" from "fits 128" from "needs arbitrary
precision" cannot choose between i64, `__int128` and GMP.

The fix is a **non-wrapping instrumented build**:
`SH2_PROFILE_WIDE=1` homes the detected *growers* (`x = x * 2`, never a
loop condition variable — see `profile_wide_vars`) in GMP for the
measurement pass only, and emits `_sh_prof_val_mpz`, which reports the
true bit length. The buckets are then honest:

| bucket | means | release tier |
|---|---|---|
| 1..8 | fits u8..u64 | narrow C int (`uint*_t`), guarded store |
| 9..16 | needs 65..128 bits | **`__int128`** (fast middle tier) |
| 17 | beyond 128 bits (saturated) | GMP `mpz_t` (exact, slow) |

## Why the middle tier is worth finding

Both tiers are exact past 64 bits; the difference is speed. The same
doubling loop in `__int128` vs GMP (this is `bench_i128_gmp.c`, and the
demo runs it):

```
doubling x 120 times, 1000000 reps
  __int128 :   0.085 s  ( 0.71 ns/op)
  mpz (GMP):   1.302 s  (10.85 ns/op)
  speedup  :   15.2x
```

So the payoff of the wide probe is: when the observed magnitude needs
128 bits but not more, the release build picks `__int128` and runs
~10-15x faster than GMP — and only falls back to GMP when the profile
shows bucket 17.

## The three profiles

`run_width_demo.sh` generates three workloads (`seq 10`, `seq 100`,
`seq 70000`), takes a **wide** profile on each
(`SH2_PROFILE=1 SH2_PROFILE_WIDE=1`), and re-renders with
`SH2_ASSUME_OBSERVED_WIDTHS=1 SH2_PROFILE_IN=<profile>`:

```
profile      lines x-bucket i-bucket  C types                            guided C          correct?
a               10        2        1  uint16_t i uint32_t x              3dc05042fbc69566  yes
b              100       13        1  __int128 x uint16_t i              7de5ef294b48cc49  yes
c            70000       17        3  __int128 x_f mpz_t x uint32_t i    e9e1b0e5914a830c  yes
merged       a+b+c        -        -  __int128 x_f mpz_t x uint32_t i    e9e1b0e5914a830c  yes

guard: profile a (x<=65535) run on b.lines (x=2**100) -> exit 127
  sh2-profile: x exceeded the observed range [0, 65535] (SH2_ASSUME_OBSERVED_WIDTHS); rebuild with a matching profile or drop the flag
```

- **profile a** — small workload (`x = 2**10`, bucket 2). The bucket
  seeds `x ∈ [0,65535]`, the `x*2` expression widens one tier, and `x`
  becomes `uint32_t` (counter `uint16_t`). **u32** — no GMP, no
  `__int128`.
- **profile b** — `x = 2**100` (bucket 13, needs 101 bits). The wide
  probe sees it, so the release build homes `x` in **`__int128`** —
  exact to 128 bits and ~15x faster than GMP.
- **profile c** — `x = 2**70000` (bucket 17, beyond 128). The **max
  bucket is 17**, so the profile cannot promise 128 bits. The grower
  gets a **tiered** variable: `__int128 x_f` fast path + `mpz_t x`
  spill, switched at runtime by `x_big`. The large counter is
  `uint32_t`.
- **merged** — `harness/profile-merge.py` unions the buckets (max per
  var): `x` is 17, `i` is 3. So merged is *identical to c* — and, unlike
  before, it **still contains `__int128 x_f`**: the tiered form is what
  keeps the fast 128-bit path when the merged maximum is beyond 128.
- **guard** — profile `a` assumed `x ≤ 65535`; on the `b` workload it
  stops with a diagnostic (exit 127) instead of wrapping. The
  profile-less build stays available and unchanged.

### Why merged keeps `__int128` (the tiered grower)

A per-var max of 17 forces GMP for *correctness* (some observation
needed more than 128 bits), but it does not mean every value is huge.
The grower therefore compiles to two tiers with a runtime switch
(`out/width/merged.guided.c`):

```c
mpz_t x;
__int128 x_f = 0;
int x_big = 0;
...
if (!x_big) {
    __int128 _tr6;
    if (__builtin_mul_overflow(x_f, (__int128)(2LL), &_tr6)) {
        _sh_i128_str(&_v7, x_f);
        mpz_set_str(x, _v7.p, 10);     /* lift the fast value ... */
        mpz_mul(x, x, _bigint_2);      /* ... then continue exactly   */
        x_big = 1;
    } else {
        x_f = _tr6;                    /* the common case: ~15x faster */
    }
} else {
    mpz_mul(x, x, _bigint_2);
}
...
if (x_big) _sh_mpz_str(&_v9, x); else _sh_i128_str(&_v9, x_f);
```

So the merged binary is exact on the 2\*\*70000 workload **and** uses
`__int128` (not GMP) until the value actually crosses 2^128. The
`__builtin_mul_overflow` check means the spill is correct for *any*
input, not just the profiled ones — this is DUAL_LOOPS §4.1's "route to
the next tier up to GMP".

## Where the width wiring lives

- `sh2perl/src/c_profile.rs` — `_sh_prof_val` buckets to 17 (16 = 128
  bits, 17 = the saturated "beyond" marker); the wide
  `_sh_prof_val_mpz` reports `mpz_sizeinbase(x,2)`; `ProfileIn.mag_buckets`
  parses the `mags` sites; `bucket_hi`; `assume_observed_widths()`.
- `sh2perl/src/c_backend.rs` — `profile_wide_vars` detects growers for
  the instrumented build; before `effective_widths` a bucket 1..8 seeds
  `[0, bucket_hi]` (guarded narrow tier) and 9..16 joins `i128_vars`;
  after `analyze_bigint_vars` a bucket ≥ 17 joins `bigint_vars`;
  `i128_vars` declare `__int128` and print through `_sh_i128_str`;
  `profile_store` / `profile_incdec` emit the `__int128`-checked guard.
  A source-hash mismatch **refuses** the render (`exit 2`, §4.2).
- `sh2perl/tests/profile_e2e.rs` — `assume_observed_width_guard` (narrow
  tier + guard), `assume_observed_width_i128_tier` (bucket 13 →
  `__int128`, not GMP), and `assume_observed_width_tiered_grower`
  (bucket 17 grower → `__int128 x_f` + `mpz_t x` spill + select-print).

Scope note: this specialises **scalar Int variables** (loop counters and
accumulators). Int-homed array element widths use the same buckets in
the design (`docs/DUAL_LOOPS.MD` §4.1) but need a per-store *widen*
guard; dynamic-bound lists in py-sh-go are string-homed and unaffected.

---

# Part 3 — effectiveness: CPython vs the transpiled C

Parts 1–2 exercise the profiler. This part measures the *transpiler*:
same Python source, same workload, CPython vs native C
(`py-sh-go → A1 shIR → otranspilerl --target c`, `-O2`).

## `bench_vs_cpython.sh`

Correctness is checked first (a differing run is reported, never timed),
then best-of-`REPS` wall time and peak RSS (`/usr/bin/time`), across
three shapes:

```
app / shape        impl      time(s)   rss(MB)    speedup
sum_squares (compute) python3     2.425     392.8
                   C           0.075      77.8
                                                    32.3x
bignum_mul (bigint) python3     0.617      10.2
                   C           0.248       2.0
                                                     2.5x
app.py (io/1000000) python3     0.837      94.3
                   C           0.250      55.5
                                                     3.3x
```

- **compute** (`bench/sum_squares.py`, 10M `i*i`): **~32x faster, 5x
  less RSS**. The C uses a native `long long` vector and a maintained
  `__int128` sum; CPython boxes 10M `PyLong`s and dispatches the
  bytecode loop.
- **bigint** (`bench/bignum_mul.py`, 100k `x *= 3` from `2**100`): **~2.5x
  faster, 5x less RSS**. GMP vs CPython's own big-int — the honest case
  where the win is real but modest, not 30x.
- **I/O** (`app.py`, 1M-line classify): **~3.3x faster, 1.7x less RSS**.
  Line reading + substring search, no interpreter.

The three rows are deliberately different so the report is not a
cherry-pick: the transpiler wins most on numeric hot loops, least on
already-optimized big-int work.

## `check_cpython_parity.sh`

Speed is only meaningful if the result is right. This runs every
`frontends/py-sh-go/testdata/*.py` through Python → A1 → C, compiles,
executes, and diffs stdout against CPython:

```
total 95   match 94   mismatch 1  (t91_set_sum_fallback)   emit fail 0  cc fail 0
```

Known C-backend gaps are listed in `KNOWN_GAPS` so a new mismatch is
loud; the test fails if one appears. (The frontend itself passes 95/95
through the ESTree backend; `t91` is a C-accumulator gap.)

## What the benchmark caught

The first run of `bench_vs_cpython.sh` at 10M elements **failed parity**:
CPython printed `333333283333335000000`, C printed `1291890006563070912`.
The native int-array sum was accumulated in `long long` and wrapped past
9.2e18 — a real miscompile, not a benchmark artifact. It is fixed:
the maintained aggregate and the on-demand scan now accumulate in
`__int128` (exact for any memory-sized i64 array: `M·2^64 < 2^127`) and
print through `_sh_i128_str`. That is the point of a differential
benchmark — the 32x number above only counts because the output now
matches.

## What to try next (not built here)

- **Cross-target parity**: the same A1 already has C/Go/Rust/Zig/Java/
  Perl/Python/JS backends. A table of "one Python app → N languages,
  same stdout, time each" would show the universal-shIR value.
- **Profile-guided vs default C**: a single table contrasting the
  default build, `SH2_PROFILE_IN` pre-sizing, and
  `SH2_ASSUME_OBSERVED_WIDTHS` tiers (i128 vs GMP is already ~15x in
  `bench_i128_gmp.c`).
- **Coverage, not just pass/fail**: extend `check_cpython_parity.sh` to
  report which Python constructs are refused vs miscompiled, and to
  run the stdin-using tests with inputs.
- **GPU offload**: the workspace's bash-O4 autoshaderization already
  turns compute loops into CUDA; running a Python-derived loop on the
  GPU is the natural next headline (the RTX 2070 work is in-tree).
- **Startup/memory for embedding**: no interpreter, ~2 MB RSS vs ~10 MB
  for CPython, no import preamble — measure `main()` time for a
  short-lived process.
