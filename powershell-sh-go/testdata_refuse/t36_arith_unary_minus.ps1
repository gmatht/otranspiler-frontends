# t36 refuse pin: a unary-operator operand (`-1` parses as
# expression_with_unary_operator) — the t36 subset pins bare decimal
# integer operands; the sign forms are unpinned → refuse.
Write-Output (-1 + 2)
