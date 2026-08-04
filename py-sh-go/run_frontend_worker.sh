#!/usr/bin/env bash
# py-sh-go scoped worker.
# Loop: wait for low load (--wait, threshold 1.5x nproc), build, smoke-run,
# commit within scope (frontends/py-sh-go/ + harness/*). On build failure,
# invoke pi (opencode-go + deepseek-v4-flash, automatic key rotation); after
# 3 consecutive failures the worker is TRAPPED: it escalates to a core
# request (core-requests/) and SLEEPS until the estree worker removes the
# core-requests/sleeping-py-sh-go marker (the wake). Fix surface: this dir
# + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd .. && pwd)"
LOG="$WORKSPACE/loop-frontend-py-sh-go.log"
echo "[$(date +%FT%T)] frontend py-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  if ! git -C "$WORKSPACE" rev-parse --git-dir >/dev/null 2>&1; then
    sleep 300; continue
  fi
  changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
            | awk '/^.. /{print $2}' \
            | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
  if [ -n "$changes" ]; then
    bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
    echo "[$(date +%FT%T)] py-sh-go: build start" >> "$LOG"
    if make test >> "$LOG" 2>if make build >> "$LOG" 2>&1; then1; then
      fail_count=0
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend py-sh-go: build/fix" >> "$LOG" 2>&1 || true
    else
      fail_count=$((fail_count+1))
      echo "[$(date +%FT%T)] py-sh-go: build FAILED ($fail_count/3) -- invoking pi" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend py-sh-go >> "$LOG" 2>&1 || true
      if [ "$fail_count" -ge 3 ]; then
        echo "[$(date +%FT%T)] py-sh-go: TRAPPED -- escalating to core request and sleeping" >> "$LOG"
        # This blocks until the estree worker removes the sleeping marker.
        bash "$WORKSPACE/setup_backends.sh" --worker-trapped py-sh-go frontend >> "$LOG" 2>&1 || true
        fail_count=0
      fi
    fi
  fi
  sleep 300
done
