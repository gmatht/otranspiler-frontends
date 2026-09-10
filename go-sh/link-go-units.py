#!/usr/bin/env python3
# link-go-units.py — compose several go-sh A1 programs into one linked A1.
#
# Each input is a `go-sh --shir --raw` emit (a library unit's Functions +
# consts, or the CLI's entry statements). The link concatenates stmts
# (units in argv order) after auditing:
#   - no duplicate user Function names across units (bare-name resolution:
#     `golib.Shir` lowers to sub `Shir`, which must be defined exactly once)
#   - no duplicate top-level vars across units (entry vars share the
#     global store with library globals)
#   - no private (`__*`) Function names (per-file tmp counters would
#     collide: `__iife_0` in two units would overwrite before either entry
#     runs). `__*` STORE temps (`__mr_N`, `__tmp_mN`) are sequencing-safe
#     (assigned+read atomically around a synchronous capture) and pass.
#
# Usage: link-go-units.py out.a1.json unit1.a1.json unit2.a1.json ...
import json
import sys


def main():
    out_path = sys.argv[1]
    merged = None
    seen_fns = {}
    seen_vars = {}
    for path in sys.argv[2:]:
        with open(path) as f:
            a1 = json.load(f)
        if merged is None:
            merged = {k: v for k, v in a1.items() if k != 'stmts'}
            merged['stmts'] = []
        for s in a1.get('stmts', []):
            if s.get('type') == 'Function':
                nm = s.get('name', '')
                if nm.startswith('__'):
                    sys.exit('link: private Function %r in %s — per-file counters collide' % (nm, path))
                if nm in seen_fns:
                    sys.exit('link: duplicate Function %r (%s vs %s)' % (nm, seen_fns[nm], path))
                seen_fns[nm] = path
            elif s.get('type') == 'Assign':
                for t in s.get('targets', []):
                    v = t.get('var')
                    if v in seen_vars:
                        sys.exit('link: duplicate top-level var %r (%s vs %s)' % (v, seen_vars[v], path))
                    seen_vars[v] = path
            merged['stmts'].append(s)
    with open(out_path, 'w') as f:
        json.dump(merged, f)
    print('link: %d stmts, %d functions -> %s' % (len(merged['stmts']), len(seen_fns), out_path))


main()
