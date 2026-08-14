# t31_switch_string_condition: a STRING / bareword clause condition —
# `switch ($x) { "one" { … } }` (the bareword form `one { … }` parses
# as the same anonymous _switch_condition_token as `default`). pwsh
# matches strings with the CASE-INSENSITIVE `-eq` (a discriminant
# "ONE" matches the clause "one") while the A1 `case` pattern match is
# case-sensitive — the branches DIVERGE for any discriminant differing
# in case; the t31 subset pins bare decimal integer clauses and
# `default` (refuse > guess).
switch ($x) {
  "one" { Write-Output "one" }
  default { Write-Output "d" }
}
