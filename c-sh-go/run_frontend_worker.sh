#!/usr/bin/env bash
# c-sh-go scoped worker — FAILURE-DRIVEN (mirror of py-sh-go's worker).
# Runs `make test` (ingress-acceptance + executed-stdout) EVERY iteration.
# Green -> commit scoped changes. Red -> invoke pi (deepseek-v4-turbo,
# automatic key rotation). After 3 consecutive failures, TRAP: escalate by TYPE (PLAN §11 /
# core-requests/README.md): a NEW shIR node -> a contract-gen SPEC
# (core-requests/contracts/<node>.json); a NEW transform -> an .rs offer
# with the §11.4 manifest (core-requests/transforms/offered/<name>.rs);
# only a genuine shared-analysis bug -> an .md request
# (core-requests/<lang>-<ts>.md). NEVER a blind 'inspect my log'
# escalation — diagnose first, then file the concrete artifact. Then
# SLEEP until the estree worker
# removes core-requests/sleeping-c-sh-go.
# Fix surface: this dir + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-c-sh-go.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend c-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] c-sh-go: gate run" >> "$LOG"
  # c-requests first: implement pending cpp-sh-go -> shared-lowering
  # requests (CPP_PLAN §4). pi runs scoped to this dir; the gate below
  # validates, and on green the requests move to c-requests/done/.
  pending=$(ls "$WORKSPACE"/c-requests/*.md 2>/dev/null | grep -v '/done/' || true)
  if [ -n "$pending" ]; then
    echo "[$(date +%FT%T)] c-sh-go: implementing c-requests ($(echo "$pending" | wc -l) pending)" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-c-requests >> "$LOG" 2>&1 || true
  fi
  if bash "$WORKSPACE/run-gate.sh" c-sh-go test >> "$LOG" 2>&1; then
    fail_count=0
    # on green, close implemented requests (the cpp worker re-validates
    # its own corpus on its next cycle)
    for r in "$WORKSPACE"/c-requests/*.md; do
      [ -f "$r" ] && mv "$r" "$WORKSPACE/c-requests/done/" 2>/dev/null || true
    done
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\// || $0 ~ /^templates\// || $0 ~ /^frontends\/c-sh-go/ || $0 ~ /^c-requests\//')
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend c-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] c-sh-go: gate GREEN" >> "$LOG"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" c-sh-go "$LOG" >> "$LOG" 2>&1 || true
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
