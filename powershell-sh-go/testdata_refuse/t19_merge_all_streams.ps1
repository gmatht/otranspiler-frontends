# t19_merge_all_streams: the `*>&1` member of the t19
# merging_redirection_operator rung — pwsh's ALL-streams merge into
# the success stream (live pwsh 7.6.4 accepts it: `Write-Output "hi"
# *>&1` prints hi). It has NO numeric fd, and the A1 IrRedirect.fd is
# an int — the contract lacks the "all streams" fd (the frontend could
# express it if shIR had an all-streams marker; a core request would
# be needed) — refuse > guess, loudly.
Write-Output "hi" *>&1
