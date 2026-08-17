#!/usr/bin/env bash
# py-sh-go scoped worker — FAILURE-DRIVEN.
#
# Runs `make test` (the byte-equality / ingress-acceptance progress
# signal) EVERY iteration, NOT just when there are uncommitted changes
# (the old change-driven loop idled forever on a clean-but-broken tree).
# Green -> commit any scoped changes. Red -> invoke pi (opencode-go +
# deepseek-v4-flash, automatic key rotation); after 3 consecutive
# failures, TRAP: escalate by TYPE (PLAN §11 / core-requests/README.md): a NEW
# shIR node -> a contract-gen SPEC (core-requests/contracts/<node>.json); a
# NEW transform -> an .rs offer with the §11.4 manifest
# (core-requests/transforms/offered/<name>.rs); only a genuine shared-analysis
# bug -> an .md request (core-requests/<lang>-<ts>.md). NEVER a blind
# 'inspect my log' escalation — diagnose first, then file the concrete
# artifact. Then SLEEP
# until the estree worker removes core-requests/sleeping-py-sh-go.
# Fix surface: this dir + harness/* (NEVER the core src/*).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-py-sh-go.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend py-sh-go worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] py-sh-go: gate run" >> "$LOG"
  if make test >> "$LOG" 2>&1; then
    fail_count=0
    # commit any scoped changes the fix may have made (this dir + harness/*).
    # `git status` paths are repo-relative, so match the frontend dir as a
    # RELATIVE prefix (the old absolute-path match never fired — fixes
    # piled up uncommitted until a stray `git stash` wiped them).
    # NOTE: ${PWD#...} (uppercase) — `pwd` is a builtin, not a variable;
    # the old ${pwd#...} was an unbound-variable error under set -u, so
    # the filter silently never matched and gate-green fixes piled up
    # uncommitted (2026-08-13 triage takeover found them still pending).
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend py-sh-go: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] py-sh-go: gate GREEN" >> "$LOG"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" py-sh-go "$LOG" >> "$LOG" 2>&1 || true
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
