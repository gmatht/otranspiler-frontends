#!/usr/bin/env bash
# rust-frontend scoped worker — FAILURE-DRIVEN + parser-node coverage mode.
# Gate: make test (refusal pins + ingress acceptance + executed-stdout
# vs native rustc). On green, worker-coverage-step.sh grows testdata
# toward full syn-kind coverage (frontends/coverage/syn-coverage/).
# Scope = frontends/rust-frontend/ + harness/* (never the core).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-rust-frontend.log"
# HARD-SERIALIZE the heavy build+test phase against every other worker
# on this box. Backend workers already flock $WORKSPACE/.gate.lock
# around --backend-gate (commit 7edd76e: N concurrent cargo builds
# thrashed the hub to load 67 / RAM-exhausted OOM kills). Frontend
# gates only did the --wait pre-check, so this gate could still race
# another worker's cargo build and lose exactly one native rustc
# compile to a timeout/OOM (2026-08-12 14:49: t01 native side empty on
# both attempts, transpiled side correct, 19/20 other tests green).
# Same lock, same one-at-a-time heavy phase; --wait stays as the
# pre-gate load check.
run_gated() { ( flock 9; "$@" ) 9>"$WORKSPACE/.gate.lock"; }
echo "[$(date +%FT%T)] frontend rust-frontend worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  echo "[$(date +%FT%T)] rust-frontend: gate run" >> "$LOG"
  if run_gated make test >> "$LOG" 2>&1; then
    fail_count=0
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="$(pwd)" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend rust-frontend: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] rust-frontend: gate GREEN" >> "$LOG"
    # parser-node coverage mode: if syn kinds are uncovered, create an example
    run_gated bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" rust-frontend "$LOG" >> "$LOG" 2>&1 || true
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] rust-frontend: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend rust-frontend >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] rust-frontend: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped rust-frontend frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
