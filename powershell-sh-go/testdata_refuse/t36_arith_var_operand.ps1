# t36 refuse pin: `Write-Output ($x + 1)` — with $x UNSET pwsh coerces
# $null to 0 (prints 1), while the A1 Arith of an unset store var is a
# string-coercion trap ("" + 1 concatenates). Divergence → refuse.
Write-Output ($x + 1)
