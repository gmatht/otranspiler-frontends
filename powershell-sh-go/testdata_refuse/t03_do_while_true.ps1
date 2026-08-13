# t03_do_while_true: `$true` in a do/while condition — pinned REFUSE.
# Verified against live pwsh 7.6.4: `$true` is a TRUTHY automatic
# variable, so `do { … } while ($true)` loops forever, while the A1
# getVar("true") store read is "" (falsy) and the transpiled loop
# terminates — a divergence. The t06 condition subset pins only
# unset-user-variable reads (both worlds falsy); the truthy-automatic
# class refuses (refuse > guess).
do { Write-Output "once" } while ($true)
