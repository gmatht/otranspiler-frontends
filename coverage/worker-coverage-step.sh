#!/bin/bash
# worker-coverage-step.sh <lang> <logfile> — the frontend workers'
# coverage-improvement hook. Called by a worker AFTER its gate went
# green (tests result in identical output).
#
# If the frontend's parser features are not fully covered by its testdata
# examples, ask pi (scoped) to create ONE testdata example covering an
# uncovered aspect. Gap source chain, EXTERNAL-GRAMMAR truth FIRST:
#   1. rules-gap.sh        — grammars-v4 rules (go/c/py) / POSIX subset
#                            (posix-sh) / PPI classes (perl);
#   2. ts-node-gap.sh      — tree-sitter NODE-TYPE coverage for the
#                            tree-sitter-backed languages (c/cpp/powershell:
#                            node types the grammar defines minus the node
#                            types the testdata parse trees exercise);
#   3. coverage-gap.sh     — the A1-node proxy / syn node kinds, only where
#                            no external grammar exists (zsh/fish/bat/zig)
#                            or it reports none.
# Commit only if the gate stays green WITH the new example; otherwise
# discard the example and record the construct as known-refused (so future
# cycles skip it) or as a lowering bug. The gate is the arbiter: an example
# that refuses by design must never land.
set -u
lang="$1"; LOG="$2"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
TS="[$(date +%FT%T)]"

# gap source — EXTERNAL-GRAMMAR rule gaps first (the official parsers'
# features: grammars-v4 rules for go/c/py, the custom POSIX subset for
# posix-sh, PPI node classes for perl — see PARSER_GAPS.md); then the
# tree-sitter NODE-TYPE gaps for the tree-sitter-backed languages
# (c/cpp/powershell); fall back to the A1-node proxy / syn node kinds
# (coverage-gap.sh) where no external grammar exists (zsh/fish/bat/zig)
# or it reports none.
gaps=$(bash "$ROOT/frontends/coverage/rules-gap.sh" "$lang" 2>/dev/null)
if [ -z "$gaps" ]; then
  gaps=$(bash "$ROOT/frontends/coverage/ts-node-gap.sh" "$lang" 2>/dev/null)
fi
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
ls "$ROOT/core-requests"/*.md 2>/dev/null | sort > "$T/core_before.txt" || true
# prune core-pending entries whose request was completed (moved to
# core-requests/done/ or deleted by the estree worker) — the gap is then
# retried (the contract now has what the construct needed).
if [ -f "$ROOT/frontends/coverage/core-pending-$lang.txt" ]; then
  : > "$T/pending.new"
  while IFS=$'\t' read -r g req; do
    [ -n "$g" ] || continue
    if [ -f "$ROOT/core-requests/$req" ]; then
      printf '%s\t%s\n' "$g" "$req" >> "$T/pending.new"
    else
      echo "$TS coverage[$lang]: core-request $req completed — gap '$g' retried" >> "$LOG"
    fi
  done < "$ROOT/frontends/coverage/core-pending-$lang.txt"
  mv "$T/pending.new" "$ROOT/frontends/coverage/core-pending-$lang.txt"
fi

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
  ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/after.txt"
  newfiles=$(comm -13 "$T/before.txt" "$T/after.txt")
  if [ -z "$newfiles" ]; then
    # pi created nothing. rc==0 is either a by-design refusal or a
    # core-request escalation (the prompt tells pi which — a new
    # core-requests/<lang>-*.md request); rc!=0 = pi failure — retry next.
    if [ "$pi_rc" -eq 0 ]; then
      ls "$ROOT/core-requests"/*.md 2>/dev/null | sort > "$T/core_after.txt" || true
      newreq=$(comm -13 "$T/core_before.txt" "$T/core_after.txt")
      if [ -n "$newreq" ]; then
        for req in $newreq; do
          printf '%s\t%s\n' "$gap" "$(basename "$req")" >> "$ROOT/frontends/coverage/core-pending-$lang.txt"
        done
        echo "$TS coverage[$lang]: '$gap' is an A1-contract gap — escalated to core-requests ($(echo $newreq | xargs -n1 basename | tr '\n' ' ')) — skipped while pending" >> "$LOG"
      else
        printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/refused-$lang.txt"
        echo "$TS coverage[$lang]: no example for '$gap' (pi judged it not expressible) — marked known-refused" >> "$LOG"
      fi
    else
      echo "$TS coverage[$lang]: no example for '$gap' (pi rc=$pi_rc) — will retry next cycle" >> "$LOG"
    fi
  else
    changes=$(git -C "$ROOT" status --porcelain 2>/dev/null \
              | awk '/^.. /{print $2}' \
              | awk -v d="frontends/$lang" '$1 ~ "^"d || $1 ~ /^harness\//' || true)
    if [ -n "$changes" ]; then
      git -C "$ROOT" add $changes 2>/dev/null || true
      git -C "$ROOT" commit -m "frontend $lang: coverage example for '$gap'" >> "$LOG" 2>&1 || true
    fi
    echo "$TS coverage[$lang]: coverage example for '$gap' committed (gate green)" >> "$LOG"
  fi
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
    # no new file was created. If pi exited 0 it either followed the
    # prompt's refusal path (not expressible — record so the next cycle
    # moves on) or escalated an A1-contract gap to core-requests (a new
    # <lang>-*.md request — pending, not refused). A nonzero rc is a pi
    # failure, not a judgment — retry.
    if [ "$pi_rc" -eq 0 ]; then
      ls "$ROOT/core-requests"/*.md 2>/dev/null | sort > "$T/core_after.txt" || true
      newreq=$(comm -13 "$T/core_before.txt" "$T/core_after.txt")
      if [ -n "$newreq" ]; then
        for req in $newreq; do
          printf '%s\t%s\n' "$gap" "$(basename "$req")" >> "$ROOT/frontends/coverage/core-pending-$lang.txt"
        done
        echo "$TS coverage[$lang]: '$gap' is an A1-contract gap — escalated to core-requests — skipped while pending" >> "$LOG"
      else
        printf '%s\n' "$gap" >> "$ROOT/frontends/coverage/refused-$lang.txt"
        echo "$TS coverage[$lang]: no example for '$gap' (pi judged it not expressible) — marked known-refused" >> "$LOG"
      fi
    else
      echo "$TS coverage[$lang]: no example for '$gap' (pi rc=$pi_rc) — will retry next cycle" >> "$LOG"
    fi
  fi
fi
exit 0
