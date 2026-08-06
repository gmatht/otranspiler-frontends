# c-sh-go frontend (dir: /home/llm/sh2loop/frontends/c-sh-go)

C source -> A1 shIR JSON (the shell-flavored subset of C).

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
