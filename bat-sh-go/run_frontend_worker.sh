#!/usr/bin/env bash
# bat-sh-go frontend worker — runs in THIS dir, scope = frontends/bat-sh-go/ +
# harness/* (shared test infra). Does NOT touch the core.
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="/home/llm/sh2loop"
LOG="$WORKSPACE/loop-frontend-bat-sh-go.log"
echo "[$(date +%FT%T)] frontend bat-sh-go worker started (pid=$$)" >> "$LOG"
while true; do
  if [ -f "$WORKSPACE/.leases/bat-sh-go" ]; then
    echo "[$(date +%FT%T)] bat-sh-go: leased — yielding" >> "$LOG"
    sleep 300; continue
  fi
  changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
            | awk '/^.. /{print $2}' \
            | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\//' || true)
  if [ -n "$changes" ]; then
    bash "$WORKSPACE/setup_backends.sh" --wait 2>>"$LOG" || true
    if make build >> "$LOG" 2>&1 && make test >> "$LOG" 2>&1; then
      git -C "$WORKSPACE" add $changes 2>>"$LOG" || true
      git -C "$WORKSPACE" commit -m "frontend bat-sh-go: build/fix" 2>>"$LOG" || true
      # parser-node coverage mode: green gate + uncovered nodes -> new example
      bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" bat-sh-go "$LOG" >> "$LOG" 2>&1 || true
    fi
  fi
  sleep 300
done
