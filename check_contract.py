#!/usr/bin/env python3
"""Cross-implementer contract checks (workspace-side; no core change).

Catches drift between the core (debashc --shir), pysh, go-sh, and the
schema. Catches the class of bug that landed repeatedly this session:
one side updates (e.g. a new field) and the others silently disagree.

Two checks:
  1. contract_version: pysh, go-sh, schema, and the core's exported JSON
     all agree on version 1.
  2. JSON round-trip: for every corpus example, emit via pysh + go-sh,
     ingest via debashc --shir-in-estree, and verify the deserializer
     accepts it (exit 0 + valid ESTree Program). Catches ingress
     validation drift at scale.

Run from the workspace root (frontends/). Exits non-zero on any
mismatch; this is the gate a future "bump contract_version" PR
must pass in reverse (update all sides, then run this)."""
import json, os, re, subprocess, sys, tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
WS = HERE.parent
DEBASHC = os.environ.get("DEBASHC", str(WS / "sh2perl/target/debug/debashc"))
PYSH = HERE / "py-sh/pysh.py"
GOSH_SRC = HERE / "go-sh/go-sh.go"  # source, not the compiled binary
GOSH = HERE / "go-sh/go-sh"        # compiled binary (for execution)
SCHEMA = HERE / "shir-contract/schema.json"
CORPUS = WS / "sh2perl/examples"

# Accept Python (`X = 1`) and Go (`const X = 1`) declaration styles.
_V1_DECL = re.compile(r"(?:const\s+)?CONTRACT_VERSION\s*=\s*1\b")
def _has_v1_decl(src):
    return bool(_V1_DECL.search(src))

def read_text(p):
    return p.read_text(encoding="utf-8", errors="replace")


def check_contract_version():
    """All four sides agree on contract_version == 1."""
    issues = []
    # core: emit a tiny program, parse, check the field
    r = subprocess.run([DEBASHC, "--shir", "-", "--raw"],
                       input="echo x\n", capture_output=True, text=True, timeout=30)
    if r.returncode != 0:
        issues.append(("core --shir", f"rc={r.returncode}"))
    else:
        try:
            core_doc = json.loads(r.stdout)
            if core_doc.get("contract_version") != 1:
                issues.append(("core --shir", core_doc.get("contract_version")))
        except json.JSONDecodeError as e:
            issues.append(("core --shir", f"non-JSON: {e}"))
    # pysh + go-sh: grep the constant in the source
    if not _has_v1_decl(read_text(PYSH)):
        issues.append(("pysh", "no CONTRACT_VERSION = 1"))
    if not _has_v1_decl(read_text(GOSH_SRC)):
        issues.append(("go-sh", "no CONTRACT_VERSION = 1"))
    # schema
    try:
        schema = json.loads(read_text(SCHEMA))
        if schema.get("_contract_version") != 1:
            issues.append(("schema", schema.get("_contract_version")))
    except json.JSONDecodeError as e:
        issues.append(("schema", f"non-JSON: {e}"))
    return issues


def check_json_roundtrip():
    """For every corpus example, emit via pysh + go-sh, ingest via
    debashc --shir-in-estree, and verify the deserializer accepts it
    (exit 0 + valid ESTree Program). Catches ingress-validation drift."""
    n = n_pass = n_unsup_pysh = n_unsup_go = n_fail = 0
    for f in sorted(CORPUS.glob("*.sh")):
        n += 1
        src = read_text(f)
        # pysh is a .py file (no execute bit); invoke via sys.executable.
        # go-sh is a compiled binary with an execute bit; invoke directly.
        for label, runner, prepend_python in [
            ("pysh", PYSH, True),
            ("go-sh", GOSH, False),
        ]:
            with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as sf:
                sf.write(src); tmp = sf.name
            try:
                cmd = ([sys.executable, str(runner)] if prepend_python else [str(runner)])
                r = subprocess.run(cmd + ["--shir", tmp, "--raw"],
                                   capture_output=True, text=True, timeout=30)
            finally:
                os.unlink(tmp)
            if r.returncode == 3:
                if label == "pysh": n_unsup_pysh += 1
                else: n_unsup_go += 1
                continue
            if r.returncode != 0:
                n_fail += 1
                continue
            shir_json = r.stdout.strip()
            with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as jf:
                jf.write(shir_json); jtmp = jf.name
            try:
                r2 = subprocess.run([DEBASHC, "--shir-in-estree", jtmp],
                                    capture_output=True, text=True, timeout=30)
            finally:
                os.unlink(jtmp)
            if r2.returncode != 0:
                n_fail += 1
                continue
            if not r2.stdout.lstrip().startswith('{"type":"Program"'):
                n_fail += 1
                continue
            n_pass += 1
    return n, n_pass, n_unsup_pysh, n_unsup_go, n_fail


def main():
    print("=== contract version consistency ===")
    issues = check_contract_version()
    if issues:
        for side, val in issues:
            print(f"  MISMATCH: {side} = {val!r}")
        sys.exit(1)
    print("  all four sides (core --shir, pysh, go-sh, schema) agree: contract_version = 1 OK")
    print()
    print("=== JSON round-trip / ingress acceptance (corpus) ===")
    n, n_pass, nu_p, nu_g, n_fail = check_json_roundtrip()
    print(f"  files={n}  pass={n_pass}  "
          f"unsupported_pysh={nu_p}  unsupported_go-sh={nu_g}  fail={n_fail}")
    if n_fail:
        sys.exit(1)
    print("  deserializer accepted every pysh + go-sh emit (no ingress failures) OK")


if __name__ == "__main__":
    main()
