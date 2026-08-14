# t28_requires_duplicate: a repeated #requires parameter — pwsh 7.6.4
# binds the WHOLE directive list into ONE parameter set, so `-Version`
# twice (even on separate lines — the grammar parses each line as its
# own requires_statement, no error) fails the run with "Cannot bind
# parameter because parameter 'version' is specified more than once"
# while a dropped directive would run — divergent output: refuse >
# guess.
#requires -Version 5.1
#requires -Version 5
Write-Output "ran"
