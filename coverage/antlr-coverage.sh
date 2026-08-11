#!/bin/bash
# antlr-coverage.sh — ANTLR4 parser coverage for sh (and, as a bonus,
# Python — the only REAL .g4 grammars in the repo).
#
# Ground truth first: the repo's "ANTLR4 POSIX parser"
# (frontends/posix-sh-go/grammars/POSIX.g4) is a 14-line placeholder stub
# — this script proves antlr4 cannot generate a parser from it. The
# meaningful measurement then runs the grammar the ANTLR plan
# (plan-antlr-go.md) actually called for: a custom POSIX-sh-subset .g4
# (frontends/coverage/antlr-sh/POSIX.g4, written for this test).
#
# Also measures the vendored grammars-v4 Python3 grammars (real, in-repo
# at py-sh-go/grammars/) over the Python corpus.
#
# Usage: bash frontends/coverage/antlr-coverage.sh
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FE="$ROOT/frontends"
DIR="$FE/coverage"
JAR="$DIR/.antlr4.jar"
W="$DIR/.work"
mkdir -p "$W"
mkdir -p "$DIR/results"

# antlr4 tool (download-on-demand, gitignored)
if [ ! -f "$JAR" ]; then
  echo "downloading antlr4 jar..."
  curl -sL -o "$JAR" https://www.antlr.org/download/antlr-4.13.2-complete.jar || exit 2
fi

echo "══════════════════════════════════════════════════════════════"
echo " 0. the repo's own 'ANTLR4 POSIX parser' (grammars/POSIX.g4)"
echo "══════════════════════════════════════════════════════════════"
java -jar "$JAR" -o "$W/stubcheck" "$FE/posix-sh-go/grammars/POSIX.g4" 2>&1 | head -2
echo "  ^ antlr4 cannot build a parser from it — it is a stub, not a grammar"

# ── generate + compile: our POSIX subset + the vendored Python3 grammars ──
cp "$DIR/antlr-sh/POSIX.g4" "$DIR/antlr-sh/Cov.java" "$W/"
cp "$FE/py-sh-go/grammars/Python3Lexer.g4" "$FE/py-sh-go/grammars/Python3Parser.g4" "$W/"
if [ ! -f "$W/Python3LexerBase.java" ]; then
  curl -sL -o "$W/Python3LexerBase.java" \
    https://raw.githubusercontent.com/antlr/grammars-v4/master/python/python3/Java/Python3LexerBase.java
  curl -sL -o "$W/Python3ParserBase.java" \
    https://raw.githubusercontent.com/antlr/grammars-v4/master/python/python3/Java/Python3ParserBase.java
fi
(cd "$W" \
  && java -jar "$JAR" -o . POSIX.g4 >/dev/null 2>&1 \
  && java -jar "$JAR" -o . Python3Lexer.g4 >/dev/null 2>&1 \
  && java -jar "$JAR" -o . -lib . Python3Parser.g4 >/dev/null 2>&1 \
  && mkdir -p out \
  && javac -cp "$JAR" *.java -d out) || { echo "ANTLR build failed"; exit 1; }

run_cov() {  # <mode> <label> <files...>
  local mode=$1 label=$2; shift 2
  local tsv="$DIR/results/antlr-$label.tsv"
  java -cp "$W/out:$JAR" Cov "$mode" "$@" 2>/dev/null > "$tsv"
  local ok err total
  ok=$(grep -c '^OK ' "$tsv" || true)
  total=$(grep -cE '^(OK|ERR) ' "$tsv" || true)
  local pct
  pct=$(awk -v o="$ok" -v t="$total" 'BEGIN { printf "%.1f", t ? 100*o/t : 0 }')
  echo "  ANTLR4 $label: $ok/$total = $pct% parse-clean"
}

echo
echo "══════════════════════════════════════════════════════════════"
echo " 1. ANTLR4 POSIX-sh subset parser — parse-clean rate"
echo "══════════════════════════════════════════════════════════════"
echo "  control corpus (posix-sh-go/testdata — POSIX-flavored, 78):"
run_cov posix sh-posix-control "$FE"/posix-sh-go/testdata/*.sh
echo "  real corpus (sh2perl/examples — bash, 532):"
run_cov posix sh-posix-corpus "$ROOT"/sh2perl/examples/*.sh

echo
echo "══════════════════════════════════════════════════════════════"
echo " 2. (bonus) vendored grammars-v4 Python3 grammars — parse-clean"
echo "══════════════════════════════════════════════════════════════"
echo "  corpus: root/harness/frontends *.py + py-sh-go/testdata (control):"
run_cov py py-python3-corpus $(find "$ROOT" -maxdepth 2 -name '*.py' -not -path '*/node_modules/*' -not -path '*/junk/*' -not -path '*/sh2perl/*' -not -path '*/.git/*' 2>/dev/null) "$FE"/py-sh-go/testdata/*.py

echo
echo " per-file: $DIR/results/antlr-*.tsv"
