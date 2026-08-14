# t20_null_coalesce_true_lhs: the `??` operator with a TRUTHY pwsh
# automatic as the left operand — pinned REFUSE. `Write-Output
# foo($true ?? "d")` prints True in live pwsh 7.6.4 (verified
# 2026-08-15) while the falsy-lowering would take the default ("d") —
# the t06 `$true` divergence precedent: the coalesce LHS lowers through
# the same condition subset (lowerCondVar), so the refusal is the same.
# Refuse > guess.
Write-Output foo($true ?? "d")
