#!/usr/bin/env bash
# semantics parity — adversarial programs where auto-typing could silently
# change Python semantics. The emitted Cython relies on the DEFAULT
# cdivision=False (Python semantics for /, //, % on C ints), and on refusing
# to type any operand of an expression that can wrap / whose operator
# differs (/mnt...: + - * & | ^ << >>).
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
"$GO" build -o py2cy ./cmd/py2cy || exit 1
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
ok=0
fail=0
run() { # <name> <source>
  local name="$1"
  printf '%s\n' "$2" > "$tmp/$name.py"
  if ! ./py2cy "$tmp/$name.py" > "$tmp/$name.pyx" 2>"$tmp/$name.err"; then
    echo "FAIL $name (annotate)"; fail=$((fail+1)); return
  fi
  if grep -q 'cdivision=True' "$tmp/$name.pyx"; then
    echo "FAIL $name (cdivision=True emitted)"; fail=$((fail+1)); return
  fi
  if ! cython --embed -3 "$tmp/$name.pyx" -o "$tmp/$name.c" 2>"$tmp/$name.cy"; then
    echo "FAIL $name (cython)"; fail=$((fail+1)); return
  fi
  if ! cc -O2 -o "$tmp/$name" "$tmp/$name.c" \
      $(python3-config --includes) $(python3-config --ldflags --embed) 2>/dev/null; then
    echo "FAIL $name (cc)"; fail=$((fail+1)); return
  fi
  local got want
  got="$(PYTHONUNBUFFERED=1 "$tmp/$name")"; want="$(PYTHONUNBUFFERED=1 python3 "$tmp/$name.py")"
  if [ "$got" = "$want" ]; then ok=$((ok+1)); else echo "FAIL $name [$got] vs [$want]"; fail=$((fail+1)); fi
}

run neg_mod 'a = -5
b = 3
print(a % b, a // b)'
run true_div 'a = 3
b = 2
print(a / b)'
run float_mix 'a = 7
b = 2.0
print(a + b, a * b, a / b)'
run overflow 'x = 9223372036854775807
y = x + 1
print(y)'
run shift 'a = 1
b = 100
c = a << b
print(c)'
run bitwise 'a = 255
b = 15
c = a & b
print(c)'
run aug_overflow 'x = 9223372036854775800
x += 100
print(x)'
run float_ops 'a = 1.5
b = 2.0
c = a * b + 1
d = 1
e = d / 2
print(c, e)'

run float_neg 'a = -5.0
b = 3.0
print(a % b, a // b, a / b)'

run div_unknown 'def f(a, b):
    return a / b
print(f(7.0, 2))'

run identity_is 'a = 1000
b = a + 0
print(a is b, a == b)'

run func_neg 'def f(a, b):
    c = a % b
    return c
print(f(-5, 3))'

echo "semantics parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
