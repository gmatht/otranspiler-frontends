# t26_pipeline: the `|` pipe — the grammar's anonymous _pipeline_tail
# token INSIDE one pipeline_chain (a second command child; NOT the
# pipeline_chain_tail `&&`/`||` node, which lives BETWEEN chains — the
# t26 rung). The t08 text-pipe rung (PLAN_POWERSHELL_F.md §1: "a | b
# pipeline | bash pipe (TEXT)") has not landed; the pre-t26 frontend
# silently DROPPED the pipe's right-hand command (the chain loop
# returned on the first command, never seeing the anonymous `|` — a
# miscompile no pin exercised), now a loud REFUSE (refuse > guess).
Write-Output "a" | Write-Output "b"
