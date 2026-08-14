# Refused-Refresh — re-proposing ledgered refusals at the idle point

Status: **landed 2026-08-14.** The coverage loop's disposition discipline
(README.md) treats a by-design refusal as a terminal disposal: "the hook
ledgers the gap in `refused-<lang>.txt` and stops retrying it." That is
correct *only while the frontend's subset boundary is static*. The
frontends grow (new testdata pins land every cycle), so a refusal can
outlive the subset boundary that justified it: the construct becomes
expressible, but the ledger suppresses it forever and the gap is never
retried. The coverage number then closes artificially — it measures
"everything disposed", not "everything covered".

This pass gives the refused ledger a fourth disposition — **re-check** —
so the lists can *shrink* as the frontends grow.

## The two stale classes

For a refused entry, three states are possible against the current
frontend + testdata:

| state | detector report | meaning | action |
|---|---|---|---|
| **stale** | entry no longer in the RAW gap set | now exercised by testdata (or the grammar inventory changed) | drop — bookkeeping, no pi call |
| **candidate** | entry still in the RAW gap set | suppressed only by the ledger | re-propose via pi (rate-limited, cursor-rotated) |
| — | — | still outside the subset | keep (pi re-confirms, no-op) |

The RAW gap set is the pre-exclusion detector output (`COVERAGE_NO_EXCLUDE=1`
toggle added to `rules-gap.sh` / `ts-node-gap.sh` / `coverage-gap.sh` — the
fresh gap chain is untouched). Raw is what the detectors would report if the
ledgers didn't exist; "in raw" ⇔ "still unexercised by testdata".

## Trigger — fresh-first

`worker-coverage-step.sh` runs the refresh **only** in the `gaps == ""`
branch (the idle point, formerly the "nothing to add" early exit). Fresh
grammar gaps always win: a language with an active gap grind never burns
refresh cycles. Languages that reach the idle point with a large refused
ledger (powershell: 0 gaps, 85 refusals suppressing 93 unexercised
tree-sitter nodes) go straight to refresh duty; the ledger becomes the next
source after the fresh set is exhausted.

## Rate limit + rotation

- **Rate limit:** one re-proposal per language per `REFUSE_REFRESH_INTERVAL`
  (default 86400s, env-overridable; stamp file
  `refused-refresh-stamp-<lang>.txt`). A construct judged refused yesterday
  is unlikely to emit today; the interval bounds pi-call burn.
- **Rotation:** a cursor (`refused-refresh-cursor-<lang>.txt`) records the
  last re-proposed entry; each refresh re-proposes the next candidate after
  the cursor (wrap around). Prevents re-litigating the same entry while
  others wait; the cursor marching through the list is the progress signal.
- The stamp/cursor are worker state — add
  `refused-refresh-stamp-*.txt` / `refused-refresh-cursor-*.txt` to
  `.gitignore` (like `results/`).

## Outcomes (mirrors the fresh path, different ledger actions)

| outcome | fresh mode (unchanged) | refresh mode |
|---|---|---|
| gate green + example | commit | commit + **drop from refused** ← the shrink |
| rc==0, no example, new core-request | append to core-pending | **migrate** refused → core-pending |
| rc==0, no example, no request | append to refused | **keep** (never duplicate) |
| gate fails, `(frontend emit)` | append to refused | keep (still refuses) |
| gate fails, stdout mismatch | append to bugs | **migrate** refused → bugs (used to refuse, now WRONG = a lowering bug) |
| pi rc!=0 | retry next cycle | retry next interval |

The refresh path also commits the ledger deltas (refused/core-pending/bugs
for the language) alongside the example, so the shrink is visible in history.

## Prompt

`setup_backends.sh --pi-coverage-example` gains a third arg (`refresh` 0/1);
the refresh prompt tells pi this was a previous refusal and re-judges
against the CURRENT frontend (FRONTEND.md / the parser source), with the
same three cases: expressible → example; still refuses by design → exit 0;
A1-contract gap → escalate (migrates refused → core-pending).

## Guards

- **Fresh-first:** refresh never runs while fresh gaps exist.
- **No duplicates:** the refresh keep-path must not re-append (`printf >>
  refused` would duplicate — the entry is already there).
- **No inventory, no refresh:** if the raw gap set is empty the detector is
  silent (no external grammar / no inventory) — refresh skips rather than
  dropping every refused entry as "stale" (that would un-suppress
  everything and churn re-refusals).
- **`grep -vxF` / `grep -xF` for ledger ops:** exact-line + fixed-string
  (entries contain spaces; `-F` avoids regex metachar surprises).
- **Atomicity:** every ledger rewrite is tmp + mv; a parallel worker
  reading `exclude()` mid-rewrite is benign (worst case a gap is retried
  once).
- **Detector toggle is opt-in:** `COVERAGE_NO_EXCLUDE=1` only changes
  output when set; the fresh chain and all existing callers are
  byte-identical.

## Cost

At the idle point the refresh runs the detector chain twice (filtered +
raw). For the ANTLR languages that's a Java parse of the testdata corpus
(a few seconds per cycle); the pi call itself dominates, and it's
rate-limited to once per interval per language.

## Measuring the shrink

`wc -l frontends/coverage/refused-<lang>.txt` is the metric; the cursor
position + refresh log lines show the march. A language whose refused list
stays flat across many refresh cycles is at its true subset boundary; one
whose list shrinks is validating the pass.
