//! census — idiom tally over .rs files, to drive the rust-frontend
//! expansion roadmap. Walks every file with syn and counts constructs by
//! kind (items, statements, exprs, pats, types, macro names), so we can
//! see WHICH unsupported idioms dominate src/ instead of stopping at the
//! first refusal.
//!
//! usage: census <file.rs>...   (aggregate counts across all files)

use std::collections::BTreeMap;
use std::process::exit;

#[derive(Default)]
struct Tally {
    items: BTreeMap<String, usize>,
    stmts: BTreeMap<String, usize>,
    exprs: BTreeMap<String, usize>,
    pats: BTreeMap<String, usize>,
    types: BTreeMap<String, usize>,
    macros: BTreeMap<String, usize>,
    ops: BTreeMap<String, usize>,
}

impl Tally {
    fn bump(map: &mut BTreeMap<String, usize>, k: &str) {
        *map.entry(k.to_string()).or_default() += 1;
    }
    #[allow(dead_code)]
    fn merge(&mut self, other: &Tally) {
        for (m, o) in [
            (&mut self.items, &other.items),
            (&mut self.stmts, &other.stmts),
            (&mut self.exprs, &other.exprs),
            (&mut self.pats, &other.pats),
            (&mut self.types, &other.types),
            (&mut self.macros, &other.macros),
            (&mut self.ops, &other.ops),
        ] {
            for (k, v) in o {
                *m.entry(k.clone()).or_default() += v;
            }
        }
    }
}

fn walk_file(file: &syn::File, t: &mut Tally) {
    for item in &file.items {
        walk_item(item, t);
    }
}

fn walk_item(item: &syn::Item, t: &mut Tally) {
    match item {
        syn::Item::Fn(f) => {
            Tally::bump(&mut t.items, "fn");
            if f.sig.asyncness.is_some() {
                Tally::bump(&mut t.items, "fn.async");
            }
            if f.sig.unsafety.is_some() {
                Tally::bump(&mut t.items, "fn.unsafe");
            }
            if f.sig.constness.is_some() {
                Tally::bump(&mut t.items, "fn.const");
            }
            if !f.sig.generics.params.is_empty() {
                Tally::bump(&mut t.items, "fn.generics");
            }
            if !matches!(f.sig.output, syn::ReturnType::Default) {
                Tally::bump(&mut t.items, "fn.ret_type");
            }
            for i in &f.sig.inputs {
                match i {
                    syn::FnArg::Receiver(_) => Tally::bump(&mut t.items, "arg.self"),
                    syn::FnArg::Typed(pt) => {
                        walk_type(&pt.ty, t);
                        walk_pat(&pt.pat, t);
                    }
                }
            }
            walk_block(&f.block, t);
        }
        syn::Item::Struct(s) => {
            Tally::bump(&mut t.items, "struct");
            for f in &s.fields {
                walk_type(&f.ty, t);
            }
        }
        syn::Item::Enum(e) => {
            Tally::bump(&mut t.items, "enum");
            for v in &e.variants {
                match &v.fields {
                    syn::Fields::Named(_) => Tally::bump(&mut t.items, "enum.variant.named"),
                    syn::Fields::Unnamed(_) => Tally::bump(&mut t.items, "enum.variant.tuple"),
                    syn::Fields::Unit => Tally::bump(&mut t.items, "enum.variant.unit"),
                }
            }
        }
        syn::Item::Impl(i) => {
            Tally::bump(&mut t.items, "impl");
            if i.trait_.is_some() {
                Tally::bump(&mut t.items, "impl.trait");
            }
            walk_type(&i.self_ty, t);
            for ii in &i.items {
                if let syn::ImplItem::Fn(f) = ii {
                    Tally::bump(&mut t.items, "impl.fn");
                    walk_block(&f.block, t);
                } else {
                    Tally::bump(&mut t.items, "impl.other_item");
                }
            }
        }
        syn::Item::Trait(tr) => {
            Tally::bump(&mut t.items, "trait");
            for ti in &tr.items {
                if let syn::TraitItem::Fn(f) = ti {
                    Tally::bump(&mut t.items, "trait.fn");
                    if let Some(blk) = &f.default {
                        walk_block(blk, t);
                    }
                } else {
                    Tally::bump(&mut t.items, "trait.other_item");
                }
            }
        }
        syn::Item::Use(_) => Tally::bump(&mut t.items, "use"),
        syn::Item::Const(c) => {
            Tally::bump(&mut t.items, "const");
            walk_type(&c.ty, t);
            walk_expr(&c.expr, t);
        }
        syn::Item::Static(s) => {
            Tally::bump(&mut t.items, "static");
            walk_type(&s.ty, t);
            walk_expr(&s.expr, t);
        }
        syn::Item::Mod(m) => {
            Tally::bump(&mut t.items, "mod");
            if let Some((_, inner)) = &m.content {
                for item in inner {
                    walk_item(item, t);
                }
            }
        }
        syn::Item::Type(ty) => {
            Tally::bump(&mut t.items, "type_alias");
            walk_type(&ty.ty, t);
        }
        syn::Item::Macro(m) => {
            let name = m
                .mac
                .path
                .segments
                .last()
                .map(|s| s.ident.to_string())
                .unwrap_or_default();
            Tally::bump(&mut t.macros, &format!("item:{name}"));
        }
        _ => Tally::bump(&mut t.items, "other_item"),
    }
}

fn walk_block(b: &syn::Block, t: &mut Tally) {
    for s in &b.stmts {
        walk_stmt(s, t);
    }
}

fn walk_stmt(s: &syn::Stmt, t: &mut Tally) {
    match s {
        syn::Stmt::Local(l) => {
            Tally::bump(&mut t.stmts, "let");
            if l.init.is_none() {
                Tally::bump(&mut t.stmts, "let.deferred");
            } else if let Some((_, els)) = &l.init.as_ref().unwrap().diverge {
                Tally::bump(&mut t.stmts, "let.else");
                walk_expr(els, t);
            }
            walk_pat(&l.pat, t);
            if let Some(init) = &l.init {
                walk_expr(&init.expr, t);
            }
        }
        syn::Stmt::Expr(e, semi) => {
            if semi.is_some() {
                Tally::bump(&mut t.stmts, "expr_stmt");
            }
            walk_expr(e, t);
        }
        syn::Stmt::Item(i) => walk_item(i, t),
        syn::Stmt::Macro(m) => {
            let name = m
                .mac
                .path
                .segments
                .last()
                .map(|s| s.ident.to_string())
                .unwrap_or_default();
            Tally::bump(&mut t.macros, &name);
        }
    }
}

fn bin_op_name(op: &syn::BinOp) -> &'static str {
    use syn::BinOp::*;
    match op {
        Add(_) => "+",
        Sub(_) => "-",
        Mul(_) => "*",
        Div(_) => "/",
        Rem(_) => "%",
        And(_) => "&&",
        Or(_) => "||",
        BitXor(_) => "^",
        BitAnd(_) => "&",
        BitOr(_) => "|",
        Shl(_) => "<<",
        Shr(_) => ">>",
        Eq(_) => "==",
        Lt(_) => "<",
        Le(_) => "<=",
        Ne(_) => "!=",
        Ge(_) => ">=",
        Gt(_) => ">",
        AddAssign(_) => "+=",
        SubAssign(_) => "-=",
        MulAssign(_) => "*=",
        DivAssign(_) => "/=",
        RemAssign(_) => "%=",
        BitXorAssign(_) => "^=",
        BitAndAssign(_) => "&=",
        BitOrAssign(_) => "|=",
        ShlAssign(_) => "<<=",
        ShrAssign(_) => ">>=",
        _ => "?",
    }
}

fn un_op_name(op: &syn::UnOp) -> &'static str {
    match op {
        syn::UnOp::Deref(_) => "*deref",
        syn::UnOp::Not(_) => "!",
        syn::UnOp::Neg(_) => "-",
        _ => "?",
    }
}

fn walk_expr(e: &syn::Expr, t: &mut Tally) {
    match e {
        syn::Expr::Binary(b) => {
            Tally::bump(&mut t.exprs, "bin");
            Tally::bump(&mut t.ops, bin_op_name(&b.op));
            walk_expr(&b.left, t);
            walk_expr(&b.right, t);
        }
        syn::Expr::Unary(u) => {
            Tally::bump(&mut t.exprs, "unary");
            Tally::bump(&mut t.ops, un_op_name(&u.op));
            walk_expr(&u.expr, t);
        }
        syn::Expr::MethodCall(m) => {
            let name = m.method.to_string();
            Tally::bump(&mut t.exprs, &format!("method.{name}"));
            walk_expr(&m.receiver, t);
            for a in &m.args {
                walk_expr(a, t);
            }
        }
        syn::Expr::Call(c) => {
            Tally::bump(&mut t.exprs, "call");
            walk_expr(&c.func, t);
            for a in &c.args {
                walk_expr(a, t);
            }
        }
        syn::Expr::Path(p) => {
            Tally::bump(&mut t.exprs, "path");
            let seg: Vec<String> =
                p.path.segments.iter().map(|s| s.ident.to_string()).collect();
            if seg.len() > 1 || p.path.leading_colon.is_some() {
                Tally::bump(&mut t.exprs, &format!("qualified_path.{}", seg.join("::")));
            }
        }
        syn::Expr::Lit(l) => {
            let k = match &l.lit {
                syn::Lit::Int(_) => "lit.int",
                syn::Lit::Str(_) => "lit.str",
                syn::Lit::Bool(_) => "lit.bool",
                syn::Lit::Float(_) => "lit.float",
                syn::Lit::Char(_) => "lit.char",
                syn::Lit::ByteStr(_) => "lit.bytestr",
                _ => "lit.other",
            };
            Tally::bump(&mut t.exprs, k);
        }
        syn::Expr::Field(f) => {
            Tally::bump(&mut t.exprs, "field");
            walk_expr(&f.base, t);
        }
        syn::Expr::Index(ix) => {
            Tally::bump(&mut t.exprs, "index");
            walk_expr(&ix.expr, t);
            walk_expr(&ix.index, t);
        }
        syn::Expr::If(ie) => {
            Tally::bump(&mut t.exprs, "if");
            if ie.else_branch.is_none() {
                Tally::bump(&mut t.exprs, "if.no_else");
            }
            walk_expr(&ie.cond, t);
            walk_block(&ie.then_branch, t);
            if let Some((_, el)) = &ie.else_branch {
                walk_expr(el, t);
            }
        }
        syn::Expr::Match(m) => {
            Tally::bump(&mut t.exprs, "match");
            walk_expr(&m.expr, t);
            for arm in &m.arms {
                walk_pat(&arm.pat, t);
                if let Some((_, g)) = &arm.guard {
                    walk_expr(g, t);
                }
                walk_expr(&arm.body, t);
            }
        }
        syn::Expr::While(w) => {
            Tally::bump(&mut t.exprs, "while");
            if w.label.is_some() {
                Tally::bump(&mut t.exprs, "labeled");
            }
            walk_expr(&w.cond, t);
            walk_block(&w.body, t);
        }
        syn::Expr::Loop(l) => {
            Tally::bump(&mut t.exprs, "loop");
            if l.label.is_some() {
                Tally::bump(&mut t.exprs, "labeled");
            }
            walk_block(&l.body, t);
        }
        syn::Expr::ForLoop(fl) => {
            Tally::bump(&mut t.exprs, "for");
            if fl.label.is_some() {
                Tally::bump(&mut t.exprs, "labeled");
            }
            walk_pat(&fl.pat, t);
            walk_expr(&fl.expr, t);
            walk_block(&fl.body, t);
        }
        syn::Expr::Closure(c) => {
            Tally::bump(&mut t.exprs, "closure");
            for i in &c.inputs {
                walk_pat(i, t);
            }
            walk_expr(&c.body, t);
        }
        syn::Expr::Range(r) => {
            Tally::bump(
                &mut t.exprs,
                if matches!(r.limits, syn::RangeLimits::Closed(_)) {
                    "range..="
                } else {
                    "range.."
                },
            );
            if let Some(s) = &r.start {
                walk_expr(s, t);
            }
            if let Some(e2) = &r.end {
                walk_expr(e2, t);
            }
        }
        syn::Expr::Reference(r) => {
            Tally::bump(&mut t.exprs, "ref");
            if r.mutability.is_some() {
                Tally::bump(&mut t.exprs, "ref.mut");
            }
            walk_expr(&r.expr, t);
        }
        syn::Expr::Assign(a) => {
            Tally::bump(&mut t.exprs, "assign");
            walk_expr(&a.left, t);
            walk_expr(&a.right, t);
        }
        syn::Expr::Macro(m) => {
            let name = m
                .mac
                .path
                .segments
                .last()
                .map(|s| s.ident.to_string())
                .unwrap_or_default();
            Tally::bump(&mut t.macros, &name);
        }
        syn::Expr::Struct(se) => {
            Tally::bump(&mut t.exprs, "struct_lit");
            walk_path_typed(&se.path, t);
            for f in &se.fields {
                walk_expr(&f.expr, t);
            }
            if let Some(r) = &se.rest {
                walk_expr(r, t);
            }
        }
        syn::Expr::Block(_) => Tally::bump(&mut t.exprs, "block"),
        syn::Expr::Paren(p) => walk_expr(&p.expr, t),
        syn::Expr::Group(g) => walk_expr(&g.expr, t),
        syn::Expr::Return(r) => {
            Tally::bump(&mut t.exprs, if r.expr.is_some() { "return.val" } else { "return.bare" });
            if let Some(v) = &r.expr {
                walk_expr(v, t);
            }
        }
        syn::Expr::Break(br) => {
            Tally::bump(&mut t.exprs, if br.expr.is_some() { "break.val" } else { "break" });
            if br.label.is_some() {
                Tally::bump(&mut t.exprs, "labeled");
            }
            if let Some(v) = &br.expr {
                walk_expr(v, t);
            }
        }
        syn::Expr::Continue(c) => {
            Tally::bump(&mut t.exprs, "continue");
            if c.label.is_some() {
                Tally::bump(&mut t.exprs, "labeled");
            }
        }
        syn::Expr::Try(q) => {
            Tally::bump(&mut t.exprs, "try_?");
            walk_expr(&q.expr, t);
        }
        syn::Expr::Await(a) => {
            Tally::bump(&mut t.exprs, "await");
            walk_expr(&a.base, t);
        }
        syn::Expr::Async(a) => {
            Tally::bump(&mut t.exprs, "async");
            walk_block(&a.block, t);
        }
        syn::Expr::Tuple(tu) => {
            Tally::bump(&mut t.exprs, "tuple");
            for e in &tu.elems {
                walk_expr(e, t);
            }
        }
        syn::Expr::Array(ar) => {
            Tally::bump(&mut t.exprs, "array_lit");
            for e in &ar.elems {
                walk_expr(e, t);
            }
        }
        syn::Expr::Cast(c) => {
            Tally::bump(&mut t.exprs, "cast");
            walk_expr(&c.expr, t);
            walk_type(&c.ty, t);
        }
        syn::Expr::Verbatim(_) => Tally::bump(&mut t.exprs, "verbatim"),
        _ => Tally::bump(&mut t.exprs, "other_expr"),
    }
}

fn walk_pat(p: &syn::Pat, t: &mut Tally) {
    match p {
        syn::Pat::Ident(pi) => {
            if pi.mutability.is_some() {
                Tally::bump(&mut t.pats, "ident.mut");
            }
            if pi.by_ref.is_some() {
                Tally::bump(&mut t.pats, "ident.ref");
            }
            if pi.subpat.is_some() {
                Tally::bump(&mut t.pats, "ident.subpat");
            }
        }
        syn::Pat::Type(pt) => {
            Tally::bump(&mut t.pats, "typed");
            walk_type(&pt.ty, t);
            walk_pat(&pt.pat, t);
        }
        syn::Pat::TupleStruct(ps) => {
            Tally::bump(&mut t.pats, "tuple_struct_pat");
            walk_path_typed(&ps.path, t);
            for f in &ps.elems {
                walk_pat(f, t);
            }
        }
        syn::Pat::Struct(ps) => {
            Tally::bump(&mut t.pats, "struct_pat");
            walk_path_typed(&ps.path, t);
            for f in &ps.fields {
                walk_pat(&f.pat, t);
            }
        }
        syn::Pat::Or(o) => {
            Tally::bump(&mut t.pats, "or_pat");
            for p in &o.cases {
                walk_pat(p, t);
            }
        }
        syn::Pat::Wild(_) => Tally::bump(&mut t.pats, "wild"),
        syn::Pat::Lit(pl) => {
            Tally::bump(&mut t.pats, "pat.lit");
            let k = match &pl.lit {
                syn::Lit::Int(_) => "lit.int",
                syn::Lit::Str(_) => "lit.str",
                syn::Lit::Char(_) => "lit.char",
                syn::Lit::Bool(_) => "lit.bool",
                _ => "lit.other",
            };
            Tally::bump(&mut t.pats, k);
        }
        syn::Pat::Range(pr) => {
            Tally::bump(&mut t.pats, "pat.range");
            if let Some(lo) = &pr.start {
                walk_expr(lo, t);
            }
            if let Some(hi) = &pr.end {
                walk_expr(hi, t);
            }
        }
        syn::Pat::Slice(sl) => {
            Tally::bump(&mut t.pats, "pat.slice");
            for e in &sl.elems {
                walk_pat(e, t);
            }
        }
        syn::Pat::Reference(r) => {
            Tally::bump(&mut t.pats, "pat.ref");
            walk_pat(&r.pat, t);
        }
        syn::Pat::Tuple(tp) => {
            Tally::bump(&mut t.pats, "tuple_pat");
            for e in &tp.elems {
                walk_pat(e, t);
            }
        }
        syn::Pat::Path(pp) => {
            Tally::bump(&mut t.pats, "pat.path");
            walk_path_typed(&pp.path, t);
        }
        _ => Tally::bump(&mut t.pats, "other_pat"),
    }
}

fn walk_ty_last_seg(path: &syn::Path, t: &mut Tally) {
    if let Some(seg) = path.segments.last() {
        Tally::bump(&mut t.types, &seg.ident.to_string());
    }
}

fn walk_path_typed(path: &syn::Path, _t: &mut Tally) {}

fn walk_type(ty: &syn::Type, t: &mut Tally) {
    match ty {
        syn::Type::Path(tp) => walk_ty_last_seg(&tp.path, t),
        syn::Type::Reference(r) => {
            Tally::bump(&mut t.types, "&ref");
            walk_type(&r.elem, t);
        }
        syn::Type::Ptr(_) => Tally::bump(&mut t.types, "*ptr"),
        syn::Type::Slice(s) => {
            Tally::bump(&mut t.types, "[slice]");
            walk_type(&s.elem, t);
        }
        syn::Type::Array(a) => {
            Tally::bump(&mut t.types, "[array;N]");
            walk_type(&a.elem, t);
        }
        syn::Type::Tuple(tup) => {
            Tally::bump(&mut t.types, "(tup)");
            for e in &tup.elems {
                walk_type(e, t);
            }
        }
        syn::Type::Paren(p) => walk_type(&p.elem, t),
        syn::Type::TraitObject(to) => {
            Tally::bump(&mut t.types, "dyn Trait");
            for b in to.bounds.iter() {
                if let syn::TypeParamBound::Trait(tr) = b {
                    walk_ty_last_seg(&tr.path, t);
                }
            }
        }
        _ => Tally::bump(&mut t.types, "other_type"),
    }
}

fn print_map(title: &str, m: &BTreeMap<String, usize>) {
    if m.is_empty() {
        return;
    }
    let mut v: Vec<(&String, &usize)> = m.iter().collect();
    v.sort_by_key(|(_, c)| std::cmp::Reverse(**c));
    println!("── {title} ──");
    for (k, c) in v {
        println!("{c:>6}  {k}");
    }
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.is_empty() {
        eprintln!("usage: census <file.rs>...");
        exit(2);
    }
    let mut all = Tally::default();
    let mut n_ok = 0usize;
    let mut n_err = 0usize;
    for f in &args {
        let Ok(src) = std::fs::read_to_string(f) else {
            eprintln!("cannot read {f}");
            n_err += 1;
            continue;
        };
        match syn::parse_file(&src) {
            Ok(ast) => {
                n_ok += 1;
                let mut t = Tally::default();
                walk_file(&ast, &mut t);
                all.merge(&t);
            }
            Err(e) => {
                n_err += 1;
                eprintln!("PARSE ERROR {f}: {e}");
            }
        }
    }
    eprintln!("# files parsed OK: {n_ok}, failed: {n_err} / {}", args.len());
    print_map("items", &all.items);
    print_map("statements", &all.stmts);
    print_map("exprs", &all.exprs);
    print_map("patterns", &all.pats);
    print_map("types", &all.types);
    print_map("macros", &all.macros);
    print_map("operators", &all.ops);
}
