# t13_foreach_parallel: the foreach_parameter child of the
# foreach_statement — `foreach -parallel ($x in $list) { … }` (the
# grammar's optional `-parallel` alias, named foreach_parameter). The
# parallel foreach runs the iterations CONCURRENTLY — a different
# execution model (the plan's `&`-parallelism machinery, unpinned), so
# it must fail the emit loudly (refuse > guess): the t13 rung pins the
# plain sequential `foreach ($x in $list) { … }` form.
foreach -parallel ($x in $list) {
    Write-Output $x
}
