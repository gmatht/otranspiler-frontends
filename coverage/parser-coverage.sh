#!/bin/bash
# parser-coverage.sh — parser coverage per frontend over real corpora.
#
# For each frontend, run its binary (`--shir file --raw`) over a corpus of
# real files in the frontend's source language and classify each file:
#
#   EMIT      exit 0, stdout starts with '{'  — the parser+lowerer fully
#             handled the file (valid A1 shIR JSON)
#   BAD-EMIT  exit 0 but stdout is not JSON — a BUG: the parser printed
#             an error to stderr yet exited 0 (the harness would treat
#             it as success and choke on the empty output)
#   REFUSE    exit != 0, stderr matches the frontend's refusal marker —
#             the parser understood the file and refused a construct
#             (a SUBSET gap: the expressible subset, not the parser)
#   PARSE-ERR exit != 0, stderr has another message — the parser itself
#             choked on the file (a PARSER gap)
#   CRASH     exit != 0, stderr shows a panic/stack overflow — a parser
#             BUG (unbounded recursion etc.), not a refusal or a parse
#             error message
#   SILENT    exit != 0, empty stderr — crash without a message
#
# The headline number is EMIT / total ("the frontend fully handled X% of
# real files"). REFUSE + EMIT is "the parser could engage with the file";
# PARSE-ERR is the pure parser gap. Corpus choice is per frontend (real
# workspace code where it exists; the frontend's own testdata only where
# no other corpus exists — marked below).
#
# Usage: bash frontends/coverage/parser-coverage.sh
# Results: one TSV per frontend in frontends/coverage/results/
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FE="$ROOT/frontends"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
RESULTS="$FE/coverage/results"
mkdir -p "$RESULTS"

run_frontend() {  # <label> <bin> <refuse-marker> <corpus-note> <file...>
  local label=$1 bin=$2 marker=$3 note=$4; shift 4
  if [ ! -x "$bin" ]; then
    echo "SKIP  $label (binary missing: $bin)"
    return
  fi
  local emit=0 bademit=0 refuse=0 parseerr=0 crash=0 silent=0 total=0 cls
  local res="$RESULTS/$label.tsv"
  : > "$res"
  for f in "$@"; do
    [ -f "$f" ] || continue
    total=$((total + 1))
    timeout 30 "$bin" --shir "$f" --raw > "$T/o" 2> "$T/e"
    local code=$?
    local err; err=$(cat "$T/e")
    if [ "$code" -eq 0 ]; then
      if [ "$(head -c1 "$T/o")" = "{" ]; then
        emit=$((emit + 1)); cls=EMIT
      else
        bademit=$((bademit + 1)); cls=BAD-EMIT
      fi
    elif [ -n "$err" ]; then
      if [ -n "$marker" ] && printf '%s' "$err" | grep -q "$marker"; then
        refuse=$((refuse + 1)); cls=REFUSE
      elif printf '%s' "$err" | grep -qE "panic|fatal error|stack exceeds|runtime:"; then
        crash=$((crash + 1)); cls=CRASH
      else
        parseerr=$((parseerr + 1)); cls=PARSE-ERR
      fi
    else
      silent=$((silent + 1)); cls=SILENT
    fi
    printf '%s\t%s\n' "$cls" "$(basename "$f")" >> "$res"
  done
  local pct
  pct=$(awk -v e="$emit" -v t="$total" 'BEGIN { printf "%.1f", t ? 100*e/t : 0 }')
  local parser_pct
  parser_pct=$(awk -v e="$emit" -v r="$refuse" -v t="$total" 'BEGIN { printf "%.1f", t ? 100*(e+r)/t : 0 }')
  echo "── $label: $note"
  echo "   $total files | EMIT $emit ($pct%) | BAD-EMIT $bademit | REFUSE $refuse | PARSE-ERR $parseerr | CRASH $crash | SILENT $silent"
  echo "   parser engagement (EMIT+REFUSE) $parser_pct% | parser gap (PARSE-ERR+CRASH+BAD-EMIT) $((parseerr + crash + bademit))"
}

echo "=============================================================="
echo " FRONTEND PARSER COVERAGE (corpus: real workspace code, or the"
echo " frontend's own testdata where no other corpus exists)"
echo "=============================================================="

# sh — the corpus the whole ecosystem exists for (532 examples, bash)
run_frontend sh-posix-sh-go "$FE/posix-sh-go/posix-sh-go" "unsupported" \
  "sh2perl/examples/*.sh (532, bash)" "$ROOT"/sh2perl/examples/*.sh

# c — the c-sh-go test corpus (74, the only C corpus in the workspace)
run_frontend c-sh-go "$FE/c-sh-go/c-sh-go" "REFUSE:" \
  "frontends/c-sh-go/testdata/*.c (74)" "$FE"/c-sh-go/testdata/*.c

# cpp — the cpp frontend's test corpus (13)
run_frontend cpp-sh-go "$FE/cpp-sh-go/cpp-sh-go" "unsupported C++:" \
  "frontends/cpp-sh-go/testdata_cpp/*.cc (13)" "$FE"/cpp-sh-go/testdata_cpp/*.cc

# go — the fleet's own Go code (real Go: packages, imports, interfaces)
# go-sh's controlled refusals all surface as `go-sh: line N: <msg>`
# (failf panics are recovered in run()); genuine crashes print raw
# panics instead — so the LINE-PREFIX is the honest refuse marker,
# not just messages containing "unsupported"
run_frontend go-sh "$FE/go-sh/go-sh" "go-sh: line" \
  "frontends/**/*.go (the fleet's own Go)" $(find "$FE" -maxdepth 3 -name '*.go' -not -path '*/gen/*')

# py — workspace Python (real Python 3) + the frontend's own testdata
# (control; the only other py corpus is py-sh-go/testdata)
run_frontend py-sh-go "$FE/py-sh-go/py-sh-go" "unsupported" \
  "root+harness+frontends *.py + py-sh-go/testdata (control)" \
  $(find "$ROOT" -maxdepth 2 -name '*.py' -not -path '*/node_modules/*' -not -path '*/junk/*' -not -path '*/sh2perl/*' -not -path '*/.git/*' 2>/dev/null) \
  "$FE"/py-sh-go/testdata/*.py

# pl — workspace Perl (real Perl; scratch excluded)
run_frontend perl-sh-go "$FE/perl-sh-go/perl-sh-go" "unsupported" \
  "root *.pl + harness + frontends (real Perl)" \
  $(find "$ROOT" -maxdepth 2 -name '*.pl' -not -path '*/junk/*' -not -path '*/sh.new/*' -not -path '*/sh2perl/*' -not -name '__tmp_*' -not -name 'pc-*' 2>/dev/null) \
  "$FE"/perl-sh-go/*.pl "$FE"/perl-sh-go/testdata/*.pl 2>/dev/null

# rust — the sh2perl core's own Rust (real Rust; all library crates —
# every file is a REFUSE at the "only fn main" item gate unless it has
# main, which is the honest measure of the v0.1 subset)
run_frontend rust-frontend "$FE/rust-frontend/target/debug/rust-frontend" "unsupported Rust:" \
  "sh2perl/src/*.rs + cli/src/*.rs (40, library crates)" \
  "$ROOT"/sh2perl/src/*.rs "$ROOT"/sh2perl/cli/src/*.rs

# fish / zsh / bat — own testdata only (no other corpus in the workspace)
run_frontend fish-sh-go "$FE/fish-sh-go/fish-sh-go" "unsupported" \
  "frontends/fish-sh-go/testdata/*.fish (75)" "$FE"/fish-sh-go/testdata/*.fish
run_frontend zsh-sh-go "$FE/zsh-sh-go/zsh-sh-go" "unsupported" \
  "frontends/zsh-sh-go/testdata/*.zsh (75)" "$FE"/zsh-sh-go/testdata/*.zsh
run_frontend bat-sh-go "$FE/bat-sh-go/bat-sh-go" "unsupported" \
  "frontends/bat-sh-go/testdata/*.bat (47)" "$FE"/bat-sh-go/testdata/*.bat

echo "=============================================================="
echo " Per-file classifications: $RESULTS/*.tsv"
