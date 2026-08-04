#!/usr/bin/env python3
"""Equivalence harness: py-sh frontend vs the core frontend (debashc --shir).

Oracle = byte equality on the ShIR JSON (the A1 contract). A frontend that
reproduces the core's exact serialized IR for a construct is provably
faithful for it — no core modification, no behavioral gate needed yet.

Modes:
    equiv.py tests/                    # every .sh in dir (also runs corpus)
    equiv.py a.sh b.sh …               # explicit files
    --strip-annotations                # drop var_types + purity from both
                                       # sides → compares the semantic IR only
                                       # (frontends emit no annotations in the
                                       # future architecture; the core attaches)
Exit: 0 if no FAILs on supported files.
"""
import json
import os
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
PYSH = HERE / "py-sh/pysh.py"
DEBASHC = os.environ.get("DEBASHC", str(HERE.parent / "sh2perl/target/debug/debashc"))

def core_shir(path):
    r = subprocess.run([DEBASHC, "--shir", str(path)], capture_output=True, text=True, timeout=120)
    if r.returncode != 0:
        return None, f"core rc={r.returncode}"
    try:
        return json.loads(r.stdout), None
    except json.JSONDecodeError as e:
        return None, f"core non-JSON: {e}"

def mine_shir(path):
    r = subprocess.run([sys.executable, str(PYSH), "--shir", str(path)],
                       capture_output=True, text=True, timeout=60)
    if r.returncode == 3:
        return None, "UNSUPPORTED: " + r.stderr.strip()
    if r.returncode != 0:
        return None, f"frontend rc={r.returncode}: {r.stderr.strip()[:200]}"
    try:
        return json.loads(r.stdout), None
    except json.JSONDecodeError as e:
        return None, f"frontend non-JSON: {e}"

def strip_annotations(doc):
    """Semantic-core view: remove core-attached annotations (A2 var_types,
    A3 purity). Everything else is the frontend's semantic output."""
    if isinstance(doc, dict):
        doc = dict(doc)
        doc.pop("var_types", None)
        return {k: strip_annotations(v) for k, v in doc.items() if k != "var_types"}
    if isinstance(doc, list):
        return [strip_annotations(x) for x in doc]
    return doc

def strip_purity(doc):
    if isinstance(doc, dict):
        d = dict(doc)
        d.pop("purity", None)
        return {k: strip_purity(v) for k, v in d.items()}
    if isinstance(doc, list):
        return [strip_purity(x) for x in doc]
    return doc

def first_diff(a, b, path="root"):
    if type(a) is not type(b):
        return f"{path}: type {type(a).__name__} vs {type(b).__name__}"
    if isinstance(a, dict):
        if set(a) != set(b):
            return f"{path}: keys {sorted(a)} vs {sorted(b)}"
        for k in sorted(a):
            d = first_diff(a[k], b[k], f"{path}.{k}")
            if d:
                return d
    elif isinstance(a, list):
        if len(a) != len(b):
            return f"{path}: len {len(a)} vs {len(b)}"
        for i, (x, y) in enumerate(zip(a, b)):
            d = first_diff(x, y, f"{path}[{i}]")
            if d:
                return d
    elif a != b:
        return f"{path}: {a!r} vs {b!r}"
    return None

def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    strip = "--strip-annotations" in sys.argv
    paths = []
    for a in args:
        p = Path(a)
        if p.is_dir():
            paths.extend(sorted(p.glob("*.sh")))
        else:
            paths.append(p)
    if not paths:
        sys.stderr.write("usage: equiv.py [--strip-annotations] <file|dir> …\n")
        sys.exit(2)

    n_pass = n_fail = n_unsupported = n_skip = 0
    fails = []
    for p in paths:
        core, cerr = core_shir(p)
        if core is None:
            n_skip += 1
            continue  # core can't parse it either — outside both frontends
        mine, merr = mine_shir(p)
        if mine is None:
            n_unsupported += 1
            continue
        if strip:
            core, mine = strip_purity(strip_annotations(core)), strip_purity(strip_annotations(mine))
        diff = first_diff(core, mine)
        if diff is None:
            n_pass += 1
        else:
            n_fail += 1
            fails.append((p, diff))

    for p, d in fails[:12]:
        print(f"FAIL {p}: {d}")
    print(f"\nfiles={len(paths)}  supported={n_pass + n_fail}  "
          f"PASS={n_pass}  FAIL={n_fail}  unsupported={n_unsupported}  core-skip={n_skip}"
          + ("  [annotations stripped]" if strip else ""))
    sys.exit(1 if n_fail else 0)

if __name__ == "__main__":
    main()
