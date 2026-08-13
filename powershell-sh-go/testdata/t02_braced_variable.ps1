# t02_braced_variable: the ${name} spelling of a variable read — the
# braced_variable token of tree-sitter-powershell (the `variable` rule
# admits `$name` and `${name}` alike; the braces are pure spelling, so
# both lower to getVar("name"), byte-identical for the same store slot).
# Exercised in both places the construct appears: as a bare command
# argument and inside a double-quoted string (the Interpolate lit/expr
# parts around the braced variable). foo is unset, so the bare read
# prints an empty line and the interpolation prints "a  b".
Write-Output ${foo}
Write-Output "a ${foo} b"
