# t36 refuse pin: `Write-Output (7 / 2)` — pwsh 7.6.4 prints 3.5 (REAL
# division) while the A1 Arith's `/` is bash INTEGER division
# (Math.trunc — the transpiled run prints 3). Divergence → refuse.
Write-Output (7 / 2)
