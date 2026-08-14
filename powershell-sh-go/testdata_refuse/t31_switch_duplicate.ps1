# t31_switch_duplicate: duplicate clause conditions — `switch (1) {
# 1 { "a" } 1 { "b" } }`. Live pwsh 7.6.4: EVERY matching clause runs
# (there is no fallthrough suppression) — this program prints "a" then
# "b". The A1 `case` runs the FIRST matching pattern's body only —
# `case 1 in 1) echo a ;; 1) echo b ;; esac` prints "a" — the branches
# DIVERGE; the t31 subset pins distinct clause values (refuse > guess).
switch (1) {
  1 { Write-Output "a" }
  1 { Write-Output "b" }
  default { Write-Output "d" }
}
