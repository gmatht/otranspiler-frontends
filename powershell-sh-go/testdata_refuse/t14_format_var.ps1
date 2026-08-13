# t14_format_var: the `-f` format operator with a VARIABLE argument —
# pinned REFUSE. The t14 rung (testdata/t14_format.ps1) folds an
# ALL-LITERAL format to a compile-time constant; a variable argument
# needs RUNTIME formatting — the plan's "printf-style" row
# (PLAN_POWERSHELL_F.md §1) via the A1 printf Call channel, a later
# milestone. Refuse > guess.
Write-Output foo("{0}" -f $x)
