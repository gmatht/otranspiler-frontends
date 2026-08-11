# powershell-sh-go frontend (dir: frontends/powershell-sh-go)

PowerShell (.ps1) source -> A1 shIR JSON — **the bat sibling**
(workspace-side dir; no git worktree; the object pipeline is a stated
TEXT approximation for v1; see PLAN_POWERSHELL_F.md).

**Status: STUB — the failure-driven worker implements it.** This dir is
the worker's delivery vehicle: the stub emits a valid EMPTY A1 program
for comment-only sources and REFUSES everything else loudly. The worker
(loop-frontend-powershell-sh-go.log) runs the gate every cycle; each
RED pin in `testdata/` (t01_echo…) is landed by pi (deepseek),
validated by `make test` (refusals + ingress acceptance + executed-
stdout vs the RECORDED expectations — pwsh is not installed on the
fleet boxes, so the native oracle is `native_limits_powershell` in
harness/frontend-stdout.sh, the bat precedent), and committed. The v1
subset and the refusal table are PLAN_POWERSHELL_F.md. The parser is
wharflab/tree-sitter-powershell (empirically verified 21/24 — the
function param()-block and switch-clause gaps are pinned refusals until
the grammar closes them).

Scope: this dir + harness/* (shared test infra). Shared core
(sh2perl/src/*, parser/) is the estree worker's — a change there goes
through core-requests/.
