#!/bin/bash
# coverage-gap.sh <lang> — report parser node types defined but NOT
# exercised by the frontend's testdata examples.
#
# The worker hook (worker-coverage-step.sh) calls this after a GREEN gate:
# a non-empty output means "the examples do not yet cover all the parser's
# nodes — create an example for one".
#
# Two detectors:
#   rust-frontend — syn node kinds: syn's typed-AST vocabulary (132 kinds)
#       minus the kinds exercised by the testdata parse trees
#       (frontends/coverage/syn-coverage/). The parser's real node types.
#   everything else — the A1-node proxy: these frontends are hand-rolled
#       parsers with no enumerated node inventory, so the observable
#       "parser output" is the A1 shIR they emit. Collect every node type
#       the testdata emits (stmt/expr/arith "type" fields) and report the
#       A1 vocabulary types none of them produce. Documented proxy — the
#       worker's pi reads the frontend's FRONTEND.md subset to judge which
#       are expressible (the gate is the final arbiter).
#
# Exclusions: one construct per line in frontends/coverage/refused-<lang>.txt
# (worker-appended when a proposed example for that gap was refused by the
# frontend — by design; stop retrying it) and bugs-<lang>.txt (worker-appended
# when the example failed the ORACLE — a frontend lowering bug, recorded for
# the worker to fix).
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FE="$ROOT/frontends"
DIR="$FE/coverage"
lang="$1"

# known-refused/known-bug exclusions (worker-appended)
excl="$DIR/refused-$lang.txt $DIR/bugs-$lang.txt"
exclude() {  # read stdin, drop lines matching the exclusion files
  local pat=""
  for e in $excl; do
    [ -f "$e" ] && pat="$pat$(tr '\n' '|' < "$e" | sed 's/|$//')"
  done
  # escalated contract gaps: skipped while the core-request is pending
  # (worker-coverage-step.sh prunes the ledger on completion)
  if [ -f "$DIR/core-pending-$lang.txt" ]; then
    pat="$pat$(cut -f1 "$DIR/core-pending-$lang.txt" | tr '\n' '|' | sed 's/|$//')"
  fi
  pat=$(printf '%s' "$pat" | sed 's/|$//')
  if [ -n "$pat" ]; then grep -vE "$pat"; else cat; fi
}

case "$lang" in
  rust-frontend)
    SYNC="$DIR/syn-coverage/target/debug/syn-coverage"
    if [ ! -x "$SYNC" ]; then
      (cd "$DIR/syn-coverage" && cargo build --offline >/dev/null 2>&1) || exit 0
    fi
    # expressible-but-uncovered syn kinds: kinds the frontend's v0.1
    # subset handles (the match arms in main.rs — see FRONTEND.md; keep
    # in sync) that no testdata example exercises. Refused kinds never
    # reach the worker — the actionable gap list.
    python3 - "$SYNC" "$FE/rust-frontend/testdata" <<'PY' | exclude
import re, subprocess, sys, glob
sync, td = sys.argv[1], sys.argv[2]
# the frontend's expressible set, per category (main.rs match arms)
handled_by = dict(
    Expr=set("Lit Path Binary Unary Paren Macro Assign If While ForLoop Return Range Block".split()),
    Stmt=set("Local Expr Macro".split()),
    Item=set("Fn".split()),
    Pat=set("Ident".split()),
    Type=set(),
    Lit=set("Int Str".split()),
    UnOp=set("Neg Not".split()),
    BinOp=set("Add Sub Mul Div Rem Eq Ne Lt Le Gt Ge And Or "
              "AddAssign SubAssign MulAssign DivAssign RemAssign".split()),
)
files = sorted(glob.glob(td + "/*.rs"))
out = subprocess.run([sync] + files, capture_output=True, text=True).stdout
cat = None
for line in out.splitlines():
    m = re.match(r"^(\w+): \d+/\d+", line)
    if m:
        cat = m.group(1)
        continue
    if line.startswith("  unused:") and cat in handled_by:
        for k in (s.strip() for s in line.replace("  unused:", "").split(",")):
            if k and k in handled_by[cat]:
                print(f"syn kind {k}")
PY
    ;;
  *)
    # per-frontend binary + testdata extension (+ dir override: cpp's
    # examples live in testdata_cpp/, not testdata/)
    td="testdata"
    case "$lang" in
      posix-sh-go) bin="$FE/posix-sh-go/posix-sh-go"; ext=sh ;;
      c-sh-go)     bin="$FE/c-sh-go/c-sh-go";         ext=c ;;
      cpp-sh-go)   bin="$FE/cpp-sh-go/cpp-sh-go";     ext=cc ; td=testdata_cpp ;;
      go-sh)       bin="$FE/go-sh/go-sh";             ext=go ;;
      py-sh-go)    bin="$FE/py-sh-go/py-sh-go";       ext=py ;;
      perl-sh-go)  bin="$FE/perl-sh-go/perl-sh-go";   ext=pl ;;
      fish-sh-go)  bin="$FE/fish-sh-go/fish-sh-go";   ext=fish ;;
      zsh-sh-go)   bin="$FE/zsh-sh-go/zsh-sh-go";     ext=zsh ;;
      bat-sh-go)   bin="$FE/bat-sh-go/bat-sh-go";     ext=bat ;;
      zig-sh-go)   bin="$FE/zig-sh-go/zig-sh-go";     ext=zig ;;
      powershell-sh-go) bin="$FE/powershell-sh-go/powershell-sh-go"; ext=ps1 ;;
      *) exit 0 ;;  # unknown frontend: no inventory
    esac
    [ -x "$bin" ] || exit 0
    # collect every node type the testdata emit produces
    T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
    : > "$T/types.txt"
    for f in "$FE/$lang"/$td/*."$ext"; do
      [ -f "$f" ] || continue
      case "$(basename "$f")" in *_refuse*|*_gap*) continue ;; esac
      timeout 30 "$bin" --shir "$f" --raw > "$T/o" 2>/dev/null || continue
      python3 - "$T/o" >> "$T/types.txt" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
def walk(v):
    if isinstance(v, dict):
        if "type" in v and isinstance(v["type"], str):
            print(v["type"])
        for x in v.values():
            walk(x)
    elif isinstance(v, list):
        for x in v:
            walk(x)
walk(d)
PY
    done
    sort -u "$T/types.txt" > "$T/used.txt"
    # A1 vocabulary (frontends/shir-contract/schema.json)
    python3 - "$T/used.txt" <<'PY' | exclude
import json, sys
s = json.load(open("/home/llm/sh2loop/frontends/shir-contract/schema.json"))
vocab = set(s["stmts"]) | set(s["exprs"]) | set(s["arith"])
used = set(l.strip() for l in open(sys.argv[1]) if l.strip())
for t in sorted(vocab - used):
    print("A1 node " + t)
PY
    ;;
esac
exit 0
