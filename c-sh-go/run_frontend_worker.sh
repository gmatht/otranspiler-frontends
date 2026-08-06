#!/usr/bin/env bash
# c-sh-go scoped worker — FAILURE-DRIVEN (mirror of py-sh-go's worker).
# Runs `make test` (ingress-acceptance + executed-stdout) EVERY iteration.
# Green -> commit scoped changes. Red -> invoke pi (deepseek-v4-turbo,
# automatic key rotation). After 3 consecutive failures, TRAP: escalate to
# a core request (core-requests/) and SLEEP until the estree worker
# removes core-requests/sleeping-c-sh-go.
# Fix surface: this dir + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-c-sh-go.log"
echo "[$(date +%FT%T)] frontend c-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] c-sh-go: gate run" >> "$LOG"
  if make test >> "$LOG" 2>&1; then
    fail_count=0
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\// || $0 ~ /^templates\// || $0 ~ /^frontends\/c-sh-go/ || true')
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend c-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] c-sh-go: gate GREEN" >> "$LOG"
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] c-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend c-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] c-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped c-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
