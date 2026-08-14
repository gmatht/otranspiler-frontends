# t28_requires_modules: `#requires -Modules PSScriptAnalyzer` — module
# presence is ENVIRONMENT-dependent (pwsh fails the run when the module
# is missing, the plan's "`using` / modules" refusal row): the v1
# subset never guesses what the target machine has installed — refuse >
# guess.
#requires -Modules PSScriptAnalyzer
Write-Output "ran"
