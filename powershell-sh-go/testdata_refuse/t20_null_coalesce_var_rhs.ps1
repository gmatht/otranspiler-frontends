# t20_null_coalesce_var_rhs: the `??` operator with a VARIABLE default
# — pinned REFUSE. `Write-Output foo($x ?? $y)` with both variables
# unset coalesces to $null, and `Write-Output` of a BARE $null prints
# NOTHING in live pwsh 7.6.4 (verified 2026-08-15) while the A1 echo
# of the empty store value prints a blank line — the t02 PRINT edge,
# outside the v1 text-closed subset. The t20 rung
# (testdata/t20_null_coalesce.ps1) pins a literal string / decimal
# integer default. Refuse > guess.
Write-Output foo($x ?? $y)
