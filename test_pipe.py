#!/usr/bin/env python3
"""Full-pipe end-to-end test (plan §2.2/§8.3): an independent frontend
(py-sh) emits ShIR JSON; the core's deserializer (debashc --shir-in-estree)
ingests it and emits ESTree. Validates the deserializer accepts a
*foreign* conformant emitter (the cargo round-trip tests prove
deser(serialize(core_prog)) is a fixed point for the core's own output;
this script proves the deserializer accepts an independent emitter).

Usage: test_pipe.py [file|dir ...]   (no args = full sh2perl/examples corpus)
Exit 0 if every supported example round-trips through the pipe."""
import json, os, subprocess, sys, tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEBASHC = os.environ.get("DEBASHC", str(HERE.parent / "sh2perl/target/debug/debashc"))
PYSH = HERE / "py-sh/pysh.py"
CORPUS = HERE.parent / "sh2perl/examples"

def collect(paths):
    ws = HERE.parent
    out = []
    for p in paths:
        P = Path(p)
        if not P.is_absolute():
            P = ws / p
        if P.is_dir():
            out.extend(sorted(P.glob("*.sh")))
        else:
            out.append(P)
    return out

def run_pipe(src_text):
    # Write source to a temp .sh file and pass the ABSOLUTE path to
    # pysh (pysh's heuristic treats ".sh"-containing args as file
    # paths; passing the source text directly hits a bug where ".sh"
    # inside the source fools the heuristic). The temp file makes the
    # path real so pysh opens it correctly regardless of cwd.
    import tempfile
    with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as sf:
        sf.write(src_text); src_path = sf.name
    try:
        r = subprocess.run([sys.executable, str(PYSH), "--shir", src_path, "--raw"],
                           capture_output=True, text=True, timeout=30)
    finally:
        os.unlink(src_path)
    if r.returncode == 3:
        return False, "UNSUPPORTED"
    if r.returncode != 0:
        return False, f"pysh rc={r.returncode}: {r.stderr.strip()[:160]}"
    shir_json = r.stdout.strip()
    if not shir_json.startswith("{"):
        return False, "pysh no-json"
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tf:
        tf.write(shir_json); tmp = tf.name
    try:
        r2 = subprocess.run([DEBASHC, "--shir-in-estree", tmp],
                            capture_output=True, text=True, timeout=30)
    finally:
        os.unlink(tmp)
    if r2.returncode != 0:
        return False, f"ingress rc={r2.returncode}: {r2.stderr.strip()[:160]}"
    estree = r2.stdout.strip()
    if not estree.startswith('{"type":"Program"'):
        return False, "not-estree-program"
    return True, None

def main():
    args = sys.argv[1:]
    files = collect(args) if args else sorted(CORPUS.glob("*.sh"))
    n_pass = n_unsup = n_fail = 0
    fails = []
    for f in files:
        try:
            src = f.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        ok, why = run_pipe(src)
        if not ok and why == "UNSUPPORTED":
            n_unsup += 1; continue
        if not ok:
            n_fail += 1; fails.append((f.name, why)); continue
        n_pass += 1
    for name, why in fails[:8]:
        print(f"FAIL {name}: {why}")
    print(f"\nfiles={len(files)}  pipe-ok={n_pass}  "
          f"unsupported={n_unsup}  fail={n_fail}")
    sys.exit(1 if n_fail else 0)

if __name__ == "__main__":
    main()
