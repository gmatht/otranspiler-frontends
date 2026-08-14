# t36 refuse pin: `Write-Output (7 \ 2)` — pwsh's `\` is INTEGER
# division (prints 3) but the A1 ArithAst has no `\` operator (the op
# field is open-ended but the A1→ESTree renderer knows + - * / % only)
# → refuse > guess.
Write-Output (7 \ 2)
