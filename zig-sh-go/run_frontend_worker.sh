#!/usr/bin/env bash
# zig-sh-go scoped worker — FAILURE-DRIVEN (mirror of cpp-sh-go's).
# Runs `make test` (refusals + ingress acceptance + executed-stdout vs
# native `zig run`) EVERY iteration. Green -> commit scoped changes.
# Red -> invoke pi (deepseek-v4-flash, automatic key rotation) to
# implement the next PLAN_ZIG_F.md construct. After 3 consecutive
# failures, TRAP: escalate to a core request and SLEEP until the estree
# worker removes core-requests/sleeping-zig-sh-go.
# Fix surface: this dir + harness/* (NEVER the core).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-zig-sh-go.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend zig-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  # WORK-STEALING: a leased slot is run by a desktop — yield until released
  if [ -f "$WORKSPACE/.leases/zig-sh-go" ]; then
    echo "[$(date +%FT%T)] zig-sh-go: leased to $(cut -d' ' -f1 "$WORKSPACE/.leases/zig-sh-go") — yielding" >> "$LOG"
    sleep 300; continue
  fi
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] zig-sh-go: gate run" >> "$LOG"
  if bash "$WORKSPACE/run-gate.sh" zig-sh-go test >> "$LOG" 2>&1; then
    fail_count=0
    # scope = THIS dir + harness/*
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend zig-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] zig-sh-go: gate GREEN" >> "$LOG"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" zig-sh-go "$LOG" >> "$LOG" 2>&1 || true
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] zig-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend zig-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] zig-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped zig-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
