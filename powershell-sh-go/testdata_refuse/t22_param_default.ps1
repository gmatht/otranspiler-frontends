# t22_param_default — the script_parameter_default child of a param
# block: `param($a = "d")`. Live pwsh 7.6.4 binds the DEFAULT when no
# argument is passed (a real assignment), so `Write-Output $a` would
# print "d" where the A1 store reads "" — divergent output; the
# assignment rung lands it (refuse > guess, the t22 param rung pin).
param($a = "d")
Write-Output "x"
