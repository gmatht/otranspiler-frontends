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

# ── official grammars-v4 grammars for the Go/C frontends (the external
#    source of truth; the frontends' own parsers are hand-rolled, so the
#    official grammars enumerate the language's grammar rules instead) ──
fetch() { [ -f "$2" ] || curl -sL -o "$2" "$1"; }
fetch "https://raw.githubusercontent.com/antlr/grammars-v4/master/golang/GoLexer.g4" "$W/GoLexer.g4"
fetch "https://raw.githubusercontent.com/antlr/grammars-v4/master/golang/GoParser.g4" "$W/GoParser.g4"
fetch "https://raw.githubusercontent.com/antlr/grammars-v4/master/golang/Java/GoParserBase.java" "$W/GoParserBase.java"
fetch "https://raw.githubusercontent.com/antlr/grammars-v4/master/c/CLexer.g4" "$W/CLexer.g4"
fetch "https://raw.githubusercontent.com/antlr/grammars-v4/master/c/CParser.g4" "$W/CParser.g4"
# stub base classes: the official grammars' gcc-preprocess/symbol-table
# hooks are skipped (coverage measurement, not semantic analysis). The
# C predicates return conservative values that resolve the testdata's
# ambiguities (no typedefs/casts in the subset; sizeof(type) used).
cat > "$W/SymbolTable.java" <<'EOF'
// minimal stub — the official C grammar's semantic symbol table (unused for coverage)
public class SymbolTable { }
EOF
cat > "$W/CLexerBase.java" <<'EOF'
import org.antlr.v4.runtime.*;
// minimal stub — the official C grammar's preprocessor hook (gcc invocation) skipped
public abstract class CLexerBase extends Lexer {
    public CLexerBase(CharStream input) { super(input); }
}
EOF
cat > "$W/CParserBase.java" <<'EOF'
import org.antlr.v4.runtime.*;
// minimal stub — conservative predicates for the official C grammar's
// ambiguity resolution (coverage measurement, not semantic analysis)
public abstract class CParserBase extends Parser {
    public CParserBase(TokenStream input) { super(input); }
    private SymbolTable _st = new SymbolTable();
    // '(' followed by a type keyword = a cast; else a paren-expr
    public boolean IsCast() {
        int t = _input.LT(2).getType();
        switch (t) {
            case CLexer.Int: case CLexer.Char: case CLexer.Float:
            case CLexer.Double: case CLexer.Void: case CLexer.Struct:
            case CLexer.Union: case CLexer.Enum: case CLexer.Signed:
            case CLexer.Unsigned: case CLexer.Short: case CLexer.Long:
            case CLexer.Bool: case CLexer.BitInt: case CLexer.Typedef:
                return true;
            default:
                return false;
        }
    }
    public boolean IsDeclaration() { return true; }
    public boolean IsDeclarationSpecifier() { return true; }
    public boolean IsInitDeclaratorList() { return true; }
    public boolean IsNullStructDeclarationListExtension() { return false; }
    // sizeof( TYPE ) vs sizeof expr — true iff a type keyword follows '('
    public boolean IsSomethingOfTypeName() {
        int t = _input.LT(3).getType();
        switch (t) {
            case CLexer.Int: case CLexer.Char: case CLexer.Float:
            case CLexer.Double: case CLexer.Void: case CLexer.Struct:
            case CLexer.Union: case CLexer.Enum: case CLexer.Signed:
            case CLexer.Unsigned: case CLexer.Short: case CLexer.Long:
            case CLexer.Bool: case CLexer.BitInt:
                return true;
            default:
                return false;
        }
    }
    public boolean IsStatement() { return true; }
    public boolean IsTypeSpecifierQualifier() { return true; }
    public boolean IsTypedefName() { return false; }
    public void LookupSymbol() { }
    public void EnterDeclaration() { }
    public void EnterScope() { }
    public void ExitScope() { }
    public void OutputSymbolTable() { }
}
EOF
cp "$DIR/antlr-sh/Rules.java" "$W/"
(cd "$W" \
  && java -jar "$JAR" -o . GoLexer.g4 GoParser.g4 >/dev/null 2>&1 \
  && java -jar "$JAR" -o . CLexer.g4 CParser.g4 >/dev/null 2>&1 \
  && javac -cp "$JAR" *.java -d out) || { echo "ANTLR rules build failed"; exit 1; }

run_rules() {  # <mode> <label> <files...>
  local mode=$1 label=$2; shift 2
  local out; out=$(java -cp "$W/out:$JAR" Rules "$mode" "$@" 2>/dev/null)
  local def exe clean
  def=$(echo "$out" | grep '^RULES-DEFINED' | cut -d' ' -f2)
  exe=$(echo "$out" | grep '^RULES-EXERCISED' | cut -d' ' -f2)
  clean=$(echo "$out" | grep '^RULES-EXERCISED' | grep -o 'by [0-9]*/[0-9]* parse-clean')
  echo "  $label: $exe/$def grammar rules exercised $clean"
  local gaps; gaps=$(echo "$out" | grep '^RULE-UNEXERCISED' | cut -f2 | tr '\n' ' ')
  [ -n "$gaps" ] && echo "    unexercised rules: $gaps" || echo "    unexercised rules: none"
}

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

echo
echo "══════════════════════════════════════════════════════════════"
echo " 3. per-example rule coverage vs the official grammars"
echo "══════════════════════════════════════════════════════════════"
echo "  (a rule no example enters = a language construct the examples"
echo "   don't cover; judge expressibility against the frontend subset"
echo "   + gate — see GOOD_EXAMPLES.md. posix uses the custom subset"
echo "   grammar (grammars-v4 has no POSIX grammar); go/c use the"
echo "   official grammars-v4 grammars.)"
echo "  posix (custom POSIX subset) × posix-sh-go/testdata:"
run_rules posix sh-posix "$FE"/posix-sh-go/testdata/*.sh
echo "  py (official grammars-v4 Python3) × py-sh-go/testdata:"
run_rules py py "$FE"/py-sh-go/testdata/*.py
# go-sh examples omit package/import/func-main boilerplate — wrap them
# like the executed-stdout oracle does (frontend-stdout.sh)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
for f in "$FE"/go-sh/testdata/*.go; do
  if grep -q 'func main()' "$f"; then cp "$f" "$T/$(basename "$f")";
  else { echo "package main"; echo "func main() {"; cat "$f"; echo "}"; } > "$T/$(basename "$f")"; fi
done
echo "  go (official grammars-v4 golang) × go-sh/testdata (oracle-wrapped):"
run_rules go go "$T"/*.go
echo "  c (official grammars-v4 c) × c-sh-go/testdata:"
run_rules c c "$FE"/c-sh-go/testdata/*.c
rm -rf "$T"
