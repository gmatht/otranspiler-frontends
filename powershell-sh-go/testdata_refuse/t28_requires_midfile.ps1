# t28_requires_midfile: a `#requires` AFTER the statements — live pwsh
# 7.6.4 enforces it in ANY position (verified: this file fails the run
# before "first" prints, the Microsoft docs' "statements before it run
# first" claim does NOT hold for 7.6.4 -File mode) while the vendored
# grammar builds requires_directive_list only at the TOP of the file
# and parses a mid-file `#requires` as a COMMENT (the t19
# grammar-over-accepts precedent): a dropped comment would run the
# transpiled program where pwsh refuses the run — divergent output:
# refuse > guess.
Write-Output "first"
#requires -Version 99
