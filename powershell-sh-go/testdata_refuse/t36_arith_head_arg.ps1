# t36 refuse pin: a head argument before the arithmetic paren —
# `Write-Output "a" (1 + 2)` writes TWO pipeline objects (prints a then
# 3 on separate lines) — the multi-object shape stays outside the v1
# single-object echo mapping → refuse.
Write-Output "a" (1 + 2)
