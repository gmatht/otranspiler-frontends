#!/usr/bin/env bash
# powershell-sh-go scoped worker — FAILURE-DRIVEN (mirror of c-sh-go's).
# Runs `make test` (refusals + ingress acceptance + executed-stdout vs
# the RECORDED expectations) EVERY iteration. Green -> commit scoped
# changes. Red -> invoke pi (deepseek-v4-flash, automatic key rotation)
# to implement the next PLAN_POWERSHELL_F.md construct. After 3
# consecutive failures, TRAP: escalate to a core request and SLEEP until
# the estree worker removes core-requests/sleeping-powershell-sh-go.
# Fix surface: this dir + harness/* (NEVER the core).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-powershell-sh-go.log"
echo "[$(date +%FT%T)] frontend powershell-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  # WORK-STEALING: a leased slot is run by a desktop — yield until released
  if [ -f "$WORKSPACE/.leases/powershell-sh-go" ]; then
    echo "[$(date +%FT%T)] powershell-sh-go: leased to $(cut -d' ' -f1 "$WORKSPACE/.leases/powershell-sh-go") — yielding" >> "$LOG"
    sleep 300; continue
  fi
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] powershell-sh-go: gate run" >> "$LOG"
  if bash "$WORKSPACE/run-gate.sh" powershell-sh-go test >> "$LOG" 2>&1; then
    fail_count=0
    # scope = THIS dir + harness/*
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend powershell-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] powershell-sh-go: gate GREEN" >> "$LOG"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" powershell-sh-go "$LOG" >> "$LOG" 2>&1 || true
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] powershell-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend powershell-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] powershell-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped powershell-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
