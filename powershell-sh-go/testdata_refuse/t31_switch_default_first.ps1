# t31_switch_default_first: a `default` clause that is NOT the last
# clause — `switch (1) { default { "d" } 1 { "one" } }`. Live pwsh
# 7.6.4 (verified 2026-08-21): ALL matching clauses run, the default
# only when NOTHING matched — so this program prints "one" (the later
# clause matches, the default is skipped). The A1 `*` pattern would
# match FIRST — `case 1 in *) echo d ;; 1) echo one ;; esac` prints
# "d" — the branches DIVERGE; the t31 subset pins default LAST (refuse
# > guess).
switch (1) {
  default { Write-Output "d" }
  1 { Write-Output "one" }
}
