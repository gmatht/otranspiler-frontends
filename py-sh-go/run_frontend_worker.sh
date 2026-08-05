#!/usr/bin/env bash
# py-sh-go scoped worker — FAILURE-DRIVEN.
#
# Runs `make test` (the byte-equality / ingress-acceptance progress
# signal) EVERY iteration, NOT just when there are uncommitted changes
# (the old change-driven loop idled forever on a clean-but-broken tree).
# Green -> commit any scoped changes. Red -> invoke pi (opencode-go +
# deepseek-v4-flash, automatic key rotation); after 3 consecutive
# failures, TRAP: escalate to a core request (core-requests/) and SLEEP
# until the estree worker removes core-requests/sleeping-py-sh-go.
# Fix surface: this dir + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd .. && pwd)"
LOG="$WORKSPACE/loop-frontend-py-sh-go.log"
echo "[$(date +%FT%T)] frontend py-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] py-sh-go: gate run" >> "$LOG"
  if make test >> "$LOG" 2>&1; then
    fail_count=0
    # commit any scoped changes the fix may have made (this dir + harness/*)
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend py-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] py-sh-go: gate GREEN" >> "$LOG"
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] py-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend py-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] py-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped py-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
