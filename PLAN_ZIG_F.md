# PLAN_ZIG_F — a Zig frontend on the c-sh-go split

**Status: PROPOSAL (no code yet).** This document proposes `frontends/zig-sh-go` —
a Zig source -> A1 shIR JSON frontend, architected as the exact mirror of
`frontends/cpp-sh-go`: a bounded DESUGAR of Zig's C-compatible surface onto
c-sh-go's `clib` package (include, never modify), with Zig-only constructs
handled by targeted desugars or pinned REFUSE > GUESS.

Grounding facts (verified 2026-08-10):

- `zig 0.16.0` is installed (`/snap/bin/zig`) — a NATIVE executed-stdout gate
  is possible (`zig run tNN.zig`), modulo the snap-confine caveat the `go`
  case already works around in `harness/frontend-stdout.sh` (prefer the
  direct snap binary `/snap/zig/current/bin/zig`).
- `frontends/cpp-sh-go` proves the split end-to-end: import `clib` as a Go
  module (never modify), desugar to C text, emit `clib.Shir`, gate = own
  corpus + the C-invariant. The `c-requests/` channel is live.
- Zig deliberately kept C-like control flow (`if`/`else`, `while`, `for`,
  `switch`, `break`, `continue`) — the C-compatible surface is real, and the
  c-sh-go v4 frontend (79/79: pointers, mem arena, structs-as-dotted-scalars,
  goto-cleanup, ternary, hoisted cond reads) is a wide base to stand on.

## 1. Why Zig (and why the cpp split)

- Zig is the other "modern C": manual memory, pointers, no hidden allocation,
  no runtime. The C frontend's whole lowering (mem seam, pointer walks,
  const folding) applies directly; the interesting new semantics are
  `defer`/`errdefer`, `comptime`, error unions, optionals, slices — the same
  list cxx-rust-adequacy maps as the shared-typed-node frontier.
- The cpp split gives the architecture for free: **one runtime, two grammars,
  one shared lowering**. The Zig walker is the only language-specific layer;
  everything above the desugar is clib's proven emit. The C-invariant gate
  (c-sh-go's corpus stays green) is the hard line, exactly as CPP_PLAN §5.

## 2. Parser choice

Hand-rolled tokenizer for v1 (the c-sh-go precedent — `main.go` is a
hand-rolled lexer/parser). Zig's grammar is small, and the v1 subset is
smaller. Upgrade path: tree-sitter-zig (like CPP_PLAN §1's tree-sitter-cpp
argument — error tolerance converts "choked at the token stream" into
"parsed the file, refused this node"), adopted when the subset needs it.

## 3. The split (mirror CPP_PLAN §3)

- `main.go` — the Zig-only surface: a tokenizer, the desugar-to-C
  translator, and the Zig-only keyword REFUSAL table.
- Desugar produces **C source text**; emit is `clib.Shir` (byte-equivalence
  oracle on the C-compatible surface). Extensions to C-owned behavior go
  through `c-requests/`.
- `go.mod` module-replaces `clib` from `frontends/c-sh-go` exactly like
  `frontends/cpp-sh-go` does — never modified here.

### The C-compatible desugar (each pinned by testdata_zig/*.zig)

| Zig | C (via clib) |
|---|---|
| `const x = 5;` / `var y: i32 = 0;` | `int x = 5;` / `int y = 0;` |
| `fn add(a: i32, b: i32) i32 { … }` / `pub fn` | `static int add(int a, int b) { … }` |
| `void` / `bool` | `void` / `int` (1/0) |
| `x.*` read/write | `*x` / `*x = v` (the mem seam) |
| `&x` | `&x` |
| `*i32` / `*const u8` | `int *` / `const char *` (pointer-to-string for u8) |
| `[]const u8` string literal `"hi"` | `char *` literal (pointer-to-string lowering) |
| `if (c) {} else if {} else {}` | identical |
| `while (c) {}` / `while (c) : (u) {}` | `while (c) { … }` / `while (c) { …; u; }` |
| `for (0..n) \|i\| {}` / `for (items) \|it\| {}` | `for (i = 0; i < n; i++) {}` / array iteration |
| `switch (v) { 1 => …, else => … }` | C switch (the fallthrough/mid-break lowering is already in clib) |
| `return e;` | identical |
| `std.debug.print("x={d}\n", .{x})` | `printf("x=%d\n", x)` — `{d}/{s}/{any}` → `%d/%s/%d`, `.{…}` → the arg list |
| `@intCast(x)` / `@as(T, x)` | `(int) x` (identity cast) |
| comments `//` `/* */` | identical |

### The Zig-only desugars (targeted, not refusal)

- **`defer stmt;`** (function scope only, v1): the deferred statements run in
  REVERSE at scope exit — desugar by appending them, reversed, before every
  `return` (and at the function end). Block-scope `defer` refuses (the
  wrapping transform is a follow-up). This is the C goto-cleanup idiom the
  c-frontend already pins (t31), made a source transform.
- **`const` compile-time values**: `const N = 5; const M = N * 2;` — the
  desugar emits `int N = 5; int M = N * 2;` and clib's foldConst folds the
  chain (the C frontend's literal-arg discipline). `comptime` BLOCKS and
  comptime FUNCTIONS refuse (a real comptime interpreter is a later rung).

### Pinned refusals (testdata_zig/*_refuse.zig — the emit must FAIL)

`?T` optionals / `null`, error unions `!T` / `try` / `catch` / `orelse`
(the error model is a dedicated rung), `errdefer`, tagged unions,
generics (`fn(comptime T)`), `anytype`, `comptime {}` blocks, async,
`usingnamespace`, labeled `break :label` / `continue :label`, `@` builtins
beyond `@intCast`/`@as`, `std.*` beyond `std.debug.print` (no `std.fs`,
no writers, no ArrayList — allocator idioms need the error model first),
block-scope `defer`, multi-line string continuation `\\`, `extern fn`,
`export fn`, `callconv`, `align`, `comptime` fields in structs.

## 4. The gate

`make test` = the zig gate (refusals + ingress acceptance via
`debashc --shir-in-estree` + executed-stdout vs native `zig run`) **AND the
C-invariant** (c-sh-go's corpus stays green — the hard line). `make
test-zig` / `make test-c-invariant` split them, exactly like cpp-sh-go.

The native side: `zig run tNN.zig` — testdata files are complete programs
(`pub fn main() void`). frontend-stdout.sh gains a `zig` case
(`native=(zig run)` — the command array gives `zig run file.zig`), with the
go-style direct-snap-binary resolution (`/snap/zig/current/bin/zig`) to
sidestep snap-confine in containerized workers.

## 5. Worker / fleet

`run_frontend_worker.sh` (the failure-driven loop) + `setup_backends.sh`
registration (`--pi-fix-frontend zig-sh-go`, DEFAULT_FRONTEND_LANGS), and
the `c-requests/` channel for anything that needs clib (the C owner
implements it, closed via `c-requests/done/`).

## 6. Testdata plan (t01+)

`t01_print.zig` (`std.debug.print` + `.{…}` + `{d}`/`{s}`), `t02_arith`,
`t03_if_else`, `t04_while` (+ `: (update)` form), `t05_for_range`,
`t06_for_items` (array iteration), `t07_fn` (multi-param, nested calls),
`t08_pointers` (`x.*`, `&x`), `t09_string` (`[]const u8` + indexing),
`t10_defer` (reverse cleanup, function scope), `t11_switch`,
`t12_comptime_const` (folded const chain), `t13_const_cast`
(`@intCast`), plus a `*_refuse` set pinning each table entry.

## 7. Risks / milestones

- **The desugar is bigger than cpp's.** cpp's surface was 4 tokens; Zig's
  C-like surface is a real translator (declarations, fn syntax, `.*`). The
  mitigation is the C-invariant + byte-equivalence oracle: every desugar
  step is pinned before it lands (refuse > guess).
- **The error model is the real frontier** — `try`/`catch`/`orelse` map to
  status-check control flow, and allocators need it first. v1 refuses them;
  the milestone after v1 is: `orelse` → the c-frontend's ternary,
  `try` → a status check, then allocator idioms on the mem seam.
- Milestones: (1) scaffold + the C-compatible desugar + C-invariant (the
  cpp template, ~a session), (2) defer/comptime-const desugars + t10/t12,
  (3) the refusal table + worker + fleet registration, (4) the native zig
  gate hardening (snap path).
