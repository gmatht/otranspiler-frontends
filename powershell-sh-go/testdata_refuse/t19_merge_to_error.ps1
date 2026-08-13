# t19_merge_to_error: the `X>&2` members of the t19
# merging_redirection_operator rung — live pwsh 7.6.4 REJECTS every
# form at parse time ("The 'N>&2' operator is reserved for future
# use", verified 2026-08-14: `Write-Output "hi" 3>&2` is a
# ParserError), while the vendored tree-sitter grammar over-accepts
# them — a real pwsh program can never contain them, so the frontend
# refuses (the t04 command-whitelist precedent: refuse what live pwsh
# rejects). `1>&2`, `2>&2`, `*>&2` and `4>&2`…`6>&2` refuse the same
# way.
Write-Output "hi" 3>&2
