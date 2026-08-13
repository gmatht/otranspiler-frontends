#!/usr/bin/env bash
# cpp-sh-go scoped worker — FAILURE-DRIVEN (mirror of c-sh-go's worker).
# Runs `make test` (refusals + ingress-acceptance + executed-stdout +
# the C-INVARIANT — the c-sh-go corpus must stay green) EVERY iteration.
# Green -> commit scoped changes. Red -> invoke pi (deepseek-v4-turbo,
# automatic key rotation). After 3 consecutive failures, TRAP: escalate
# to a core request (core-requests/) and SLEEP until the estree worker
# removes core-requests/sleeping-cpp-sh-go.
# Fix surface: this dir + harness/* + c-requests/ (NEVER c-sh-go-owned
# files, NEVER the core). The c-sh-go worker implements c-requests.
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-cpp-sh-go.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend cpp-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  # WORK-STEALING: a leased slot is run by a desktop — yield until released
  if [ -f "$WORKSPACE/.leases/cpp-sh-go" ]; then
    echo "[$(date +%FT%T)] cpp-sh-go: leased to $(cut -d' ' -f1 "$WORKSPACE/.leases/cpp-sh-go") — yielding" >> "$LOG"
    sleep 300; continue
  fi
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  # SELF-HEAL: a stale root-owned cpp-sh-go-latest.txt (left by a
  # pre-single-owner gate run) makes run-gate.sh's report write fail
  # with "Permission denied" even when make test is green. Remove it
  # (and a stale running.txt from a crashed run) before each gate.
  rm -f "$WORKSPACE/gate-reports/cpp-sh-go-latest.txt" "$WORKSPACE/gate-reports/running.txt" 2>/dev/null || true
  echo "[$(date +%FT%T)] cpp-sh-go: gate run" >> "$LOG"
  if bash "$WORKSPACE/run-gate.sh" cpp-sh-go test >> "$LOG" 2>&1; then
    fail_count=0
    # scope = THIS dir + harness/* + c-requests/ — never c-sh-go-owned files
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\// || $0 ~ /^c-requests\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend cpp-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] cpp-sh-go: gate GREEN" >> "$LOG"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" cpp-sh-go "$LOG" >> "$LOG" 2>&1 || true
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] cpp-sh-go: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend cpp-sh-go >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] cpp-sh-go: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped cpp-sh-go frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
