#!/usr/bin/env bash
# py-sh frontend worker — runs in THIS dir, scope = frontends/py-sh/ +
# harness/* (shared test infra). Does NOT touch the core
# (src/shir.rs, src/ir.rs, src/estree.rs, src/parser/) — those are
# single-owner (the estree worker during the lowering phase).
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="/nvme/ai/sh2loop"
LOG="/nvme/ai/sh2loop/loop-frontend-py-sh.log"
echo "[$(date +%FT%T)] frontend py-sh worker started (pid=$$, scope=/nvme/ai/sh2loop/frontends/py-sh)" >> "$LOG"
while true; do
  # light ops (no load gate): git status, scope check
  changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null             | awk '/^.. /{print $2}'             | awk -v d="/nvme/ai/sh2loop/frontends/py-sh" '$0 ~ "^"d || $0 ~ /^harness\//'             || true)
  if [ -n "$changes" ]; then
    # heavy op (gated): build
    if bash "$WORKSPACE/setup_backends.sh" --noop-gated build frontend py-sh 2>>"$LOG"; then
      git -C "$WORKSPACE" add $changes 2>>"$LOG" || true
      git -C "$WORKSPACE" commit -m "frontend py-sh: build/fix" 2>>"$LOG" || true
    fi
  fi
  sleep 300
done
