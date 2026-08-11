//! syn-coverage — which of syn's AST node kinds (the typed-Rust grammar
//! surface) do our examples exercise? Parses every .rs file with syn and
//! walks the typed AST, counting node kinds per category (Expr, Item,
//! Stmt, Pat, Type, Lit, BinOp, UnOp). The DEFINED sets below are the
//! syn 2.0.119 variant lists (from the crate source); exercised = kinds
//! that appear in at least one parse tree.
//!
//! Usage: syn-coverage <file.rs>...   (prints kind counts + a summary)

use std::collections::BTreeMap;
use syn::visit::Visit;

// syn 2.0.119 vocabulary (from syn's src/{expr,item,stmt,pat,ty,lit,op}.rs)
const DEFINED: &[(&str, &[&str])] = &[
    (
        "Expr",
        &[
            "Array", "Assign", "Async", "Await", "Binary", "Block", "Break", "Call", "Cast",
            "Closure", "Const", "Continue", "Field", "ForLoop", "Group", "If", "Index", "Infer",
            "Let", "Lit", "Loop", "Macro", "Match", "MethodCall", "Paren", "Path", "Range",
            "RawAddr", "Reference", "Repeat", "Return", "Struct", "Try", "TryBlock", "Tuple",
            "Unary", "Unsafe", "Verbatim", "While", "Yield",
        ],
    ),
    (
        "Item",
        &[
            "Const", "Enum", "ExternCrate", "Fn", "ForeignMod", "Impl", "Macro", "Mod", "Static",
            "Struct", "Trait", "TraitAlias", "Type", "Union", "Use", "Verbatim",
        ],
    ),
    ("Stmt", &["Local", "Item", "Expr", "Macro"]),
    (
        "Pat",
        &[
            "Const", "Ident", "Lit", "Macro", "Or", "Paren", "Path", "Range", "Reference", "Rest",
            "Slice", "Struct", "Tuple", "TupleStruct", "Type", "Verbatim", "Wild",
        ],
    ),
    (
        "Type",
        &[
            "Array", "BareFn", "Group", "ImplTrait", "Infer", "Macro", "Never", "Paren", "Path",
            "Ptr", "Reference", "Slice", "TraitObject", "Tuple", "Verbatim",
        ],
    ),
    ("Lit", &["Str", "ByteStr", "CStr", "Byte", "Char", "Int", "Float", "Bool", "Verbatim"]),
    (
        "BinOp",
        &[
            "Add", "Sub", "Mul", "Div", "Rem", "And", "Or", "BitXor", "BitAnd", "BitOr", "Shl",
            "Shr", "Eq", "Lt", "Le", "Ne", "Ge", "Gt", "AddAssign", "SubAssign", "MulAssign",
            "DivAssign", "RemAssign", "BitXorAssign", "BitAndAssign", "BitOrAssign", "ShlAssign",
            "ShrAssign",
        ],
    ),
    ("UnOp", &["Deref", "Not", "Neg"]),
];

/// Top-level variant name from Debug output ("Expr::Binary { ... }" -> "Binary").
fn debug_kind<T: std::fmt::Debug>(v: &T) -> String {
    let d = format!("{:?}", v);
    let mut toks = d
        .split(|c: char| !(c.is_ascii_alphanumeric() || c == '_'))
        .filter(|s| !s.is_empty());
    let _enum_name = toks.next(); // "Expr" in "Expr::Binary { ... }"
    toks.next().unwrap_or("?").to_string() // the variant
}

#[derive(Default)]
struct V {
    counts: BTreeMap<String, usize>,
}

impl V {
    fn bump(&mut self, cat: &str, kind: &str) {
        *self.counts.entry(format!("{cat}::{kind}")).or_insert(0) += 1;
    }
}

impl<'ast> Visit<'ast> for V {
    fn visit_expr(&mut self, e: &'ast syn::Expr) {
        self.bump("Expr", &debug_kind(e));
        syn::visit::visit_expr(self, e);
    }
    fn visit_item(&mut self, i: &'ast syn::Item) {
        self.bump("Item", &debug_kind(i));
        syn::visit::visit_item(self, i);
    }
    fn visit_stmt(&mut self, s: &'ast syn::Stmt) {
        self.bump("Stmt", &debug_kind(s));
        syn::visit::visit_stmt(self, s);
    }
    fn visit_pat(&mut self, p: &'ast syn::Pat) {
        self.bump("Pat", &debug_kind(p));
        syn::visit::visit_pat(self, p);
    }
    fn visit_type(&mut self, t: &'ast syn::Type) {
        self.bump("Type", &debug_kind(t));
        syn::visit::visit_type(self, t);
    }
    fn visit_lit(&mut self, l: &'ast syn::Lit) {
        self.bump("Lit", &debug_kind(l));
        syn::visit::visit_lit(self, l);
    }
    fn visit_bin_op(&mut self, op: &'ast syn::BinOp) {
        self.bump("BinOp", &debug_kind(op));
        syn::visit::visit_bin_op(self, op);
    }
    fn visit_un_op(&mut self, op: &'ast syn::UnOp) {
        self.bump("UnOp", &debug_kind(op));
        syn::visit::visit_un_op(self, op);
    }
}

fn main() {
    let mut v = V::default();
    let mut files = 0usize;
    let mut parsed = 0usize;
    for arg in std::env::args().skip(1) {
        files += 1;
        let Ok(src) = std::fs::read_to_string(&arg) else { continue };
        if let Ok(file) = syn::parse_file(&src) {
            parsed += 1;
            v.visit_file(&file);
        }
    }
    for (cat, kinds) in DEFINED {
        let exercised: Vec<&str> = kinds
            .iter()
            .filter(|k| v.counts.contains_key(&format!("{cat}::{k}")))
            .copied()
            .collect();
        let n = exercised.len();
        let pct = 100.0 * n as f64 / kinds.len() as f64;
        println!("{cat}: {n}/{} ({pct:.0}%) exercised", kinds.len());
        println!("  used:   {}", exercised.join(", "));
        let unused: Vec<&str> = kinds
            .iter()
            .filter(|k| !v.counts.contains_key(&format!("{cat}::{k}")))
            .copied()
            .collect();
        println!("  unused: {}", unused.join(", "));
    }
    eprintln!(
        "files={} parsed_ok={} total_kinds_exercised={}",
        files,
        parsed,
        v.counts.len()
    );
}
