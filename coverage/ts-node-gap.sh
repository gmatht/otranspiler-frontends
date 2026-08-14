#!/bin/bash
# ts-node-gap.sh <lang> — tree-sitter NODE-TYPE coverage: node types the
# grammar defines (node-types.json) minus the node types the testdata
# parse trees exercise. THE EXTERNAL TRUTH for the tree-sitter-backed
# languages (c, cpp, powershell): the frontends' own parsers are
# tree-sitter (cpp, powershell) or hand-rolled (c), but the GRAMMAR is
# the source of truth for "do the examples cover the language's parser".
#
# Prints one "ts node <type>" per gap. Empty output where no tree-sitter
# grammar/inventory exists (zsh/fish/bat/zig — hand-rolled, no vendored
# grammar) — the worker hook then falls back to the A1-node proxy.
#
# Exclusions: refused-<lang>.txt / bugs-<lang>.txt / core-pending-<lang>.txt
# (same ledgers as coverage-gap.sh).
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
DIR="$ROOT/frontends/coverage"
lang="$1"
BIN="$DIR/ts-node-gap/ts-node-gap"
if [ ! -x "$BIN" ]; then
  (cd "$DIR/ts-node-gap" && make ts-node-gap >/dev/null 2>&1) || exit 0
fi

case "$lang" in
  c-sh-go)        td="$ROOT/frontends/c-sh-go/testdata";       ext=c ;;
  cpp-sh-go)      td="$ROOT/frontends/cpp-sh-go/testdata_cpp"; ext=cc ;;
  powershell-sh-go) td="$ROOT/frontends/powershell-sh-go/testdata"; ext=ps1 ;;
  fish-sh-go)     td="$ROOT/frontends/fish-sh-go/testdata";    ext=fish ;;
  zsh-sh-go)      td="$ROOT/frontends/zsh-sh-go/testdata";     ext=zsh ;;
  zig-sh-go)      td="$ROOT/frontends/zig-sh-go/testdata";     ext=zig ;;
  *) exit 0 ;;  # no vendored tree-sitter grammar for this frontend
esac
inv="$DIR/grammar-inventories/$lang.json"
case "$lang" in
  powershell-sh-go|fish-sh-go|zsh-sh-go|zig-sh-go)
    inv="$ROOT/frontends/$lang/grammars/tree-sitter-${lang%-sh-go}/src/node-types.json" ;;
esac

# known-refused / known-bug / core-pending exclusions (same as coverage-gap.sh)
excl="$DIR/refused-$lang.txt $DIR/bugs-$lang.txt"
exclude() {
  # COVERAGE_NO_EXCLUDE=1: the refused-refresh pass (worker-coverage-step.sh)
  # needs the RAW unexercised set (what the detectors would report without
  # the ledgers) to tell "still unexercised" (candidate) from "now
  # exercised" (stale). Opt-in pass-through; the fresh chain is unchanged.
  [ -n "${COVERAGE_NO_EXCLUDE:-}" ] && cat && return 0
  local pat="" p=""
  for e in $excl; do
    if [ -f "$e" ]; then
      p=$(tr '\n' '|' < "$e" | sed 's/|$//')
      [ -n "$p" ] && pat="${pat:+$pat|}$p"
    fi
  done
  if [ -f "$DIR/core-pending-$lang.txt" ]; then
    p=$(cut -f1 "$DIR/core-pending-$lang.txt" | tr '\n' '|' | sed 's/|$//')
    [ -n "$p" ] && pat="${pat:+$pat|}$p"
  fi
  if [ -n "$pat" ]; then grep -vE "$pat"; else cat; fi
}
"$BIN" "$lang" "$td" "$inv" "$ext" | exclude
exit 0
