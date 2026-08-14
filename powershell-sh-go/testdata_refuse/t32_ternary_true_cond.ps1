# t32_ternary_true_cond: the ternary operator with a TRUTHY pwsh
# automatic as the condition — pinned REFUSE. `Write-Output
# foo($true ? "a" : "b")` prints the then-branch `a` in live pwsh
# 7.6.4 (verified 2026-08-21) while the falsy-lowering would take the
# else branch ("b") — the t06 `$true` divergence precedent: the
# ternary condition lowers through the same condition subset
# (lowerCondVar), so the refusal is the same. Refuse > guess.
Write-Output foo($true ? "a" : "b")
