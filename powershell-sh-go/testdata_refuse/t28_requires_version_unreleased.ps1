# t28_requires_version_unreleased: `#requires -Version 99` — no such
# PowerShell release line: pwsh 7.6.4 fails the run ("does not match
# the currently running version of PowerShell 7.6.4") while a dropped
# directive would run — divergent output: refuse > guess.
#requires -Version 99
Write-Output "ran"
