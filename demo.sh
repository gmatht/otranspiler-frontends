#!/usr/bin/env bash
# demo.sh — the REAL language frontends → ShIR → the JS backend.
#
# Pipeline:  source → <frontend> → ShIR JSON (A1 contract) → otranspilerl-cli
#            - --target estree → ESTree JSON → estree-runner → JS output
#
# Frontends (frontends/, each a hand-rolled lexer+parser+emitter):
#   posix-sh-go  — a POSIX-shell-subset frontend (the strongest)
#   perl-sh-go   — a shell-subset frontend
#   py-sh-go     — a shell-subset frontend (immature — the eval shows why)
#   go-sh        — a GO-subset frontend (fmt.Println / := / = of literals)
#
# Evaluation: (1) does the pipeline run, (2) does the JS output match the
# reference, (3) frontend ShIR byte-equality vs the CORE frontend
# (otranspilerl-cli --shir — the frontends' oracle).
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI="$ROOT/otranspilerl/target/debug/otranspilerl-cli"
RUNNER="node $ROOT/harness/estree-runner.mjs"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# a v1-subset shell demo (supported by the frontends)
cat > "$TMP/in.sh" <<'EOF'
# a comment
name=world
echo hello $name
EOF

echo "=== 1. the pipeline (posix-sh-go → ShIR → JS backend) ==="
frontends/posix-sh-go/posix-sh-go --shir "$TMP/in.sh" --raw 2>/dev/null > "$TMP/p.json"
$(CLI) - --target estree < "$TMP/p.json" 2>/dev/null > "$TMP/p_e.json"
echo "--- the generated JS (estree-gen view):"
node --input-type=module -e "
import { generate } from '$ROOT/harness/estree-gen.mjs';
import fs from 'fs';
console.log(generate(JSON.parse(fs.readFileSync('$TMP/p_e.json','utf8'))));
"
echo "--- run it:"
$RUNNER "$TMP/p_e.json" --source "$TMP/in.sh" 2>/dev/null
echo "--- bash (reference):"
bash "$TMP/in.sh" 2>/dev/null

echo
echo "=== 2. all shell frontends on a small battery ==="
run_one() { # frontend input → JS output
  local f=$1 src=$2
  if frontends/$f/$f --shir "$src" --raw 2>/dev/null > "$TMP/o.json"; then
    $(CLI) - --target estree < "$TMP/o.json" 2>/dev/null > "$TMP/o_e.json" \
      && $RUNNER "$TMP/o_e.json" --source "$src" 2>/dev/null | tr '\n' '|'
  else
    printf 'FRONTEND-ERR'
  fi
}
printf 'echo hello\n' > "$TMP/t1.sh"
printf 'x=5\necho $x\n' > "$TMP/t2.sh"
printf 'FOO=bar echo hi\n' > "$TMP/t3.sh"
printf 'echo "quoted $x"\n' > "$TMP/t4.sh"
printf '%-14s %-12s %-12s %-12s %-12s\n' "frontend" "echo" "assign" "env-pfx" "quoted"
for f in posix-sh-go perl-sh-go py-sh-go; do
  printf '%-14s' "$f"
  for t in t1 t2 t3 t4; do
    out=$(run_one "$f" "$TMP/$t.sh")
    ref=$(bash "$TMP/$t.sh" 2>/dev/null | tr '\n' '|')
    [ "$out" = "$ref" ] && mark="OK" || mark="✗"
    printf ' %-8s%-4s' "$(echo "$out" | head -c 6)" "$mark"
  done
  echo
done

echo
echo "=== 3. go-sh — the GO-subset frontend → JS ==="
cat > "$TMP/in.go" <<'EOF'
fmt.Println("hi from go")
n := 40
fmt.Println("n =", n)
EOF
if frontends/go-sh/go-sh --shir "$TMP/in.go" --raw 2>/dev/null > "$TMP/g.json"; then
  echo "--- the Go source lowered to ShIR (stmts):"
  node -e "const j=JSON.parse(require('fs').readFileSync('$TMP/g.json','utf8')); console.log(j.stmts.map(s=>s.type).join(','));"
  $(CLI) - --target estree < "$TMP/g.json" 2>/dev/null > "$TMP/g_e.json" \
    && $RUNNER "$TMP/g_e.json" --source "$TMP/in.go" 2>/dev/null
else
  echo "(go-sh rejected the input — its v1 subset is fmt.Println / := / = of LITERALS; a non-literal expression like n+2 is refused)"
fi

echo
echo "=== 4. the frontends' oracle: byte-equality with the CORE frontend ==="
for f in posix-sh-go perl-sh-go py-sh-go; do
  if frontends/$f/$f --shir "$TMP/in.sh" --raw 2>/dev/null > "$TMP/f.json"; then
    "$CLI" "$TMP/in.sh" --source-lang sh --target shir --raw 2>/dev/null > "$TMP/core.json"
    if diff -q "$TMP/f.json" "$TMP/core.json" >/dev/null 2>&1; then
      echo "$f: byte-identical to the core frontend ✓"
    else
      echo "$f: DIFFERS from the core frontend (node shape drift — see below)"
      diff <(node -e "console.log(Object.keys(JSON.parse(require('fs').readFileSync('$TMP/f.json','utf8')).stmts[0]))" 2>/dev/null) \
           <(node -e "console.log(Object.keys(JSON.parse(require('fs').readFileSync('$TMP/core.json','utf8')).stmts[0]))" 2>/dev/null) | head -4
    fi
  else
    echo "$f: rejected the input"
  fi
done
