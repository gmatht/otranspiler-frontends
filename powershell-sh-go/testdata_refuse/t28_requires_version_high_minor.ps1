# t28_requires_version_high_minor: `#requires -Version 6.3` — the
# release-line whitelist edge: pwsh 7.6.4's IsValidPSVersion accepts
# major 6 ONLY with minor 0-2 (`-Version 6.2` runs clean, `-Version
# 6.3` fails the run — the check is NOT a numeric comparison, and a
# naive "≤ 7.6" would have silently accepted this, a miscompile).
# Dropping it would run the transpiled program where pwsh refuses the
# run: divergent output — refuse > guess.
#requires -Version 6.3
Write-Output "ran"
