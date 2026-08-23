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


/// Minimal receiver types, enough to lower methods WITHOUT guessing:
/// the store holds JS strings / numbers / booleans / native arrays, and
/// a method's Rust semantics must map onto the exact JS the core renders.
#[derive(Clone, PartialEq, Eq, Debug)]
enum Ty {
    Int,
    Str,
    Bool,
    Arr,
    /// a named struct (`Point`) — enables receiver-method dispatch
    Struct(String),
    /// a named UNIT-variant enum (`Color`) — variants lower to their
    /// qualified tag strings (`"Color::Red"`); matching is string
    /// equality. Tuple/struct-variant enums refuse until record storage
    /// lands (payloads need object values).
    Enum(String),
    Unknown,
}

fn ty_from_type(t: &syn::Type, cx: &Cx) -> Ty {
    match t {
        syn::Type::Path(tp) => {
            let seg = match tp.path.segments.last() {
                Some(s) => s,
                None => return Ty::Unknown,
            };
            let id = seg.ident.to_string();
            if cx.structs.iter().any(|s| *s == id) {
                return Ty::Struct(id);
            }
            if cx.enums.iter().any(|e| *e == id) {
                return Ty::Enum(id);
            }
            match seg.ident.to_string().as_str() {
                "i8" | "i16" | "i32" | "i64" | "i128" | "isize" | "u8" | "u16" | "u32"
                | "u64" | "u128" | "usize" => Ty::Int,
                "str" | "String" | "Cow" => Ty::Str,
                "bool" => Ty::Bool,
                "Vec" | "VecDeque" | "HashSet" | "BTreeSet" | "HashMap" | "BTreeMap"
                | "Slice" => Ty::Arr,
                _ => Ty::Unknown,
            }
        }
        syn::Type::Reference(r) => ty_from_type(&r.elem, cx),
        syn::Type::Slice(_) => Ty::Arr,
        _ => Ty::Unknown,
    }
}

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
    /// variable -> receiver type (per-function scope; cleared between
    /// functions, re-seeded from params/literals/annotations). RefCell:
    /// type tracking mutates during lowering while Cx travels as &Cx.
    vars: std::cell::RefCell<HashMap<String, Ty>>,
    /// declared struct names (receiver-method dispatch targets)
    structs: Vec<String>,
    /// declared enum names (unit-variant tag lowering)
    enums: Vec<String>,
    /// fresh-temporary counter (field-read hoists)
    tmp: std::cell::RefCell<u32>,
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

    let mut cx = Cx::default();
    // type registries first — resolution consults them
    for item in &ast.items {
        match item {
            syn::Item::Struct(s) => cx.structs.push(s.ident.to_string()),
            syn::Item::Enum(e) => cx.enums.push(e.ident.to_string()),
            _ => {}
        }
    }
    // Flatten inline `mod` blocks into one item list — namespace nesting
    // has no effect on the lowered program (names stay as written;
    // qualified paths resolve only for `Type::method` call targets).
    let mut flat: Vec<&syn::Item> = Vec::new();
    flatten_items(&ast.items, &mut flat);

    // pre-pass: collect consts/statics (globals + int-const table) so any
    // later lowering step can const-fold against them.
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
                        fns.push(FnSource::Method(mangled, m, ty.clone()));
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
                cx.vars.borrow_mut().clear();
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
                None,
                &cx,
            ),
            FnSource::Method(name, m, ty) => {
                function_def(name.clone(), &m.attrs, &m.sig, &m.block, Some(ty), &cx)
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
        if let Some(v) = lower_stmt(stmt, cx, in_fn, out) {
            out.push(v);
        }
    }
}

fn lower_stmt(stmt: &syn::Stmt, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Option<Value> {
    match stmt {
        syn::Stmt::Local(local) => lower_local(local, cx, out),
        syn::Stmt::Expr(e, _semi) => lower_expr_stmt(e, cx, in_fn, out),
        syn::Stmt::Item(item) => refuse("nested item declarations", item.span()),
        syn::Stmt::Macro(m) => Some(lower_print_macro(&m.mac, cx)),
    }
}

fn lower_local(local: &syn::Local, cx: &Cx, out: &mut Vec<Value>) -> Option<Value> {
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
    // if-as-value: bind the target in EVERY branch (exact — rustc
    // guarantees all paths produce the value)
    if let syn::Expr::Match(m) = &*init.expr {
        return Some(match_value_node(m, BranchTarget::Var(&name), cx, false, out));
    }
    if let syn::Expr::If(ie) = &*init.expr {
        let v = if_value_node(ie, BranchTarget::Var(&name), cx, false);
        let ty = branch_ty(ie, cx);
        cx.vars.borrow_mut().insert(name.clone(), ty);
        return Some(v);
    }
    // NOTE: infer BEFORE taking the write guard (infer_ty reads vars).
    let init_ty = infer_ty(&init.expr, cx);
    // arithmetic over field reads: hoist the pure reads into temps
    if arith_contains_field(&init.expr) {
        let mut ctr = cx.tmp.borrow_mut();
        let ast = hoisted_arith(&init.expr, cx, out, &mut ctr);
        drop(ctr);
        cx.vars.borrow_mut().insert(name.clone(), Ty::Int);
        return Some(assign_stmt(&name, arith_value(ast)));
    }
    cx.vars.borrow_mut().insert(name.clone(), init_ty);
    Some(assign_stmt(&name, value_expr(&init.expr, cx)))
}

/// Infer a value's receiver type from its shape: literals are exact,
/// vec![] is an array, everything else is Unknown (Rust's own static
/// typing guarantees correctness; we only track what we can SEE).
fn infer_ty(e: &syn::Expr, cx: &Cx) -> Ty {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(_) => Ty::Int,
            syn::Lit::Str(_) => Ty::Str,
            syn::Lit::Bool(_) => Ty::Bool,
            _ => Ty::Unknown,
        },
        syn::Expr::Macro(m) => {
            let name = m.mac.path.segments.last().map(|s| s.ident.to_string()).unwrap_or_default();
            if name == "vec" {
                Ty::Arr
            } else {
                Ty::Unknown
            }
        }
        syn::Expr::Paren(p) => infer_ty(&p.expr, cx),
        syn::Expr::Reference(r) => infer_ty(&r.expr, cx),
        syn::Expr::Array(_) => Ty::Arr,
        // a struct literal HAS its named record type (receiver dispatch)
        syn::Expr::Struct(se) => {
            let seg = se.path.segments.last();
            match seg {
                Some(s) => Ty::Struct(s.ident.to_string()),
                None => Ty::Unknown,
            }
        }
        syn::Expr::Field(_) => Ty::Unknown,
        syn::Expr::Path(p) => {
            // `Color::Red` — a unit-variant VALUE has its enum's type
            if p.qself.is_none() && p.path.segments.len() == 2 {
                let e = p.path.segments[0].ident.to_string();
                if cx.enums.iter().any(|x| *x == e) {
                    return Ty::Enum(e);
                }
            }
            single_path(p)
                .and_then(|id| cx.vars.borrow().get(&id.to_string()).cloned())
                .unwrap_or(Ty::Unknown)
        }
        _ => Ty::Unknown,
    }
}

fn lower_expr_stmt(e: &syn::Expr, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Option<Value> {
    match e {
        syn::Expr::Macro(m) => Some(lower_print_macro(&m.mac, cx)),
        syn::Expr::Assign(a) => Some(plain_assign(a, cx, out)),
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
        syn::Expr::Field(_) => {
            // bare `p.x;` — a pure read, discarded (like bare `x;`)
            None
        }
        syn::Expr::Match(m) => {
            // STATEMENT-position match: unit-variant arms -> if-chain;
            // bodies are plain statements (Unit target).
            Some(match_value_node(m, BranchTarget::Unit, cx, in_fn, out))
        }
        syn::Expr::MethodCall(m) => {
            // statement-position method call: only PURE methods lower at
            // all (the whitelist in value_expr), and a discarded pure
            // call is a no-op — drop it. Anything impure/unknown already
            // refuses inside value_expr.
            let _ = value_expr(e, cx);
            None
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
        syn::Expr::Paren(p) => lower_expr_stmt(&p.expr, cx, in_fn, out),
        other => refuse("statement form", other.span()),
    }
}

fn plain_assign(a: &syn::ExprAssign, cx: &Cx, out: &mut Vec<Value>) -> Value {
    let syn::Expr::Path(p) = &*a.left else {
        refuse("assignment target (only plain variables are supported)", a.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    // `x = if c {A} else {B}` — bind x in every branch
    if let syn::Expr::If(ie) = &*a.right {
        return if_value_node(ie, BranchTarget::Var(&name.to_string()), cx, false);
    }
    // NOTE: infer BEFORE taking the write guard (infer_ty reads vars).
    let rhs_ty = infer_ty(&a.right, cx);
    // arithmetic over field reads: hoist the pure reads into temps
    if arith_contains_field(&a.right) {
        let mut ctr = cx.tmp.borrow_mut();
        let ast = hoisted_arith(&a.right, cx, out, &mut ctr);
        drop(ctr);
        cx.vars.borrow_mut().insert(name.to_string(), Ty::Int);
        return assign_stmt(&name.to_string(), arith_value(ast));
    }
    cx.vars.borrow_mut().insert(name.to_string(), rhs_ty);
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
    Method(String, &'a syn::ImplItemFn, String),
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

/// Std constructor calls with EXACT store-native values:
/// `String::from(x)` is x itself; `String::new()` is ""; `Vec::new()` /
/// map/set constructors are the empty array. Anything else -> None.
fn std_ctor_value(
    p: &syn::ExprPath,
    args: &syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
    cx: &Cx,
) -> Option<Value> {
    if p.qself.is_some() || p.path.segments.len() < 2 {
        return None;
    }
    // match the LAST two segments (`std::collections::HashMap::new`)
    let t = p.path.segments[p.path.segments.len() - 2].ident.to_string();
    let f = p.path.segments[p.path.segments.len() - 1].ident.to_string();
    match (t.as_str(), f.as_str()) {
        ("String", "from") if args.len() == 1 => Some(value_expr(&args[0], cx)),
        ("String", "new") if args.is_empty() => Some(str_lit("")),
        ("String", "with_capacity") if args.len() == 1 => Some(str_lit("")),
        ("Vec", "new") | ("Vec", "with_capacity") | ("VecDeque", "new")
        | ("HashMap", "new") | ("BTreeMap", "new")
        | ("HashSet", "new") | ("BTreeSet", "new")
            if args.is_empty() =>
        {
            Some(json!({"type": "Array", "elements": []}))
        }
        _ => None,
    }
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
    let owned: Vec<syn::Expr> = args.iter().cloned().collect();
    fn_call_expr_values(name, owned, func, cx)
}

/// The same call node over already-collected argument expressions (the
/// receiver-method dispatch passes the receiver as first positional).
fn fn_call_expr_values<I>(name: &str, args: I, func: &str, cx: &Cx) -> Value
where
    I: IntoIterator<Item = syn::Expr>,
{
    json!({
        "args": [
            str_lit(name),
            json!({"elements": args.into_iter().map(|a| value_expr(&a, cx)).collect::<Vec<_>>(), "type": "Array"}),
        ],
        "func": func, "purity": "Emulable", "type": "Call"
    })
}

/// Does e contain a field read inside its ARITHMETIC spine? Field reads
/// cannot be Arith operands (the A1 arith AST has no field form), so such
/// exprs need temporary hoisting.
fn arith_contains_field(e: &syn::Expr) -> bool {
    match e {
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            if matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_) | And(_) | Or(_)) {
                false // comparison spine lowers via the test grammar
            } else {
                arith_contains_field(&b.left) || arith_contains_field(&b.right)
            }
        }
        syn::Expr::Unary(u) => arith_contains_field(&u.expr),
        syn::Expr::Paren(p) => arith_contains_field(&p.expr),
        syn::Expr::Field(_) => true,
        _ => false,
    }
}

/// Build the A1 ARITH AST for an arithmetic expression whose field-read
/// leaves are extracted into fresh temporaries; the hoist assignments are
/// appended to `stmts` IN ORDER (pure reads => the snapshot is exact).
/// Returns the raw arith AST json (Num/Var/Bin) — wrap in
/// {"type":"Arith","ast":..} at the consumption site.
fn hoisted_arith(
    e: &syn::Expr,
    cx: &Cx,
    stmts: &mut Vec<Value>,
    ctr: &mut u32,
) -> Value {
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
            // named consts fold (mirrors the condition operand rule)
            if let Some(v) = cx.ints.get(&name.to_string()) {
                return arith_num(*v);
            }
            arith_var(&name.to_string())
        }
        syn::Expr::Paren(p) => hoisted_arith(&p.expr, cx, stmts, ctr),
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            let op = match b.op {
                Add(_) => "+",
                Sub(_) => "-",
                Mul(_) => "*",
                Div(_) => "/",
                Rem(_) => "%",
                _ => refuse("binary operator in arithmetic", b.op.span()),
            };
            json!({
                "lhs": hoisted_arith(&b.left, cx, stmts, ctr),
                "op": op,
                "rhs": hoisted_arith(&b.right, cx, stmts, ctr),
                "type": "Bin",
            })
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Neg(_) => json!({
                "lhs": arith_num(0),
                "op": "-",
                "rhs": hoisted_arith(&u.expr, cx, stmts, ctr),
                "type": "Bin",
            }),
            _ => refuse("unary operator in arithmetic", u.op.span()),
        },
        syn::Expr::Field(f) => {
            let name = match &f.member {
                syn::Member::Named(ident) => ident.to_string(),
                syn::Member::Unnamed(ix) => refuse("tuple-index field read", ix.span()),
            };
            *ctr += 1;
            let temp = format!("__sh2f{}", ctr);
            stmts.push(assign_stmt(
                &temp,
                json!({
                    "type": "FieldRead",
                    "object": value_expr(&f.base, cx),
                    "name": name,
                }),
            ));
            arith_var(&temp)
        }
        other => refuse("expression in arithmetic", other.span()),
    }
}

/// Wrap a raw arith AST into the A1 Arith VALUE expr.
fn arith_value(ast: Value) -> Value {
    json!({"type": "Arith", "ast": ast})
}

/// A block's VALUE expression: a semi-less trailing Expr, else None
/// (unit block).
fn block_value(b: &syn::Block) -> Option<&syn::Expr> {
    match b.stmts.last()? {
        syn::Stmt::Expr(e, None) => Some(e),
        _ => None,
    }
}

/// Where an if-expression's per-branch VALUE goes: a variable binding
/// (assigned in every branch — rustc guarantees all paths produce the
/// value) or a native return.
#[derive(Clone, Copy)]
enum BranchTarget<'a> {
    Var(&'a str),
    Ret,
    /// statement-position match: bodies are plain statements
    Unit,
}

/// A variable's type from an if-expression: the first provable branch
/// value's type (Rust guarantees both branches unify).
fn branch_ty(ie: &syn::ExprIf, cx: &Cx) -> Ty {
    if let Some(v) = block_value(&ie.then_branch) {
        let t = infer_ty(v, cx);
        if t != Ty::Unknown {
            return t;
        }
    }
    match &ie.else_branch {
        Some((_, inner)) => match &**inner {
            syn::Expr::Block(blk) => {
                block_value(&blk.block).map(|v| infer_ty(v, cx)).unwrap_or(Ty::Unknown)
            }
            syn::Expr::If(inner) => branch_ty(inner, cx),
            _ => Ty::Unknown,
        },
        None => Ty::Unknown,
    }
}

/// Push a branch BLOCK's statements for the given target: the trailing
/// semi-less expression is the branch VALUE (bound/returned), everything
/// else lowers normally. Unit targets just lower statements.
fn push_branch_block(
    b: &syn::Block,
    target: BranchTarget,
    cx: &Cx,
    in_fn: bool,
    out: &mut Vec<Value>,
) {
    let n = b.stmts.len();
    for (i, s) in b.stmts.iter().enumerate() {
        if i + 1 == n {
            if let syn::Stmt::Expr(e, None) = s {
                match target {
                    BranchTarget::Var(name) => {
                        out.push(assign_stmt(name, value_expr(e, cx)))
                    }
                    BranchTarget::Ret => {
                        out.push(json!({"type": "Return", "value": value_expr(e, cx)}))
                    }
                    BranchTarget::Unit => {
                        if let Some(v) = lower_expr_stmt(e, cx, in_fn, out) {
                            out.push(v);
                        }
                    }
                    _ => unreachable!("Var/Ret handled above"),
                }
                continue;
            }
        }
        if let Some(v) = lower_stmt(s, cx, in_fn, out) {
            out.push(v);
        }
    }
}

/// A UNIT-variant pattern (`Color::Red`) -> its qualified tag string;
/// None for anything else (payload variants, literals, bindings).
fn unit_variant_pat(p: &syn::Pat, cx: &Cx) -> Option<String> {
    if let syn::Pat::Path(pp) = p {
        if pp.qself.is_none()
            && pp.path.leading_colon.is_none()
            && pp.path.segments.len() == 2
            && pp.path.segments[1].arguments.is_none()
        {
            let e = pp.path.segments[0].ident.to_string();
            let v = pp.path.segments[1].ident.to_string();
            if cx.enums.iter().any(|x| *x == e) {
                return Some(format!("{e}::{v}"));
            }
        }
    }
    None
}

/// A match arm's test condition: string equality on the hoisted
/// scrutinee, or-patterns join with `-o`.
fn arm_cond(temp: &str, pat: &syn::Pat, cx: &Cx) -> Option<String> {
    fn one(temp: &str, p: &syn::Pat, cx: &Cx) -> Option<String> {
        unit_variant_pat(p, cx).map(|tag| format!("${temp} = \"{tag}\""))
    }
    match pat {
        syn::Pat::Or(o) => {
            let mut parts = Vec::new();
            for p2 in &o.cases {
                parts.push(one(temp, p2, cx)?);
            }
            Some(parts.join(" -o "))
        }
        other => one(temp, other, cx),
    }
}

/// Lower a `match` whose arms are ALL unit-variant patterns (or `_`):
/// nested Ifs over string-equality tests on a hoisted scrutinee. The
/// LAST arm is the fall-through else (rustc guarantees exhaustiveness,
/// so reaching it means nothing earlier matched — exact). Guards and
/// payload patterns refuse.
fn match_value_node(
    m: &syn::ExprMatch,
    target: BranchTarget,
    cx: &Cx,
    in_fn: bool,
    out: &mut Vec<Value>,
) -> Value {
    if !m.arms.iter().all(|a| a.guard.is_none()) {
        refuse("match guard", m.span());
    }
    // scrutinee hoist — any expression, read once (exact: Rust moves/
    // borrows the scrutinee once too)
    let n = cx.tmp.borrow_mut();
    let temp = format!("__sh2m{n}");
    drop(n);
    out.push(assign_stmt(&temp, value_expr(&m.expr, cx)));
    let mut else_: Vec<Value> = Vec::new();
    for arm in m.arms.iter().rev() {
        let Some(cond_s) = arm_cond(&temp, &arm.pat, cx) else {
            refuse(
                "match pattern (only unit variants, or-patterns of them, and `_`)",
                arm.pat.span(),
            );
        };
        let mut then = Vec::new();
        match &*arm.body {
            syn::Expr::Block(blk) => {
                push_branch_block(&blk.block, target, cx, in_fn, &mut then)
            }
            body_e => match target {
                BranchTarget::Var(name) => {
                    then.push(assign_stmt(name, value_expr(body_e, cx)))
                }
                BranchTarget::Ret => then.push(
                    json!({"type": "Return", "value": value_expr(body_e, cx)}),
                ),
                BranchTarget::Unit => {
                    if let Some(v) = lower_expr_stmt(body_e, cx, in_fn, &mut then) {
                        then.push(v);
                    }
                }
            },
        }
        else_ = vec![json!({
            "cond": test_call(&cond_s),
            "then": then,
            "elsifs": [],
            "else": else_,
            "type": "If",
        })];
    }
    else_.remove(0)
}

/// Lower `if c {A} else {B}` used as a VALUE into the equivalent A1 If:
/// each branch lowers its statements then binds/returns the branch value.
/// else-if chains recurse (nested If in the else position). A missing
/// else refuses (rustc rejects it too — never reached for valid code).
fn if_value_node(
    ie: &syn::ExprIf,
    target: BranchTarget,
    cx: &Cx,
    in_fn: bool,
) -> Value {
    fn push_branch(
        b: &syn::Block,
        target: BranchTarget,
        cx: &Cx,
        in_fn: bool,
        out: &mut Vec<Value>,
    ) {
        let n = b.stmts.len();
        for (i, s) in b.stmts.iter().enumerate() {
            if i + 1 == n {
                if let syn::Stmt::Expr(e, None) = s {
                    match target {
                        BranchTarget::Var(name) => {
                            out.push(assign_stmt(name, value_expr(e, cx)))
                        }
                        BranchTarget::Ret => out.push(
                            json!({"type": "Return", "value": value_expr(e, cx)}),
                        ),
                        BranchTarget::Unit => {
                            if let Some(v) = lower_expr_stmt(e, cx, in_fn, out) {
                                out.push(v);
                            }
                        }
                    }
                    continue;
                }
            }
            if let Some(v) = lower_stmt(s, cx, in_fn, out) {
                out.push(v);
            }
        }
    }
    let cond = test_call(&test_string(&ie.cond, cx));
    let mut then = Vec::new();
    push_branch(&ie.then_branch, target, cx, in_fn, &mut then);
    let else_ = match &ie.else_branch {
        None => refuse("if-expression without else", ie.span()),
        Some((_, inner)) => match &**inner {
            syn::Expr::If(inner_if) => vec![if_value_node(inner_if, target, cx, in_fn)],
            syn::Expr::Block(blk) => {
                let mut v = Vec::new();
                push_branch(&blk.block, target, cx, in_fn, &mut v);
                v
            }
            other => refuse("else form in if-expression", other.span()),
        },
    };
    json!({"cond": cond, "then": then, "elsifs": [], "else": else_, "type": "If"})
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
    self_ty: Option<&str>,
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
    // fresh variable-type scope per function (params re-seed it)
    cx.vars.borrow_mut().clear();
    let mut body: Vec<Value> = Vec::new();
    let mut seen: Vec<String> = Vec::new();
    for (i, input) in sig.inputs.iter().enumerate() {
        let pt = match input {
            syn::FnArg::Typed(pt) => pt,
            syn::FnArg::Receiver(r) => {
                // `&self` / `&mut self` / `self` — the receiver object is
                // the FIRST positional (the dispatch passes it there).
                // All three forms share one exact lowering: JS objects are
                // references, so `&mut self` writes through to the caller
                // (true), `&self` can't mutate (true), and by-value `self`
                // moves are use-after-move-checked by rustc itself (no
                // aliasing hazard reaches us).
                let Some(st) = self_ty else {
                    refuse("method receiver outside an impl block", r.span())
                };
                if seen.contains(&"self".to_string()) {
                    refuse("duplicate parameter name", r.span());
                }
                seen.push("self".to_string());
                cx.vars
                    .borrow_mut()
                    .insert("self".to_string(), Ty::Struct(st.to_string()));
                body.push(assign_stmt("self", get_var(&(i + 1).to_string())));
                continue;
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
        cx.vars.borrow_mut().insert(pname.clone(), ty_from_type(&pt.ty, cx));
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
                        // an `if`/`match` tail IS the function's value on
                        // every path: per-branch Returns. loop tails still
                        // refuse.
                        match e {
                            syn::Expr::If(ie) => body.push(if_value_node(
                                ie,
                                BranchTarget::Ret,
                                cx,
                                true,
                            )),
                            syn::Expr::Match(m) => {
                                let node =
                                    match_value_node(m, BranchTarget::Ret, cx, true, &mut body);
                                body.push(node);
                            }
                            _ => refuse(
                                "tail control-flow expression in a value-returning fn \
                                 (loop values not supported)",
                                e.span(),
                            ),
                        }
                        continue;
                    }
                    if let Some(v) = lower_stmt(stmt, cx, true, &mut body) {
                        body.push(v);
                    }
                    continue;
                }
                if returns_value {
                    // arithmetic over field reads hoists its pure reads
                    // into temporaries first (the arith AST has no
                    // field operand form)
                    if arith_contains_field(e) {
                        let mut ctr = cx.tmp.borrow_mut();
                        let ast = hoisted_arith(e, cx, &mut body, &mut ctr);
                        drop(ctr);
                        body.push(json!({"type": "Return", "value": arith_value(ast)}));
                        continue;
                    }
                    body.push(json!({"type": "Return", "value": value_expr(e, cx)}));
                } else if let Some(v) = lower_stmt(stmt, cx, true, &mut body) {
                    body.push(v);
                }
                continue;
            }
        }
        if let Some(v) = lower_stmt(stmt, cx, true, &mut body) {
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
            // `Color::Red` — a UNIT-variant value lowers to its qualified
            // tag string (matching is string equality). Tuple/struct
            // variants carry payloads and refuse until record storage.
            if p.qself.is_none() && p.path.leading_colon.is_none() && p.path.segments.len() == 2 {
                let e = p.path.segments[0].ident.to_string();
                let v = p.path.segments[1].ident.to_string();
                if cx.enums.iter().any(|x| *x == e) {
                    if !p.path.segments[1].arguments.is_none() {
                        refuse("enum variant with payload", p.path.segments[1].span());
                    }
                    return str_lit(&format!("{e}::{v}"));
                }
            }
            let Some(name) = single_path(p) else {
                refuse("path expression", p.span());
            };
            get_var(&name.to_string())
        }
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            if matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_) | And(_) | Or(_)) {
                // VALUE-position boolean: the fleet convention (py-sh-go
                // CompareE) — a `test` call, which the runtime evaluates
                // to a native JS boolean (prints like Rust's bool {}).
                // Operands are ints/int-vars (string comparison refuses
                // in the test grammar — loud, not guessed).
                test_call(&test_string(e, cx))
            } else {
                arith_from_binary(b, cx)
            }
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => test_call(&test_string(e, cx)),
            _ => arith_from_unary(u, cx),
        },
        syn::Expr::Array(ar) => {
            // `[e1, e2]` -> an A1 Array literal, exactly like vec![..] —
            // the runtime store keeps native JS arrays.
            json!({
                "type": "Array",
                "elements": ar.elems.iter().map(|a| value_expr(a, cx)).collect::<Vec<_>>(),
            })
        }
        syn::Expr::Reference(r) => {
            // SHARED borrows erase to the value itself: while a `&T`
            // borrow lives, Rust forbids mutation through the original
            // (no `&mut` aliasing exists), so a value SNAPSHOT has exactly
            // the borrow's observable semantics. `&mut` refuses — writes
            // through it are NOT representable in the value store.
            if r.mutability.is_some() {
                refuse("`&mut` borrow (mutation through a borrow is not representable)", r.span());
            }
            value_expr(&r.expr, cx)
        }
        syn::Expr::Macro(m) => {
            // `vec![a, b, c]` -> an A1 Array literal — the runtime store
            // keeps native JS arrays, so this is the EXACT value shape
            // (infer_ty already types vec![] as Arr).
            let name = m.mac.path.segments.last().map(|s| s.ident.to_string()).unwrap_or_default();
            if name != "vec" {
                refuse(&format!("macro `{name}!` in value position"), m.span());
            }
            struct VecElems(syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>);
            impl syn::parse::Parse for VecElems {
                fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
                    Ok(VecElems(
                        syn::punctuated::Punctuated::parse_terminated(input)?,
                    ))
                }
            }
            let VecElems(elems) = syn::parse2(m.mac.tokens.clone())
                .unwrap_or_else(|e| refuse(&format!("vec! arguments: {e}"), m.span()));
            json!({
                "type": "Array",
                "elements": elems.iter().map(|a| value_expr(a, cx)).collect::<Vec<_>>(),
            })
        }
        syn::Expr::Struct(se) => {
            // `Point { x: 3, y: 4 }` -> an A1 Object literal (the store's
            // record value; field shorthand lowers like any path read).
            // Struct-update rest (`..base`) refuses — field merging is
            // not representable yet.
            if let Some(rest) = &se.rest {
                refuse("struct-update syntax (`..base`)", rest.span());
            }
            let mut props: Vec<Value> = Vec::new();
            for f in &se.fields {
                let key = match &f.member {
                    syn::Member::Named(ident) => ident.to_string(),
                    syn::Member::Unnamed(ix) => {
                        refuse("tuple-struct positional field", ix.span())
                    }
                };
                props.push(json!({"key": key, "value": value_expr(&f.expr, cx)}));
            }
            json!({"type": "Object", "properties": props})
        }
        syn::Expr::Field(f) => {
            // `p.x` -> the FieldRead ext node (shir_nodes/field_read.node:
            // named field read on a composite value). Tuple reads (`p.0`)
            // refuse — no tuple contract yet.
            let name = match &f.member {
                syn::Member::Named(ident) => ident.to_string(),
                syn::Member::Unnamed(ix) => refuse("tuple-index field read", ix.span()),
            };
            json!({
                "type": "FieldRead",
                "object": value_expr(&f.base, cx),
                "name": name,
            })
        }
        syn::Expr::MethodCall(m) => {
            // obj.method(args) -> the A1 MethodCall expr (the contract's
            // generic member call; the ESTree path renders it as a NATIVE
            // JS call). The method NAME must be a valid JS member whose
            // semantics match Rust exactly — the table below only maps
            // string-receiver methods where the mapping is 1:1 and pure.
            // Everything else refuses (REFUSE > GUESS): iterators,
            // Vec methods, bool-returning predicates, indexing...
            let recv_ty = infer_ty(&m.receiver, cx);
            let rust_name = m.method.to_string();
            if let Ty::Struct(ty_name) = &recv_ty {
                // inherent method dispatch: `obj.m(args)` ->
                // fnValue("Type_m", [obj, args...]) — the mangled def's
                // first positional IS the receiver object (its `self`).
                let target = format!("{}_{}", ty_name, rust_name);
                if !cx.fns.iter().any(|f| *f == target) {
                    refuse(&format!("no method `{}` on struct `{}`", rust_name, ty_name),
                           m.method.span());
                }
                let mut call_args: Vec<syn::Expr> = vec![(*m.receiver).clone()];
                call_args.extend(m.args.iter().cloned());
                return fn_call_expr_values(&target, call_args, "fnValue", cx);
            }
            let js_name = match (&recv_ty, rust_name.as_str()) {
                (Ty::Str, "contains") => "includes",
                (Ty::Str, "starts_with") => "startsWith",
                (Ty::Str, "ends_with") => "endsWith",
                (Ty::Str, "trim") => "trim",
                (Ty::Str, "trim_start") => "trimStart",
                (Ty::Str, "trim_end") => "trimEnd",
                (Ty::Str, "to_lowercase") => "toLowerCase",
                (Ty::Str, "to_uppercase") => "toUpperCase",
                _ => refuse(
                    &format!(
                        "method `{}` on {}receiver (no proven JS-equivalent yet)",
                        rust_name,
                        match recv_ty {
                            Ty::Int => "int ",
                            Ty::Str => "string ",
                            Ty::Bool => "bool ",
                            Ty::Arr => "array ",
                            Ty::Struct(ref n) => {
                                unreachable!("struct receivers dispatched above: {}", n)
                            }
                            Ty::Enum(ref n) => {
                                unreachable!("enum receivers are not method targets: {}", n)
                            }
                            Ty::Unknown => "",
                        }
                    ),
                    m.method.span(),
                ),
            };
            json!({
                "type": "MethodCall",
                "object": value_expr(&m.receiver, cx),
                "method": js_name,
                "args": m.args.iter().map(|a| value_expr(a, cx)).collect::<Vec<_>>(),
            })
        }
        syn::Expr::Paren(p) => value_expr(&p.expr, cx),
        syn::Expr::Call(c) => {
            // value-position call: the VALUE-returning dispatch (fnValue,
            // c-sh-go t58). NOTE: calls inside ARITHMETIC refuse — the A1
            // arith AST has no call operand (hoisting to temporaries is a
            // later, semantics-preserving frontend step).
            let Some(syn::Expr::Path(p)) = Some(c.func.as_ref()) else {
                refuse("call target path", c.func.span());
            };
            if let Some(v) = std_ctor_value(p, &c.args, cx) {
                return v;
            }
            let Some(target) = call_target(p, cx) else {
                refuse("call to unknown function", c.func.span());
            };
            fn_call_expr(&target, &c.args, "fnValue", cx)
        }
        other => refuse(
            &format!("expression kind `{}`", expr_kind(other)),
            other.span(),
        ),
    }
}

/// A stable name for an expression variant — refusal diagnostics only.
fn expr_kind(e: &syn::Expr) -> &'static str {
    match e {
        syn::Expr::Array(_) => "array literal",
        syn::Expr::Assign(_) => "assignment",
        syn::Expr::Async(_) => "async block",
        syn::Expr::Await(_) => ".await",
        syn::Expr::Binary(_) => "binary op",
        syn::Expr::Block(_) => "block",
        syn::Expr::Break(_) => "break",
        syn::Expr::Call(_) => "call",
        syn::Expr::Cast(_) => "cast (`as`)",
        syn::Expr::Closure(_) => "closure",
        syn::Expr::Const(_) => "const block",
        syn::Expr::Continue(_) => "continue",
        syn::Expr::Field(_) => "field access",
        syn::Expr::ForLoop(_) => "for loop",
        syn::Expr::Group(_) => "group",
        syn::Expr::If(_) => "if",
        syn::Expr::Index(_) => "indexing",
        syn::Expr::Infer(_) => "_",
        syn::Expr::Let(_) => "let-chain",
        syn::Expr::Lit(_) => "literal",
        syn::Expr::Loop(_) => "loop",
        syn::Expr::Macro(_) => "macro",
        syn::Expr::Match(_) => "match",
        syn::Expr::MethodCall(_) => "method call",
        syn::Expr::Paren(_) => "parens",
        syn::Expr::Path(_) => "path",
        syn::Expr::Range(_) => "range",
        syn::Expr::Reference(_) => "borrow `&x`",
        syn::Expr::Repeat(_) => "[e; n]",
        syn::Expr::Return(_) => "return",
        syn::Expr::Struct(_) => "struct literal",
        syn::Expr::Try(_) => "`?`",
        syn::Expr::TryBlock(_) => "try block",
        syn::Expr::Tuple(_) => "tuple",
        syn::Expr::Unary(_) => "unary op",
        syn::Expr::Unsafe(_) => "unsafe block",
        syn::Expr::Verbatim(_) => "verbatim (macro-expanded)",
        syn::Expr::While(_) => "while",
        syn::Expr::Yield(_) => "yield",
        _ => "other",
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
                Eq(_) => bin_test(&eq_op(&b.left, &b.right, false, cx), &b.left, &b.right, cx),
                Ne(_) => bin_test(&eq_op(&b.left, &b.right, true, cx), &b.left, &b.right, cx),
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
        // a bare boolean VARIABLE is not representable in the test
        // grammar: `$b` alone parses as a nonempty-string/file test, not
        // a bool read. Refuse until a bool-var contract lands.
        syn::Expr::Path(p) => {
            refuse(
                "boolean variable as a condition (bool vars not yet representable)",
                p.span(),
            )
        }
        other => refuse("condition expression", other.span()),
    }
}

fn bin_test(op: &str, l: &syn::Expr, r: &syn::Expr, cx: &Cx) -> String {
    let str_eq = op == "=" || op == "!=";
    format!(
        "{} {} {}",
        operand(l, cx, str_eq),
        op,
        operand(r, cx, str_eq)
    )
}

/// Comparison operand: a variable (`$x`) or an integer literal. For
/// STRING equality tests this also admits quoted string literals and
/// unit-variant values (their qualified tag strings).
fn operand(e: &syn::Expr, cx: &Cx, str_eq: bool) -> String {
    match e {
        syn::Expr::Path(p) => {
            // a named const resolves at compile time (const folding);
            // everything else is a runtime variable read.
            if p.qself.is_none() && p.path.segments.len() == 2 && str_eq {
                let en = p.path.segments[0].ident.to_string();
                let v = p.path.segments[1].ident.to_string();
                if cx.enums.iter().any(|x| *x == en) {
                    return format!("\"{en}::{v}\"");
                }
            }
            let Some(name) = single_path(p) else {
                refuse("path in condition operand", p.span());
            };
            if let Some(v) = cx.ints.get(&name.to_string()) {
                return v.to_string();
            }
            format!("${name}")
        }
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => int_text(li),
            syn::Lit::Str(ls) if str_eq => format!("\"{}\"", ls.value()),
            other => refuse("literal in condition operand", other.span()),
        },
        other => refuse(
            "condition operand (variable or integer literal only)",
            other.span(),
        ),
    }
}

/// Equality operator for a comparison: numeric `-eq`/`-ne` unless either
/// side is PROVEN textual (a string literal / enum variant path / a var
/// of Str or Enum type) — then bash string equality `=` / `!=`.
fn eq_op(l: &syn::Expr, r: &syn::Expr, ne: bool, cx: &Cx) -> &'static str {
    fn textual(e: &syn::Expr, cx: &Cx) -> bool {
        match e {
            syn::Expr::Lit(l2) => matches!(l2.lit, syn::Lit::Str(_)),
            syn::Expr::Reference(r2) => textual(&r2.expr, cx),
            syn::Expr::Paren(p) => textual(&p.expr, cx),
            syn::Expr::Path(p) => {
                if p.qself.is_none() && p.path.segments.len() == 2 {
                    let en = p.path.segments[0].ident.to_string();
                    if cx.enums.iter().any(|x| *x == en) {
                        return true;
                    }
                }
                single_path(p)
                    .and_then(|id| cx.vars.borrow().get(&id.to_string()).cloned())
                    .map(|t| matches!(t, Ty::Str | Ty::Enum(_)))
                    .unwrap_or(false)
            }
            _ => false,
        }
    }
    if textual(l, cx) || textual(r, cx) {
        if ne {
            "!="
        } else {
            "="
        }
    } else if ne {
        "-ne"
    } else {
        "-eq"
    }
}
