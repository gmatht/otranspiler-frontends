"""Walrus operator (`:=`) coverage — the grammar gap that used to be the
only Python-3 construct the ANTLR front end rejected. Parsed, never run."""


def scan(f, data):
    if (n := len(data)) > 3:
        print(n)
    if chunk := f.read():
        pass
    while line := f.readline():
        print(line)
    while (m := re.match(p, line)) is not None:
        print(m)
    print(total := n + 1)
    vals = [y := f(x) for x in data]
    pairs = {(k := key(i)): i for i in range(3)}
    g = lambda z: (w := z * 2) + w
    return (a := 1) + (b := 2)
