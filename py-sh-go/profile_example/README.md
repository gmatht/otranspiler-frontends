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
