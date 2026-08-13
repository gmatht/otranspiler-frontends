@echo off
rem sync-refs.cmd - mirror the bat frontend's refs/ between the WSL
rem workspace and D:\Misc\refs with robocopy (drop-in for the old
rem `rsync -a --delete` step; /MIR keeps both sides exact).
rem
rem   sync-refs.cmd to-d     workspace refs\  -> D:\Misc\refs
rem   sync-refs.cmd from-d   D:\Misc\refs\ref -> workspace refs\ref
rem
rem Run from WSL (%~dp0 resolves to the \\wsl.localhost UNC path, which
rem robocopy handles):
rem   cmd.exe /c "\\wsl.localhost\Ubuntu2404\home\llm\sh2loop\frontends\bat-sh-go\sync-refs.cmd" to-d
set WS=%~dp0
if /i "%~1"=="to-d" (
    robocopy "%WS%refs" "D:\Misc\refs" /MIR /NFL /NDL /NJH /NJS /R:1 /W:1
    goto :done
)
if /i "%~1"=="from-d" (
    robocopy "D:\Misc\refs\ref" "%WS%refs\ref" /MIR /NFL /NDL /NJH /NJS /R:1 /W:1
    goto :done
)
echo usage: sync-refs.cmd to-d ^| from-d
exit /b 1

:done
rem robocopy's exit code is a bitmask: 0-7 = success, 8+ = failure
if %errorlevel% LEQ 7 exit /b 0
echo robocopy failed (exit %errorlevel%)
exit /b 1
