# t32_ternary_var_branch: the ternary operator with a VARIABLE branch
# — pinned REFUSE. `Write-Output foo($x ? "a" : $y)` with both
# variables unset evaluates to $null ($x falsy → the else branch $y),
# and `Write-Output` of a BARE $null prints NOTHING in live pwsh 7.6.4
# (verified 2026-08-21) while the A1 echo of the empty store value
# prints a blank line — the t02 PRINT edge, outside the v1 text-closed
# subset. The t32 rung (testdata/t32_ternary.ps1) pins a literal
# string / decimal integer on each branch. Refuse > guess.
Write-Output foo($x ? "a" : $y)
