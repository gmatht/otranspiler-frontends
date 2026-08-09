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
      "$dir/bat-sh-go" --shir "$f" --raw 2>/dev/null \
        | "$debashc" --shir-in-estree - 2>/dev/null > /tmp/refs_estree.json
      node "$runner" /tmp/refs_estree.json --source "$f" 2>/dev/null > /tmp/refs_trans.out || true
      if diff -q "$out" /tmp/refs_trans.out >/dev/null 2>&1; then
        echo "OK   $bn (cmd.exe == transpiled)"
      else
        echo "DIFF $bn (cmd.exe vs transpiled — a real batch-semantics bug)"
        diff "$out" /tmp/refs_trans.out | head -6
        fail=$((fail+1))
      fi
    done
    echo "cmd-reference verify: $((total-fail))/$total match"
    [ "$fail" -eq 0 ] || exit 1
    ;;
  *)
    # stage: corpus copies + capture.cmd + empty ref/
    rm -rf "$refs"
    mkdir -p "$refs/corpus" "$refs/ref"
    cp "$dir"/testdata/*.bat "$refs/corpus/"
    cat > "$refs/capture.cmd" <<'CMD'
@echo off
rem capture.cmd - run each corpus .bat under cmd.exe, capture stdout.
rem Run from THIS directory on Windows:  capture.cmd  (or double-click).
rem Writes ref\<name>.out per corpus file (the SOURCE-LANGUAGE TRUTH the
rem bat frontend's gate should validate against).
cd /d "%~dp0"
if not exist ref mkdir ref
for %%f in (corpus\*.bat) do (
    call "%%f" > "ref\%%~nf.out"
)
echo Done. ref outputs written to ref\*.out - copy ref/ back to the Linux
echo workspace and run:  ./capture-refs.sh --verify
CMD
    echo "staged $refs: $(ls "$refs/corpus" | wc -l) corpus files + capture.cmd"
    echo "next: copy refs/ to Windows, run refs\\capture.cmd, copy ref/ back,"
    echo "      then: ./capture-refs.sh --verify"
    ;;
esac
