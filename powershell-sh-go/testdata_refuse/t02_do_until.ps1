# t02_do_until: the `until` keyword form of the do_statement node —
# pinned REFUSE. The t06 pin covers the `while` form; `do { … }
# until (cond)` is unpinned (its lowering needs the A1 Not-cond wrap
# the core uses for bash `until` — a later rung), so the emit must
# fail loudly (refuse > guess).
do { Write-Output "once" } until ($x)
