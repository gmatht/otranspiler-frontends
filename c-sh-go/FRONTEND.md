# c-sh-go frontend (dir: /home/llm/sh2loop/frontends/c-sh-go)

C source -> A1 shIR JSON (the shell-flavored subset of C).

## v5.2 — system-wide transient gate guard (2026-08-15), 103/103

No frontend change: the 17:23 gate FAIL (0/103 match, every transpiled
stdout EMPTY) was a system-wide transient, not a regression. It overlapped
the estree worker's concurrent core rebuild (`sh2perl/src/shir.rs` saved
17:23:10, binary relinked 17:27:20 — the gate ran 17:23:47–17:26:39, 172s
vs the usual 41–53s): the multi-GB rustc starved/OOM-killed EVERY node
execution in the executed-stdout phase, so the per-test retry could not
help (the whole window was affected). c-sh-go sources were untouched since
the previous green gate; the gate passes 103/103 standalone.

Hardened in `harness/frontend-stdout.sh` (shared, all frontends):

- `run_estree` captures node stderr to a per-test file; a DIFF with EMPTY
transpiled stdout now surfaces its head ("Killed" OOM, "FATAL ERROR: …",
"Cannot find module") instead of an opaque `>` row.
- Whole-phase retry: if EVERY test in the phase failed (fails == total),
sleep 45s (letting a concurrent relink finish) and re-run the entire phase
once. A deterministic regression fails the retry too — the gate still
FAILs; partial failures are never masked.

## v5.1 — loop-carried div/mod accumulators on typed ints (2026-08-14), 99/99

Gate fix for t47_digit_sum.c (a `while` loop carrying `sum = sum + n % 10`
printed EMPTY instead of `30`). The core's i53 BigInt escalation
(`analyze_big_i53`) widens a loop-carried var whose bound moves outward to
the full ±i64 domain, so the Int32 `sum` was homed as BigInt; the `%` keeps
the statement off the numeric lift, and the store path rendered pure BigInt
arithmetic inside the runtime's `arithEval` boundary — which only accepted
finite Numbers and corrupted the BigInt result to `''`. Fixed in the shared
runtime: `arithEval` now stringifies BigInt values exactly (`String(6n)` =
`"6"`; only NaN/±Infinity — the bash zero-divisor abort — map to `''`).
The same fix landed independently for fish-sh-go (harness/sh2-namespace.mjs,
commit 07b95f9f). Verified: c-sh-go 99/99, py-sh-go 77/77, go-sh 86/86,
cpp-sh-go 10/17 (C-invariant green).

## v5 — typed integers: int / long long / unsigned / sizeof (2026-08-12), 80/80

C's integer types no longer collapse into the widthless `Int`: the frontend
now carries the declared type per variable (A1 `var_types`, `{"kind":
"Int32"}` etc.) and the typed arithmetic nodes (`Cast`/`Sizeof` in the Arith
AST), and the C-executed ESTree path lowers them per the BinInt64
benchmarks (`benchmarks/i64/BinInt64.md`):

| C type | IrType kind | ESTree lowering |
|---|---|---|
| `int` / `signed` | `Int32` | native numbers, `| 0` wrap, `Math.imul` |
| `unsigned [int]` | `UInt32` | native numbers, `>>> 0`, `(a>>>0)<(b>>>0)` cmp |
| `long` / `long long` | `Int64` | BigInt (`BigInt("N")` literals, `BigInt.asIntN(64, …)`) |
| `unsigned long long` | `UInt64` | BigInt (`BigInt.asUintN(64, …)`) |

- **Declarations**: the full type-specifier sequence is parsed
  (`long long`, `unsigned`, `unsigned long long`, `signed`); typed vars are
  recorded in `var_types` (the core keeps frontend-supplied types — it only
  backfills its own analysis when the field is empty). Integer literal
  suffixes (`LL`/`ULL`/`u`/`l`) are stripped at the lexer.
- **Casts** `(T)x` — emitted as `ArithAst::Cast`; the ESTree path renders
  `Number(x) | 0` (Int32), `Number(x) >>> 0` (UInt32), `BigInt.asIntN(64,
  BigInt(x))` (Int64), `BigInt.asUintN(64, BigInt(x))` (UInt64).
  `(int)strlen(s)`-style casts of VALUE-machinery calls recurse into the
  value lowering.
- **sizeof(T)** — emitted as `ArithAst::Sizeof(ty)`; the core folds it to
  4/8 (int/unsigned = 4, long long = 8; `sizeof(char)` = 1, `sizeof(double)`
  = 8 fold at the frontend — no IR type for them). `sizeof(structvar)`
  folds to the flattened layout size. malloc/calloc sizes keep folding.
- **64-bit arithmetic**: every Int64/UInt64-typed expression wraps its
  leaves AND root in `Cast(ty, …)` so the ESTree path renders pure BigInt
  arithmetic (a mixed BigInt/Number binary op would throw). `printf` args
  of 64-bit type render the typed Arith node, and `%lld`/`%llu`/`%ld` are
  supported by the core's native printf fold (BigInt args bypass
  `parseInt` — the template stringifies them exactly).
- Signed vs unsigned at 64 bits: `BigUint64Array` would be 2–10× slower
  than `BigInt64Array` on V8 (BinInt64.md §6), so the JS lowering keeps
  u64 as BigInt values with `asUintN` wrap — exact, no rounding past 2^53.

Verified end-to-end (gcc vs A1→ESTree→JS): t31_types.c (sizeof int/ll,
`%lld` beyond 2^32, `%u`/`%llu`, `(int)` narrowing — 705032704 =
5000000000 mod 2^32), t41_typed_conditions.c (i64/u32 in if conditions:
comparisons AND arithmetic — `ll % 2 == 0`, `ll + 1 > 5000000000LL`,
`u / 3 > 100`, `!ll`), t42_typed_pointers.c (`long long *`,
`unsigned long long *`, `unsigned int *` heap pointers with malloc,
element-scaled offsets, pointer advance, exact u64 values past 2^53),
t43_64ptr_arith.c (64-bit pointer reads INSIDE arithmetic — `x =
a[0] + 1`, `y += a[0]`, `a[0] + 2` — exact past 2^53, 2^64 wrap
included), t44_huge_cond.c (i64 bitwise/mod conditions on
9223372036854775807 — `ll % 2`, `ll & 1`, `ll >> 62` — exact past
2^53). Gate 85/85.

Still refused (honest): char ORDERING comparisons (`c < 'b'` — the
untyped test-string grammar has no string ordering), and i64 in
`switch` discriminants (untyped case-value comparison).

## v4 — the last refused rung (2026-08-10), 79/79

## v4.1 — bare `!` on a numeric operand (c-request cpp-20260810-054745)

`if (!no)` was silently DROPPING the branch in the estree run: the test
string `! $no` negates the test grammar's "is the string non-empty"
(a set variable `"0"` is a non-empty string → `! 0` is FALSE), not C's
"is the value zero". The lowering now folds the negation into the
comparison — C truth `!x` is exactly `x == 0`, so a bare `!` on an int
var / int literal renders `$x -eq 0` (the grammar's `!` binds to the
WHOLE rest, so `! $x -eq 0` would be the mirror image). STRING-valued
operands (char vars, char* vars, literals) keep the bare `! $c` — the
faithful "is the string empty" model (an `-eq` would coerce the char
to 0). The proven `!(a == 1)` → `! $a -eq 1` shape is unchanged. The
bare-int-operand truthiness of `&&`/`||` (`$x` directly under `-a`/
`-o` — string-truth, documented deliberate) is a SEPARATE pre-existing
gap, untouched.


The five documented refusals that remained after v3, each pinned by a
stdout example (t75–t79):

- **Runtime VALUE reads in CONDITIONS** (deref / index / call / prefix-
  inc inside `if`/`while`/`for`/`switch` conditions): hoisted to temps.
  An `if` hoists once (Block[temps, If]); a `while`/`for`/`do-while`
  whose cond needs reads becomes the refresh-and-guard structure
  `while (1) { temps; if (!cond) break; body }` — the cond must
  re-evaluate per iteration, so the temps refresh at the top of each
  (the while-header cond is emitted before the body). A foldable call
  (`twice(3)`) is hoisted too — the test-string grammar has no call
  node. `switch (*p)` hoists the discriminant once. t75: array-max
  loop, `while (*q < 3)` walk, call in cond.
- **Prefix `++i` / `--i` in EXPRESSION position**: the value is the NEW
  value — hoisted to an increment statement + the plain var read at the
  statement level (printf args, decl inits, compound RHS, and inside
  conditions). t76: `printf("%d", ++x + 1)`, `int y = ++x;`,
  `while (++i < 3)`.
- **Multi-char literals** `'ab'`: C packs the bytes big-endian into an
  int (GCC 'ab' = 0x6162). Single chars stay the 1-char string form.
  t77.
- **Read+write out-params** (`void bump(int *x) { *x = *x + 1; }`): the
  frontend lowers the RHS memory read through a temp (arithOperand now
  recurses into bins — only the read subtree temps), and the transform
  treats a read+write write-param as IN-OUT: it keeps its input
  position (only write-ONLY params shift later positions), the caller
  passes the current value, the function's load reads it, and the new
  value comes back via the echo channel. t78: bump + addout(&v, 5).
- **Switch mid-arm breaks** (`case 1: if (c) break; rest;`): a guarded
  mid-arm break keeps its guard with an EMPTY then and wraps the
  REMAINDER of the merged arm (the rest of this case + the fallthrough
  tail) in the guard's ELSE — a true guard exits the switch by skipping
  everything, a false guard falls through (C fallthrough). Trailing
  breaks still end the arm; bare mid-breaks drop the unreachable rest.
  (The Goto/Label route was tried first — the shared RestructureGoto
  pass handles one goto per label and removes it, so multiple
  break-gotos to one switch-end label panic the renderer.) t79.

Still refused (honest): char ORDERING comparisons (`c < 'b'` — the test
grammar has no string ordering and char values are strings), pointer
ADVANCE on array-derived pointers (`q++` where `q = &a[1]` — the
ptrTarget model is compile-time; heap pointers advance fine),
`int a, b;` with pointer declarators.

## v3 — mem-slice-2 + multi-return + the next rung (2026-08-10), 74/74

Three stacked work items, each pinned by a testdata stdout example
(t69–t74):

- **mem-slice-2 (core request c-mem-slice2) — dynamic pointer
  arithmetic**: the arena (numeric ids, element-size scaling) was
  already runtime-side; this lands the missing dynamic POSITION model.
  A pointer that is ever advanced/comparison-used carries its position
  in a dedicated runtime handle var (the while-header cond is emitted
  BEFORE the body's advance, so a compile-time offset could never
  advance per-iteration — ptrNeedsDyn pre-scans the remaining tokens at
  the declaration to force the runtime model). `p = p + n` / `p++` /
  `p += n` lower to `memAdvance` (a NEW handle with the embedded
  element offset); `p < end` / `p == end` lower to the runtime
  `memTest` (position compare); reads/writes go through the handle's
  embedded offset. The root var keeps the base `:0` allocation handle
  (pointer-copy semantics: `int *p = a; a++` leaves p at the original
  element). t69 walk-sum, t70 store-walk.
- **multi-return (core request c-multi-return) — multi-out-param
  functions**: `void getdim(int *w, int *h) { *w = 3; *h = 5; }` — the
  out-param transform (harness/outparam_to_returns.py) now handles
  MULTIPLE write-targets: each write-param's last store becomes an
  `echo` (one value per line, in body order), the dropped write-params'
  bindings are removed and later read-params renumber, and the caller
  captures once and destructures via the runtime `line` helper (the
  core renders `line` natively so the destructure takes the native
  store-write path — a lifted destructured var would desync from a
  runtime store write). Mixed shapes work: write + read-only non-
  pointer params (`getdim(&w, &h, scale)` — renumbering) and
  read-only pointer params (`copy(&dst, &src)` — `*dst = *src`). The
  gate pipeline (Makefile + frontend-stdout.sh, lang c) runs the
  transform on every emitted A1 (conservative identity for programs
  without out-params). Statement-position user calls now emit fnCall
  (they were silently DROPPED before — the call vanished).
- **next rung — calls/ternaries inside ARITHMETIC** (previously "lower
  it to a temp", now automated): hoistArithCalls rewrites any runtime
  call or ternary nested inside arithmetic to a temp var (the A1 Arith
  AST has no Call/Cond node), applied at printf args, declaration
  initializers, plain and compound assignments (a compound RHS is an
  implicit arith OPERAND, so even a top-level call there hoists) and
  for headers. t73: `twice(x) + 1`, `add(x, 2) * 3`, `(x > 3 ? 10 : 20)
  + 1`, `x += twice(x)`.
- **switch fallthrough**: a case body that does NOT end with a break
  merges the next case's arm (C semantics — the shared-body `case 1:
  case 2: body` form and fallthrough into default both fall out). A
  break in the MIDDLE of a case body is still stripped (the if-chain
  has no mid-arm escape) — documented limitation.

## v2 subset (2026-08-10) — 68/68 gate, beyond the v1 refusal wall

v2 lands the common C idioms v1 refused, each pinned by a testdata
stdout example (t58–t68):

- **Runtime function calls return VALUES** — `twice(x)` with a
  non-literal arg lowers to A1 `Call("fnValue", ...)` (the VALUE
  channel; the old `fnCall` emission SILENTLY yielded 0 — the shell
  fnCall is status-only — see the runtime fnValue / core ternary
  arm). Multi-param, multi-statement bodies (`int y = x + 1; return
  y * 2;`) and nested calls work. A call INSIDE arithmetic still
  refuses (the A1 Arith AST has no Call node — lower to a temp).
- **Multi-declarator** `int a, b;` / `int c = 3, d = 4;` (pointers
  refuse — the pointer-init machinery is per-name).
- **Compound assignments** `*= /= %= <<= >>= &= |= ^=` (v1 had
  only `=`/`+=`/`-=`); also in for headers and function bodies.
- **Prefix `++i` / `--i`** in statements and for headers (statement
  position discards the value — the statement forms and the for-header
  postfix now emit the A1 arith `IncDec` node directly (prefix/postfix
  x +/-1, verified t94: gcc == estree); the for-header PREFIX and the
  EXPRESSION-position forms keep the assignment/hoist lowering).
- **Char literals** `'x'` — a 1-char STRING in the store (multi-char
  refuses). Char comparisons use the STRING test operators
  (`=`/`!=` via charVars + operandIsString) — `-eq` would coerce
  both sides to 0. Char ORDERING (`<`/`>`) still refuses (the test
  grammar has no string ordering).
- **Bitwise** `& | ^ ~ << >>` (the structured Arith AST renders
  native JS int32 ops — C `int` semantics; `~x` lowers to `x ^ -1`)
  and **bitwise/mod in CONDITIONS** (the test-string grammar is
  comparison-only, so such conditions route to the runtime
  `testArith` — bash-arith truth — via condCall; even/odd, flag
  masks, `n % 2 == 0`).
- **Ternary** `cond ? a : b` — the runtime `ternary` call; the cond
  is the test-string (native-first) or a testArith call; branches
  are pure values (eager evaluation is sound for the subset).
  Literal conds fold (switch-case values, user bodies). Ternary in
  an ARITH context refuses (lower to a temp).
- **Dynamic array writes** `a[i] = v` in a loop — the runtime
  `arrayStore` call (the baked `a[$i]` target would resolve the
  subscript via the STORE, stale for lifted index vars; the arith
  index arg is lowered natively by the core's `arith` arm).
- **Dynamic heap indices** `p[i]` (read AND write) with a runtime
  index — the mem-arena offset becomes a runtime arith call (the
  arena/element-size seam was already runtime-side).

Still refused (honest, refuse > guess): char ordering comparisons
(`c < 'b'`), pointer advance on array-derived pointers (`q++` where
`q = &a[1]`), `int a, b;` with pointer declarators.

## v1 subset (the refusal wall the v2 idioms crossed)

Yours (in THIS dir): the lexer, parser, emitter, tests. The v1 subset
(t01–t18, all green): printf, int assignments (+=/-=), binary arith,
comparisons, if/else, while, for (lowered to the equivalent while — the
A1 For node is for value-list iteration; the header may declare its
counter `for (int i = 1; ...)` and use postfix i++/i--), function
signatures (skipped; main's body becomes the program), user functions
(`static int triple(int n) { return n * 3; }` — the v1 subset is a
single PURE return expression; a call with LITERAL args constant-folds
through the body, the body itself is never emitted, anything richer
REFUSES), return (skipped), comments, #include. Stdlib string calls
lower to shell-native shapes: strlen("lit") folds to the constant
length, strlen(x) is the A1 param("len") `${#x}` idiom, atoi("lit")
folds to its integer text, atoi(x) is the value itself (the store is
string-typed; Arith coerces), strcmp(a,b) with literal args folds to
the sign of the C result (-1/0/1 — the magnitude is
implementation-defined, so the fold only models the sign; variable
args REFUSE); (int)/(char) casts are identity. Anything else REFUSES
(exit 1) — refuse > guess; every new construct lands by pinning the
executable shape first (probe: gcc vs A1->ESTree->JS) — the
executed-stdout oracle in frontends-stdout.sh `c`.

Shared (do NOT fork): the core (src/shir.rs, ir.rs, estree.rs, parser/);
harness/frontend-stdout.sh (the `c` lang case); setup_backends.sh (the
fleet registration). Core needs: core-requests/.

Pointers (slice 1 — the sh2.mem seam): `int *p`, `&x`, `*p` reads and
`*p = v` writes, pointer copy (`q = p`). Pointers lower to allocation_id +
offset HANDLES — encoded as tagged strings (`\u0001mem:<id>:<offset>`) by
the runtime's sh2.addrOf/memLoad/memStore, because the sh2 store is
string-typed. Slice 1: the allocation IS a named variable (offset 0) —
address-taken vars are pre-scanned and their assignments routed through
setVar so their storage lives in the store (the emitter would otherwise
lift them to native JS bindings and the seam would read a stale store).
Slice 2 (not yet): malloc -> numeric allocation ids over a typed slot
arena, real offsets, pointer arithmetic (needs the minimal type layer:
element size + struct layout). The C backend could pass the seam through
to real pointers instead of emulating.

Pointer-to-string lowering (the natural case): `char *s = "lit"` lowers
the POINTER TO THE STRING itself — no mem.* seam, no handles. s + n -> a
substring (param slice -> native String.slice), s[i] -> a 1-char slice,
%s/%c -> the string/char. Verified t10: gcc == estree with ZERO sh2.mem
calls (contrast t08/t09 int* which use the seam). This is the "lower
naturally when the pattern fits" branch: a char* IS a string, so the
allocation_id + offset machinery is bypassed entirely. Dynamic indices
(s[i] with i a variable) and writable buffers are follow-ups.

Pointer-to-array reduction (the natural case for contiguous storage):
`int a[3] = {...}`, `int *p = &a[1]` — the pointer's target is STATICALLY
known, so p becomes the compile-time pair (array, base) and every use
folds to direct array indexing: *p -> a[base], p[k] -> a[base+k],
*p = v -> the baked-name array write. The pointer variable is never
emitted. This is the target-natural form for every language with
indexable storage (JS/Perl/Python arrays, Go slices, C identity); the
shell family has no memory-like arrays, so the seam stays the floor.
Verified t11: gcc == estree (20/30/10/99), zero pointer machinery.
Non-static pointers (p passed to a function, p = p + n in a loop) refuse
— the seam or a follow-up handles those.

Static alias folding (the zero-cost scalar case): `int *p = &x;` where x
is a scalar and p never escapes -> p is ELIMINATED — *p reads/writes
become x directly, the alias chain (`int *q = p`) folds through, and the
emitted program is exactly as if written without pointers (t12: gcc ==
estree 5/9, zero mem calls, zero pointer vars). This makes the seam
unnecessary for the most common scalar-pointer pattern and sidesteps the
store-vs-lifted-binding problem entirely (the writes ARE the variable).
Raw pointer-value uses (p == NULL, printf %p) refuse via the unsupported
marker.

Worker: ./run_frontend_worker.sh — failure-driven (make test -> pi
deepseek-v4-turbo -> commit/stash -> trap/escalate).

Out-parameter elimination (harness/outparam_to_returns.py): a function
whose param is a PURE WRITE-TARGET (memStore through the handle, never
read, no escape) is transformed per the param's role:
  write-only  -> the value is ECHOED and the caller CAPTURES it (the shell
                 value-return channel — fnCall returns STATUS, not values,
                 so x = $(f) is the faithful form): f(&x) -> x=$(f)
  read+write  -> PASS-BY-VALUE + return: the caller passes the current
                 value, the param renumbers: f(&x) -> x=$(f $x)
  read-only   -> pass-by-value input (no return use)
Param renumbering is applied when the dropped out-param shifts the
read-params ($2 -> $1). Verified: fill (pure return, f()) and copy
(pass-by-value, f(x)) — before (seam) and after (echo+capture) both
output a=7 b=7; the after has zero mem.* calls.
