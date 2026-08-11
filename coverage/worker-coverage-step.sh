#!/bin/bash
# worker-coverage-step.sh <lang> <logfile> — the frontend workers'
# coverage-improvement hook. Called by a worker AFTER its gate went
# green (tests result in identical output).
#
# If the frontend's parser node types are not fully covered by its
# testdata examples (coverage-gap.sh reports gaps), ask pi (scoped) to
# create ONE testdata example covering an uncovered aspect. Commit only
# if the gate stays green WITH the new example; otherwise discard the
# example and record the construct as known-refused (so future cycles
# skip it). The gate is the arbiter: an example that refuses by design
# must never land.
set -u
lang="$1"; LOG="$2"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
TS="[$(date +%FT%T)]"

gaps=$(bash "$ROOT/frontends/coverage/coverage-gap.sh" "$lang" 2>/dev/null)
if [ -z "$gaps" ]; then
  echo "$TS coverage[$lang]: all known parser nodes covered (or no inventory) — nothing to add" >> "$LOG"
  exit 0
fi
gap=$(printf '%s\n' "$gaps" | head -1)
echo "$TS coverage[$lang]: gate green but parser node '$gap' uncovered — asking pi for a coverage example" >> "$LOG"

# remember which testdata files exist before pi runs (the reject path
# discards ONLY files pi adds — never git clean, which would destroy an
# untracked testdata dir)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/before.txt"

# pi creates ONE example in frontends/<lang>/testdata/ (scoped prompt).
bash "$ROOT/setup_backends.sh" --pi-coverage-example "$lang" "$gap" >> "$LOG" 2>&1 || true

if (cd "$ROOT/frontends/$lang" && make test >> "$LOG" 2>&1); then
  # the new example keeps the gate green — commit it
  changes=$(git -C "$ROOT" status --porcelain 2>/dev/null \
            | awk '/^.. /{print $2}' \
            | awk -v d="$ROOT/frontends/$lang" '$0 ~ "^"d || $0 ~ /^harness\//' || true)
  if [ -n "$changes" ]; then
    git -C "$ROOT" add $changes 2>/dev/null || true
    git -C "$ROOT" commit -m "frontend $lang: coverage example for '$gap'" >> "$LOG" 2>&1 || true
  fi
  echo "$TS coverage[$lang]: coverage example for '$gap' committed (gate green)" >> "$LOG"
else
  # rejected: the construct refuses by design or the example is wrong.
  # Discard ONLY what pi added (files not present before), revert tracked
  # edits to the testdata dir, remember the gap as known-refused.
  ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/after.txt"
  comm -13 "$T/before.txt" "$T/after.txt" | while IFS= read -r bn; do
    rm -f "$ROOT/frontends/$lang/testdata/$bn"
  done
  git -C "$ROOT" checkout -- "frontends/$lang/testdata" 2>/dev/null || true
  printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/refused-$lang.txt"
  echo "$TS coverage[$lang]: example for '$gap' REJECTED (gate red) — discarded, marked known-refused" >> "$LOG"
fi
exit 0
