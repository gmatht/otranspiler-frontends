# t03_cast: the cast_expression node — `[type] operand` (tree-sitter
# powershell: a type_literal followed by the operand unary_expression,
# wrapped in expression_with_unary_operator). The v1 mapping is
# IDENTITY (PLAN_POWERSHELL_F.md §1 — the C `(int)` precedent): the A1
# is text-typed, so the type_literal is dropped and the operand lowers
# exactly as if the cast were absent — this line emits byte-identical
# to `Write-Output "cast value"` (the t01 echo Call), and the
# transpiled run prints "cast value".
Write-Output [string]"cast value"
