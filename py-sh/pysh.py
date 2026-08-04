#!/usr/bin/env python3
"""py-sh — first non-Rust frontend for the sh2perl ecosystem.

Parses a shell subset and emits the language-neutral ShIR JSON (the A1
contract, `debashc --shir`), attaching the same conservative annotations
(var_types / A2, purity / A3) so that on its supported subset the output is
BYTE-IDENTICAL to the core frontend. The oracle is frontends/equiv.py.

Usage:
    pysh.py --shir <file.sh>      # file
    pysh.py --shir <string>       # direct input
    pysh.py --shir <file.sh> --raw  # dump JSON to stdout, no banner

Design notes (see frontends/plan.md §5 for the pinned shapes):
- frontends parse; they do not optimize. var_types/purity are attached here
  ONLY so the byte-equality oracle works; the architecture keeps them as
  core-attached annotations (equiv.py --strip-annotations compares the
  semantic core without them).
- Anything outside the v1 subset raises Unsupported() — fail loudly, never
  guess a shape.
"""
import json
import sys

# ── A3 purity table (mirror of shir.rs SYNC_BUILTINS + shir_json.rs call_purity) ──
SYNC_BUILTINS = {
    ".", ":", "basename", "break", "cat", "cd", "cmp", "comm", "continue",
    "cut", "declare", "dirname", "echo", "eval", "exit", "export", "false",
    "head", "let", "local", "mapfile", "mktemp", "printf", "pwd", "read",
    "readarray", "readonly", "return", "seq", "set", "shift", "sort",
    "source", "stat", "tail", "test", "touch", "trap", "true", "type",
    "typeset", "uniq", "unset", "wc",
}

# Builtins whose semantics are outside the v1 subset (store writes / arith /
# control flow): refusing them is honest — "unsupported" beats a wrong shape.
# A1 contract version (must match sh2perl src/shir_json.rs CONTRACT_VERSION).
CONTRACT_VERSION = 1

REFUSE_BUILTINS = {
    "let", "eval", "declare", "typeset", "local", "readonly", "export",
    "unset", "read", "readarray", "mapfile", "shift", "source", "trap",
    "set", "exec", "test", "time", "[", "[[", ".",
}
# Compound-command keywords that need real parsers (v1: simple commands only).
KEYWORDS = {
    "if", "then", "else", "elif", "fi", "while", "do", "done", "until",
    "for", "case", "esac", "function", "select", "[[", "!", "{", "}",
}

class Unsupported(Exception):
    """Construct outside the v1 subset — the frontend refuses rather than guesses."""


# ── helpers: JSON leaf builders (keys must serialize alphabetically) ──
def O(d):  # dict
    return d

def stmt(type_, **kw):
    return O(dict(type=type_, **kw))

def expr(type_, **kw):
    return O(dict(type=type_, **kw))

def str_(s, style="DoubleQuoted"):
    return expr("Str", value=s, style=style)

def get_var(name):
    return expr("Call", func="getVar", args=[str_(name)], purity="Emulable")

def exec_call(cmd, args, env=None):
    call_args = [str_(cmd), expr("Array", elements=args)]
    if env:
        call_args.append(expr("Object", properties=[
            O(dict(key=k, value=v)) for k, v in env
        ]))
    return expr("Call", func="exec", args=call_args,
                purity="Emulable" if cmd in SYNC_BUILTINS else "Spawn")

def interp(parts):
    return expr("Interpolate", parts=[
        O(dict(kind="lit", text=t_or_e)) if k == "lit" else O(dict(kind="expr", expr=t_or_e))
        for k, t_or_e in parts
    ])

def dump(program):
    return json.dumps(program, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


# ── word scanning ─────────────────────────────────────────────────────
# Segments: ("lit", text) | ("sq", text) | ("dq", [("lit",t)|("ref",n)]) | ("ref", n)
# v1 restricts: no ${}, $((, $(, backtick, positional params, tilde, globs.
_IDENT_RE = None  # not needed as regex; manual scan below

def scan_word(src, i):
    """Scan one word starting at src[i]; return (segments, starts_with_dq, next_i)."""
    segs = []
    starts_with_dq = False
    first = True
    while i < len(src):
        c = src[i]
        if c in " \t;|&()<>\n":
            break
        if c == "#" and first:
            break  # comment to EOL (word-initial #)
        if c == "'":
            j = src.find("'", i + 1)
            if j < 0:
                raise Unsupported("unterminated single quote")
            segs.append(("sq", src[i + 1:j]))
            i = j + 1
        elif c == '"':
            if first:
                starts_with_dq = True
            parts = []
            j = i + 1
            has_newline = False
            has_esc_quote = False
            while j < len(src):
                if src[j] == "\n":
                    has_newline = True
                if src[j] == "\\" and j + 1 < len(src) and src[j + 1] == '"':
                    has_esc_quote = True
                if src[j] == '"':
                    break
                if src[j] == "\\" and j + 1 < len(src):
                    nxt = src[j + 1]
                    if nxt == "\n":
                        j += 2  # dq line continuation: \<newline> drops both
                        continue
                    if nxt == "\\":
                        parts.append(("lit", "\\"))  # \\ → \
                        j += 2
                        continue
                    if nxt == "$":
                        parts.append(("lit", "$"))  # \$ → $ (no expansion)
                        j += 2
                        continue
                    parts.append(("lit", src[j] + nxt))  # \X kept raw
                    j += 2
                    continue
                if src[j] == "$":
                    n, j2 = scan_ref(src, j)
                    parts.append(("ref", n))
                    j = j2
                    continue
                # accumulate literal run
                k = j
                while k < len(src) and src[k] not in '"\\$':
                    k += 1
                if "\n" in src[j:k]:
                    has_newline = True
                parts.append(("lit", src[j:k]))
                j = k
            if j >= len(src):
                raise Unsupported("unterminated double quote")
            if has_newline and has_esc_quote:
                # pinned: core's \" handling inside MULTILINE dq differs from
                # single-line (multiline-dq-hashbang) — refuse rather than guess
                raise Unsupported("escaped quote inside multiline double-quoted word")
            segs.append(("dq", parts))
            i = j + 1
        elif c == "$":
            n, i = scan_ref(src, i)
            segs.append(("ref", n))
        elif c == "\\":
            if i + 1 >= len(src):
                raise Unsupported("trailing backslash")
            # unquoted \X → X (pinned: \$x→$x, a\ b→a b, \\n stays as newline)
            segs.append(("lit", src[i + 1]))
            i += 2
        elif c == "`" or c == "{":
            raise Unsupported(f"construct char {c!r} outside v1 subset")
        else:
            # accumulate a run of literal chars (quotes/refs/special end it)
            k = i
            while k < len(src) and src[k] not in " \t;|&()<>\n'\"$`#\\":
                k += 1
            if k == i:
                segs.append(("lit", c))
                i += 1
            else:
                segs.append(("lit", src[i:k]))
                i = k
        first = False
    return segs, starts_with_dq, i

def scan_ref(src, i):
    """src[i] == '$'. v1: only $name; ${…}, $(…), $((…)), $1, $@ → Unsupported."""
    assert src[i] == "$"
    if i + 1 < len(src) and src[i + 1] in "({[`":
        raise Unsupported("$ with complex expansion outside v1 subset")
    j = i + 1
    if j < len(src) and (src[j].isalpha() or src[j] == "_"):
        k = j
        while k < len(src) and (src[k].isalnum() or src[k] == "_"):
            k += 1
        return src[j:k], k
    raise Unsupported(f"$ followed by {src[j] if j < len(src) else 'EOF'!r} outside v1 subset")

def segs_to_text(segs):
    """Concatenated literal text (quote markers stripped, refs excluded)."""
    out = []
    for s in segs:
        if s[0] == "lit":
            out.append(s[1])
        elif s[0] == "sq":
            out.append(s[1])
        elif s[0] == "dq":
            out.append("".join(p[1] for p in s[1] if p[0] == "lit"))
    return "".join(out)

def word_expr(segs, starts_with_dq):
    """Shape a word into an IrExpr — the pinned rules (plan.md §5)."""
    refs = [s for s in segs if s[0] == "ref" or (s[0] == "dq" and any(p[0] == "ref" for p in s[1]))]
    if not refs:
        text = segs_to_text(segs)
        if starts_with_dq:
            return interp([("lit", text)])
        return str_(text)
    parts = []
    for s in segs:
        if s[0] == "lit":
            parts.append(("lit", s[1]))
        elif s[0] == "sq":
            parts.append(("lit", s[1]))
        elif s[0] == "ref":
            parts.append(("expr", get_var(s[1])))
        elif s[0] == "dq":
            for p in s[1]:
                parts.append(("expr", get_var(p[1])) if p[0] == "ref" else ("lit", p[1]))
    if len(parts) == 1 and parts[0][0] == "expr":
        return parts[0][1]  # whole word = one ref → getVar directly
    return interp(parts)


# ── statement parsing ─────────────────────────────────────────────────
_ASSIGN_RE = None

def split_assignment(segs, starts_with_dq):
    """If the word is `name=value…` (name in first lit segment), return
    (name, value_segs, value_starts_dq) else None. v1: the `=` must sit in
    the FIRST segment and it must be a plain literal (unquoted)."""
    if starts_with_dq or not segs:
        return None
    first = segs[0]
    if first[0] != "lit":
        return None
    text = first[1]
    eq = text.find("=")
    if eq < 0:
        return None
    name = text[:eq]
    if not (name and (name[0].isalpha() or name[0] == "_") and
            all(c.isalnum() or c == "_" for c in name)):
        return None
    value_segs = []
    if eq + 1 < len(text):
        value_segs.append(("lit", text[eq + 1:]))
    value_segs += segs[1:]
    # drop a leading empty lit segment (x="a b" → lit "x=" + dq)
    if value_segs and value_segs[0] == ("lit", ""):
        value_segs = value_segs[1:]
    value_starts_dq = bool(value_segs) and value_segs[0][0] == "dq"
    return name, value_segs, value_starts_dq

def parse_line(src, i=0):
    """Parse one statement from src[i:] (until ; or newline or EOF).
    Returns (words, next_i). Raises Unsupported."""
    words = []
    n = len(src)
    while i < n:
        c = src[i]
        if c in " \t":
            i += 1
            continue
        if c == "#":
            # comment to EOL (not EOF — parse_line works on the whole src)
            nl = src.find("\n", i)
            return words, (n if nl < 0 else nl)
        if c in ";|\n":
            return words, i
        if c in "&()":
            raise Unsupported(f"operator {c!r} outside v1 subset")
        prev_i = i
        segs, swdq, i = scan_word(src, i)
        if not segs:
            if i == prev_i:
                raise Unsupported(f"operator {src[i]!r} outside v1 subset")
            continue
        words.append((segs, swdq))
    return words, n

def parse_statement(words):
    """words → list of IrStmt (Assign stmts and/or one exec Expr)."""
    stmts = []
    assigns = []   # (name, value_expr) env-prefix form
    i = 0
    while i < len(words):
        segs, swdq = words[i]
        asg = split_assignment(segs, swdq)
        if asg is None:
            break
        name, value_segs, value_swdq = asg
        value_expr = word_expr(value_segs, value_swdq)
        assigns.append((name, value_expr))
        i += 1
    if i < len(words):
        # command: word i is the command name, rest are args
        cmd_segs, cmd_swdq = words[i]
        if any(s[0] != "lit" for s in cmd_segs):
            # v1: command name must be a plain literal (no refs/quotes)
            raise Unsupported("command name with refs/quotes outside v1 subset")
        cmd = segs_to_text(cmd_segs)
        if not cmd:
            raise Unsupported("empty command name")
        if cmd in KEYWORDS or cmd in REFUSE_BUILTINS:
            raise Unsupported(f"{cmd!r} outside v1 subset (keyword/builtin)")
        args = [word_expr(segs, swdq) for segs, swdq in words[i + 1:]]
        stmts.append(stmt("Expr", expr=exec_call(cmd, args, env=assigns)))
    else:
        for name, value_expr in assigns:
            stmts.append(stmt("Assign", targets=[
                O(dict(var=name, sigil=None, indices=[]))
            ], expr=value_expr))
    return stmts


# ── A2 var_types (subset of analyze_var_types) ────────────────────────
def _collect(stmts):
    """v1: Assign stmts → sources; exec env Object props → excluded."""
    assigns = {}
    excluded = set()
    def walk_expr(e):
        if not isinstance(e, dict):
            return
        t = e.get("type")
        if t == "Call":
            if e.get("func") == "exec":
                for a in e.get("args", [])[2:]:  # env Object is args[2]
                    if a.get("type") == "Object":
                        for p in a.get("properties", []):
                            excluded.add(p.get("key"))
            for a in e.get("args", []):
                walk_expr(a)
        elif t == "Array":
            for el in e.get("elements", []):
                walk_expr(el)
        elif t == "Object":
            for p in e.get("properties", []):
                walk_expr(p.get("value"))
        elif t == "Interpolate":
            for p in e.get("parts", []):
                walk_expr(p.get("expr"))
    def walk_stmt(s):
        if s.get("type") == "Assign":
            for tgt in s.get("targets", []):
                if not tgt.get("indices"):
                    assigns.setdefault(tgt["var"], []).append(s["expr"])
                else:
                    excluded.add(tgt["var"])
            walk_expr(s.get("expr"))
        elif s.get("type") == "Expr":
            walk_expr(s.get("expr"))
    for s in stmts:
        walk_stmt(s)
    return assigns, excluded

def _source_numeric(e, lifted):
    t = e.get("type")
    if t == "Int":
        return True
    if t == "Str":
        try:
            int(e["value"].strip())
            return True
        except ValueError:
            return False
    if t == "Call" and e.get("func") == "getVar":
        arg = e.get("args", [{}])[0]
        return arg.get("type") == "Str" and arg.get("value") in lifted
    return False

def _source_string_lit(e, lifted):
    t = e.get("type")
    if t == "Str":
        # pinned: the core does NOT lift multi-line string values
        return "\n" not in e.get("value", "")
    if t == "Interpolate":
        # pinned: any interpolation is string-typed by construction
        # (dest_root=$emmccheck'p3' → Str verdict)
        return True
    if t == "Call" and e.get("func") == "getVar":
        arg = e.get("args", [{}])[0]
        return arg.get("type") == "Str" and arg.get("value") in lifted
    return False

def var_types(stmts):
    """Mirror analyze_var_types for the v1 subset: lifted-only, sorted names.
    numeric lift: every source parses as i64 or getVar-of-lifted.
    string lift: every source is a literal Str / all-lit Interpolate or
    getVar-of-lifted; numeric-lifted vars excluded from the string pass."""
    assigns, excluded = _collect(stmts)
    numeric = set()
    changed = True
    while changed:
        changed = False
        for name, sources in assigns.items():
            if name in numeric or name in excluded:
                continue
            if all(_source_numeric(s, numeric) for s in sources):
                numeric.add(name)
                changed = True
    string = set()
    changed = True
    while changed:
        changed = False
        for name, sources in assigns.items():
            if name in numeric or name in string or name in excluded:
                continue
            if all(_source_string_lit(s, string) for s in sources):
                string.add(name)
                changed = True
    return [O(dict(name=n, type="Int" if n in numeric else "Str"))
            for n in sorted(numeric | string)]


# ── program assembly ──────────────────────────────────────────────────
def compile_(src):
    """shell subset → ShIR JSON doc (dict). Raises Unsupported."""
    stmts = []
    i = 0
    n = len(src)
    saw_any = False
    while i < n:
        if src[i] in " \t\n":
            i += 1
            continue
        prev_i = i
        words, i = parse_line(src, i)
        if not words:
            if i == prev_i:
                # an operator we cannot consume (pipe/redirect/etc.) — v1 fails loud
                raise Unsupported(f"operator {src[i]!r} outside v1 subset")
            continue
        saw_any = True
        stmts.extend(parse_statement(words))
        # advance past the separator
        while i < n and src[i] in " \t":
            i += 1
        if i < n and src[i] == ";":
            i += 1
    return O(dict(
        type="Program",
        contract_version=1,
        imports=[],
        requires=[],
        var_types=var_types(stmts),
        subs=[],
        stmts=stmts,
    ))

def main():
    args = sys.argv[1:]
    raw = "--raw" in args
    args = [a for a in args if a != "--raw"]
    if len(args) != 2 or args[0] != "--shir":
        sys.stderr.write("usage: pysh.py --shir <file.sh|string> [--raw]\n")
        sys.exit(2)
    inp = args[1]
    src = inp
    if ".sh" in inp or not any(c.isspace() for c in inp):
        try:
            with open(inp, encoding="utf-8") as f:
                src = f.read()
        except OSError:
            pass  # direct input
    try:
        doc = compile_(src)
    except Unsupported as e:
        sys.stderr.write(f"UNSUPPORTED: {e}\n")
        sys.exit(3)
    out = dump(doc)
    if not raw:
        sys.stdout.write(out + "\n")
    else:
        sys.stdout.write(out + "\n")

if __name__ == "__main__":
    main()
