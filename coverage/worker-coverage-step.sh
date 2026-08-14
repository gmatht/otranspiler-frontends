#!/bin/bash
# worker-coverage-step.sh <lang> <logfile> — the frontend workers'
# coverage-improvement hook. Called by a worker AFTER its gate went
# green (tests result in identical output).
#
# If the frontend's parser features are not fully covered by its testdata
# examples, ask pi (scoped) to create ONE testdata example covering an
# uncovered aspect. Gap source chain, EXTERNAL-GRAMMAR truth FIRST:
#   1. rules-gap.sh        — grammars-v4 rules (go/c/py) / POSIX subset
#                            (posix-sh) / PPI classes (perl) / the
#                            tree-sitter NODE-TYPE coverage for the
#                            tree-sitter-backed languages
#                            (powershell: ts-coverage in the frontend);
#   2. ts-node-gap.sh      — tree-sitter NODE-TYPE coverage for
#                            c/cpp/powershell (node types the grammar
#                            defines minus the node types the testdata
#                            parse trees exercise);
#   3. coverage-gap.sh     — the A1-node proxy / syn node kinds, only where
#                            no external grammar exists (zsh/fish/bat/zig)
#                            or it reports none.
# Commit only if the gate stays green WITH the new example; otherwise
# discard the example and record the construct as known-refused (so future
# cycles skip it) or as a lowering bug. The gate is the arbiter: an example
# that refuses by design must never land.
#
# REFUSED-REFRESH: at the idle point (fresh gaps exhausted) the refused
# ledger becomes the next source, at low frequency — entries are re-proposed
# against the CURRENT frontend (rate-limited, cursor-rotated) so the lists
# can shrink as the frontends grow. See REFUSED-REFRESH.md.
set -u
lang="$1"; LOG="$2"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
TS="[$(date +%FT%T)]"
DIR="$ROOT/frontends/coverage"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# ---- ledger helpers -----------------------------------------------------
# ledger_drop <file> <exact-line> — remove one entry (exact, fixed-string;
# entries contain spaces, so -xF, not a regex).
ledger_drop() {
  [ -f "$1" ] || return 0
  # always mv — grep exits 1 when the filter EMPTIES the result (the
  # last-entry drop), and `&& mv` would then discard the empty file and
  # resurrect the entry (same trap rules-gap.sh documents)
  grep -vxF "$2" "$1" > "$1.tmp" 2>/dev/null
  mv -f "$1.tmp" "$1"
}

# ---- gap source chain (external grammar first; see the header) ---------
gap_chain() {
  local g
  g=$(bash "$DIR/rules-gap.sh" "$lang" 2>/dev/null)
  [ -z "$g" ] && g=$(bash "$DIR/ts-node-gap.sh" "$lang" 2>/dev/null)
  [ -z "$g" ] && g=$(bash "$DIR/coverage-gap.sh" "$lang" 2>/dev/null)
  printf '%s' "$g"
}
# raw_vocabs: write each detector's raw (pre-exclusion) output to a temp
# file (COVERAGE_NO_EXCLUDE=1 — an opt-in pass-through in the detectors'
# exclude()). Used by refresh_refused to tell "still unexercised"
# (candidate) from "now exercised / inventory changed" (stale).
#
# PER-VOCABULARY, NOT first-non-empty: the refused ledger accumulates
# entries from every detector across time, so each entry must be judged
# against ITS OWN detector's raw set. First-non-empty reports only one
# vocabulary and falsely "stales" all refusals from the others
# (zsh-sh-go 2026-08-14 22:26: 22 A1-node refusals dropped because only
# ts-node gaps were reported — they re-surfaced as fresh gaps within the
# hour and were re-refused/re-escalated).
raw_vocabs() {  # fills $T/raw.{rules,ts,proxy}
  COVERAGE_NO_EXCLUDE=1 bash "$DIR/rules-gap.sh" "$lang" 2>/dev/null > "$T/raw.rules"
  COVERAGE_NO_EXCLUDE=1 bash "$DIR/ts-node-gap.sh" "$lang" 2>/dev/null > "$T/raw.ts"
  COVERAGE_NO_EXCLUDE=1 bash "$DIR/coverage-gap.sh" "$lang" 2>/dev/null > "$T/raw.proxy"
}

# ---- core-pending prune -------------------------------------------------
# Entries whose core-request was completed (moved to core-requests/done/ or
# deleted by the estree worker) leave the ledger — the gap is then retried
# (the contract now has what the construct needed).
prune_core_pending() {
  [ -f "$DIR/core-pending-$lang.txt" ] || return 0
  : > "$T/pending.new"
  while IFS=$'\t' read -r g req; do
    [ -n "$g" ] || continue
    if [ -f "$ROOT/core-requests/$req" ]; then
      printf '%s\t%s\n' "$g" "$req" >> "$T/pending.new"
    else
      echo "$TS coverage[$lang]: core-request $req completed — gap '$g' retried" >> "$LOG"
    fi
  done < "$DIR/core-pending-$lang.txt"
  mv "$T/pending.new" "$DIR/core-pending-$lang.txt"
}

# ---- one pi attempt (shared by the fresh + refresh paths) ---------------
# mode=fresh:    a by-design refusal APPENDS to the refused ledger.
# mode=refresh:  the entry is already ledgered — a re-refusal KEEPS it
#                (never duplicate); success / escalation / bug migrate it
#                OFF the ledger (the shrink).
attempt_gap() {  # <gap> <mode: fresh|refresh>
  local gap="$1" mode="$2" refresh=0
  [ "$mode" = refresh ] && refresh=1

  if [ "$refresh" -eq 1 ]; then
    echo "$TS coverage[$lang]: re-checking '$gap' (refused-refresh)" >> "$LOG"
  else
    echo "$TS coverage[$lang]: gate green but parser construct '$gap' uncovered — asking pi for a coverage example" >> "$LOG"
  fi

  # remember which testdata files exist before pi runs (the reject path
  # discards ONLY files pi adds — never git clean, which would destroy an
  # untracked testdata dir)
  ls "$ROOT/frontends/$lang"/testdata 2>/dev/null | sort > "$T/before.txt"
  ls "$ROOT/core-requests"/*.md 2>/dev/null | sort > "$T/core_before.txt" || true

  # pi creates ONE example in frontends/<lang>/testdata/ (scoped prompt).
  # rc==0 + no new file = pi deliberately refused (per the prompt: "if the
  # frontend REFUSES this construct by design, do NOT create the example;
  # exit 0") — ledger that so the gap stops being retried. rc!=0 (e.g. a
  # model/network error) must NOT be ledgered — the gap is retried next
  # cycle.
  bash "$ROOT/setup_backends.sh" --pi-coverage-example "$lang" "$gap" "$refresh" >> "$LOG" 2>&1
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
            printf '%s\t%s\n' "$gap" "$(basename "$req")" >> "$DIR/core-pending-$lang.txt"
          done
          # refresh: the entry migrates refused -> core-pending
          [ "$refresh" -eq 1 ] && ledger_drop "$DIR/refused-$lang.txt" "$gap"
          echo "$TS coverage[$lang]: '$gap' is an A1-contract gap — escalated to core-requests ($(echo $newreq | xargs -n1 basename | tr '\n' ' ')) — skipped while pending" >> "$LOG"
        else
          if [ "$refresh" -eq 1 ]; then
            echo "$TS coverage[$lang]: '$gap' re-checked, still refused — kept on the ledger" >> "$LOG"
          else
            printf '%s\n' "$gap" >> "$DIR/refused-$lang.txt"
            echo "$TS coverage[$lang]: no example for '$gap' (pi judged it not expressible) — marked known-refused" >> "$LOG"
          fi
        fi
      else
        echo "$TS coverage[$lang]: no example for '$gap' (pi rc=$pi_rc) — will retry next cycle" >> "$LOG"
      fi
    else
      # refresh: the construct now EMITs — the shrink (drop from refused)
      [ "$refresh" -eq 1 ] && ledger_drop "$DIR/refused-$lang.txt" "$gap"
      changes=$(git -C "$ROOT" status --porcelain 2>/dev/null \
                | awk '/^.. /{print $2}' \
                | awk -v d="frontends/$lang" '$1 ~ "^"d || $1 ~ /^harness\//' || true)
      if [ "$refresh" -eq 1 ]; then
        # the refresh's record IS the ledger deltas — commit them with the
        # example so the shrink is visible in history
        changes=$(printf '%s\n%s' "$changes" \
          "$(git -C "$ROOT" status --porcelain 2>/dev/null | awk '/^.. /{print $2}' \
            | grep -E "^frontends/coverage/(refused|core-pending|bugs)-$lang\.txt$" || true)" \
          | grep -v '^$' | sort -u)
      fi
      if [ -n "$changes" ]; then
        git -C "$ROOT" add $changes 2>/dev/null || true
        git -C "$ROOT" commit -m "frontend $lang: coverage example for '$gap'${refresh:+ (previously refused)}" >> "$LOG" 2>&1 || true
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
        if [ "$refresh" -eq 1 ]; then
          echo "$TS coverage[$lang]: '$gap' re-checked, still refuses (by design) — kept on the ledger" >> "$LOG"
        else
          printf '%s\n' "$gap" >> "$DIR/refused-$lang.txt"
          echo "$TS coverage[$lang]: example for '$gap' REFUSED (by design) — discarded, marked known-refused" >> "$LOG"
        fi
      else
        # refresh: used to refuse, now emits WRONG output — a lowering bug,
        # not a refusal; migrate refused -> bugs
        [ "$refresh" -eq 1 ] && ledger_drop "$DIR/refused-$lang.txt" "$gap"
        printf '%s\n' "$gap" >> "$DIR/bugs-$lang.txt"
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
            printf '%s\t%s\n' "$gap" "$(basename "$req")" >> "$DIR/core-pending-$lang.txt"
          done
          [ "$refresh" -eq 1 ] && ledger_drop "$DIR/refused-$lang.txt" "$gap"
          echo "$TS coverage[$lang]: '$gap' is an A1-contract gap — escalated to core-requests — skipped while pending" >> "$LOG"
        else
          if [ "$refresh" -eq 1 ]; then
            echo "$TS coverage[$lang]: '$gap' re-checked, still refused — kept on the ledger" >> "$LOG"
          else
            printf '%s\n' "$gap" >> "$DIR/refused-$lang.txt"
            echo "$TS coverage[$lang]: no example for '$gap' (pi judged it not expressible) — marked known-refused" >> "$LOG"
          fi
        fi
      else
        echo "$TS coverage[$lang]: no example for '$gap' (pi rc=$pi_rc) — will retry next cycle" >> "$LOG"
      fi
    fi
  fi
}

# ---- refused-refresh: re-propose ledgered refusals at the idle point ----
refresh_refused() {
  local r="$DIR/refused-$lang.txt"
  [ -f "$r" ] && [ -s "$r" ] || return 0
  # rate limit: one re-proposal per language per REFUSE_REFRESH_INTERVAL
  # (checked BEFORE the detector runs — rate-limited cycles stay cheap)
  local stamp="$DIR/refused-refresh-stamp-$lang.txt" now last
  if [ -f "$stamp" ]; then
    now=$(date +%s); last=$(cat "$stamp" 2>/dev/null || echo 0)
    if [ $((now - last)) -lt "${REFUSE_REFRESH_INTERVAL:-86400}" ]; then
      echo "$TS coverage[$lang]: refused-refresh rate-limited" >> "$LOG"
      return 0
    fi
  fi
  # per-vocabulary raw sets (rules / ts-node / A1+syn). Each refused entry
  # is judged against ITS OWN vocabulary's raw set — see raw_vocabs.
  raw_vocabs
  # no raw inventory at all (all detectors silent / no external grammar):
  # do nothing — dropping every entry as "stale" would un-suppress
  # everything and churn re-refusals.
  if [ ! -s "$T/raw.rules" ] && [ ! -s "$T/raw.ts" ] && [ ! -s "$T/raw.proxy" ]; then
    # ambiguous by design: the detectors exit 0 with EMPTY output both on
    # failure (no grammar / failed inventory / torn binary) and on a
    # healthy run with nothing uncovered (rust: every expressible syn kind
    # is exercised — syn-coverage builds fine and reports all-used).
    # Either way there is nothing to re-propose or judge stale, so skip;
    # the log line is informational, not a diagnosis.
    echo "$TS coverage[$lang]: no raw gap output (nothing uncovered, or no detector/inventory) — refused-refresh skipped" >> "$LOG"
    return 0
  fi
  # stale-drop: an entry is STALE only when its own vocabulary's detector
  # is healthy (raw non-empty) AND no longer reports it (now exercised by
  # testdata, or the grammar/inventory changed). When the own detector is
  # silent (torn binary / failed inventory / no grammar) the verdict is
  # unprovable — keep the entry (conservative; a false drop costs a pi
  # re-judgment, a missed drop costs nothing).
  : > "$T/ref.keep"; : > "$T/ref.stale"
  local e det
  while IFS= read -r e; do
    [ -n "$e" ] || continue
    case "$e" in
      "ts node "*)   det="$T/raw.ts" ;;
      "A1 node "*)   det="$T/raw.proxy" ;;
      "syn kind "*)  det="$T/raw.proxy" ;;
      *)             det="$T/raw.rules" ;;
    esac
    if [ ! -s "$det" ] || grep -qxF "$e" "$det"; then
      printf '%s\n' "$e" >> "$T/ref.keep"
    else
      printf '%s\n' "$e" >> "$T/ref.stale"
    fi
  done < "$r"
  if [ -s "$T/ref.stale" ]; then
    local g
    while IFS= read -r g; do
      [ -n "$g" ] || continue
      ledger_drop "$r" "$g"
      echo "$TS coverage[$lang]: refused entry '$g' now exercised — dropped (stale)" >> "$LOG"
    done < "$T/ref.stale"
  fi
  [ -s "$T/ref.keep" ] || return 0
  # rotating cursor: re-propose the candidate after the last one (wrap)
  local cur="$DIR/refused-refresh-cursor-$lang.txt" prev=""
  [ -f "$cur" ] && prev=$(cat "$cur")
  local gap
  gap=$(awk -v p="$prev" '
    NR==1 { first=$0 }
    $0==p { after=1; next }
    after && !out { out=$0 }
    END { print (out=="" ? first : out) }
  ' "$T/ref.keep")
  [ -n "$gap" ] || return 0
  printf '%s\n' "$gap" > "$cur"
  date +%s > "$stamp"
  attempt_gap "$gap" refresh
}

# ---- main ---------------------------------------------------------------
prune_core_pending
gaps=$(gap_chain)
if [ -z "$gaps" ]; then
  # fresh gaps exhausted — the refused ledger is the next source, at low
  # frequency (rate-limited + cursor-rotated)
  refresh_refused
  echo "$TS coverage[$lang]: all known parser nodes covered (or no external grammar / inventory) — nothing to add" >> "$LOG"
  exit 0
fi
gap=$(printf '%s\n' "$gaps" | head -1)
attempt_gap "$gap" fresh
exit 0
