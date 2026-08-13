#!/usr/bin/env bash
# perl-sh-go scoped worker — FAILURE-DRIVEN.
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-perl-sh-go.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend perl-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] perl-sh-go: gate run" >> "$LOG"
  if make test >> "$LOG" 2>&1; then
    fail_count=0
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend perl-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] perl-sh-go: gate GREEN" >> "$LOG"
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] perl-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend perl-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] perl-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped perl-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
