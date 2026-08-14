#!/bin/bash
# rules-gap.sh <lang> — the EXTERNAL-GRAMMAR rule gaps: language grammar
# rules (grammars-v4 for go/c/py, the custom POSIX subset for posix-sh,
# PPI node classes for perl) that NO testdata example exercises. The
# frontends' own parsers are hand-rolled, so the official grammars are the
# source of truth for "do the examples cover the parser's features".
#
# Prints one rule name per line. Noise is excluded (abstract base classes,
# alternate start rules, lexer details). Entries already ledgered in
# refused-<lang>.txt / bugs-<lang>.txt are excluded (the worker records a
# by-design refusal or a lowering bug there and must not retry it).
#
# Exit 0 with EMPTY output on any failure or where no external grammar
# exists — the worker hook then falls back to ts-node-gap.sh (the
# tree-sitter node-type coverage) and finally the A1-node proxy
# (coverage-gap.sh). See PARSER_GAPS.md for the per-frontend status.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FE="$ROOT/frontends"
DIR="$FE/coverage"
W="$DIR/.work"
JAR="$DIR/.antlr4.jar"
lang="$1"

# ledger exclusions (worker-appended; FIXED-STRING match — the ledger
# lines may be rule names OR free-text bug descriptions containing regex
# metacharacters like $#, so regex alternation would corrupt the filter)
exclude() {
  # COVERAGE_NO_EXCLUDE=1: the refused-refresh pass (worker-coverage-step.sh)
  # needs the RAW unexercised set (what the detectors would report without
  # the ledgers) to tell "still unexercised" (candidate) from "now
  # exercised" (stale). Opt-in pass-through; the fresh chain is unchanged.
  [ -n "${COVERAGE_NO_EXCLUDE:-}" ] && cat && return 0
  local tmp; tmp=$(mktemp); cat > "$tmp"
  for e in "$DIR/refused-$lang.txt" "$DIR/bugs-$lang.txt"; do
    [ -f "$e" ] || continue
    while IFS= read -r pat; do
      [ -n "$pat" ] || continue
      # grep exits 1 when the filter EMPTIES the list (no lines selected);
      # `&& mv` would then discard the (empty) result and resurrect the
      # pattern as a phantom gap every cycle (the last ledgered refusal
      # could never be excluded — c-sh-go toplevelAsmArgument was
      # re-issued 6 times despite 6 ledger entries). Apply the filter
      # whenever grep wrote output, empty or not.
      if grep -vF "$pat" "$tmp" > "$tmp.2" 2>/dev/null; then
        mv -f "$tmp.2" "$tmp"
      elif [ ! -s "$tmp.2" ]; then
        mv -f "$tmp.2" "$tmp"
      fi
    done < "$e"
  done
  # escalated contract gaps: skipped while the core-request is pending
  # (worker-coverage-step.sh prunes the ledger when the estree worker
  # completes the request, so the gap is retried once the contract has it)
  if [ -f "$DIR/core-pending-$lang.txt" ]; then
    cut -f1 "$DIR/core-pending-$lang.txt" | grep -vE '^$' > "$tmp.pats" 2>/dev/null || true
    if [ -s "$tmp.pats" ]; then
      grep -vFf "$tmp.pats" "$tmp" > "$tmp.2" && mv "$tmp.2" "$tmp"
    fi
    rm -f "$tmp.pats"
  fi
  cat "$tmp"; rm -f "$tmp"
}

# build the ANTLR parsers once (mirrors antlr-coverage.sh; the .work dir
# is gitignored so this is a one-time warm-up per checkout)
ensure_antlr_built() {
  [ -f "$JAR" ] || curl -sL -o "$JAR" https://www.antlr.org/download/antlr-4.13.2-complete.jar || return 1
  [ -f "$W/out/Rules.class" ] && [ -f "$W/out/POSIXParser.class" ] && \
  [ -f "$W/out/Python3Parser.class" ] && [ -f "$W/out/GoParser.class" ] && \
  [ -f "$W/out/CParser.class" ] && return 0
  bash "$DIR/antlr-coverage.sh" >/dev/null 2>&1 || true
  [ -f "$W/out/Rules.class" ] && [ -f "$W/out/POSIXParser.class" ] && \
  [ -f "$W/out/Python3Parser.class" ] && [ -f "$W/out/GoParser.class" ] && \
  [ -f "$W/out/CParser.class" ]
}

# noise: alternate start rules / lexer details / abstract bases per mode
noise() { # <mode>
  case "$1" in
    posix) grep -vE '^(start|word)$' ;;
    py)    grep -vE '^(single_input|eval_input|file_input|encoding_decl)$' ;;
    go)    grep -vE '^(sourceFile|eos)$' ;;
    c)     grep -vE '^(compilationUnit)$' ;;
    ppi)   grep -vE '^(PPI::Document::File|PPI::Document::Fragment|PPI::Element|PPI::Node|PPI::Statement|PPI::Structure|PPI::Token|PPI::Token::Quote|PPI::Token::QuoteLike|PPI::Token::Regexp|PPI::Token::Number)$' ;;
    *)     cat ;;
  esac
}

case "$lang" in
  posix-sh-go)
    ensure_antlr_built || exit 0
    java -cp "$W/out:$JAR" Rules posix "$FE/$lang"/testdata/*.sh 2>/dev/null \
      | grep '^RULE-UNEXERCISED' | cut -f2 | noise posix | exclude ;;
  py-sh-go)
    ensure_antlr_built || exit 0
    java -cp "$W/out:$JAR" Rules py "$FE/$lang"/testdata/*.py 2>/dev/null \
      | grep '^RULE-UNEXERCISED' | cut -f2 | noise py | exclude ;;
  go-sh)
    ensure_antlr_built || exit 0
    # examples omit package/import/func-main boilerplate — wrap them like
    # the executed-stdout oracle does (frontend-stdout.sh)
    T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
    for f in "$FE/$lang"/testdata/*.go; do
      if grep -q 'func main()' "$f"; then cp "$f" "$T/$(basename "$f")";
      else { echo "package main"; echo "func main() {"; cat "$f"; echo "}"; } > "$T/$(basename "$f")"; fi
    done
    java -cp "$W/out:$JAR" Rules go "$T"/*.go 2>/dev/null \
      | grep '^RULE-UNEXERCISED' | cut -f2 | noise go | exclude ;;
  c-sh-go)
    ensure_antlr_built || exit 0
    java -cp "$W/out:$JAR" Rules c "$FE/$lang"/testdata/*.c 2>/dev/null \
      | grep '^RULE-UNEXERCISED' | cut -f2 | noise c | exclude ;;
  perl-sh-go)
    command -v perl >/dev/null || exit 0
    perl -MPPI -e 'exit 0' 2>/dev/null || exit 0
    perl "$DIR/ppi-coverage.pl" "$FE/$lang"/testdata/*.pl 2>/dev/null \
      | grep '^RULE-UNEXERCISED' | cut -f2 | noise ppi | exclude ;;
  powershell-sh-go)
    # tree-sitter-powershell (vendored under grammars/) is the official
    # parser — report its NAMED node types that no testdata example
    # exercises (ts-coverage, a Go tool in the frontend module using the
    # same smacker/go-tree-sitter + cgo bindings the frontend uses).
    command -v go >/dev/null || exit 0
    mkdir -p "$W"
    tsb="$W/ts-coverage-powershell"
    if [ ! -x "$tsb" ] || find "$FE/powershell-sh-go/cmd/ts-coverage" -newer "$tsb" 2>/dev/null | grep -q .; then
      (cd "$FE/powershell-sh-go" && CGO_ENABLED=1 go build -o "$tsb" ./cmd/ts-coverage) >/dev/null 2>&1 || exit 0
    fi
    "$tsb" "$FE/powershell-sh-go/testdata" \
      "$FE/powershell-sh-go/grammars/tree-sitter-powershell/src/node-types.json" 2>/dev/null \
      | exclude ;;
  *) exit 0 ;;  # no external grammar (zsh/fish/bat/zig/cpp/rust) — zig's
                # tokenizer is hand-rolled (tree-sitter-zig is planned,
                # PLAN_ZIG_F §2); the A1 proxy (or syn for rust) in
                # coverage-gap.sh stays the source
esac
exit 0
