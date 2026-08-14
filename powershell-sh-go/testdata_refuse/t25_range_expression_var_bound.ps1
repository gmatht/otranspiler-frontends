# t25_range_expression_var_bound: the parenthesized `..` range with a
# VARIABLE bound — pinned REFUSE. The t25 rung
# (testdata/t25_range_expression.ps1) folds an ALL-LITERAL
# parenthesized range to compile-time per-element echoes (the t24
# fold, shared through lowerRange); a variable bound would need the
# RUNTIME array (the A1 Range bounded-iterable node — a later
# milestone), and the t24 variable-bound edge carries over by
# construction (the shared lowerRange / rangeBound): refuse > guess.
Write-Output (1 .. $x)
