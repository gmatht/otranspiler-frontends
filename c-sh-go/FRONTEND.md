# c-sh-go frontend (dir: /home/llm/sh2loop/frontends/c-sh-go)

C source -> A1 shIR JSON (the shell-flavored subset of C).

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
  position discards the value, so the lowering is `i = i +/- 1`;
  prefix in EXPRESSION position still refuses).
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

Still refused (honest, refuse > guess): calls/ternary inside TEST
operands (`*p < 5` — a value read in a condition; lower to a temp),
prefix ++/-- in EXPRESSION position (`x = ++i` — statements and for
headers work), multi-char literals, `int a, b;` with pointers,
multi-return with read+write pointer params, switch mid-arm breaks,
char ordering comparisons (`c < 'b'`).

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
