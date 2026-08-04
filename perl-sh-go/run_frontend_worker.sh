#!/usr/bin/env bash
# perl-sh-go scoped worker.
#
# Loop: wait for low load (setup_backends.sh --wait, threshold 1.5x nproc,
# 60s poll), build, smoke-run the frontend, commit within scope
# (frontends/perl-sh-go/ + harness/*) if there are changes, and on
# build/test failure invoke `pi` (opencode-go + deepseek-v4-flash,
# automatic key rotation) scoped to this frontend's dir + harness/*.
#
# Fix surface: this dir + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd .. && pwd)"
LOG="$WORKSPACE/loop-frontend-perl-sh-go.log"
echo "[$(date +%FT%T)] frontend perl-sh-go worker started (pid=$$)" >> "$LOG"
while true; do
  # light: git status / scope check
  if ! git -C "$WORKSPACE" rev-parse --git-dir >/dev/null 2>&1; then
    sleep 300; continue
  fi
  changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
            | awk '/^.. /{print $2}' \
            | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
  if [ -n "$changes" ]; then
    # heavy: wait for low load, then build
    bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
    echo "[$(date +%FT%T)] perl-sh-go: build start" >> "$LOG"
    if make test >> "$LOG" 2>if make build >> "$LOG" 2>&1; then1; then
      # commit within scope
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend perl-sh-go: build/fix" >> "$LOG" 2>&1 || true
    else
      # build FAIL: invoke pi (deepseek-v4-flash, scoped)
      echo "[$(date +%FT%T)] perl-sh-go: build FAILED -- invoking pi" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend perl-sh-go >> "$LOG" 2>&1 || true
    fi
  fi
  sleep 300
done
