# t28_requires_swallow: a BARE `#requires` (no arguments) makes the
# vendored grammar's requires_statement rule SWALLOW the following
# statements as argument groups (verified against the CST —
# `#requires` + a `-Version 5.1` line parses as ONE requires_statement
# with valid-looking groups, no statement_list at all) while live pwsh
# 7.6.4 parse-errors: a silent drop would run the transpiled program
# where pwsh refuses the run — the single-line guard refuses (refuse >
# guess).
#requires
-Version 5.1
