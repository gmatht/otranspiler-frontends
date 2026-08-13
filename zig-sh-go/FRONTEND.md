# zig-sh-go frontend (dir: frontends/zig-sh-go)

Zig source -> A1 shIR JSON — **the C++-style split onto c-sh-go's
lowering** (PLAN_ZIG_F.md: one runtime, two grammars, one shared
lowering; include but never modify `frontends/c-sh-go`).

**Status: rungs t01–t13 landed (gate green 2026-08-13).** The
failure-driven worker implements the PLAN_ZIG_F §3 rung table in
order — the Makefile's rung-pin guard stays RED until each planned pin
lands (subset starvation guard). Each rung is a pinned `testdata/`
example, landed by pi (deepseek), validated by `make test` (refusals +
ingress acceptance + executed-stdout vs native `zig run`), and
committed. The v1 subset and the refusal table are PLAN_ZIG_F.md.

## What is implemented (the expressible surface, pinned by testdata)

- the std import binding `const std = @import("std");` — skipped
  (comptime-only; the enabler of `std.debug.print`)
- `pub fn main() void { … }` → `int main(void) { … }` (clib's consumed
  signature; main's body becomes the program); user functions
  `fn f(a: i32, b: i32) i32` → `static int f(int a, int b)` (t07)
- `std.debug.print(FMT, .{args})` → `printf(CFMT, args…)`:
  `{d}`/`{s}`/`{c}`/`{any}` → `%d`/`%s`/`%c`/`%d`, `{{`/`}}` → `{`/`}`,
  the `.{…}` anonymous tuple → the C argument list
- the C-compatible control flow, mapped 1:1 onto clib (the c-sh-go
  corpus shapes): `if`/`else if`/`else` (t03), `while` + the
  `: (update)` continue expression (t04), `for (0..n) |i|` and
  `for (arr) |it|` range/items iteration (t05/t06), `switch` with
  `else =>` → C switch with case breaks (t11), `return e;` (t07)
- `var`/`const` declarations: `var x: i32 = 5;` → `int x = 5;`,
  `const p: *i32 = &x;` → `int *p = &x;` (t08, incl. the `x.*` deref
  read/write and `&x`), `const s: []const u8 = "hi";` →
  `char *s = "hi";` + `s[0]` indexing and `s.len` → `strlen(s)` (t09),
  `const items = [_]i32{…};` → `int items[N] = {…};` (t06), comptime
  const VALUES `const N = 5; const M = N * 2;` → plain int decls (t12)
- function-scope `defer stmt;` → appended in REVERSE before every
  return and at the function end (t10; block-scope defer refuses)
- `@intCast(x)` / `@as(T, x)` → the identity cast `(int) (x)` (t13)
- comment/whitespace-only sources emit a valid EMPTY A1 program (the
  stub contract, now via clib.Shir("") — byte-identical shape)
- the Zig-only keyword REFUSAL table (PLAN_ZIG_F §3) — error unions,
  optionals, try/catch/orelse, errdefer, comptime blocks, break/continue
  (later rungs), generics, async, asm, structs, std.* beyond
  std.debug.print, `@` builtins beyond @import/@intCast/@as,
  non-decimal/float literals… each refuses loudly with its rung
  (REFUSE > GUESS). if/while/for/switch/return/defer are statement-only
  in the subset — expression-position uses refuse loudly too.

## Architecture (the cpp split, PLAN_ZIG_F §3)

- `main.go` — the Zig-only surface: a tokenizer, the desugar-to-C
  translator, and the Zig-only keyword REFUSAL table. The hand-rolled
  tokenizer is the PROVISIONAL parser (the cpp template): PLAN_ZIG_F §2
  replaces it with tree-sitter-zig when the full grammar work lands.
- Desugar produces **C source text**; emit is `clib.Shir` — the SHARED
  lowering + A1 emitter, byte-equivalent to the C frontend on the
  expressible subset (the oracle). The C corpus staying green is the
  hard invariant; extensions to C-owned behavior go through
  `c-requests/` (implemented by the c-sh-go worker).
- `go.mod` module-replaces `clib` from `frontends/c-sh-go` exactly like
  `frontends/cpp-sh-go` does — never modified here.

The native oracle captures `zig run` with 2>&1 — Zig's idiomatic
`std.debug.print` targets stderr; the transpiled target emits stdout,
and the observable output is the comparison. The harness resolves the
direct snap binary (`/snap/zig/current/zig`) and passes an absolute
path to `zig run` (it resolves its argument against the CWD, and the
gate `cd`s into a scratch dir).

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/. A change to the shared C lowering goes through
c-requests/ (implemented by the c-sh-go worker).
