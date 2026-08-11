# zig-sh-go frontend (dir: frontends/zig-sh-go)

Zig source -> A1 shIR JSON — **the C++-style split onto c-sh-go's
lowering** (PLAN_ZIG_F.md: one runtime, two grammars, one shared
lowering; include but never modify `frontends/c-sh-go`).

**Status: STUB — the failure-driven worker implements it.** This dir is
the worker's delivery vehicle: the stub emits a valid EMPTY A1 program
for comment-only sources and REFUSES everything else loudly. The worker
(loop-frontend-zig-sh-go.log) runs the gate every cycle; each RED pin in
`testdata/` (t01_print…) is landed by pi (deepseek), validated by
`make test` (refusals + ingress acceptance + executed-stdout vs native
`zig run`), and committed. The v1 subset and the refusal table are
PLAN_ZIG_F.md.

The native oracle captures `zig run` with 2>&1 — Zig's idiomatic
`std.debug.print` targets stderr; the transpiled target emits stdout,
and the observable output is the comparison.

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/. A change to the shared C lowering goes through
c-requests/ (implemented by the c-sh-go worker).
