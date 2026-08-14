# t26_pipeline_chain_three: a THREE-chain chain (`a && b && c` — also
# the mixed `a && b || c` form: both parse as 3 pipeline_chain parts +
# 2 pipeline_chain_tail operators). The nested BinOp shape the core
# emits for `echo a && echo b && echo c` (left-assoc nesting) is real
# A1, but the t26 subset pins exactly TWO chains and ONE operator — the
# nested shape is unpinned (refuse > guess; the next rung lands it with
# its own pin).
Write-Output "a" && Write-Output "b" && Write-Output "c"
