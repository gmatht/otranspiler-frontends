#!/usr/bin/env bash
# gen_parser.sh — regenerate gen/ (the ANTLR4 Go Python parser) from
# grammars/Python3Lexer.g4 + grammars/Python3Parser.g4.
#
# The in-repo grammars are the upstream grammars-v4 files written for the
# Java target (actions use `this.`). The Go target generates lexer actions
# into `*_Action` methods with receiver `l` and predicates into `*_Sempred`
# methods with receiver `p`, and the parser always uses `p`; so the actions
# are retargeted here before generation. The hand-maintained base classes
# (gen/python3_lexer_base.go — indentation / NEWLINE-INDENT-DEDENT lexing,
# gen/python3_parser_base.go) are NOT regenerated.
#
# ANTLR is a dev-time tool only (the generated parser is committed):
#   ANTLR_JAR=/path/to/antlr-4.13.2-complete.jar ./tools/gen_parser.sh
set -euo pipefail
cd "$(dirname "$0")/.."

ANTLR_JAR="${ANTLR_JAR:-${HOME}/.antlr/antlr-4.13.2-complete.jar}"
if [ ! -f "$ANTLR_JAR" ]; then
  echo "gen_parser: ANTLR jar not found; set ANTLR_JAR to antlr-4.x-complete.jar" >&2
  exit 1
fi
command -v java >/dev/null || { echo "gen_parser: java required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "gen_parser: python3 required" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/out"

cp grammars/Python3Lexer.g4 grammars/Python3Parser.g4 "$tmp/"
python3 - "$tmp" <<'PY'
import os, re, sys
d = sys.argv[1]
# Lexer: predicate bodies land in *_Sempred (receiver p); action bodies land
# in *_Action (receiver l).
p = os.path.join(d, "Python3Lexer.g4")
s = open(p).read()
s = re.sub(r"\{[^{}]*\}\?", lambda m: m.group(0).replace("this.", "p."), s)
s = re.sub(r"\{[^{}]*\}", lambda m: m.group(0).replace("this.", "l."), s)
open(p, "w").write(s)
# Parser: actions and predicates both use receiver p.
p = os.path.join(d, "Python3Parser.g4")
s = open(p).read().replace("this.", "p.")
open(p, "w").write(s)
PY

# Generate from inside the temp dir with RELATIVE grammar names so the
# `Code generated from ...` header is stable (no temp path in the diff).
( cd "$tmp" && java -jar "$ANTLR_JAR" -Dlanguage=Go -package gen -visitor -o out Python3Lexer.g4 )
( cd "$tmp" && java -jar "$ANTLR_JAR" -Dlanguage=Go -package gen -visitor -o out \
  -lib out Python3Parser.g4 )

# Copy only the generated sources; the base classes stay hand-maintained.
for f in "$tmp/out"/python3_lexer.go "$tmp/out"/python3_parser.go "$tmp/out"/python3parser_*.go; do
  cp "$f" gen/
done
echo "gen_parser: regenerated gen/ ($(ls gen/python3*.go | wc -l) generated+base files)"
