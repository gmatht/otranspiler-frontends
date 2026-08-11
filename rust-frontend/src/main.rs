//! rust-frontend — Rust source -> A1 shIR JSON (the sh2perl frontend
//! contract, `debashc --shir` shape).
//!
//! syn-based (stable crates.io parser — no nightly, no rustc-dev). The
//! frontend parses Rust and emits the language-neutral ShIR JSON that the
//! core's deserializer accepts (`debashc --shir-in-estree`); the oracle
//! is executed-stdout: native rustc vs the A1->ESTree->JS run
//! (harness/frontend-stdout.sh `rust`).
//!
//! v0.1 subset (REFUSE > GUESS — everything else exits 1):
//!   - `fn main()` only; no other items, no attrs, no async/unsafe/const
//!   - `let`/`let mut` integer and string bindings (`let x;` deferred-init
//!     declarations are dropped — Rust guarantees assignment before use)
//!   - plain assigns and compound assigns (`+= -= *= /= %=`)
//!   - integer arithmetic (+ - * / %), literals incl. hex/bin/octal and
//!     `_` separators, unary minus (lowered to `0 - x`)
//!   - if/else/else-if, while, `for i in 0..N` / `0..=N` (LITERAL bounds
//!     only — the A1 Range end must be an int)
//!   - print!/println! with `{}` placeholders (translated to printf %s —
//!     the store is string-typed, %s is safe for ints and strings alike;
//!     literal `%` escapes to `%%`; `{{`/`}}` unescape)
//!   - conditions: comparisons == != < <= > >= (-> -eq -ne -lt -le -gt
//!     -ge), `&&`/`||` (-> `-a`/`-o`), `!`, parens
//!   - bare `return;` (-> the A1 Exit statement — control flow, not a
//!     no-op) and bare `x;` (no-op) statements
//!
//! Everything else refuses loudly with a line number — borrows, String
//! methods, match, loops other than while/for-range, indexing, vec!/dbg!/
//! eprintln!, floats, bools, char literals, dynamic range bounds, macros.

use serde_json::{json, Value};
use std::process::exit;
use syn::spanned::Spanned;

// ── errors ──────────────────────────────────────────────────────────

fn refuse(msg: &str, span: proc_macro2::Span) -> ! {
    let loc = span.start();
    eprintln!("unsupported Rust: {msg} (line {})", loc.line);
    exit(1);
}

// ── A1 node constructors (byte-matching the core's --shir shapes) ───

fn str_lit(s: &str) -> Value {
    json!({"style": "DoubleQuoted", "type": "Str", "value": s})
}

fn get_var(name: &str) -> Value {
    json!({"args": [str_lit(name)], "func": "getVar", "purity": "Emulable", "type": "Call"})
}

fn exec_call(name: &str, args: Vec<Value>) -> Value {
    json!({
        "args": [str_lit(name), json!({"elements": args, "type": "Array"})],
        "func": "exec", "purity": "Emulable", "type": "Call"
    })
}

fn test_call(s: &str) -> Value {
    json!({"args": [str_lit(s)], "func": "test", "purity": "Emulable", "type": "Call"})
}

fn expr_stmt(e: Value) -> Value {
    json!({"expr": e, "type": "Expr"})
}

fn assign_stmt(name: &str, expr: Value) -> Value {
    json!({
        "expr": expr,
        "targets": [json!({"indices": [], "sigil": null, "var": name})],
        "type": "Assign"
    })
}

fn arith_num(v: i64) -> Value {
    json!({"type": "Num", "value": v})
}

fn arith_var(name: &str) -> Value {
    json!({"name": name, "type": "Var"})
}

fn arith_bin(op: &str, lhs: Value, rhs: Value) -> Value {
    json!({"ast": json!({"lhs": lhs, "op": op, "rhs": rhs, "type": "Bin"}), "type": "Arith"})
}

// ── CLI ─────────────────────────────────────────────────────────────

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let mut file: Option<String> = None;
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--shir" => {
                i += 1;
                if i < args.len() {
                    file = Some(args[i].clone());
                }
            }
            "--raw" => {} // accepted for CLI parity with the fleet; we always emit the same
            _ => {
                eprintln!("usage: rust-frontend --shir <file.rs> [--raw]");
                exit(2);
            }
        }
        i += 1;
    }
    let Some(file) = file else {
        eprintln!("usage: rust-frontend --shir <file.rs> [--raw]");
        exit(2);
    };
    let src = std::fs::read_to_string(&file).unwrap_or_else(|e| {
        eprintln!("cannot read {file}: {e}");
        exit(2);
    });
    let ast = syn::parse_file(&src).unwrap_or_else(|e| {
        eprintln!("parse error in {file}: {e}");
        exit(1);
    });

    let mut stmts: Vec<Value> = Vec::new();
    let mut found_main = false;
    for item in &ast.items {
        match item {
            syn::Item::Fn(f) => {
                if f.sig.ident != "main" {
                    refuse("functions other than `main`", f.sig.ident.span());
                }
                if !f.attrs.is_empty() {
                    refuse("attributes on `fn main`", f.attrs[0].span());
                }
                if !f.sig.inputs.is_empty() {
                    refuse("`main` with arguments", f.sig.ident.span());
                }
                if !matches!(f.sig.output, syn::ReturnType::Default) {
                    refuse("`main` with an explicit return type", f.sig.ident.span());
                }
                if f.sig.asyncness.is_some() || f.sig.unsafety.is_some() || f.sig.constness.is_some() {
                    refuse("non-plain `fn main` (async/unsafe/const)", f.sig.ident.span());
                }
                found_main = true;
                lower_block(&f.block, &mut stmts);
            }
            other => refuse("items other than `fn main`", other.span()),
        }
    }
    if !found_main {
        refuse("no `main` function", proc_macro2::Span::call_site());
    }

    let prog = json!({
        "contract_version": 1,
        "imports": [],
        "requires": [],
        "stmt_lines": [],
        "stmts": stmts,
        "subs": [],
        "type": "Program",
        "var_const": [],
        "var_lengths": [],
        "var_lifetimes": [],
        "var_types": [],
    });
    println!("{}", serde_json::to_string(&prog).unwrap());
}

// ── statements ──────────────────────────────────────────────────────

fn lower_block(block: &syn::Block, out: &mut Vec<Value>) {
    for stmt in &block.stmts {
        if let Some(v) = lower_stmt(stmt) {
            out.push(v);
        }
    }
}

fn lower_stmt(stmt: &syn::Stmt) -> Option<Value> {
    match stmt {
        syn::Stmt::Local(local) => lower_local(local),
        syn::Stmt::Expr(e, _semi) => lower_expr_stmt(e),
        syn::Stmt::Item(item) => refuse("nested item declarations", item.span()),
        syn::Stmt::Macro(m) => Some(lower_print_macro(&m.mac)),
    }
}

fn lower_local(local: &syn::Local) -> Option<Value> {
    let syn::Pat::Ident(pi) = &local.pat else {
        refuse("non-identifier `let` binding", local.pat.span());
    };
    let name = pi.ident.to_string();
    let Some(init) = &local.init else {
        // `let x;` — deferred init. Rust guarantees assignment before use
        // (and the native oracle enforces it); the declaration itself is a
        // no-op in the store model, exactly like c-sh-go's dropped `int i;`.
        return None;
    };
    Some(assign_stmt(&name, value_expr(&init.expr)))
}

fn lower_expr_stmt(e: &syn::Expr) -> Option<Value> {
    match e {
        syn::Expr::Macro(m) => Some(lower_print_macro(&m.mac)),
        syn::Expr::Assign(a) => Some(plain_assign(a)),
        syn::Expr::Binary(b) => {
            // `x += e` parses as Expr::Binary with a compound-assign op
            use syn::BinOp::*;
            if matches!(
                b.op,
                AddAssign(_) | SubAssign(_) | MulAssign(_) | DivAssign(_) | RemAssign(_)
            ) {
                Some(compound_assign(b))
            } else {
                refuse("binary-operator statement (only compound assigns)", b.op.span())
            }
        }
        syn::Expr::If(ie) => Some(if_stmt(ie)),
        syn::Expr::While(w) => Some(while_stmt(w)),
        syn::Expr::ForLoop(fl) => Some(for_stmt(fl)),
        syn::Expr::Return(r) => {
            if r.expr.is_some() {
                refuse("`return` with a value", r.span());
            }
            // `return;` in main ENDS the program — native rustc stops there.
            // Not a no-op (the coverage gate caught this: dropping it ran
            // the rest of main). Lower to the A1 Exit statement.
            Some(json!({"type": "Exit", "value": null}))
        }
        syn::Expr::Path(p) => {
            // bare `x;` — evaluates the variable, no side effect (c-sh-go
            // skips bare `id;` the same way)
            if single_path(p).is_some() {
                None
            } else {
                refuse("bare path statement", e.span());
            }
        }
        syn::Expr::Paren(p) => lower_expr_stmt(&p.expr),
        other => refuse("statement form", other.span()),
    }
}

fn plain_assign(a: &syn::ExprAssign) -> Value {
    let syn::Expr::Path(p) = &*a.left else {
        refuse("assignment target (only plain variables are supported)", a.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    assign_stmt(&name.to_string(), value_expr(&a.right))
}

fn compound_assign(b: &syn::ExprBinary) -> Value {
    let syn::Expr::Path(p) = &*b.left else {
        refuse("assignment target (only plain variables are supported)", b.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    let name = name.to_string();
    use syn::BinOp::*;
    let op = match b.op {
        AddAssign(_) => "+",
        SubAssign(_) => "-",
        MulAssign(_) => "*",
        DivAssign(_) => "/",
        RemAssign(_) => "%",
        _ => refuse("compound assignment operator", b.op.span()),
    };
    // `i += 1` lowers to `i = i + 1` — the c-sh-go compound shape
    assign_stmt(&name, arith_bin(op, arith_var(&name), arith_expr(&b.right)))
}

fn if_stmt(ie: &syn::ExprIf) -> Value {
    let cond = test_call(&test_string(&ie.cond));
    let mut then = Vec::new();
    lower_block(&ie.then_branch, &mut then);
    let else_stmts: Vec<Value> = match &ie.else_branch {
        None => vec![],
        Some((_, inner)) => match &**inner {
            // `else if` lowers to a NESTED If in the else position — the
            // exact shape the shell frontend emits for elif chains (the
            // `elsifs` field stays empty).
            syn::Expr::If(inner_if) => vec![if_stmt(inner_if)],
            // `else { ... }`
            syn::Expr::Block(blk) => {
                let mut v = Vec::new();
                lower_block(&blk.block, &mut v);
                v
            }
            other => refuse("else form", other.span()),
        },
    };
    json!({"cond": cond, "then": then, "elsifs": [], "else": else_stmts, "type": "If"})
}

fn while_stmt(w: &syn::ExprWhile) -> Value {
    if w.label.is_some() {
        refuse("labeled while", w.span());
    }
    let cond = test_call(&test_string(&w.cond));
    let mut body = Vec::new();
    lower_block(&w.body, &mut body);
    json!({"cond": cond, "body": body, "type": "While"})
}

fn for_stmt(fl: &syn::ExprForLoop) -> Value {
    if fl.label.is_some() {
        refuse("labeled for", fl.span());
    }
    let syn::Pat::Ident(pi) = fl.pat.as_ref() else {
        refuse("for-loop pattern (destructuring not supported)", fl.pat.span());
    };
    let name = pi.ident.to_string();
    // A1 Range is INCLUSIVE (bash seq semantics: `i <= end`); Rust `a..b`
    // is exclusive, `a..=b` inclusive. Dynamic bounds refuse — the A1
    // deserializer requires an int end.
    let (start, end, inclusive) = match &*fl.expr {
        syn::Expr::Range(r) => {
            let start = match &r.start {
                Some(e) => int_literal(e),
                None => refuse("range without a start bound", r.span()),
            };
            let end = match &r.end {
                Some(e) => int_literal(e),
                None => refuse("open-ended range", r.span()),
            };
            (start, end, matches!(r.limits, syn::RangeLimits::Closed(..)))
        }
        other => refuse("for-loop iterator (only `0..N` / `0..=N` ranges)", other.span()),
    };
    let mut body = Vec::new();
    lower_block(&fl.body, &mut body);
    let end = if inclusive { end } else { end - 1 };
    json!({
        "body": body,
        "iter": json!({"end": end, "start": start, "type": "Range"}),
        "runs": true,
        "type": "For",
        "var": name
    })
}

// ── print!/println! (the only supported macros) ─────────────────────

struct FmtArgs {
    fmt: syn::LitStr,
    args: syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
}

impl syn::parse::Parse for FmtArgs {
    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
        let fmt = input.parse()?;
        let args = if input.is_empty() {
            syn::punctuated::Punctuated::new()
        } else {
            input.parse::<syn::Token![,]>()?; // the separator before the first arg
            syn::punctuated::Punctuated::parse_terminated(input)?
        };
        Ok(FmtArgs { fmt, args })
    }
}

fn lower_print_macro(mac: &syn::Macro) -> Value {
    let newline = if mac.path.segments.len() == 1 && mac.path.leading_colon.is_none() {
        match mac.path.segments[0].ident.to_string().as_str() {
            "println" => true,
            "print" => false,
            other => refuse(&format!("macro invocation `{other}!`"), mac.path.span()),
        }
    } else {
        refuse("qualified macro path", mac.path.span());
    };
    let fmt_args: FmtArgs = syn::parse2(mac.tokens.clone())
        .unwrap_or_else(|e| refuse(&format!("macro arguments: {e}"), mac.span()));
    let fmt = fmt_args.fmt.value();
    let (translated, n) =
        translate_format(&fmt).unwrap_or_else(|e| refuse(&e, fmt_args.fmt.span()));
    let args = &fmt_args.args;
    if n != args.len() {
        refuse(
            &format!("format string has {n} placeholders but {} arguments", args.len()),
            fmt_args.fmt.span(),
        );
    }
    let mut elements = vec![str_lit(&if newline {
        format!("{translated}\n")
    } else {
        translated
    })];
    for a in args {
        elements.push(value_expr(a));
    }
    expr_stmt(exec_call("printf", elements))
}

/// Rust format string -> printf format string. `{}` -> `%s` (safe for the
/// string-typed store: ints and strings both print correctly), literal `%`
/// -> `%%` (the runtime printf would misread a lone `%`), `{{`/`}}` ->
/// `{`/`}`. Named/debug/width specs refuse.
fn translate_format(fmt: &str) -> Result<(String, usize), String> {
    let mut out = String::new();
    let mut n = 0usize;
    let mut chars = fmt.chars().peekable();
    while let Some(c) = chars.next() {
        match c {
            '{' => {
                if chars.peek() == Some(&'{') {
                    chars.next();
                    out.push('{');
                } else {
                    let mut inner = String::new();
                    for c2 in chars.by_ref() {
                        if c2 == '}' {
                            break;
                        }
                        inner.push(c2);
                    }
                    if inner.is_empty() {
                        n += 1;
                        out.push_str("%s");
                    } else {
                        return Err(format!(
                            "format specifier '{{{inner}}}' (only positional {{}} is supported)"
                        ));
                    }
                }
            }
            '}' => {
                if chars.peek() == Some(&'}') {
                    chars.next();
                    out.push('}');
                } else {
                    return Err("unescaped '}' in format string".into());
                }
            }
            '%' => out.push_str("%%"),
            c => out.push(c),
        }
    }
    Ok((out, n))
}

// ── expressions ─────────────────────────────────────────────────────

/// A single unqualified identifier path (`x`), or None.
fn single_path(p: &syn::ExprPath) -> Option<&syn::Ident> {
    if p.qself.is_none()
        && p.path.leading_colon.is_none()
        && p.path.segments.len() == 1
        && p.path.segments[0].arguments.is_none()
    {
        Some(&p.path.segments[0].ident)
    } else {
        None
    }
}

/// Value-position expression: int/string literals -> Str, vars -> getVar,
/// arithmetic -> Arith (the c-sh-go value-node shapes).
fn value_expr(e: &syn::Expr) -> Value {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => str_lit(&int_text(li)),
            syn::Lit::Str(ls) => str_lit(&ls.value()),
            other => refuse("literal type (only integers and strings)", other.span()),
        },
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("path expression", p.span());
            };
            get_var(&name.to_string())
        }
        syn::Expr::Binary(b) => arith_from_binary(b),
        syn::Expr::Unary(u) => arith_from_unary(u),
        syn::Expr::Paren(p) => value_expr(&p.expr),
        other => refuse("expression", other.span()),
    }
}

/// Arithmetic-position expression -> the A1 Arith AST (Num/Var/Bin).
fn arith_expr(e: &syn::Expr) -> Value {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => arith_num(int_text(li).parse().unwrap_or_else(|_| {
                refuse("integer literal out of range", li.span())
            })),
            other => refuse("literal in arithmetic", other.span()),
        },
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("path in arithmetic", p.span());
            };
            arith_var(&name.to_string())
        }
        syn::Expr::Binary(b) => arith_from_binary(b),
        syn::Expr::Unary(u) => arith_from_unary(u),
        syn::Expr::Paren(p) => arith_expr(&p.expr),
        other => refuse("expression in arithmetic", other.span()),
    }
}

fn arith_from_binary(b: &syn::ExprBinary) -> Value {
    use syn::BinOp::*;
    let op = match b.op {
        Add(_) => "+",
        Sub(_) => "-",
        Mul(_) => "*",
        Div(_) => "/",
        Rem(_) => "%",
        _ => refuse("binary operator in arithmetic", b.op.span()),
    };
    arith_bin(op, arith_expr(&b.left), arith_expr(&b.right))
}

fn arith_from_unary(u: &syn::ExprUnary) -> Value {
    match u.op {
        syn::UnOp::Neg(_) => arith_bin("-", arith_num(0), arith_expr(&u.expr)),
        _ => refuse("unary operator", u.op.span()),
    }
}

/// Integer literal -> its DECIMAL text (hex/bin/octal and `_` separators
/// normalized, suffixes refused). The store is decimal-string-typed.
fn int_text(li: &syn::LitInt) -> String {
    if !li.suffix().is_empty() {
        refuse("integer literal suffix", li.span());
    }
    let v: i64 = li
        .base10_parse()
        .unwrap_or_else(|_| refuse("integer literal (out of i64 range)", li.span()));
    v.to_string()
}

fn int_literal(e: &syn::Expr) -> i64 {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => int_text(li)
                .parse()
                .unwrap_or_else(|_| refuse("integer literal out of range", li.span())),
            other => refuse("range bound (integer literal expected)", other.span()),
        },
        other => refuse(
            "range bound (literal only — the A1 Range end must be an int)",
            other.span(),
        ),
    }
}

// ── conditions ──────────────────────────────────────────────────────

/// A boolean condition -> the `test` STRING grammar the runtime parses
/// (`$x -gt 1`, `-a`/`-o` chains, `!`, parens) — the c-sh-go shapes.
fn test_string(e: &syn::Expr) -> String {
    match e {
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            match b.op {
                Eq(_) => bin_test("-eq", &b.left, &b.right),
                Ne(_) => bin_test("-ne", &b.left, &b.right),
                Lt(_) => bin_test("-lt", &b.left, &b.right),
                Le(_) => bin_test("-le", &b.left, &b.right),
                Gt(_) => bin_test("-gt", &b.left, &b.right),
                Ge(_) => bin_test("-ge", &b.left, &b.right),
                // the runtime's test parser binds -a tighter than -o,
                // matching Rust's && before ||
                And(_) => format!("{} -a {}", test_string(&b.left), test_string(&b.right)),
                Or(_) => format!("{} -o {}", test_string(&b.left), test_string(&b.right)),
                _ => refuse("binary operator in condition", b.op.span()),
            }
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => format!("! {}", test_string(&u.expr)),
            _ => refuse("unary operator in condition", u.op.span()),
        },
        syn::Expr::Paren(p) => format!("( {} )", test_string(&p.expr)),
        other => refuse("condition expression", other.span()),
    }
}

fn bin_test(op: &str, l: &syn::Expr, r: &syn::Expr) -> String {
    format!("{} {} {}", operand(l), op, operand(r))
}

/// Comparison operand: a variable (`$x`) or an integer literal.
fn operand(e: &syn::Expr) -> String {
    match e {
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("path in condition operand", p.span());
            };
            format!("${name}")
        }
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => int_text(li),
            other => refuse("literal in condition operand", other.span()),
        },
        other => refuse(
            "condition operand (variable or integer literal only)",
            other.span(),
        ),
    }
}
