#!/usr/bin/env bash
# go-sh scoped worker — FAILURE-DRIVEN (TRANSLATE_ONE_APPLICATION playbook:
# source-frontend worker for the Go→JS dog-food).
#   gate    = make test (A1-ingress + frontend-stdout oracle) AND
#             fail-go --gate (the standing Go→JS end-to-end gate over the
#             corpus ladder; --rust verdicts and --app integration on the
#             same oracle)
#   red     → setup_backends.sh --pi-fix-frontend go-sh (cheap fast agent,
#             scoped prompt, xhigh thinking)
#   3 reds  → --worker-trapped go-sh frontend (structured core request) +
#             sleep; never bless a regression (the whole gate re-runs
#             before any commit)
#   commit  = frontends/go-sh/ + harness/ + fail-go + templates/go/ only.
set -euo pipefail
cd "$(dirname "$0")"
WORKSPACE="$(cd ../.. && pwd)"
LOG="$WORKSPACE/loop-frontend-go-sh.log"
# Worker cgroup enrollment (harness/WorkerPool.pm): this frontend worker
# runs inside sh2workers; best-effort (unprivileged WSL -> cooperative).
perl "$WORKSPACE/harness/WorkerPool.pm" --enter-worker $$ >> "$LOG" 2>&1 || true
echo "[$(date +%FT%T)] frontend go-sh worker started (pid=$$)" >> "$LOG"
fail_count=0
while true; do
  bash "$WORKSPACE/setup_backends.sh" --wait >> "$LOG" 2>&1 || true
  # /tmp hygiene: the gate and go build die with ENOSPC when scratch from
  # other loops accumulates (rustgate_* checkouts, pi output captures).
  # Prune aggressively-but-safely before every gate run (non-fatal).
  rm -rf /tmp/rustgate_* /tmp/zshlog.bin 2>/dev/null || true
  find /tmp -maxdepth 1 -name 'pi-bash-*.log' -mtime +1 -delete 2>/dev/null || true
  echo "[$(date +%FT%T)] go-sh: gate run" >> "$LOG"
  if make test >> "$LOG" 2>&1 && bash "$WORKSPACE/fail-go" --gate >> "$LOG" 2>&1; then
    fail_count=0
    changes=$(git -C "$WORKSPACE" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="${PWD#$WORKSPACE/}" '$0 ~ "^"d"/" || $0 ~ /^harness\// || $0 ~ /^fail-go$/ || $0 ~ /^templates\/go\//' || true)
    if [ -n "$changes" ]; then
      git -C "$WORKSPACE" add $changes 2>/dev/null || true
      git -C "$WORKSPACE" commit -m "frontend go-sh: gate green" >> "$LOG" 2>&1 || true
    fi
    echo "[$(date +%FT%T)] go-sh: gate GREEN (make test + fail-go)" >> "$LOG"
    mkdir -p "$WORKSPACE/gate-reports"
    printf '%s frontend-go-sh: gate GREEN (make test + fail-go)\n' "$(date +%FT%T)" >> "$WORKSPACE/gate-reports/frontend-go-sh.report"
    bash "$WORKSPACE/frontends/coverage/worker-coverage-step.sh" go-sh "$LOG" >> "$LOG" 2>&1 || true
  else
    fail_count=$((fail_count+1))
    echo "[$(date +%FT%T)] go-sh: gate FAILED ($fail_count/3) — invoking pi" >> "$LOG"
    mkdir -p "$WORKSPACE/gate-reports"
    printf '%s frontend-go-sh: gate RED (make test + fail-go)\n' "$(date +%FT%T)" >> "$WORKSPACE/gate-reports/frontend-go-sh.report"
    bash "$WORKSPACE/setup_backends.sh" --pi-fix-frontend go-sh >> "$LOG" 2>&1 || true
    if [ "$fail_count" -ge 3 ]; then
      echo "[$(date +%FT%T)] go-sh: TRAPPED — escalating to core request and sleeping" >> "$LOG"
      bash "$WORKSPACE/setup_backends.sh" --worker-trapped go-sh frontend >> "$LOG" 2>&1 || true
      fail_count=0
    fi
  fi
  sleep 300
done
