# t31_switch_parameters: the switch_parameters node of
# tree-sitter-powershell — the `switch -Regex ($x) { … }` flag forms
# (the grammar's switch_statement rule: switch + optional
# switch_parameters + switch_condition + switch_body; the
# switch_parameter tokens are -regex / -wildcard / -exact /
# -casesensitive / -parallel — the vendored grammar does not parse
# pwsh's `-Case` flag at all). The flags change the matching semantics
# — `-Regex` (regex matching), `-Wildcard` (glob matching), `-Exact`,
# `-CaseSensitive`, `-Parallel` (concurrent clause evaluation) — none
# of which the A1 `case` pattern equality can express; the t31 subset
# pins plain `switch` only (refuse > guess).
switch -Regex ($x) {
  1 { Write-Output "one" }
  default { Write-Output "d" }
}
