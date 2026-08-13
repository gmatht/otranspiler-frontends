# t05_if_true: `$true` in an if condition — pinned REFUSE. The same
# divergence as testdata_refuse/t03_do_while_true: `$true` is a TRUTHY
# automatic variable in pwsh (the then-branch runs there), while the
# A1 getVar("true") store read is "" (falsy) and the transpiled run
# takes the else-branch — a divergence. The t07 condition subset pins
# only unset-user-variable reads (both worlds falsy).
if ($true) { Write-Output "yes" } else { Write-Output "no" }
