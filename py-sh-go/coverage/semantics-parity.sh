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

run aug_float_keep 'x = 0.0
i = 0
while i < 3:
    x += i * 0.5
    i = i + 1
print(x)'
run aug_float_refuse 'def f():
    return 2.5
x = 0.0
x += f()
print(x)'

run scope_func_float 'def f():
    y = 1.5
    return y
print(f())'
run walrus_lit 'y = (n := 5)
print(n, y)'
run walrus_refuse 'x = 1
y = [(x := str(i)) for i in range(3)]
print(x, y)'
run with_refuse 'fh = 0
with open("/dev/null") as fh:
    print(fh.read(1) == "")'
run except_refuse 'e = 0
try:
    1/0
except ZeroDivisionError as e:
    print(type(e).__name__)'
run del_ok 'x = 1
del x
print("done")'
run annassign_value 'x = 1.5
x: str = "s"
print(x)'
run import_as 'x = 1
import os as x
print(x.__name__)'
run global_aug 'x = 0
def f():
    global x
    x *= 1.5
f()
print(x, type(x).__name__)'

run identity_is 'a = 1000
b = a + 0
print(a is b, a == b)'

run func_neg 'def f(a, b):
    c = a % b
    return c
print(f(-5, 3))'

run pep695_generic 'def first[T](xs: list[T]) -> T:
    return xs[0]
print(first([10, 20]))'
run pep695_bound 'def pick[T: int](x: T) -> T:
    return x
print(pick(3))'
run pep695_class 'class Box[T]:
    def __init__(self, v: T) -> None:
        self.v = v
    def get(self) -> T:
        return self.v
print(Box("s").get())'
run pep695_alias 'type IntList = list[int]
def total(xs: IntList) -> int:
    s = 0
    for x in xs:
        s += x
    return s
print(total([1, 2, 3]))'

echo "semantics parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]

# The entry-guarded dual arm (docs/AUTO_CYTHON.md §11): both sides of the guard
# must agree with CPython. The out-of-range calls (a 101-bit int, a float, a
# negative) are the adversarial half — a missing or too-wide guard would wrap,
# raise, or coerce them to a C integer.
run dual_guard_both_arms 'def scaled(n):
    t = 0
    for i in range(20):
        t = (t + i * n) % 1000000007
    return t
print(scaled(0))
print(scaled(2147483647))
print(scaled(2147483648))
print(scaled(2 ** 100))
print(scaled(2.5))
print(scaled(-1))'
