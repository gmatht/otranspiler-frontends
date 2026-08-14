# t28_requires_psedition_desktop: `#requires -PSEdition Desktop` — the
# subset pins "Core" (pwsh IS the Core edition; verified: the Desktop
# requirement fails the run — "does not match the currently running
# PowerShell Core edition") while a dropped directive would run —
# divergent output: refuse > guess.
#requires -PSEdition Desktop
Write-Output "ran"
