# t31_switch_file: the switch_filename child of switch_condition — the
# `switch -File f { … }` form, which matches each LINE of the file
# against the clauses (line-based file matching). The A1 `case`
# discriminant is a single value — file-line enumeration is a
# different execution model, outside the v1 subset (refuse > guess).
switch -File /tmp/lines.txt {
  1 { Write-Output "one" }
  default { Write-Output "d" }
}
