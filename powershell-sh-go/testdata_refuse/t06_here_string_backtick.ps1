# t06_here_string_backtick: a backtick escape_character inside an
# expandable here-string — pinned REFUSE. Verified against live pwsh
# 7.6.4: `n inside @"…"@ is processed (a real newline), but the
# vendored runtime does not materialize backtick escapes as named
# children, so the byte-span reconstruction would emit them as literal
# text — a silent miscompile. The t10 rung lowers the here-string
# exactly like the double-quoted string; backtick text refuses (the
# t05 precedent: refuse > guess).
Write-Output @"
a`nb
"@
