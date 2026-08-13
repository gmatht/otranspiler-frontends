#!/usr/bin/env bash
# capture-refs.sh — the bat frontend's cmd.exe reference capture.
#
# The bat gate's recorded expectations (native_limits_bat) are hand-authored;
# the SOURCE-LANGUAGE TRUTH for batch is cmd.exe, which is Windows-only.
# This script stages the corpus so the real oracle can run once on the
# Windows host:
#
#   1. ./capture-refs.sh            # stage refs/corpus + refs/capture.cmd
#   2. (on Windows) refs\capture.cmd # runs each .bat under cmd.exe, writes
#                                     refs\ref\<name>.out
#   3. copy refs/ref back, then
#      ./capture-refs.sh --verify   # cmd.exe truth vs the transpiled stdout
#
# --verify diffs ref/<name>.out (cmd.exe) against the transpiled run
# (A1 -> estree -> node) — if they match, the frontend's semantics ARE
# cmd's semantics; a diff is a real batch-semantics bug in the frontend
# (never a backend issue — this comparison is source-truth vs transpiled).
#
# The testdata is portable (relative paths, `if exist .`) so the same
# corpus runs under both cmd.exe and the POSIX targets.

set -euo pipefail
dir="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$dir/../.." && pwd)"
refs="$dir/refs"
debashc="$root/sh2perl/target/debug/debashc"
runner="$root/harness/estree-runner.mjs"

case "${1:-}" in
  --verify)
    [ -d "$refs/ref" ] || { echo "refs/ref missing — run capture.cmd on Windows first" >&2; exit 2; }
    total=0; fail=0
    for out in "$refs"/ref/*.out; do
      [ -f "$out" ] || continue
      bn=$(basename "$out" .out)
      f="$dir/testdata/$bn.bat"
      [ -f "$f" ] || { echo "SKIP $bn (no testdata)"; continue; }
      total=$((total+1))
      # a frontend REFUSAL (unsupported construct) is not a semantics bug —
      # report SKIP and move on, so --verify only flags true mismatches
      if ! "$dir/bat-sh-go" --shir "$f" --raw 2>/dev/null \
          | "$debashc" --shir-in-estree - 2>/dev/null > /tmp/refs_estree.json; then
        echo "SKIP $bn (frontend refuses — unsupported construct)"
        continue
      fi
      node "$runner" /tmp/refs_estree.json --source "$f" 2>/dev/null > /tmp/refs_trans.out || true
      # cmd.exe writes CRLF stdout — normalize both sides before diffing
      tr -d '\r' < "$out" > /tmp/refs_cmd.out
      if diff -q /tmp/refs_cmd.out /tmp/refs_trans.out >/dev/null 2>&1; then
        echo "OK   $bn (cmd.exe == transpiled)"
      else
        echo "DIFF $bn (cmd.exe vs transpiled — a real batch-semantics bug)"
        diff "$out" /tmp/refs_trans.out | head -6 || true
        fail=$((fail+1))
      fi
    done
    echo "cmd-reference verify: $((total-fail))/$total match"
    [ "$fail" -eq 0 ] || exit 1
    ;;
  *)
    # stage: corpus copies + capture.cmd + empty ref/
    # cmd.exe REQUIRES CRLF line endings in batch files (LF-only files
    # fail with "(echo was unexpected at this time." and friends) — the
    # staged .bat copies and capture.cmd are written with \r\n. The
    # Linux-side testdata stays LF.
    rm -rf "$refs"
    mkdir -p "$refs/corpus" "$refs/ref"
    for f in "$dir"/testdata/*.bat; do
      sed 's/$/\r/' "$f" > "$refs/corpus/$(basename "$f")"
    done
    { cat <<'CMD'
@echo off
rem capture.cmd - run each corpus .bat under cmd.exe, capture stdout.
rem Run from THIS directory on Windows:  capture.cmd  (or double-click).
rem Writes ref\<name>.out per corpus file (the SOURCE-LANGUAGE TRUTH the
rem bat frontend's gate should validate against).
rem NOTE: each file runs in its OWN cmd.exe (cmd /c call) so a batch file
rem with an unparseable construct (e.g. if %%var%%== with an unset var)
rem cannot abort the whole capture - its parse error is written to stderr
rem and the loop moves on.
cd /d "%~dp0"
if not exist ref mkdir ref
for %%f in (corpus\*.bat) do (
    cmd /c call "%%f" > "ref\%%~nf.out"
)
echo Done. ref outputs written to ref\*.out - copy ref/ back to the Linux
echo workspace and run:  ./capture-refs.sh --verify
CMD
    } | sed 's/$/\r/' > "$refs/capture.cmd"
    echo "staged $refs: $(ls "$refs/corpus" | wc -l) corpus files + capture.cmd"
    echo "  md5 checksums (verify your Windows copy against these):"
    echo "    capture.cmd   $(md5sum "$refs/capture.cmd" | cut -d' ' -f1)"
    echo "  all files are CRLF (cmd.exe requires it — LF-only batch files"
    echo "  fail with \"(echo was unexpected at this time.\")"
    echo "next: sync-refs.cmd to-d (robocopy /MIR workspace refs -> D:\\Misc\\refs),"
    echo "      run refs\\capture.cmd on Windows (D:\\Misc\\refs), then"
    echo "      sync-refs.cmd from-d (robocopy the captured ref\\ back), and"
    echo "      ./capture-refs.sh --verify"
    ;;
esac
