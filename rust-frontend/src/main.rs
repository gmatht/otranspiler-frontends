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
use std::collections::HashMap;
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


/// Lowering context — the integer-const table. Rust const-evaluates
/// `const`/`static` items with literal initializers; range bounds and
/// comparison operands may legally reference them, so path lowering
/// resolves against this table. Const folding is semantics-preserving
/// compiler work, NOT a backend encoding.
#[derive(Default)]
struct Cx {
    ints: HashMap<String, i64>,
    /// names of the program's non-main functions (call targets)
    fns: Vec<String>,
    /// global assignments (consts/statics), in source order
    globals: Vec<Value>,
}

/// Const-evaluate an expression to i64 if possible (literal / -literal /
/// paren / already-tabled path).
fn const_int(e: &syn::Expr, cx: &Cx) -> Option<i64> {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => li.base10_parse().ok(),
            _ => None,
        },
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Neg(_) => Some(-const_int(&u.expr, cx)?),
            _ => None,
        },
        syn::Expr::Paren(p) => const_int(&p.expr, cx),
        syn::Expr::Path(p) => single_path(p).and_then(|id| cx.ints.get(&id.to_string()).copied()),
        _ => None,
    }
}

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

    // Flatten inline `mod` blocks into one item list — namespace nesting
    // has no effect on the lowered program (names stay as written;
    // qualified paths resolve only for `Type::method` call targets).
    let mut flat: Vec<&syn::Item> = Vec::new();
    flatten_items(&ast.items, &mut flat);

    // pre-pass: collect consts/statics (globals + int-const table) so any
    // later lowering step can const-fold against them.
    let mut cx = Cx::default();
    for item in &flat {
        match item {
            syn::Item::Const(c) => {
                cx.globals.push(assign_stmt(&c.ident.to_string(), value_expr(&c.expr, &cx)));
                if let Some(v) = const_int(&c.expr, &cx) {
                    cx.ints.insert(c.ident.to_string(), v);
                }
            }
            syn::Item::Static(s) => {
                cx.globals.push(assign_stmt(&s.ident.to_string(), value_expr(&s.expr, &cx)));
                if let Some(v) = const_int(&s.expr, &cx) {
                    cx.ints.insert(s.ident.to_string(), v);
                }
            }
            _ => {}
        }
    }
    // second pre-pass: register the non-main functions (call targets) —
    // free fns under their own name, inherent impl methods under the
    // mangled `Type_method` name (`Point::new` -> `Point_new`).
    let mut fns: Vec<FnSource> = Vec::new();
    for item in &flat {
        match item {
            syn::Item::Fn(f) if f.sig.ident != "main" => {
                cx.fns.push(f.sig.ident.to_string());
                fns.push(FnSource::Free(f));
            }
            syn::Item::Impl(i) => {
                // Inherent AND trait impls lower their methods under the
                // mangled `Type_method` name. This guesses nothing: every
                // method-CALL path (receiver syntax, trait dispatch)
                // refuses at its expression site until the method-contract
                // tranche lands, so a dropped/lowered impl body is dead
                // code until then.
                let ty = type_ident(&i.self_ty).unwrap_or_else(|| {
                    refuse("impl of generic/qualified type", i.self_ty.span())
                });
                for ii in &i.items {
                    if let syn::ImplItem::Fn(m) = ii {
                        let mangled = format!("{}_{}", ty, m.sig.ident);
                        cx.fns.push(mangled.clone());
                        fns.push(FnSource::Method(mangled, m));
                    }
                    // non-fn impl items (consts/types) are declaration-only
                }
            }
            _ => {}
        }
    }
    let mut stmts: Vec<Value> = Vec::new();
    for item in &flat {
        match item {
            // `use` imports name things for the TYPE CHECKER only — they
            // have no runtime effect, so lowering drops them.
            syn::Item::Use(_) => {}
            // consts/statics were collected in the pre-pass above.
            syn::Item::Const(_) | syn::Item::Static(_) => {}
            // `#[cfg(test)]`-gated items are test-harness-only code —
            // they do not exist in a normal build; drop the subtree.
            syn::Item::Fn(f) if cfg_test_only(&f.attrs) => {}
            syn::Item::Mod(m) if cfg_test_only(&m.attrs) => {}
            syn::Item::Impl(i) if cfg_test_only(&i.attrs) => {}
            syn::Item::Struct(s) if cfg_test_only(&s.attrs) => {}
            syn::Item::Enum(e) if cfg_test_only(&e.attrs) => {}
            syn::Item::Static(s) if cfg_test_only(&s.attrs) => {}
            syn::Item::Const(c) if cfg_test_only(&c.attrs) => {}
            // non-main fns were collected just above and lower below.
            syn::Item::Fn(f) if f.sig.ident != "main" => {}
            syn::Item::Impl(_) => {}
            // the mod ITEM itself: contents were flattened above; file
            // modules (`mod x;`) contribute nothing on their own.
            syn::Item::Mod(_) => {}
            // Declaration-only items: no runtime effect BY THEMSELVES.
            // Every USE (struct literal, variant path, trait method call)
            // refuses at its own expression site — nothing is smeared.
            syn::Item::Struct(_)
            | syn::Item::Enum(_)
            | syn::Item::Union(_)
            | syn::Item::Type(_)
            | syn::Item::Trait(_) => {}
            // `macro_rules!` DEFINITIONS have no runtime effect; unknown
            // INVOCATIONS still refuse at their site. Any other item-level
            // macro (lazy_static! etc. creates state) refuses.
            syn::Item::Macro(m) => {
                let is_rules = m
                    .mac
                    .path
                    .segments
                    .last()
                    .map(|s| s.ident == "macro_rules")
                    .unwrap_or(false);
                if !is_rules {
                    refuse(
                        "item-level macro invocation (creates state / selects code)",
                        m.span(),
                    );
                }
            }
            syn::Item::Fn(f) => {
                if let Some(a) = first_semantic_attr(&f.attrs) {
                    refuse("semantic attribute on `fn main`", a.span());
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
                lower_block(&f.block, &cx, false, &mut stmts);
            }
            other => refuse("items other than `fn`/`use`/`const`/`static`", other.span()),
        }
    }
    // NOTE: no `main` is LEGAL for library-style sources (definitions
    // only) — the program body is then empty. The stdout oracle never
    // sees such a file (testdata examples are all runnable).
    let mut all_stmts = std::mem::take(&mut cx.globals);
    // function definitions FIRST — runtime def-before-use order (the A1
    // Function statement lowers to a definition executed in stmt order).
    for src_fn in &fns {
        all_stmts.push(match src_fn {
            FnSource::Free(f) => function_def(
                f.sig.ident.to_string(),
                &f.attrs,
                &f.sig,
                &f.block,
                &cx,
            ),
            FnSource::Method(name, m) => {
                function_def(name.clone(), &m.attrs, &m.sig, &m.block, &cx)
            }
        });
    }
    all_stmts.append(&mut stmts);

    let prog = json!({
        "contract_version": 1,
        "imports": [],
        "requires": [],
        "stmt_lines": [],
        "stmts": all_stmts,
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

fn lower_block(block: &syn::Block, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) {
    for stmt in &block.stmts {
        if let Some(v) = lower_stmt(stmt, cx, in_fn) {
            out.push(v);
        }
    }
}

fn lower_stmt(stmt: &syn::Stmt, cx: &Cx, in_fn: bool) -> Option<Value> {
    match stmt {
        syn::Stmt::Local(local) => lower_local(local, cx),
        syn::Stmt::Expr(e, _semi) => lower_expr_stmt(e, cx, in_fn),
        syn::Stmt::Item(item) => refuse("nested item declarations", item.span()),
        syn::Stmt::Macro(m) => Some(lower_print_macro(&m.mac, cx)),
    }
}

fn lower_local(local: &syn::Local, cx: &Cx) -> Option<Value> {
    // `let x: i64 = ...` parses as Pat::Type wrapping Pat::Ident — the
    // annotation is type-checker information, erased here (the store is
    // dynamically typed).
    let pat: &syn::Pat = match &local.pat {
        syn::Pat::Type(pt) => &pt.pat,
        p => p,
    };
    let syn::Pat::Ident(pi) = pat else {
        refuse("non-identifier `let` binding", local.pat.span());
    };
    let name = pi.ident.to_string();
    let Some(init) = &local.init else {
        // `let x;` — deferred init. Rust guarantees assignment before use
        // (and the native oracle enforces it); the declaration itself is a
        // no-op in the store model, exactly like c-sh-go's dropped `int i;`.
        return None;
    };
    Some(assign_stmt(&name, value_expr(&init.expr, cx)))
}

fn lower_expr_stmt(e: &syn::Expr, cx: &Cx, in_fn: bool) -> Option<Value> {
    match e {
        syn::Expr::Macro(m) => Some(lower_print_macro(&m.mac, cx)),
        syn::Expr::Assign(a) => Some(plain_assign(a, cx)),
        syn::Expr::Binary(b) => {
            // `x += e` parses as Expr::Binary with a compound-assign op
            use syn::BinOp::*;
            if matches!(
                b.op,
                AddAssign(_) | SubAssign(_) | MulAssign(_) | DivAssign(_) | RemAssign(_)
            ) {
                Some(compound_assign(b, cx))
            } else {
                refuse("binary-operator statement (only compound assigns)", b.op.span())
            }
        }
        syn::Expr::If(ie) => Some(if_stmt(ie, cx, in_fn)),
        syn::Expr::While(w) => Some(while_stmt(w, cx, in_fn)),
        syn::Expr::ForLoop(fl) => Some(for_stmt(fl, cx, in_fn)),
        syn::Expr::Return(r) => {
            if in_fn {
                // inside a user function: a NATIVE return carries the
                // value back through fnValue (the c-sh-go t58 protocol).
                let v = match &r.expr {
                    Some(e) => value_expr(e, cx),
                    None => serde_json::Value::Null,
                };
                Some(json!({"type": "Return", "value": v}))
            } else {
                if r.expr.is_some() {
                    refuse("`return` with a value", r.span());
                }
                // `return;` in main ENDS the program — native rustc stops
                // there. Not a no-op (the coverage gate caught this:
                // dropping it ran the rest of main). Lower to A1 Exit.
                Some(json!({"type": "Exit", "value": null}))
            }
        }
        syn::Expr::Call(c) => {
            // statement-position call: the VOID dispatch (fnCall). Any
            // returned value is discarded.
            let Some(syn::Expr::Path(p)) = Some(c.func.as_ref()) else {
                refuse("call target path", c.func.span());
            };
            let Some(target) = call_target(p, cx) else {
                refuse("call to unknown function", c.func.span());
            };
            Some(expr_stmt(fn_call_expr(&target, &c.args, "fnCall", cx)))
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
        syn::Expr::Paren(p) => lower_expr_stmt(&p.expr, cx, in_fn),
        other => refuse("statement form", other.span()),
    }
}

fn plain_assign(a: &syn::ExprAssign, cx: &Cx) -> Value {
    let syn::Expr::Path(p) = &*a.left else {
        refuse("assignment target (only plain variables are supported)", a.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    assign_stmt(&name.to_string(), value_expr(&a.right, cx))
}

fn compound_assign(b: &syn::ExprBinary, cx: &Cx) -> Value {
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
    assign_stmt(&name, arith_bin(op, arith_var(&name), arith_expr(&b.right, cx)))
}

fn if_stmt(ie: &syn::ExprIf, cx: &Cx, in_fn: bool) -> Value {
    let cond = test_call(&test_string(&ie.cond, cx));
    let mut then = Vec::new();
    lower_block(&ie.then_branch, cx, in_fn, &mut then);
    let else_stmts: Vec<Value> = match &ie.else_branch {
        None => vec![],
        Some((_, inner)) => match &**inner {
            // `else if` lowers to a NESTED If in the else position — the
            // exact shape the shell frontend emits for elif chains (the
            // `elsifs` field stays empty).
            syn::Expr::If(inner_if) => vec![if_stmt(inner_if, cx, in_fn)],
            // `else { ... }`
            syn::Expr::Block(blk) => {
                let mut v = Vec::new();
                lower_block(&blk.block, cx, in_fn, &mut v);
                v
            }
            other => refuse("else form", other.span()),
        },
    };
    json!({"cond": cond, "then": then, "elsifs": [], "else": else_stmts, "type": "If"})
}

fn while_stmt(w: &syn::ExprWhile, cx: &Cx, in_fn: bool) -> Value {
    if w.label.is_some() {
        refuse("labeled while", w.span());
    }
    let cond = test_call(&test_string(&w.cond, cx));
    let mut body = Vec::new();
    lower_block(&w.body, cx, in_fn, &mut body);
    json!({"cond": cond, "body": body, "type": "While"})
}

fn for_stmt(fl: &syn::ExprForLoop, cx: &Cx, in_fn: bool) -> Value {
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
                Some(e) => int_literal(e, cx),
                None => refuse("range without a start bound", r.span()),
            };
            let end = match &r.end {
                Some(e) => int_literal(e, cx),
                None => refuse("open-ended range", r.span()),
            };
            (start, end, matches!(r.limits, syn::RangeLimits::Closed(..)))
        }
        other => refuse("for-loop iterator (only `0..N` / `0..=N` ranges)", other.span()),
    };
    let mut body = Vec::new();
    lower_block(&fl.body, cx, in_fn, &mut body);
    let end = if inclusive { end } else { end - 1 };
    json!({
        "body": body,
        "iter": json!({"end": end, "start": start, "type": "Range"}),
        "runs": true,
        "type": "For",
        "var": name
    })
}

/// Where a lowered function definition came from: a free `fn` item or an
/// inherent impl method (lowered under the mangled `Type_method` name).
enum FnSource<'a> {
    Free(&'a syn::ItemFn),
    Method(String, &'a syn::ImplItemFn),
}

/// Flatten inline `mod` blocks into one item list (recursively). File
/// modules (`mod x;`) contribute nothing — their contents live in other
/// files this single-file lowering never sees; any cross-file USE refuses
/// at its own site.
fn flatten_items<'a>(items: &'a [syn::Item], out: &mut Vec<&'a syn::Item>) {
    for item in items {
        // a `#[cfg(test)]`-gated inline mod contributes NOTHING to a
        // normal build — do not descend into it (its children must not
        // re-enter the flat list as standalone items).
        if let syn::Item::Mod(m) = item {
            if cfg_test_only(&m.attrs) {
                continue;
            }
        }
        out.push(item);
        if let syn::Item::Mod(m) = item {
            if let Some((_, inner)) = &m.content {
                flatten_items(inner, out);
            }
        }
    }
}

/// The plain identifier of a non-generic path type (`Point`), or None
/// (`Vec<T>`, `&T`, qualified paths — none name a lowerable impl target).
fn type_ident(ty: &syn::Type) -> Option<String> {
    if let syn::Type::Path(tp) = ty {
        if tp.qself.is_none() {
            if let Some(seg) = tp.path.segments.last() {
                if seg.arguments.is_none() && tp.path.leading_colon.is_none() {
                    return Some(seg.ident.to_string());
                }
            }
        }
    }
    None
}

/// True when EVERY attribute on the item is `#[cfg(test)]` — the item is
/// test-harness-only code that does not exist in a normal build, so
/// dropping it (with its whole subtree) is semantics-preserving.
fn cfg_test_only(attrs: &[syn::Attribute]) -> bool {
    !attrs.is_empty()
        && attrs.iter().all(|a| {
            a.path().is_ident("cfg")
                && a.meta.require_list().map(|l| l.tokens.to_string()).map(|t| t.trim() == "test").unwrap_or(false)
        })
}

/// Inert attributes: compile-time-only metadata with NO runtime effect
/// (`#[test]` defs are ordinary functions outside `cargo test`;
/// `#[macro_export]`/doc/allow/inline/deprecated are compiler bookkeeping).
/// Dropped silently; ANY other attribute refuses (cfg/cfg_attr are
/// compile-time CONFIG — they select code, so dropping them guesses).
fn first_semantic_attr<'a>(attrs: &'a [syn::Attribute]) -> Option<&'a syn::Attribute> {
    attrs.iter().find(|a| {
        let name = a.path().segments.last().map(|s| s.ident.to_string()).unwrap_or_default();
        !matches!(name.as_str(), "test" | "macro_export" | "allow" | "inline" | "deprecated" | "doc")
    })
}

/// Resolve a call target path to a registered lowered-name: a free fn
/// (`f`) or an inherent impl method (`Type::method` -> `Type_method`).
/// Returns None for anything else (unknown names, deeper paths,
/// turbofish) — the caller refuses.
fn call_target(p: &syn::ExprPath, cx: &Cx) -> Option<String> {
    if p.qself.is_some() || p.path.leading_colon.is_some() {
        return None;
    }
    let segs: Vec<&syn::PathSegment> = p.path.segments.iter().collect();
    let known = |n: &str| cx.fns.iter().any(|f| *f == n);
    match segs.len() {
        1 if segs[0].arguments.is_none() => {
            let n = segs[0].ident.to_string();
            if known(&n) {
                Some(n)
            } else {
                None
            }
        }
        2 if segs.iter().all(|s| s.arguments.is_none()) => {
            let mangled = format!("{}_{}", segs[0].ident, segs[1].ident);
            if known(&mangled) {
                Some(mangled)
            } else {
                None
            }
        }
        _ => None,
    }
}

/// A user-fn call node — `func` is "fnCall" (statement/void) or "fnValue"
/// (value position); args lower as values (the c-sh-go protocol).
fn fn_call_expr(
    name: &str,
    args: &syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
    func: &str,
    cx: &Cx,
) -> Value {
    json!({
        "args": [
            str_lit(name),
            json!({"elements": args.iter().map(|a| value_expr(a, cx)).collect::<Vec<_>>(), "type": "Array"}),
        ],
        "func": func, "purity": "Emulable", "type": "Call"
    })
}

/// Control-flow tails are UNIT unless proven otherwise (no type info yet):
/// an `if`/`match` tail of a value-returning fn REFUSES rather than
/// silently dropping its value (REFUSE > GUESS).
fn unit_tail(e: &syn::Expr) -> bool {
    matches!(
        e,
        syn::Expr::If(_)
            | syn::Expr::While(_)
            | syn::Expr::ForLoop(_)
            | syn::Expr::Loop(_)
            | syn::Expr::Match(_)
            | syn::Expr::Block(_)
    )
}

/// Lower a function definition to the A1 Function statement — the
/// c-sh-go protocol: positional params copied from getVar("1")... at
/// entry; calls dispatch via fnCall (void) / fnValue (value). `name` is
/// the free-fn ident or the mangled `Type_method` for impl methods.
fn function_def(
    name: String,
    attrs: &[syn::Attribute],
    sig: &syn::Signature,
    block: &syn::Block,
    cx: &Cx,
) -> Value {
    if let Some(a) = first_semantic_attr(attrs) {
        refuse("semantic attribute on a function", a.span());
    }
    if !sig.generics.params.is_empty() {
        refuse("generic function", sig.generics.params.span());
    }
    if sig.asyncness.is_some() || sig.unsafety.is_some() || sig.constness.is_some() {
        refuse("non-plain `fn` (async/unsafe/const)", sig.ident.span());
    }
    if sig.variadic.is_some() {
        refuse("variadic function", sig.variadic.span());
    }
    let returns_value = !matches!(sig.output, syn::ReturnType::Default);
    let mut body: Vec<Value> = Vec::new();
    let mut seen: Vec<String> = Vec::new();
    for (i, input) in sig.inputs.iter().enumerate() {
        let pt = match input {
            syn::FnArg::Typed(pt) => pt,
            syn::FnArg::Receiver(r) => {
                refuse("method receiver (`self`) — impl blocks not supported yet", r.span())
            }
        };
        let pat: &syn::Pat = match &*pt.pat {
            syn::Pat::Type(inner) => &inner.pat,
            p => p,
        };
        let syn::Pat::Ident(pi) = pat else {
            refuse("parameter pattern (only plain identifiers)", pat.span());
        };
        let pname = pi.ident.to_string();
        if seen.contains(&pname) {
            refuse("duplicate parameter name", pi.ident.span());
        }
        seen.push(pname.clone());
        // positional binding: param i <- getVar("i+1")
        body.push(assign_stmt(&pname, get_var(&(i + 1).to_string())));
    }
    // Body statements; a SEMI-LESS tail expression RETURNS ITS VALUE when
    // the signature declares a return type (Rust tail-expression return).
    let n = block.stmts.len();
    for (i, stmt) in block.stmts.iter().enumerate() {
        if i + 1 == n {
            if let syn::Stmt::Expr(e, None) = stmt {
                if unit_tail(e) {
                    if returns_value {
                        refuse(
                            "tail control-flow expression in a value-returning fn \
                             (if/match-as-value not yet supported)",
                            e.span(),
                        );
                    }
                    if let Some(v) = lower_stmt(stmt, cx, true) {
                        body.push(v);
                    }
                    continue;
                }
                if returns_value {
                    body.push(json!({"type": "Return", "value": value_expr(e, cx)}));
                } else if let Some(v) = lower_stmt(stmt, cx, true) {
                    body.push(v);
                }
                continue;
            }
        }
        if let Some(v) = lower_stmt(stmt, cx, true) {
            body.push(v);
        }
    }
    json!({"body": body, "name": name, "type": "Function"})
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

fn lower_print_macro(mac: &syn::Macro, cx: &Cx) -> Value {
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
        elements.push(value_expr(a, cx));
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
fn value_expr(e: &syn::Expr, cx: &Cx) -> Value {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => str_lit(&int_text(li)),
            syn::Lit::Str(ls) => str_lit(&ls.value()),
            // booleans are their OWN store kind — the A1 Bool expr renders
            // to a JS boolean (native `true`/`false`), which prints exactly
            // like Rust's `{}` formatting for bools.
            syn::Lit::Bool(lb) => json!({"type": "Bool", "value": lb.value()}),
            other => refuse("literal type (only integers, strings, bools)", other.span()),
        },
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("path expression", p.span());
            };
            get_var(&name.to_string())
        }
        syn::Expr::Binary(b) => arith_from_binary(b, cx),
        syn::Expr::Unary(u) => arith_from_unary(u, cx),
        syn::Expr::Paren(p) => value_expr(&p.expr, cx),
        syn::Expr::Call(c) => {
            // value-position call: the VALUE-returning dispatch (fnValue,
            // c-sh-go t58). NOTE: calls inside ARITHMETIC refuse — the A1
            // arith AST has no call operand (hoisting to temporaries is a
            // later, semantics-preserving frontend step).
            let Some(syn::Expr::Path(p)) = Some(c.func.as_ref()) else {
                refuse("call target path", c.func.span());
            };
            let Some(target) = call_target(p, cx) else {
                refuse("call to unknown function", c.func.span());
            };
            fn_call_expr(&target, &c.args, "fnValue", cx)
        }
        other => refuse("expression", other.span()),
    }
}

/// Arithmetic-position expression -> the A1 Arith AST (Num/Var/Bin).
fn arith_expr(e: &syn::Expr, cx: &Cx) -> Value {
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
        syn::Expr::Binary(b) => arith_from_binary(b, cx),
        syn::Expr::Unary(u) => arith_from_unary(u, cx),
        syn::Expr::Paren(p) => arith_expr(&p.expr, cx),
        other => refuse("expression in arithmetic", other.span()),
    }
}

fn arith_from_binary(b: &syn::ExprBinary, cx: &Cx) -> Value {
    use syn::BinOp::*;
    let op = match b.op {
        Add(_) => "+",
        Sub(_) => "-",
        Mul(_) => "*",
        Div(_) => "/",
        Rem(_) => "%",
        _ => refuse("binary operator in arithmetic", b.op.span()),
    };
    arith_bin(op, arith_expr(&b.left, cx), arith_expr(&b.right, cx))
}

fn arith_from_unary(u: &syn::ExprUnary, cx: &Cx) -> Value {
    match u.op {
        syn::UnOp::Neg(_) => arith_bin("-", arith_num(0), arith_expr(&u.expr, cx)),
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

fn int_literal(e: &syn::Expr, cx: &Cx) -> i64 {
    match e {
        syn::Expr::Path(p) => {
            // a named const (`for i in 0..LIMIT`) — Rust const-evaluates
            // it; resolve from the table or refuse.
            match const_int(e, cx) {
                Some(v) => v,
                None => refuse("range bound (unknown const)", p.span()),
            }
        }
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
fn test_string(e: &syn::Expr, cx: &Cx) -> String {
    match e {
        // literal-bool conditions (`while true`, `if false`) — the test
        // grammar has no boolean literals, so they fold to always-true /
        // always-false numeric comparisons.
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Bool(lb) => {
                if lb.value() {
                    "1 -eq 1".into()
                } else {
                    "0 -eq 1".into()
                }
            }
            _ => refuse("literal in condition", l.span()),
        },
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            match b.op {
                Eq(_) => bin_test("-eq", &b.left, &b.right, cx),
                Ne(_) => bin_test("-ne", &b.left, &b.right, cx),
                Lt(_) => bin_test("-lt", &b.left, &b.right, cx),
                Le(_) => bin_test("-le", &b.left, &b.right, cx),
                Gt(_) => bin_test("-gt", &b.left, &b.right, cx),
                Ge(_) => bin_test("-ge", &b.left, &b.right, cx),
                // the runtime's test parser binds -a tighter than -o,
                // matching Rust's && before ||
                And(_) => format!("{} -a {}", test_string(&b.left, cx), test_string(&b.right, cx)),
                Or(_) => format!("{} -o {}", test_string(&b.left, cx), test_string(&b.right, cx)),
                _ => refuse("binary operator in condition", b.op.span()),
            }
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => format!("! {}", test_string(&u.expr, cx)),
            _ => refuse("unary operator in condition", u.op.span()),
        },
        syn::Expr::Paren(p) => format!("( {} )", test_string(&p.expr, cx)),
        other => refuse("condition expression", other.span()),
    }
}

fn bin_test(op: &str, l: &syn::Expr, r: &syn::Expr, cx: &Cx) -> String {
    format!("{} {} {}", operand(l, cx), op, operand(r, cx))
}

/// Comparison operand: a variable (`$x`) or an integer literal.
fn operand(e: &syn::Expr, cx: &Cx) -> String {
    match e {
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("path in condition operand", p.span());
            };
            // a named const resolves at compile time (const folding);
            // everything else is a runtime variable read.
            if let Some(v) = cx.ints.get(&name.to_string()) {
                return v.to_string();
            }
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
