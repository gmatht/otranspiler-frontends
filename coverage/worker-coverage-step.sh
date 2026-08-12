#!/bin/bash
# worker-coverage-step.sh <lang> <logfile> — the frontend workers'
# coverage-improvement hook. Called by a worker AFTER its gate went
# green (tests result in identical output).
#
# If the frontend's parser features are not fully covered by its testdata
# examples, ask pi (scoped) to create ONE testdata example covering an
# uncovered aspect. Gap source: the EXTERNAL-GRAMMAR rule gaps first
# (rules-gap.sh — grammars-v4 rules / POSIX subset / PPI classes; the
# official parsers are the source of truth for hand-rolled frontends),
# falling back to the A1-node proxy / syn node kinds (coverage-gap.sh)
# where no external grammar exists. Commit only if the gate stays green
# WITH the new example; otherwise discard the example and record the
# construct as known-refused (so future cycles skip it) or as a lowering
# bug. The gate is the arbiter: an example that refuses by design must
# never land.
set -u
lang="$1"; LOG="$2"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
TS="[$(date +%FT%T)]"

# gap source — EXTERNAL-GRAMMAR rule gaps first (the official parsers'
# features: grammars-v4 rules for go/c/py, the custom POSIX subset for
# posix-sh, PPI node classes for perl — see PARSER_GAPS.md); fall back to
# the A1-node proxy / syn node kinds (coverage-gap.sh) where no external
# grammar exists (zsh/fish/bat/zig/powershell/cpp) or it reports none.
gaps=$(bash "$ROOT/frontends/coverage/rules-gap.sh" "$lang" 2>/dev/null)
if [ -z "$gaps" ]; then
  gaps=$(bash "$ROOT/frontends/coverage/coverage-gap.sh" "$lang" 2>/dev/null)
fi
if [ -z "$gaps" ]; then
  echo "$TS coverage[$lang]: all known parser nodes covered (or no external grammar / inventory) — nothing to add" >> "$LOG"
  exit 0
fi
gap=$(printf '%s\n' "$gaps" | head -1)
echo "$TS coverage[$lang]: gate green but parser construct '$gap' uncovered — asking pi for a coverage example" >> "$LOG"

# remember which testdata files exist before pi runs (the reject path
# discards ONLY files pi adds — never git clean, which would destroy an
# untracked testdata dir)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/before.txt"

# pi creates ONE example in frontends/<lang>/testdata/ (scoped prompt).
# rc==0 + no new file = pi deliberately refused (per the prompt: "if the
# frontend REFUSES this construct by design, do NOT create the example;
# exit 0") — ledger that so the gap stops being retried. rc!=0 (e.g. a
# model/network error) must NOT be ledgered — the gap is retried next
# cycle.
bash "$ROOT/setup_backends.sh" --pi-coverage-example "$lang" "$gap" >> "$LOG" 2>&1
pi_rc=$?

if (cd "$ROOT/frontends/$lang" && make test > "$T/gate.log" 2>&1); then
  # the new example keeps the gate green — commit it
  cat "$T/gate.log" >> "$LOG"
  changes=$(git -C "$ROOT" status --porcelain 2>/dev/null \
            | awk '/^.. /{print $2}' \
            | awk -v d="frontends/$lang" '$2 ~ "^"d || $2 ~ /^harness\//' || true)
  if [ -n "$changes" ]; then
    git -C "$ROOT" add $changes 2>/dev/null || true
    git -C "$ROOT" commit -m "frontend $lang: coverage example for '$gap'" >> "$LOG" 2>&1 || true
  fi
  echo "$TS coverage[$lang]: coverage example for '$gap' committed (gate green)" >> "$LOG"
else
  cat "$T/gate.log" >> "$LOG"
  # Discard ONLY what pi added (files not present before), revert tracked
  # edits to the testdata dir.
  ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/after.txt"
  newfiles=$(comm -13 "$T/before.txt" "$T/after.txt")
  for bn in $newfiles; do
    rm -f "$ROOT/frontends/$lang/testdata/$bn"
  done
  git -C "$ROOT" checkout -- "frontends/$lang/testdata" 2>/dev/null || true
  # Classify the rejection: a FRONTEND-EMIT failure on the new example is a
  # by-design refusal (remember it so future cycles skip it); a stdout
  # MISMATCH is a LOWERING BUG in the frontend (never mark refused — record
  # it as a bug for the worker to fix).
  if printf '%s\n' $newfiles | grep -q .; then
    if grep -q "(frontend emit)" "$T/gate.log"; then
      printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/refused-$lang.txt"
      echo "$TS coverage[$lang]: example for '$gap' REFUSED (by design) — discarded, marked known-refused" >> "$LOG"
    else
      printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/bugs-$lang.txt"
      echo "$TS coverage[$lang]: example for '$gap' FAILED THE ORACLE — a frontend LOWERING BUG, recorded in bugs-$lang.txt" >> "$LOG"
    fi
  else
    # no new file was created. If pi exited 0 it followed the prompt's
    # refusal path (the construct is not expressible) — record it so the
    # next cycle moves to the NEXT gap instead of retrying this one
    # forever. A nonzero rc is a pi failure, not a judgment — retry.
    if [ "$pi_rc" -eq 0 ]; then
      printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/refused-$lang.txt"
      echo "$TS coverage[$lang]: no example for '$gap' (pi judged it not expressible) — marked known-refused" >> "$LOG"
    else
      echo "$TS coverage[$lang]: no example for '$gap' (pi rc=$pi_rc) — will retry next cycle" >> "$LOG"
    fi
  fi
fi
exit 0
