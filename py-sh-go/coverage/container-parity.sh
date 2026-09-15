#!/usr/bin/env bash
# int-list container parity — `py2cy --pyx` rewrites an append-built list of
# i64 ints to a C `long long` vector. Unsupported uses must be left a Python
# list (REFUSE > GUESS) and still match CPython.
set -uo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
"$GO" build -o py2cy ./cmd/py2cy || exit 1
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
ok=0
fail=0
run() {
  local name="$1"
  printf '%s\n' "$2" > "$tmp/$name.py"
  if ! ./py2cy --pyx "$tmp/$name.py" > "$tmp/$name.pyx" 2>"$tmp/$name.err"; then
    echo "FAIL $name (annotate)"; fail=$((fail+1)); return
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

run append_len 'xs = []
for i in range(1000):
    xs.append(i * i)
print(len(xs), xs[3], xs[999])'
run append_only 'xs = []
n = 0
while n < 100:
    xs.append(n)
    n = n + 1
print(len(xs))'
run refuse_iter 'xs = []
for i in range(10):
    xs.append(i)
for v in xs:
    print(v)'
run refuse_nonint 'xs = []
for i in range(10):
    xs.append("s")
print(len(xs))'
run refuse_sum 'xs = []
for i in range(10):
    xs.append(i)
print(sum(xs))'
run refuse_bare 'xs = []
for i in range(3):
    xs.append(i)
print(xs)'

echo "container parity: $ok ok, $fail fail"
[ "$fail" -eq 0 ]
