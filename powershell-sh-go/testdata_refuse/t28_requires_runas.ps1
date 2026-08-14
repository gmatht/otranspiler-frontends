# t28_requires_runas: `#requires -RunAsAdministrator` — the elevation
# requirement is ENVIRONMENT-dependent (pwsh fails the run when the
# shell is not elevated): the v1 subset never guesses the target
# machine's privileges — refuse > guess.
#requires -RunAsAdministrator
Write-Output "ran"
