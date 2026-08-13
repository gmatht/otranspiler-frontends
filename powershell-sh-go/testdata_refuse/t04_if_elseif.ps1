# t04_if_elseif: elseif clauses in an if_statement — pinned REFUSE.
# The t07 rung lands the if_statement with its else_clause tail; the
# elseif_clauses field is the NEXT rung (the A1 If node's elsifs slot
# is ready — the plan's "If / else-if chain" row — but unpinned;
# refuse > guess).
if ($x) { Write-Output "a" } elseif ($y) { Write-Output "b" } else { Write-Output "c" }
