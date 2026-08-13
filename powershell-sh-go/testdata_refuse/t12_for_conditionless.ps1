# t12_for_conditionless (REFUSE pin): the conditionless for — `for
# (;;)` — an infinite loop. pwsh runs the body forever (the empty
# condition reads TRUE) while the A1 would need a true-literal
# condition the subset doesn't pin; the t06 `$true`-divergence
# precedent (pwsh truthy vs the A1 store "" — a while condition would
# loop forever) applies: refuse > guess.
for (;;) { Write-Output "spin" }
