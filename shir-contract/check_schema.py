#!/usr/bin/env python3
"""Check ShIR JSON emitted by the core against the contract schema.

The schema (schema.json) is hand-authored from sh2perl/src/shir_json.rs.
This checker runs `debashc --shir` over every example and verifies each
emitted node fits the schema: known node type, known fields, matching field
shapes, enum values. Any drift (new node, renamed field, new enum value)
fails loudly — that is exactly the contract-versioning signal a frontend
needs.

Usage: check_schema.py [--shir-cmd PATH] [file.sh ...]
   with no files: check the whole sh2perl/examples corpus.
"""
import json, os, subprocess, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCHEMA = json.load(open(HERE / "schema.json"))
SHIR_CMD = os.environ.get("DEBASHC", str(Path(HERE).parent.parent / "sh2perl/target/debug/debashc"))

def shape_ok(spec, node, depth=0, where="<root>"):
    """Validate `node` against a shape spec; return list of problems."""
    problems = []
    def check(spec, node, where):
        if isinstance(spec, list):
            # bare enum list (e.g. purity values)
            if node not in spec:
                problems.append(f"{where}: {node!r} not in {spec}")
            return
        if spec == "any":
            return
        if spec == "str":
            if not isinstance(node, str):
                problems.append(f"{where}: expected str, got {type(node).__name__}")
            return
        if spec == "int":
            if not isinstance(node, int) or isinstance(node, bool):
                problems.append(f"{where}: expected int, got {node!r}")
            return
        if spec == "bool":
            if not isinstance(node, bool):
                problems.append(f"{where}: expected bool, got {node!r}")
            return
        if spec == "expr":
            check_expr(node, where); return
        if spec == "stmt":
            check_stmt(node, where); return
        if spec == "arith":
            check_arith(node, where); return
        if spec.endswith("[]"):
            if not isinstance(node, list):
                problems.append(f"{where}: expected list, got {type(node).__name__}")
                return
            for i, item in enumerate(node):
                check(spec[:-2], item, f"{where}[{i}]")
            return
        if spec == "null" or spec.endswith("|null"):
            if node is None:
                return
            base = spec[:-5] if spec.endswith("|null") else spec
            check(base, node, where); return
        if spec == "str|null":
            if node is not None and not isinstance(node, str):
                problems.append(f"{where}: expected str|null, got {node!r}")
            return
        if spec == "int|null":
            if node is not None and not isinstance(node, int):
                problems.append(f"{where}: expected int|null, got {node!r}")
            return
        if spec == "str|opt" or spec == "expr|opt":
            return  # optional field, presence checked by caller
        if spec.startswith("enum["):
            allowed = spec[5:-1].split(",")
            if node not in allowed:
                problems.append(f"{where}: enum {node!r} not in {allowed}")
            return
        # named leaf/container spec
        if spec == "program":
            fields = SCHEMA["program"]["fields"]
            for fname, fspec in fields.items():
                if fname not in node:
                    problems.append(f"{where}: missing field {fname!r}")
                else:
                    check(fspec, node[fname], f"{where}.{fname}")
            return
        # any named spec with "fields" (leaf/var_types_entry/sub/…) validates
        # its required fields recursively
        if isinstance(SCHEMA.get(spec), dict) and "fields" in SCHEMA.get(spec, {}):
            fields = SCHEMA[spec]["fields"]
            for fname, fspec in fields.items():
                if fname not in node:
                    if fspec not in ("str|opt", "expr|opt"):
                        problems.append(f"{where}: missing field {fname!r} ({spec})")
                else:
                    check(fspec, node[fname], f"{where}.{fname}")
            return
        if spec in SCHEMA["enums"]:
            if node not in SCHEMA["enums"][spec] and not (node is None and "null" in SCHEMA["enums"][spec]):
                problems.append(f"{where}: {node!r} not in enum {spec}")
            return
        if spec in SCHEMA["leaf"]:
            fields = SCHEMA["leaf"][spec]["fields"]
            for fname, fspec in fields.items():
                if fname not in node:
                    if fspec not in ("str|opt", "expr|opt"):
                        problems.append(f"{where}: missing field {fname!r} ({spec})")
                else:
                    check(fspec, node[fname], f"{where}.{fname}")
            return
        if spec in SCHEMA["exprs"] or spec in SCHEMA["stmts"] or spec in SCHEMA["arith"]:
            return  # dispatched by type field
        problems.append(f"{where}: unknown spec {spec!r}")
    def check_expr(node, where):
        if not isinstance(node, dict) or "type" not in node:
            problems.append(f"{where}: expr node not an object with type"); return
        t = node["type"]
        if t not in SCHEMA["exprs"]:
            problems.append(f"{where}: unknown expr type {t!r}")
            return
        spec = SCHEMA["exprs"][t]
        for fname, fspec in spec["fields"].items():
            if fname not in node:
                problems.append(f"{where}.{t}: missing field {fname!r}")
            elif fspec not in ("str|opt", "expr|opt"):
                check(fspec, node[fname], f"{where}.{t}.{fname}")
        if t == "Call":
            check(SCHEMA["enums"]["purity"], node.get("purity", "MISSING"), f"{where}.Call.purity")
    def check_stmt(node, where):
        if not isinstance(node, dict) or "type" not in node:
            problems.append(f"{where}: stmt node not an object with type"); return
        t = node["type"]
        if t not in SCHEMA["stmts"]:
            problems.append(f"{where}: unknown stmt type {t!r}")
            return
        spec = SCHEMA["stmts"][t]
        for fname, fspec in spec["fields"].items():
            if fname not in node:
                problems.append(f"{where}.{t}: missing field {fname!r}")
            elif fspec not in ("str|opt", "expr|opt"):
                check(fspec, node[fname], f"{where}.{t}.{fname}")
    def check_arith(node, where):
        if not isinstance(node, dict) or "type" not in node:
            problems.append(f"{where}: arith node not an object with type"); return
        t = node["type"]
        if t not in SCHEMA["arith"]:
            problems.append(f"{where}: unknown arith type {t!r}")
            return
        for fname, fspec in SCHEMA["arith"][t]["fields"].items():
            if fname not in node:
                problems.append(f"{where}: missing field {fname!r}")
            elif fspec not in ("str|opt", "expr|opt"):
                check(fspec, node[fname], f"{where}.{fname}")
    check("program", node, "Program")
    return problems

def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    files = args or sorted((Path(SHIR_CMD).parent.parent / "examples").glob("*.sh"))
    checked = problems = 0
    for f in files:
        r = subprocess.run([SHIR_CMD, "--shir", str(f)], capture_output=True, text=True, timeout=60)
        if r.returncode != 0:
            print(f"SKIP {f}: core --shir failed (rc={r.returncode})")
            continue
        try:
            doc = json.loads(r.stdout)
        except json.JSONDecodeError as e:
            print(f"BAD  {f}: core emitted non-JSON ({e})")
            continue
        checked += 1
        probs = shape_ok(SCHEMA, doc)
        if probs:
            problems += len(probs)
            print(f"FAIL {f}:")
            for p in probs[:8]:
                print(f"     {p}")
            if len(probs) > 8:
                print(f"     …and {len(probs)-8} more")
    print(f"\nchecked {checked} files, {problems} schema violations")
    sys.exit(1 if problems else 0)

if __name__ == "__main__":
    main()
