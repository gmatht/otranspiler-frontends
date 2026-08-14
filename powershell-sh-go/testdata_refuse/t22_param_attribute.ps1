# t22_param_attribute — the attribute_list child of a param block or
# script_parameter: `[CmdletBinding()]` / `[Parameter()]` /
# `param([string]$a)`. Binding metadata that affects how arguments bind
# — the plan's "`Param()` advanced attributes" refusal
# (PLAN_POWERSHELL_F.md §1 pinned refusals); refuse > guess.
param([string]$a)
Write-Output "x"
