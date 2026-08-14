# t24_range_var_bound: the `..` range operator with a VARIABLE bound —
# pinned REFUSE. The t24 rung (testdata/t24_range.ps1) folds an
# ALL-LITERAL range to compile-time per-element echoes; a variable
# bound needs RUNTIME array construction — the plan's array rung via
# the A1 Range bounded-iterable node, a later milestone. Refuse >
# guess.
Write-Output foo($a..3)
