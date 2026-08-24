# corpus-c — the C-idiom example corpus

GOOD_EXAMPLES.md conventions applied to C source (see ../../s2p.c/GOOD_EXAMPLES.md):
one or two C constructs per example, a header comment saying what is
demonstrated, diagnostic stdout that diverges the moment a lowering is
wrong, self-contained programs (no external files), descriptive numbered
names.

Gate: `harness/corpus-c.sh [--gate] [--verbose]` — the oracle is the
native gcc compile+run of each example; every backend target must
reproduce the same stdout. The frontend is c-sh-go (the C frontend);
the same A1 feeds every backend renderer.

Coverage contract: the corpus includes at least every construct the
cpp-sh-go frontend's corpus (testdata_cpp) exercises — printf/puts,
int/char arith with bool sugar, if/else, while/do-while/for,
switch/case/default/fallthrough, functions + calls, structs, pointers
(&/*/->) incl. malloc/free, sizeof + sized ints, goto/labels, varargs
(plain; GNU typed `int ...` pinned by cpp t33), preprocessor
conditionals, comments.

## Examples

| file | demonstrates |
|---|---|
| 001_control_flow_func | user function (call/return) + if/else + while + switch in main |
| 002_bool_sugar | C23/stdbool bool, true/false, boolean ops |
| 003_switch_dispatch | switch/case/default over a helper fn (shared-body cases) |
| 004_goto_loop | backward guarded goto (post-test loop) + nested-loop exit goto |
| 005_pointers_swap | &x/*p call-by-reference swap (the outparam channel) |
| 006_heap_malloc | malloc/free heap array, element stores/loads |
| 007_structs | struct definition, dot field writes/reads, field arith |
| 008_sized_ints | long long / unsigned + sizeof folding |
| 009_varargs | `...` variadic signature with literal-arg call |
| 010_dowhile_menu | do-while post-test loop |
| 011_preproc | object-like #define: literal + derived (parenthesized) bodies |
| 012_char_strings | char arrays, NUL-terminator length loop, char arithmetic |
| 013_array_sort | bubble sort: nested loops, element swaps |
| 014_ternary_cond | conditional operator in assigns and printf args |
| 015_fib_loop | loop-carried accumulators (fibonacci) |
| 016_continue_odd | continue/break loop control |
| 017_struct_arrow | struct field mutation through a pointer (-> idiom) |
| 018_enum_state | enum constants as named ints driving dispatch |

## Status

125/180 cells green (was 69 at goal start). Per-backend state with the current core
(s2p.c f120f194 + workspace bed252b3):

- **perl**: green except function-outparam/struct examples (the
  capture channel landed this session; remaining cells are Function
  body-shape gaps).
- **estree/js**: green except structs/enums (js DIFFs/RUN-FAILs on
  dotted-name vars) and 003.
- **sh**: green through control flow/goto/outparams/ternary; red on
  structs/enums/heap-line reads (RENDER refusals to fill).
- **python**: control flow green; RUN-FAIL(1)/(2) on goto guards,
  ternary conds, bool prints, enums (sh2 stubs).
- **go/rust**: simple idioms green; DIFF on structs/enums/sized ints/
  heap (typed-value rendering gaps); go COMPILE-FAIL on goto flags +
  continue.
- **java**: simple idioms green; user functions RENDER-FAIL (nested
  static fn inside main) — needs real function hoisting.
- **zig**: migrated to the zig 0.16 Io API this session; green on
  straight-line/loop/bool/vararg/fib/preproc examples; red wherever
  user functions or structs appear (fn params are sh2Env stubs).
- **c (round-trip)**: green on most control flow incl. gotos; SEGFAULT
  (139) on char-array/heap/ternary examples — the C backend's memory
  rendering needs triage.

## Known frontend limits (REFUSE > GUESS honored; silent drops fixed)

- user-function bodies support only simple statement shapes: a switch
  or multiple returns inside a helper (003) mis-lowers — the corpus
  keeps such logic in main until clib grows the body grammar.
- `#if`/`#elif` branch selection is not evaluated (both branches'
  defines collect, last wins) — conditional compilation must stay
  UNUSED-in-code (cpp corpus pins the same discipline). Object macros
  used in code ARE supported incl. derived parenthesized bodies.
- enum case labels don't constant-fold (use them in comparisons).
- struct-typed parameters refuse (`void f(struct S *p)`); use int
  pointers or restructure.
