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
use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::process::exit;
use syn::spanned::Spanned;

// ── errors ──────────────────────────────────────────────────────────

/// Census mode (`--census`): a refusal PANICS instead of exiting, so the
/// caller can catch it per function/item and keep reporting the rest.
static CENSUS: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
static CENSUS_HITS: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);
/// While a DEMAND-PULLED function is lowered, a refusal must unwind to
/// the caller (which drops the function) instead of exiting.
static CATCHING: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

fn census_mode() -> bool {
    CENSUS.load(std::sync::atomic::Ordering::Relaxed)
}

fn catching_mode() -> bool {
    CATCHING.load(std::sync::atomic::Ordering::Relaxed)
}

/// Lower a DEMAND-PULLED function with refusals turning into a catch: a
/// body that refuses is dropped (its name leaves the call set), so the
/// pull can never make a file worse than it was without the pull.
fn caught<F>(f: F) -> Result<Value, Box<dyn std::any::Any + Send>>
where
    F: FnOnce() -> Value + std::panic::UnwindSafe,
{
    let prev = std::panic::take_hook();
    std::panic::set_hook(Box::new(|info| {
        if std::env::var("RUST_FRONTEND_PANIC_DEBUG").is_ok() {
            eprintln!(
                "PANIC: {info}\n{}",
                std::backtrace::Backtrace::force_capture()
            );
        }
    }));
    CATCHING.store(true, std::sync::atomic::Ordering::Relaxed);
    let r = std::panic::catch_unwind(f);
    CATCHING.store(false, std::sync::atomic::Ordering::Relaxed);
    std::panic::set_hook(prev);
    r
}

fn path_text(e: &syn::Expr) -> String {
    match e {
        syn::Expr::Path(p) => p
            .path
            .segments
            .iter()
            .map(|s| s.ident.to_string())
            .collect::<Vec<_>>()
            .join("::"),
        other => format!("<{}>", expr_kind(other)),
    }
}

fn refuse(msg: &str, span: proc_macro2::Span) -> ! {
    let loc = span.start();
    let text = format!("unsupported Rust: {msg} (line {})", loc.line);
    if census_mode() || catching_mode() {
        std::panic::panic_any(text);
    }
    eprintln!("{text}");
    exit(1);
}

fn census_hit(unit: &str, name: &str, payload: Box<dyn std::any::Any + Send>) {
    let msg = payload
        .downcast_ref::<String>()
        .cloned()
        .or_else(|| payload.downcast_ref::<&str>().map(|s| s.to_string()))
        .unwrap_or_else(|| "refusal".to_string());
    CENSUS_HITS.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    eprintln!("CENSUS\t{unit}\t{name}\t{msg}");
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

/// String concatenation: the A1 `Interpolate` of two EXPR parts (the core's
/// shape for `s="$a$b"`; see shir_json_in.rs "Interpolate"). Each part is
/// stringified by the store's own semantics, which is exactly Rust's
/// Display-free concatenation for the Str values this frontend produces.
fn str_concat(lhs: Value, rhs: Value) -> Value {
    json!({
        "type": "Interpolate",
        "parts": [
            {"kind": "expr", "expr": lhs},
            {"kind": "expr", "expr": rhs},
        ]
    })
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

/// Store through the runtime `setVar`, NOT a plain Assign: an Assign of a
/// record RHS is rendered with the scalar String() coercion, which would
/// destroy struct/enum payload objects. Used wherever a value may be a
/// record (destructuring temps, field binds).
fn store_stmt(name: &str, expr: Value) -> Value {
    expr_stmt(json!({
        "args": [str_lit(name), expr],
        "func": "setVar", "purity": "Emulable", "type": "Call"
    }))
}

fn arith_num(v: i64) -> Value {
    json!({"type": "Num", "value": v})
}

fn arith_var(name: &str) -> Value {
    json!({"name": name, "type": "Var"})
}

/// `getVar("i")` — the TEXT store read. Right for every shell value, and
/// WRONG for a record: the core's text-mode read inlines a positional as
/// `String(sh2.positional[i-1] ?? "")`, so a struct/enum payload arrives
/// as "[object Object]" and every field read answers from that string.
///
/// Handing `getVar` the index as an ARITHMETIC value instead keeps it a
/// generic `sh2.getVar(<n>)` call, which the runtime answers with the
/// positional EXACTLY as the caller passed it: the same text every
/// primitive gets (argv entries are text) and the object itself for a
/// record. Only RECORD-typed parameters use this; primitives, arrays and
/// `&self` receivers keep the text read they take today.
///
/// WHY IT IS EXACT: a numeric name reaches the runtime's `default:` branch,
/// which resolves a DIGIT name (and `1` coerces to `"1"`) as
/// `positional[n-1]` — the digit-named branch. That branch first asks
/// `name in this.vars`, so a store variable LITERALLY named `"1"` would
/// shadow the positional and this read would answer that variable instead.
/// This frontend never creates one: every name it binds is a Rust
/// identifier (never all-digit) or a `__sh2*` temp, so the shadow case is
/// unreachable and the call is the positional read it claims to be. The
/// old inline read (`String(sh2.positional[i-1] ?? "")`) had no such
/// caveat because it never consulted the store at all — this note is the
/// price of going through the general read, and it is why the frontend must
/// keep digit-only names out of the store.
fn record_param_bind(name: &str, pos: usize) -> Value {
    assign_stmt(
        name,
        json!({
            "args": [json!({"ast": arith_num(pos as i64), "type": "Arith"})],
            "func": "getVar", "purity": "Emulable", "type": "Call"
        }),
    )
}

/// A parameter whose value can be a tagged OBJECT (struct/enum payload)
/// rather than a store string. `Option<record>` counts: its `None` is the
/// store's empty value (a text read and a value read agree there) while a
/// `Some(payload)` carries the object.
fn is_record_ty(t: &Ty) -> bool {
    match t {
        Ty::Struct(_) | Ty::Enum(_) => true,
        Ty::Opt(inner) => is_record_ty(inner),
        _ => false,
    }
}

fn arith_bin(op: &str, lhs: Value, rhs: Value) -> Value {
    json!({"ast": json!({"lhs": lhs, "op": op, "rhs": rhs, "type": "Bin"}), "type": "Arith"})
}

// ── CLI ─────────────────────────────────────────────────────────────


/// Minimal receiver types, enough to lower methods WITHOUT guessing:
/// the store holds JS strings / numbers / booleans / native arrays, and
/// a method's Rust semantics must map onto the exact JS the core renders.
/// The iteration-order promise of an associative container. Rust makes it
/// observable: `BTreeMap`/`BTreeSet` iterate in KEY order, `HashMap`/
/// `HashSet` in an unspecified order (so the store's insertion order is a
/// valid enumeration of them, and NO lowering may depend on it).
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum MapOrder {
    Hash,
    BTree,
}

#[derive(Clone, PartialEq, Eq, Debug)]
enum Ty {
    Int,
    Str,
    Bool,
    /// an array/Vec; element type when provable
    Arr(Option<std::boxed::Box<Ty>>, Option<u64>),
    /// a named struct (`Point`) — enables receiver-method dispatch
    Struct(String),
    /// a named UNIT-variant enum (`Color`) — variants lower to their
    /// qualified tag strings (`"Color::Red"`); matching is string
    /// equality. Tuple/struct-variant enums refuse until record storage
    /// lands (payloads need object values).
    Enum(String),
    /// an `AtomicBool` static — single-threaded JS makes load/store a
    /// plain value read/write (exact)
    AtomicBool,
    /// a TUPLE of the given arity — values lower to native arrays;
    /// `.N` reads are computed index reads
    Tuple(u32),
    /// a LIST-OBJECT id (strSplit / arrayToList): raw elements are read
    /// with listGet, length with listLen (dynamic, runtime-trusted)
    List,
    /// HashMap/BTreeMap: a NAMED associative store (assocGet/assocSet),
    /// keyed by the stringified key. The third field is the ORDERING the
    /// source container promises: `BTreeMap` ITERATES IN KEY ORDER (an
    /// observable fact of the program's output) while `HashMap` promises
    /// nothing, so insertion order is a valid enumeration for it.
    Map(Box<Ty>, Box<Ty>, MapOrder),
    /// HashSet/BTreeSet: an associative store whose keys are the elements.
    /// Same ordering tag as `Map`: `BTreeSet` iterates in key order.
    Set(Box<Ty>, MapOrder),
    /// a FUNCTION ITEM (`transforms::f`) — its store value is the
    /// REGISTERED lowered name; indirect calls dispatch by that name
    Fn(String),
    /// an OPTION of T. Store convention: None ≡ "" (the empty string IS
    /// the absent marker, exactly like c-sh-go's dropped `int i;` and
    /// the runtime getVar's unset->""). Sound whenever no code
    /// distinguishes Some("") from None.
    Opt(Box<Ty>),
    /// `Mutex<T>` / `RwLock<T>` / `Cell<T>` / `RefCell<T>` — interior
    /// mutability. Single-threaded JS makes the wrapper vacuous, so the
    /// static IS a mutable global; only its inner type is represented.
    Cel(Box<Ty>),
    /// what `.lock()` / `.borrow_mut()` / `.borrow()` yields: still the
    /// wrapper's shape, peeled by `.unwrap()` (a guard is not a value).
    Guard(Box<Ty>),
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
            if cx.structs.borrow().contains_key(&id) {
                return Ty::Struct(id);
            }
            if cx.enums.iter().any(|e| *e == id) {
                return Ty::Enum(id);
            }
            // Option<T> -> Opt(T): the store convention maps None ≡ ""
            if id == "Option" {
                let elem = match &seg.arguments {
                    syn::PathArguments::AngleBracketed(a) => a.args.iter().find_map(|arg| {
                        match arg {
                            syn::GenericArgument::Type(t) => {
                                Some(Box::new(ty_from_type(t, cx)))
                            }
                            _ => None,
                        }
                    }),
                    _ => None,
                };
                return Ty::Opt(elem.unwrap_or(Box::new(Ty::Unknown)));
            }
            // Result<T, E> -> Opt(T): under the SAME convention Ok(v) IS v
            // and Err IS the absent marker (the convention `?` and `ok()`
            // already assume). The ERROR payload is not representable, so
            // every accessor that observes it (`.err()`, `unwrap_err`,
            // `Err(e)` patterns, Debug formatting) refuses at its own site.
            // A UNIT Ok type stays Unknown: `Ok(())` and `Err(..)` would
            // both be "", so no value could tell success from failure --
            // nothing claims it.
            if id == "Result" {
                let ok = match &seg.arguments {
                    syn::PathArguments::AngleBracketed(a) => a.args.iter().find_map(|arg| match arg {
                        syn::GenericArgument::Type(t) => Some(t),
                        _ => None,
                    }),
                    _ => None,
                };
                let Some(ok) = ok else { return Ty::Unknown };
                if matches!(ok, syn::Type::Tuple(t) if t.elems.is_empty()) {
                    return Ty::Unknown;
                }
                return Ty::Opt(Box::new(ty_from_type(ok, cx)));
            }
            // interior-mutability wrappers are erased: JS is single-
            // threaded, so a Mutex/RwLock/Cell static IS a plain global.
            // The wrapper's inner type is what the store carries.
            if matches!(
                seg.ident.to_string().as_str(),
                "Mutex" | "RwLock" | "Cell" | "RefCell"
            ) {
                let inner = match &seg.arguments {
                    syn::PathArguments::AngleBracketed(a) => a
                        .args
                        .iter()
                        .find_map(|arg| match arg {
                            syn::GenericArgument::Type(t) => Some(ty_from_type(t, cx)),
                            _ => None,
                        })
                        .unwrap_or(Ty::Unknown),
                    _ => Ty::Unknown,
                };
                return Ty::Cel(Box::new(inner));
            }
            // Arc/Rc are allocator wrappers (no alias hazard: a shared
            // borrow cannot mutate through the original)
            if matches!(seg.ident.to_string().as_str(), "Arc" | "Rc") {
                return match &seg.arguments {
                    syn::PathArguments::AngleBracketed(a) => a
                        .args
                        .iter()
                        .find_map(|arg| match arg {
                            syn::GenericArgument::Type(t) => Some(ty_from_type(t, cx)),
                            _ => None,
                        })
                        .unwrap_or(Ty::Unknown),
                    _ => Ty::Unknown,
                };
            }
            match seg.ident.to_string().as_str() {
                "i8" | "i16" | "i32" | "i64" | "i128" | "isize" | "u8" | "u16" | "u32"
                | "u64" | "u128" | "usize" => Ty::Int,
                // atomic integers: single-threaded JS makes every
                // ordering vacuous and the counter a plain number
                "AtomicUsize" | "AtomicU64" | "AtomicU32" | "AtomicI64" | "AtomicI32"
                | "AtomicU8" | "AtomicI8" => Ty::Int,
                "str" | "String" | "Cow" => Ty::Str,
                "bool" => Ty::Bool,
                "AtomicBool" => Ty::AtomicBool,
                "HashMap" | "BTreeMap" => {
                    let order = if seg.ident == "BTreeMap" {
                        MapOrder::BTree
                    } else {
                        MapOrder::Hash
                    };
                    let args: Vec<Ty> = match &seg.arguments {
                        syn::PathArguments::AngleBracketed(a) => a
                            .args
                            .iter()
                            .filter_map(|arg| match arg {
                                syn::GenericArgument::Type(t) => Some(ty_from_type(t, cx)),
                                _ => None,
                            })
                            .collect(),
                        _ => Vec::new(),
                    };
                    Ty::Map(
                        Box::new(args.first().cloned().unwrap_or(Ty::Unknown)),
                        Box::new(args.get(1).cloned().unwrap_or(Ty::Unknown)),
                        order,
                    )
                }
                "HashSet" | "BTreeSet" => {
                    let order = if seg.ident == "BTreeSet" {
                        MapOrder::BTree
                    } else {
                        MapOrder::Hash
                    };
                    let elem = match &seg.arguments {
                        syn::PathArguments::AngleBracketed(a) => {
                            a.args.iter().find_map(|arg| match arg {
                                syn::GenericArgument::Type(t) => {
                                    Some(Box::new(ty_from_type(t, cx)))
                                }
                                _ => None,
                            })
                        }
                        _ => None,
                    };
                    Ty::Set(elem.unwrap_or(Box::new(Ty::Unknown)), order)
                }
                "Vec" | "VecDeque" | "Slice" => {
                    // Vec<T>: extract the element type when shape allows
                    let elem = match &seg.arguments {
                        syn::PathArguments::AngleBracketed(a) => {
                            a.args.iter().find_map(|arg| match arg {
                                syn::GenericArgument::Type(t) => {
                                    Some(Box::new(ty_from_type(t, cx)))
                                }
                                _ => None,
                            })
                        }
                        _ => None,
                    };
                    Ty::Arr(elem, None)
                }
                _ => Ty::Unknown,
            }
        }
        syn::Type::Reference(r) => ty_from_type(&r.elem, cx),
        syn::Type::Slice(s) => {
            Ty::Arr(Some(Box::new(ty_from_type(&s.elem, cx))), None)
        }
        syn::Type::Tuple(tup) => Ty::Tuple(tup.elems.len() as u32),
        _ => Ty::Unknown,
    }
}

/// Literal STRING-array contents of a const initializer (`&["a", "b"]`,
/// `vec!["a"]`, `["a"]`) — membership chains compile from these.
fn const_str_array(e: &syn::Expr) -> Option<Vec<String>> {
    let arr = match e {
        syn::Expr::Reference(r) => match &*r.expr {
            syn::Expr::Array(a) => a,
            _ => return None,
        },
        syn::Expr::Array(a) => a,
        _ => return None,
    };
    let mut out = Vec::new();
    for el in &arr.elems {
        match el {
            syn::Expr::Lit(l) => match &l.lit {
                syn::Lit::Str(ls) => out.push(ls.value()),
                _ => return None,
            },
            _ => return None,
        }
    }
    Some(out)
}

/// A signature's declared return Ty (`-> T`), Unknown when unit/absent.
/// One `static` read out of an item-level macro (`lazy_static!` /
/// `thread_local!`), before it becomes a store global.
struct MacroStatic {
    name: String,
    ty: Ty,
    init: Option<syn::Expr>,
}

/// `lazy_static! { static ref NAME: TY = EXPR; .. }` — the macro's whole
/// effect IS those items (a lazily-initialized static is a static), so
/// reading them back out of the token stream is exact.
struct LazyStatics(Vec<(syn::Ident, syn::Type, syn::Expr)>);

impl syn::parse::Parse for LazyStatics {
    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
        let mut v = Vec::new();
        while !input.is_empty() {
            // doc comments / attributes on the item (`/// ..`) are
            // type-checker-invisible here but must parse
            syn::Attribute::parse_outer(input)?;
            input.parse::<syn::Token![static]>()?;
            input.parse::<syn::Token![ref]>()?;
            let name: syn::Ident = input.parse()?;
            input.parse::<syn::Token![:]>()?;
            let ty: syn::Type = input.parse()?;
            input.parse::<syn::Token![=]>()?;
            let init: syn::Expr = input.parse()?;
            input.parse::<syn::Token![;]>()?;
            v.push((name, ty, init));
        }
        Ok(LazyStatics(v))
    }
}

/// `thread_local! { static NAME: TY = EXPR; .. }` (same shape, no `ref`).
struct ThreadLocals(Vec<(syn::Ident, syn::Type, syn::Expr)>);

impl syn::parse::Parse for ThreadLocals {
    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
        let mut v = Vec::new();
        while !input.is_empty() {
            syn::Attribute::parse_outer(input)?;
            input.parse::<syn::Token![static]>()?;
            let name: syn::Ident = input.parse()?;
            input.parse::<syn::Token![:]>()?;
            let ty: syn::Type = input.parse()?;
            input.parse::<syn::Token![=]>()?;
            let init: syn::Expr = input.parse()?;
            input.parse::<syn::Token![;]>()?;
            v.push((name, ty, init));
        }
        Ok(ThreadLocals(v))
    }
}

fn ret_ty(output: &syn::ReturnType, cx: &Cx) -> Ty {
    match output {
        syn::ReturnType::Default => Ty::Unknown,
        syn::ReturnType::Type(_, t) => ty_from_type(t, cx),
    }
}

// ── clean-lowering verdict cache (mtime-keyed, cross-process) ───────
// The gated splice checks each module by running this binary on it;
// nested checks recurse, so verdicts MUST be cached to stay linear.
// Stale entries can only over-splice a dirty module — visible as an
// entry refusal, never as silent miscompilation.
fn clean_cache_path() -> PathBuf {
    std::env::temp_dir().join("rust-frontend-clean-cache.json")
}

fn mtime_secs(p: &Path) -> Option<u64> {
    p.metadata().ok()?.modified().ok()?;
    Some(
        p.metadata()
            .ok()?
            .modified()
            .ok()?
            .duration_since(std::time::UNIX_EPOCH)
            .ok()?
            .as_secs(),
    )
}

fn cache_get(path: &Path) -> Option<bool> {
    let txt = std::fs::read_to_string(clean_cache_path()).ok()?;
    let map: HashMap<String, (u64, bool)> = serde_json::from_str(&txt).ok()?;
    let m = mtime_secs(path)?;
    map.get(&path.to_string_lossy().to_string())
        .filter(|(m0, _)| *m0 == m)
        .map(|(_, ok)| *ok)
}

fn cache_put(path: &Path, ok: bool) {
    let Some(m) = mtime_secs(path) else { return };
    let mut map: HashMap<String, (u64, bool)> = std::fs::read_to_string(clean_cache_path())
        .ok()
        .and_then(|t| serde_json::from_str(&t).ok())
        .unwrap_or_default();
    map.insert(path.to_string_lossy().to_string(), (m, ok));
    let _ = std::fs::write(clean_cache_path(), serde_json::to_string(&map).unwrap_or_default());
}

/// Does this module file lower cleanly on its own? Disk-cached verdicts
/// keep the nested subprocess checks linear.
fn lowers_clean(path: &Path) -> bool {
    if let Some(cached) = cache_get(path) {
        return cached;
    }
    let ok = std::env::current_exe().ok()
        .and_then(|exe| {
            std::process::Command::new(exe)
                .args(["--shir"])
                .arg(path)
                .arg("--raw")
                // SINGLE-FILE mode: no crate expansion, no nested
                // clean-checks — breaks the check-recursion cycle
                .env("RUST_FRONTEND_NO_EXPAND", "1")
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .ok()
        })
        .map(|st| st.success())
        .unwrap_or(false);
    cache_put(path, ok);
    ok
}

/// WHOLE-CRATE expansion: resolve file `mod x;` declarations against
/// sibling sources, recursively, so cross-module calls resolve (the
/// inline-mod flattening then merges namespaces by bare name). Roots =
/// the entry FILE plus its crate's lib.rs/main.rs tree when present;
/// the visited set dedupes (the entry file always comes first).
fn expand_crate(
    items: &[syn::Item],
    base: &Path,
    visited: &mut HashSet<PathBuf>,
) -> Vec<syn::Item> {
    let mut out = Vec::new();
    for item in items {
        match item {
            syn::Item::Mod(m) if m.content.is_none() => {
                // cfg-gated file mods contribute nothing to a normal build
                if cfg_test_only(&m.attrs) {
                    continue;
                }
                let cands = [
                    base.join(format!("{}.rs", m.ident)),
                    base.join(m.ident.to_string()).join("mod.rs"),
                ];
                let Some(path) = cands.iter().find(|p| p.exists()) else {
                    continue; // unresolved module: any USE refuses at its site
                };
                // Splice ONLY modules that lower cleanly ON THEIR OWN: a
                // module whose own body refuses would poison the entry
                // (its deep gaps are not this file's semantics).
                // Self-exactness: the check IS this same binary.
                if !lowers_clean(path) {
                    continue;
                }
                // Mark SPLICED only after the check: the demand pass must
                // still see gated-out (dirty) modules to pull the few
                // functions the entry actually calls from them.
                if !visited.insert(path.clone()) {
                    continue;
                }
                if let Ok(src_text) = std::fs::read_to_string(path) {
                    if let Ok(f) = syn::parse_file(&src_text) {
                        let sub_base = path.parent().unwrap_or(base).to_path_buf();
                        out.extend(expand_crate(&f.items, &sub_base, visited));
                    }
                }
            }
            other => out.push(other.clone()),
        }
    }
    out
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
    /// declared struct fields: struct name -> [(field, type)] — lets
    /// `self.list.push(..)` / `prog.var_types.is_empty()` resolve
    structs: std::cell::RefCell<HashMap<String, Vec<(String, Ty)>>>,
    /// declared enum names (unit-variant tag lowering)
    enums: Vec<String>,
    /// enum name -> variant name -> payload shape. Payload variants lower
    /// to tagged objects (`{tag: "E::V", 0: ..}` / named fields); unit
    /// variants stay qualified tag strings.
    variants: HashMap<String, HashMap<String, VariantShape>>,
    /// enum names declared with DIFFERENT shapes in two modules: the
    /// unqualified-name registry cannot pick one, so payload/unit use
    /// refuses instead of guessing.
    ambiguous: HashSet<String>,
    /// tuple-struct name -> positional field types (`struct T(i64, ..)`)
    tuple_structs: HashMap<String, Vec<Ty>>,
    /// registered fn name -> declared return Ty (chain desugar typing)
    fn_rets: HashMap<String, Ty>,
    /// registered fn name -> parameter names, positionally. A `&mut`
    /// argument is exact only when its variable's name is the callee's
    /// parameter name (the store keys a location by name), so the call site
    /// needs the callee's spelling to decide — never to guess.
    fn_params: HashMap<String, Vec<String>>,
    /// registered fn name -> is each parameter a `&mut` borrow? A `&mut`
    /// parameter is NOT bound from its positional (v0.41): the caller writes
    /// the location itself (copy-in) and reads it back (copy-out), so the
    /// callee's slot must not be clobbered by an argument read.
    fn_param_mut: HashMap<String, Vec<bool>>,
    /// set while a call site is emitting `&mut` READBACK statements for
    /// arguments bound under a different name than the parameter (v0.41).
    /// Off everywhere else, so the same shape refuses instead of dropping
    /// the writes.
    mut_rw: std::cell::RefCell<bool>,
    /// CONST array items with fully-literal STRING contents
    /// (`const BUILTINS: &[&str] = &["a", ..]`) — membership tests
    /// lower to exact OR-equality chains over these literals
    const_arrays: HashMap<String, Vec<String>>,
    /// fresh-temporary counter (field-read hoists)
    tmp: std::cell::RefCell<u32>,
    /// STATIC item types (persist across the per-function vars clears)
    statics: std::cell::RefCell<HashMap<String, Ty>>,
    /// global assignments (consts/statics), in source order
    globals: Vec<Value>,
    /// statements a VALUE lowering had to emit (an if-value's A1 If, a
    /// hoisted pure read). Drained by `lower_block` just BEFORE the
    /// statement that consumes the temp, so the order is the source order.
    pending: std::cell::RefCell<Vec<Value>>,
    /// every name the program can put in the STORE: `let`/param/pattern
    /// bindings plus const/static globals. An env read lowers to
    /// getVar(name), whose runtime lookup prefers the store over
    /// process.env, so it is exact only when this set does not contain
    /// the env name (checked at the read site).
    bindings: HashSet<String>,
    /// the enclosing impl's type while one of its methods lowers —
    /// `Self::m` resolves against it (`Self::rank` -> `Width_rank`).
    /// None at module level and inside free fns, where `Self` is not a
    /// legal path in Rust either.
    self_ty: std::cell::RefCell<Option<String>>,
    // Nested `fn` items are hoisted to top-level definitions named
    // `<parent>__<local>` (Rust's own symbol shape for them). This maps the
    // local spelling visible INSIDE one enclosing function to that mangled
    // name, so a bare `inner(x)` call resolves; it is reset per function.
    fn_alias: std::cell::RefCell<HashMap<String, String>>,
    // per-DEFINITION alias frame: `<def name>` -> the local names visible in
    // its body (its own nested `fn`s plus the sibling ones inherited from the
    // enclosing scope — Rust's items are visible to the whole scope)
    fn_scopes: std::cell::RefCell<HashMap<String, HashMap<String, String>>>,
    // how many `&&`/`||` levels deep the condition being lowered is: Rust
    // SHORT-CIRCUITS there, so hoisting a CALL out of an operand would run it
    // when Rust would not. The hoists refuse inside a chain instead.
    sc_depth: std::cell::Cell<u32>,
    /// guard alias -> (static name, inner Ty): `let g = NAME.lock().
    /// unwrap();` binds a HANDLE on the static, not a copy, so every use
    /// of `g` reads/writes `NAME`. Per-function scope (cleared with vars).
    guards: std::cell::RefCell<HashMap<String, (String, Ty)>>,
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
        // an exact constant cast stays a compile-time integer
        syn::Expr::Cast(c) => {
            let v = const_int(&c.expr, cx)?;
            let (bits, signed) = int_ty_bits(&c.ty)?;
            if int_cast_range_ok(v as i128, bits, signed) {
                Some(v)
            } else {
                None
            }
        }
        // compile-time integer arithmetic, including the bit/shift ops a
        // bound constant like `1 << 53` uses (checked, so an overflow is
        // never silently wrapped)
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            let l = const_int(&b.left, cx)?;
            let r = const_int(&b.right, cx)?;
            match b.op {
                Add(_) => l.checked_add(r),
                Sub(_) => l.checked_sub(r),
                Mul(_) => l.checked_mul(r),
                Div(_) if r != 0 => l.checked_div(r),
                Rem(_) if r != 0 => l.checked_rem(r),
                Shl(_) if (0..64).contains(&r) => l.checked_shl(r as u32),
                Shr(_) if (0..64).contains(&r) => l.checked_shr(r as u32),
                BitAnd(_) => Some(l & r),
                BitOr(_) => Some(l | r),
                BitXor(_) => Some(l ^ r),
                _ => None,
            }
        }
        _ => None,
    }
}

/// A primitive integer type used as an `as` cast TARGET -> (bits, signed).
/// Anything else (float, pointer, path type) has no exact store value.
fn int_ty_bits(t: &syn::Type) -> Option<(u32, bool)> {
    let syn::Type::Path(tp) = t else { return None };
    if tp.qself.is_some() || tp.path.leading_colon.is_some() || tp.path.segments.len() != 1 {
        return None;
    }
    let seg = &tp.path.segments[0];
    if !matches!(seg.arguments, syn::PathArguments::None) {
        return None;
    }
    match seg.ident.to_string().as_str() {
        "i8" => Some((8, true)),
        "i16" => Some((16, true)),
        "i32" => Some((32, true)),
        "i64" => Some((64, true)),
        "i128" => Some((128, true)),
        "isize" => Some((64, true)),
        "u8" => Some((8, false)),
        "u16" => Some((16, false)),
        "u32" => Some((32, false)),
        "u64" => Some((64, false)),
        "u128" => Some((128, false)),
        "usize" => Some((64, false)),
        _ => None,
    }
}

/// Does `v` survive `as` into the given integer type UNCHANGED? Rust's
/// integer `as` wraps (two's-complement truncation) outside the range,
/// and a wrapped value is not what the decimal store says — so only an
/// in-range cast is an identity, and everything else must refuse.
fn int_cast_range_ok(v: i128, bits: u32, signed: bool) -> bool {
    if bits >= 128 {
        return if signed { true } else { v >= 0 };
    }
    if signed {
        let max = (1i128 << (bits - 1)) - 1;
        let min = -(1i128 << (bits - 1));
        v >= min && v <= max
    } else {
        let max = (1i128 << bits) - 1;
        v >= 0 && v <= max
    }
}

/// Const-evaluate an integer expression to i128 (literal / -literal /
/// paren / already-tabled path / exact constant cast).
fn const_int_i128(e: &syn::Expr, cx: &Cx) -> Option<i128> {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => li.base10_parse().ok(),
            _ => None,
        },
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Neg(_) => Some(-const_int_i128(&u.expr, cx)?),
            _ => None,
        },
        syn::Expr::Paren(p) => const_int_i128(&p.expr, cx),
        syn::Expr::Path(p) => single_path(p)
            .and_then(|id| cx.ints.get(&id.to_string()).map(|v| *v as i128))
            .or_else(|| int_assoc_const_path(p).and_then(|t| t.parse::<i128>().ok())),
        syn::Expr::Cast(c) => {
            let v = const_int_i128(&c.expr, cx)?;
            let (bits, signed) = int_ty_bits(&c.ty)?;
            if int_cast_range_ok(v, bits, signed) {
                Some(v)
            } else {
                None
            }
        }
        _ => None,
    }
}

/// A CONSTANT operand for an `as` cast: its EXACT decimal text, plus the
/// i128 value when it fits (a u128-range associated constant does not).
fn const_int_cast_operand(e: &syn::Expr, cx: &Cx) -> Option<(String, Option<i128>)> {
    if let Some(v) = const_int_i128(e, cx) {
        return Some((v.to_string(), Some(v)));
    }
    if let syn::Expr::Path(p) = e {
        if let Some(t) = int_assoc_const_path(p) {
            let v = t.parse::<i128>().ok();
            return Some((t, v));
        }
    }
    None
}

/// The payload shape of one enum variant — a type-checker fact, so
/// registering it (even for modules whose bodies do not lower) is exact.
#[derive(Clone, PartialEq, Eq, Debug)]
enum VariantShape {
    Unit,
    Tuple(Vec<Ty>),
    Struct(Vec<(String, Ty)>),
}

fn enum_shape(e: &syn::ItemEnum, cx: &Cx) -> HashMap<String, VariantShape> {
    let mut vs: HashMap<String, VariantShape> = HashMap::new();
    for v in &e.variants {
        let shape = match &v.fields {
            syn::Fields::Unit => VariantShape::Unit,
            syn::Fields::Unnamed(f) => VariantShape::Tuple(
                f.unnamed.iter().map(|f| ty_from_type(&f.ty, cx)).collect(),
            ),
            syn::Fields::Named(f) => VariantShape::Struct(
                f.named
                    .iter()
                    .filter_map(|f| {
                        f.ident
                            .as_ref()
                            .map(|id| (id.to_string(), ty_from_type(&f.ty, cx)))
                    })
                    .collect(),
            ),
        };
        vs.insert(v.ident.to_string(), shape);
    }
    vs
}

/// Declare every enum/struct NAME before any shape is computed.
/// `ty_from_type` classifies a path by looking the name up in the
/// registry, so a self-referential field (`Vec<CuSeqStmt>` inside
/// `CuSeqStmt`) resolved to `Unknown` on the first registration pass and
/// to `Arr(Enum(..))` on a later one. Two differing shapes for ONE
/// declaration read as a real name collision, so the enum was dropped as
/// ambiguous and every `CuSeqStmt::While { .. }` pattern refused. A name
/// is a declaration fact, so declaring names up front makes shape
/// resolution order-independent while genuine collisions stay ambiguous.
fn predeclare_item(item: &syn::Item, cx: &mut Cx) {
    if cfg_test_only(item_attrs(item)) {
        return;
    }
    match item {
        syn::Item::Enum(e) => {
            let name = e.ident.to_string();
            if !cx.enums.iter().any(|x| *x == name) {
                cx.enums.push(name);
            }
        }
        syn::Item::Struct(s) => {
            let name = s.ident.to_string();
            if let syn::Fields::Unnamed(f) = &s.fields {
                let arity = f.unnamed.len();
                cx.tuple_structs
                    .entry(name)
                    .or_insert_with(|| vec![Ty::Unknown; arity]);
            } else {
                cx.structs.borrow_mut().entry(name).or_default();
            }
        }
        syn::Item::Mod(m) => {
            if let Some((_, inner)) = &m.content {
                predeclare_types(inner, cx);
            }
        }
        _ => {}
    }
}

fn predeclare_types(items: &[syn::Item], cx: &mut Cx) {
    for item in items {
        predeclare_item(item, cx);
    }
}

fn register_enum(e: &syn::ItemEnum, cx: &mut Cx) {
    let name = e.ident.to_string();
    let vs = enum_shape(e, cx);
    if let Some(prev) = cx.variants.get(&name) {
        if prev != &vs {
            // Same unqualified name, different declaration: never pick.
            cx.variants.remove(&name);
            cx.ambiguous.insert(name);
            return;
        }
    }
    if cx.ambiguous.contains(&name) {
        return;
    }
    if !cx.enums.iter().any(|x| *x == name) {
        cx.enums.push(name.clone());
    }
    cx.variants.insert(name, vs);
}

fn register_struct(s: &syn::ItemStruct, cx: &mut Cx) {
    let name = s.ident.to_string();
    if let syn::Fields::Unnamed(f) = &s.fields {
        cx.tuple_structs.insert(
            name,
            f.unnamed.iter().map(|f| ty_from_type(&f.ty, cx)).collect(),
        );
        return;
    }
    let fields: Vec<(String, Ty)> = s
        .fields
        .iter()
        .filter_map(|f| match &f.ident {
            Some(id) => Some((id.to_string(), ty_from_type(&f.ty, cx))),
            None => None,
        })
        .collect();
    cx.structs.borrow_mut().insert(name, fields);
}

/// A declaration-only index must not depend on a module's BODIES lowering:
/// type declarations are type-checker facts. Find the crate source root by
/// walking up to `lib.rs`/`main.rs`, then scan every `.rs` under it (the
/// gated splice skips dirty modules entirely, so `NativeCtx`/`IrStmt`
/// variants were invisible and their patterns refused).
fn find_crate_src_root(start: &Path) -> Option<PathBuf> {
    // CANONICALIZE first: the harness passes RELATIVE entry paths, and
    // popping a relative path reaches "" (whose `join("lib.rs")` finds
    // the CWD's lib.rs but `read_dir("")` fails) — the declaration index
    // silently saw zero files and every cross-module enum pattern refused.
    let mut d = std::fs::canonicalize(start).unwrap_or_else(|_| start.to_path_buf());
    loop {
        if d.join("lib.rs").is_file() || d.join("main.rs").is_file() {
            return Some(d);
        }
        if !d.pop() {
            return None;
        }
    }
}

fn collect_rs_files(dir: &Path, out: &mut Vec<PathBuf>) {
    let Ok(rd) = std::fs::read_dir(dir) else {
        return;
    };
    for e in rd.flatten() {
        let p = e.path();
        if p.is_dir() {
            let skip = p
                .file_name()
                .and_then(|n| n.to_str())
                .map(|n| n == "target" || n == ".git")
                .unwrap_or(true);
            if !skip {
                collect_rs_files(&p, out);
            }
        } else if p.extension().and_then(|x| x.to_str()) == Some("rs") {
            out.push(p);
        }
    }
}

fn index_items(items: &[syn::Item], cx: &mut Cx) {
    for item in items {
        // test-only declarations are not part of a normal build's name set
        if cfg_test_only(item_attrs(item)) {
            continue;
        }
        match item {
            syn::Item::Enum(e) => register_enum(e, cx),
            syn::Item::Struct(s) => register_struct(s, cx),
            syn::Item::Mod(m) => {
                if let Some((_, inner)) = &m.content {
                    index_items(inner, cx);
                }
            }
            _ => {}
        }
    }
}

fn index_declarations(root: &Path, cx: &mut Cx) {
    let mut files = Vec::new();
    collect_rs_files(root, &mut files);
    let mut asts: Vec<syn::File> = Vec::new();
    for f in files {
        let Ok(text) = std::fs::read_to_string(&f) else {
            continue;
        };
        let Ok(ast) = syn::parse_file(&text) else {
            continue;
        };
        asts.push(ast);
    }
    // NAMES FIRST: a shape must not depend on which file was read first
    // (see `predeclare_item` — self-referential fields).
    for ast in &asts {
        predeclare_types(&ast.items, cx);
    }
    for ast in &asts {
        index_items(&ast.items, cx);
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
            "--census" => {
                CENSUS.store(true, std::sync::atomic::Ordering::Relaxed);
                std::panic::set_hook(Box::new(|info| {
        if std::env::var("RUST_FRONTEND_PANIC_DEBUG").is_ok() {
            eprintln!(
                "PANIC: {info}\n{}",
                std::backtrace::Backtrace::force_capture()
            );
        }
    }));
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
    // type registries first — resolution consults them. NAMES first, then
    // shapes, so a self-referential field resolves the same way in every
    // pass (`predeclare_item`).
    predeclare_types(&ast.items, &mut cx);
    for item in &ast.items {
        match item {
            syn::Item::Struct(s) => {
                let fields: Vec<(String, Ty)> = s
                    .fields
                    .iter()
                    .filter_map(|f| match &f.ident {
                        Some(id) => Some((id.to_string(), ty_from_type(&f.ty, &cx))),
                        None => None,
                    })
                    .collect();
                cx.structs.borrow_mut().insert(s.ident.to_string(), fields);
            }
            syn::Item::Enum(e) => register_enum(e, &mut cx),
            _ => {}
        }
    }
    // WHOLE-CRATE expansion: entry file + its lib.rs tree (deduped) so
    // cross-module calls resolve; then flatten inline `mod` blocks.
    let base_dir = Path::new(&file)
        .parent()
        .map(|p| p.to_path_buf())
        .unwrap_or_else(|| PathBuf::from("."));
    // Entry-file expansion first (including its own mod tree), then the
    // lib.rs MODULE TREE with the same GATED splice: a module joins the
    // program only when it lowers cleanly on its own. This differs from
    // naive whole-crate merging (reverted earlier: 0/30) because dirty
    // modules never poison the entry — their calls simply refuse.
    let no_expand = std::env::var("RUST_FRONTEND_NO_EXPAND").is_ok();
    let mut visited: HashSet<PathBuf> = HashSet::new();
    visited.insert(std::fs::canonicalize(&file).unwrap_or_else(|_| PathBuf::from(&file)));
    // Functions pulled out of gated-out modules (demand pass). They are
    // lowered SEPARATELY and only registered when their body succeeds:
    // a pulled function that refuses must degrade to the old
    // "call to unknown function" refusal, never fail the whole file.
    let mut demand_extra: Vec<syn::Item> = Vec::new();
    // Entry file + its mod tree first...
    let mut roots: Vec<syn::Item> = if no_expand {
        ast.items.clone()
    } else {
        expand_crate(&ast.items, &base_dir, &mut visited)
    };
    // ...then the lib.rs MODULE TREE with the same GATED splice: a module
    // joins only when it lowers cleanly alone, so a dirty module never
    // poisons the entry (its cross-calls simply refuse at the site).
    // The crate root is found by WALKING UP: a leaf entry
    // (`pipeline_native/capabilities/cut.rs`) has no lib.rs beside it, and
    // using its own directory made the whole module tree invisible.
    let crate_root = find_crate_src_root(&base_dir).unwrap_or_else(|| base_dir.clone());
    let lib_path = crate_root.join("lib.rs");
    if !no_expand && lib_path.exists() && !visited.contains(&lib_path) {
        visited.insert(lib_path.clone());
        if let Ok(src_text) = std::fs::read_to_string(&lib_path) {
            if let Ok(f) = syn::parse_file(&src_text) {
                roots.extend(expand_crate(&f.items, &crate_root, &mut visited));
            }
        }
    }
    // DEMAND-DRIVEN SPLICE: a module that does not lower cleanly is
    // excluded wholesale, so one dirty function poisons every clean
    // function beside it. Pull only the functions (and impl methods) the
    // entry actually CALLS out of those modules, transitively.
    if !no_expand {
        let mut mods: Vec<PathBuf> = Vec::new();
        collect_mod_files(&ast.items, &base_dir, &mut mods);
        // The lib.rs MODULE TREE is the crate's real structure for a leaf
        // entry (go_backend.rs has no `mod` of its own).
        if let Ok(text) = std::fs::read_to_string(crate_root.join("lib.rs")) {
            if let Ok(f) = syn::parse_file(&text) {
                collect_mod_files(&f.items, &crate_root, &mut mods);
            }
        }
        if std::env::var("RUST_FRONTEND_DEMAND_DEBUG").is_ok() {
            eprintln!("DEMAND mods: {mods:?}");
        }
        let mut dirty: Vec<syn::File> = Vec::new();
        for f in &mods {
            let canon = std::fs::canonicalize(f).unwrap_or_else(|_| f.clone());
            // `visited` already distinguishes SPLICED (clean) modules
            // from gated-out ones; re-running lowers_clean here would
            // re-lower every dirty module (minutes per file).
            if visited.contains(&canon) || visited.contains(f) {
                continue;
            }
            if let Ok(text) = std::fs::read_to_string(f) {
                if let Ok(ast) = syn::parse_file(&text) {
                    dirty.push(ast);
                }
            }
        }
        if !dirty.is_empty() {
            let mut defined = defined_names(&roots);
            let mut needed = HashSet::new();
            collect_calls_in_items(&roots, &mut needed);
            let mut extra: Vec<syn::Item> = Vec::new();
            for _round in 0..12 {
                let missing: HashSet<String> =
                    needed.difference(&defined).cloned().collect();
                if std::env::var("RUST_FRONTEND_DEMAND_DEBUG").is_ok() {
                    eprintln!("DEMAND round: needed={needed:?} missing={missing:?} extra={}", extra.len());
                }
                if missing.is_empty() || extra.len() > 400 {
                    break;
                }
                let mut progress = false;
                for ast in &dirty {
                    for item in &ast.items {
                        // demand-pulling a test-only fn would lower code
                        // that a normal build does not contain
                        if cfg_test_only(item_attrs(item)) {
                            continue;
                        }
                        match item {
                            syn::Item::Fn(f)
                                if missing.contains(&f.sig.ident.to_string()) =>
                            {
                                defined.insert(f.sig.ident.to_string());
                                extra.push(item.clone());
                                progress = true;
                            }
                            syn::Item::Impl(i) => {
                                let Some(t) = type_ident(&i.self_ty) else {
                                    continue;
                                };
                                let keep: Vec<syn::ImplItem> = i
                                    .items
                                    .iter()
                                    .filter(|ii| match ii {
                                        syn::ImplItem::Fn(m) => missing
                                            .contains(&format!("{}_{}", t, m.sig.ident)),
                                        _ => false,
                                    })
                                    .cloned()
                                    .collect();
                                if keep.is_empty() {
                                    continue;
                                }
                                for ii in &keep {
                                    if let syn::ImplItem::Fn(m) = ii {
                                        defined.insert(format!("{}_{}", t, m.sig.ident));
                                    }
                                }
                                let mut ci = i.clone();
                                ci.items = keep;
                                extra.push(syn::Item::Impl(ci));
                                progress = true;
                            }
                            _ => {}
                        }
                    }
                }
                if !progress {
                    break;
                }
                collect_calls_in_items(&extra, &mut needed);
            }
            demand_extra = extra;
        }
    }
    // Flatten inline `mod` blocks into one item list — namespace nesting
    // has no effect on the lowered program (names stay as written;
    // qualified paths resolve only for `Type::method` call targets).
    let mut flat: Vec<&syn::Item> = Vec::new();
    flatten_items(&roots, &mut flat);
    // NAMES before shapes over the expanded tree too: a module's enum must
    // not look different from the entry file's copy of it.
    for item in &flat {
        predeclare_item(item, &mut cx);
    }
    // EXPANDED-TREE type registry: the pre-pass above sees only the entry
    // file's top level, but the module closure contributes structs/enums
    // too (`BraceExpansion` lives beside mir.rs). Declared field types
    // are type-checker facts, so registering them is exact — it is what
    // lets `expansion.prefix.as_deref()` prove Option<String>.
    for item in &flat {
        match item {
            syn::Item::Struct(s) => {
                let fields: Vec<(String, Ty)> = s
                    .fields
                    .iter()
                    .filter_map(|f| match &f.ident {
                        Some(id) => Some((id.to_string(), ty_from_type(&f.ty, &cx))),
                        None => None,
                    })
                    .collect();
                cx.structs.borrow_mut().insert(s.ident.to_string(), fields);
            }
            syn::Item::Enum(e) => register_enum(e, &mut cx),
            _ => {}
        }
    }
    // DECLARATION-ONLY index of the whole crate: enum/struct declarations
    // in modules whose BODIES do not lower are still type-checker facts.
    // This is what makes `IrStmt::Function { .. }` / `NativeCtx::Pipe(..)`
    // patterns resolvable when the module closure is gated out.
    if let Some(root) = find_crate_src_root(&base_dir) {
        index_declarations(&root, &mut cx);
    }
    if std::env::var("RUST_FRONTEND_DEBUG_TYPES").is_ok() {
        eprintln!("TYPES enums: {:?}", cx.enums);
        eprintln!("TYPES ambiguous: {:?}", cx.ambiguous);
        if let Some(v) = cx.variants.get("NativeCtx") {
            eprintln!("TYPES NativeCtx variants: {:?}", v.keys().collect::<Vec<_>>());
        } else {
            eprintln!("TYPES NativeCtx MISSING");
        }
    }
    // Program-wide store-binding names (see Cx::bindings).
    {
        use syn::visit::Visit as _;
        struct Collect<'a>(&'a mut HashSet<String>);
        impl<'ast> syn::visit::Visit<'ast> for Collect<'_> {
            fn visit_pat_ident(&mut self, p: &'ast syn::PatIdent) {
                self.0.insert(p.ident.to_string());
                syn::visit::visit_pat_ident(self, p);
            }
            fn visit_item_const(&mut self, c: &'ast syn::ItemConst) {
                self.0.insert(c.ident.to_string());
                syn::visit::visit_item_const(self, c);
            }
            fn visit_item_static(&mut self, s: &'ast syn::ItemStatic) {
                self.0.insert(s.ident.to_string());
                syn::visit::visit_item_static(self, s);
            }
        }
        let mut collect = Collect(&mut cx.bindings);
        for item in &flat {
            collect.visit_item(item);
        }
    }

    // pre-pass: collect consts/statics (globals + int-const table) so any
    // later lowering step can const-fold against them.
    for item in &flat {
        match item {
            syn::Item::Const(c) => {
                // a CONST is its folded value: emitting the literal (never
                // re-evaluating the expression at run time) is exact, and
                // it is the text every folded use already reads
                if let Some(v) = const_int(&c.expr, &cx) {
                    cx.globals
                        .push(assign_stmt(&c.ident.to_string(), str_lit(&v.to_string())));
                    cx.ints.insert(c.ident.to_string(), v);
                } else {
                    let ce = &c.expr;
                    let lowered = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                        value_expr(ce, &cx)
                    }));
                    match lowered {
                        Ok(v) => cx.globals.push(assign_stmt(&c.ident.to_string(), v)),
                        Err(p) => census_hit("const", &c.ident.to_string(), p),
                    }
                }
                if let Some(lits) = const_str_array(&c.expr) {
                    cx.const_arrays.insert(c.ident.to_string(), lits);
                }
            }
            // item-level global-state macros: `lazy_static!` and
            // `thread_local!` only DECLARE statics (with a lazy
            // initializer); both are registered exactly like a plain
            // `static`, and their accesses go through the guard layer.
            syn::Item::Macro(m) => {
                let mac = m
                    .mac
                    .path
                    .segments
                    .last()
                    .map(|s| s.ident.to_string())
                    .unwrap_or_default();
                // `lazy_static!` / `thread_local!` only DECLARE statics;
                // they are registered in the ITEM pass below (still
                // before any function is lowered), so a malformed body
                // is censused at its own item instead of aborting the
                // whole unit.
                let _ = (&mac, m);
            }
            syn::Item::Static(s) => {
                if let Some(lits) = const_str_array(&s.expr) {
                    cx.const_arrays.insert(s.ident.to_string(), lits);
                }
                // the static's declared type persists in its own map
                // (infer_ty consults it even after per-fn clears)
                let ty = ty_from_type(&s.ty, &cx);
                // the initializer goes through the STATIC rules: a wrapper
                // ctor (`Mutex::new(v)`) is the wrapper's state, a
                // collection is its named assoc store, a scalar its slot
                let init = static_init_stmt(&s.ident.to_string(), &ty, Some(&s.expr), &cx);
                cx.statics.borrow_mut().insert(s.ident.to_string(), ty);
                cx.globals.push(init);
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
                cx.fn_rets
                    .insert(f.sig.ident.to_string(), ret_ty(&f.sig.output, &cx));
                cx.fn_params
                    .insert(f.sig.ident.to_string(), param_names(&f.sig));
                cx.fn_param_mut
                    .insert(f.sig.ident.to_string(), param_muts(&f.sig));
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
                        cx.fn_rets.insert(mangled.clone(), ret_ty(&m.sig.output, &cx));
                        cx.fn_params.insert(mangled.clone(), param_names(&m.sig));
                        cx.fn_param_mut.insert(mangled.clone(), param_muts(&m.sig));
                        fns.push(FnSource::Method(mangled, m, ty.clone()));
                    }
                    // non-fn impl items (consts/types) are declaration-only
                }
            }
            _ => {}
        }
    }
    // nested `fn` items (legal anywhere in a body, capturing nothing) are
    // hoisted to top-level defs named `<parent>__<local>`. Collect and
    // register EVERY one before ANY body lowers: a nested fn is visible to
    // its whole enclosing scope, including textually above its definition.
    let mut nested: Vec<(String, &syn::ItemFn)> = Vec::new();
    {
        let mut scopes: HashMap<String, HashMap<String, String>> = HashMap::new();
        let empty: HashMap<String, String> = HashMap::new();
        for item in &flat {
            match item {
                syn::Item::Fn(f) => {
                    collect_nested_fns(
                        &f.block,
                        &f.sig.ident.to_string(),
                        &empty,
                        &mut nested,
                        &mut scopes,
                    );
                }
                syn::Item::Impl(i) => {
                    if let Some(ty) = type_ident(&i.self_ty) {
                        for ii in &i.items {
                            if let syn::ImplItem::Fn(m) = ii {
                                let parent = format!("{}_{}", ty, m.sig.ident);
                                collect_nested_fns(
                                    &m.block, &parent, &empty, &mut nested, &mut scopes,
                                );
                            }
                        }
                    }
                }
                _ => {}
            }
        }
        for (n, f) in &nested {
            cx.fns.push(n.clone());
            cx.fn_rets.insert(n.clone(), ret_ty(&f.sig.output, &cx));
            cx.fn_params.insert(n.clone(), param_names(&f.sig));
            cx.fn_param_mut.insert(n.clone(), param_muts(&f.sig));
        }
        *cx.fn_scopes.borrow_mut() = scopes;
    }
    // Lower the hoisted definitions before their parents so every call site
    // already has a definition to point at. A nested `fn` is NOT its own
    // censused unit (it belongs to the enclosing function): when it cannot
    // lower the name is dropped, so every call site refuses rather than
    // calling a function that was never emitted.
    let mut nested_defs: Vec<Value> = Vec::new();
    for (n, f) in &nested {
        let lowered = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            function_def(n.clone(), &f.attrs, &f.sig, &f.block, None, &cx)
        }));
        match lowered {
            Ok(v) => nested_defs.push(v),
            Err(_) => {
                cx.fns.retain(|x| x != n);
                cx.fn_rets.remove(n);
            }
        }
    }
    // DEMAND-PULLED definitions: register + lower BEFORE the entry so
    // its calls resolve. A body that refuses is REMOVED from the call
    // set (the call site then refuses, exactly as before the pull).
    let mut demand_defs: Vec<Value> = Vec::new();
    for item in demand_extra.iter().rev() {
        match item {
            syn::Item::Fn(f) if f.sig.ident != "main" => {
                let name = f.sig.ident.to_string();
                if !cx.fns.contains(&name) {
                    cx.fns.push(name.clone());
                    cx.fn_rets
                        .insert(name.clone(), ret_ty(&f.sig.output, &cx));
                }
                let res = caught(std::panic::AssertUnwindSafe(|| {
                    function_def(
                        name.clone(),
                        &f.attrs,
                        &f.sig,
                        &f.block,
                        None,
                        &cx,
                    )
                }));
                match res {
                    Ok(v) => demand_defs.push(v),
                    Err(e) => {
                        if std::env::var("RUST_FRONTEND_DEMAND_DEBUG").is_ok() {
                            let msg = e
                                .downcast_ref::<String>()
                                .cloned()
                                .unwrap_or_else(|| "<panic>".to_string());
                            eprintln!("DEMAND dropped {name}: {msg}");
                        }
                        cx.fns.retain(|n| *n != name);
                        cx.fn_rets.remove(&name);
                    }
                }
            }
            syn::Item::Impl(i) => {
                let Some(t) = type_ident(&i.self_ty) else {
                    continue;
                };
                for ii in &i.items {
                    let syn::ImplItem::Fn(m) = ii else {
                        continue;
                    };
                    let name = format!("{}_{}", t, m.sig.ident);
                    if !cx.fns.contains(&name) {
                        cx.fns.push(name.clone());
                        cx.fn_rets
                            .insert(name.clone(), ret_ty(&m.sig.output, &cx));
                    }
                    let res = caught(std::panic::AssertUnwindSafe(|| {
                        function_def(
                            name.clone(),
                            &m.attrs,
                            &m.sig,
                            &m.block,
                            Some(&t),
                            &cx,
                        )
                    }));
                    match res {
                        Ok(v) => demand_defs.push(v),
                        Err(_) => {
                            cx.fns.retain(|n| *n != name);
                            cx.fn_rets.remove(&name);
                        }
                    }
                }
            }
            _ => {}
        }
    }
    let mut stmts: Vec<Value> = Vec::new();
    let _items_census = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
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
            // `macro_rules!` DEFINITIONS have no runtime effect; the two
            // state macros were registered in the pre-pass above. Unknown
            // INVOCATIONS still refuse at their site (`include!` splices
            // build-generated code — no source to lower).
            syn::Item::Macro(m) => {
                let name = m
                    .mac
                    .path
                    .segments
                    .last()
                    .map(|s| s.ident.to_string())
                    .unwrap_or_default();
                match name.as_str() {
                    "macro_rules" => {}
                    "lazy_static" | "thread_local" => {
                        let items = if name == "lazy_static" {
                            syn::parse2::<LazyStatics>(m.mac.tokens.clone())
                                .unwrap_or_else(|e| refuse("lazy_static! body", e.span()))
                                .0
                        } else {
                            syn::parse2::<ThreadLocals>(m.mac.tokens.clone())
                                .unwrap_or_else(|e| refuse("thread_local! body", e.span()))
                                .0
                        };
                        for (n, t, i) in items {
                            // the static's store name is the Rust name; its
                            // declared shape is what every access resolves
                            // against (Ty::Cel for the wrappers)
                            let sname = n.to_string();
                            let ty = ty_from_type(&t, &cx);
                            cx.statics.borrow_mut().insert(sname.clone(), ty.clone());
                            cx.bindings.insert(sname.clone());
                            let init = static_init_stmt(&sname, &ty, Some(&i), &cx);
                            cx.globals.push(init);
                        }
                    }
                    _ => refuse(
                        "item-level macro invocation (creates state / selects code)",
                        m.span(),
                    ),
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
                // `Self` is not a legal path in a free fn: drop the impl
                // type the last method_def left behind, so an invalid
                // source can never resolve a `Self::` call here.
                *cx.self_ty.borrow_mut() = None;
                // hoisted nested `fn`s of `main`'s own body
                {
                    let frame = cx.fn_scopes.borrow().get("main").cloned().unwrap_or_default();
                    *cx.fn_alias.borrow_mut() = frame;
                }
                cx.vars.borrow_mut().clear();
                lower_block(&f.block, &cx, false, &mut stmts);
            }
            other => refuse("items other than `fn`/`use`/`const`/`static`", other.span()),
        }
    }
    }));
    if let Err(p) = _items_census {
        census_hit("program", "items", p);
    }
    // NOTE: no `main` is LEGAL for library-style sources (definitions
    // only) — the program body is then empty. The stdout oracle never
    // sees such a file (testdata examples are all runnable).
    let mut all_stmts = std::mem::take(&mut cx.globals);
    // hoisted nested `fn` definitions first (they are ordinary defs: nothing
    // runs them until a call, and any order among defs is equivalent)
    all_stmts.extend(nested_defs);
    all_stmts.extend(demand_defs);
    // function definitions FIRST — runtime def-before-use order (the A1
    // Function statement lowers to a definition executed in stmt order).
    for src_fn in &fns {
        let (unit, label) = match src_fn {
            FnSource::Free(f) => ("fn", f.sig.ident.to_string()),
            FnSource::Method(name, _, _) => ("fn", name.clone()),
        };
        let lowered = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| match src_fn {
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
        }));
        match lowered {
            Ok(v) => all_stmts.push(v),
            Err(p) => census_hit(unit, &label, p),
        }
    }
    all_stmts.append(&mut stmts);
    if census_mode() {
        eprintln!(
            "CENSUS-SUMMARY\t{file}\t{}\tblocked units",
            CENSUS_HITS.load(std::sync::atomic::Ordering::Relaxed)
        );
        exit(0);
    }

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
        // statements a VALUE lowering hoisted (an if-value, a pure-read
        // snapshot) land in `pending`; they must run BEFORE the statement
        // that reads their temp.
        cx.pending.borrow_mut().clear();
        let v = lower_stmt(stmt, cx, in_fn, out);
        let mut pend: Vec<Value> = cx.pending.borrow_mut().drain(..).collect();
        out.append(&mut pend);
        if let Some(v) = v {
            out.push(v);
        }
    }
}

/// Lower ONE statement of a function body and drain what its value
/// lowering hoisted into `pending` first (a branch, a pure-read
/// snapshot): those run BEFORE the statement that reads their temp,
/// exactly as `lower_block` arranges for main/loops.
fn lower_fn_stmt(stmt: &syn::Stmt, cx: &Cx, body: &mut Vec<Value>) {
    cx.pending.borrow_mut().clear();
    let v = lower_stmt(stmt, cx, true, body);
    let mut pend: Vec<Value> = cx.pending.borrow_mut().drain(..).collect();
    body.append(&mut pend);
    if let Some(v) = v {
        body.push(v);
    }
}

fn lower_stmt(stmt: &syn::Stmt, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Option<Value> {
    match stmt {
        syn::Stmt::Local(local) => lower_local(local, cx, in_fn, out),
        syn::Stmt::Expr(e, _semi) => lower_expr_stmt(e, cx, in_fn, out),
        // a nested `fn` was HOISTED to a top-level definition by the
        // pre-pass: a `fn` item captures nothing and is visible in the whole
        // enclosing body (even textually above its own definition), so the
        // hoist is exact — the same shape Rust itself gives it. A `use`
        // inside a body only names paths and has no runtime meaning.
        syn::Stmt::Item(syn::Item::Fn(_)) => None,
        syn::Stmt::Item(syn::Item::Use(_)) => None,
        syn::Stmt::Item(item) => refuse("nested item declarations", item.span()),
        syn::Stmt::Macro(m) => Some(lower_print_macro(&m.mac, cx)),
    }
}

/// `let PAT = EXPR;` / `let PAT = EXPR else { DIVERGE };` for
/// non-identifier patterns. The initializer evaluates ONCE into a temp;
/// the refutable part becomes an `if`, the bindings live in the success
/// branch, and a diverging `else` block lives in the failure branch.
fn let_pattern_stmt(
    pat: &syn::Pat,
    init: &syn::LocalInit,
    cx: &Cx,
    in_fn: bool,
    out: &mut Vec<Value>,
) -> Option<Value> {
    // `let (a, b) = f()?;` — the `?` propagates BEFORE the destructure.
    let (init_expr, try_prop) = match &*init.expr {
        syn::Expr::Try(t) => (&*t.expr, true),
        e => (e, false),
    };
    let temp = fresh_tmp(cx, "l");
    out.push(store_stmt(&temp, value_expr(init_expr, cx)));
    // The temp's shape drives slice-pattern element reads (array vs list).
    let temp_ty = infer_ty(&init.expr, cx);
    cx.vars.borrow_mut().insert(temp.clone(), temp_ty);
    if try_prop {
        let prop = if in_fn {
            json!({"type": "Return", "value": str_lit("")})
        } else {
            json!({"type": "Exit", "value": null})
        };
        out.push(json!({
            "cond": test_call(&format!("\"${temp}\" = \"\"")),
            "then": [prop],
            "elsifs": [],
            "else": [],
            "type": "If",
        }));
    }
    let mut pre = Vec::new();
    let mut binds = Vec::new();
    let cond = destructure(&temp, pat, cx, &mut pre, &mut binds);
    out.extend(pre);
    match (&init.diverge, cond) {
        (Some((_, diverge)), Some(c)) => {
            let mut else_body = Vec::new();
            match &**diverge {
                syn::Expr::Block(blk) => lower_block(&blk.block, cx, true, &mut else_body),
                other => refuse("let-else statement form", other.span()),
            }
            out.push(json!({
                "cond": test_call(&c),
                "then": binds,
                "elsifs": [],
                "else": else_body,
                "type": "If",
            }));
            None
        }
        (Some(_), None) => refuse("irrefutable `let ... else`", pat.span()),
        (None, Some(_)) => refuse("refutable `let` binding without `else`", pat.span()),
        (None, None) => {
            out.extend(binds);
            None
        }
    }
}

/// Does `e` contain a `?` anywhere BELOW its root? (A root `?` keeps the
/// existing dedicated lowering.)
fn has_nested_try(e: &syn::Expr) -> bool {
    fn walk(e: &syn::Expr) -> bool {
        match e {
            syn::Expr::Try(_) => true,
            syn::Expr::MethodCall(m) => walk(&m.receiver) || m.args.iter().any(walk),
            syn::Expr::Call(c) => c.args.iter().any(walk),
            syn::Expr::Field(x) => walk(&x.base),
            syn::Expr::Index(x) => walk(&x.expr) || walk(&x.index),
            syn::Expr::Binary(x) => walk(&x.left) || walk(&x.right),
            syn::Expr::Unary(x) => walk(&x.expr),
            syn::Expr::Paren(x) => walk(&x.expr),
            syn::Expr::Reference(x) => walk(&x.expr),
            syn::Expr::Cast(x) => walk(&x.expr),
            syn::Expr::Tuple(t) => t.elems.iter().any(walk),
            _ => false,
        }
    }
    match e {
        syn::Expr::Try(t) => walk(&t.expr),
        other => walk(other),
    }
}

/// Rewrite `e` so every `?` becomes `tmp = <inner>; if tmp is empty
/// {propagate}; ` and the value of the whole expression is the temp.
fn hoist_tries(e: &syn::Expr, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> syn::Expr {
    let tmp_path = |name: &str| {
        syn::parse_str::<syn::Expr>(name).unwrap_or_else(|_| refuse("temporary name", e.span()))
    };
    match e {
        syn::Expr::Try(t) => {
            let inner = hoist_tries(&t.expr, cx, in_fn, out);
            let tmp = fresh_tmp(cx, "q");
            let inner_ty = infer_ty(&inner, cx);
            out.push(store_stmt(&tmp, value_expr(&inner, cx)));
            // the temp IS the unwrapped value: its type drives the outer
            // method dispatch (`prog.strip_prefix('{')?.strip_suffix('}')`)
            cx.vars.borrow_mut().insert(tmp.clone(), inner_ty);
            let prop = if in_fn {
                json!({"type": "Return", "value": str_lit("")})
            } else {
                json!({"type": "Exit", "value": null})
            };
            out.push(json!({
                "cond": test_call(&format!("\"${tmp}\" = \"\"")),
                "then": [prop],
                "elsifs": [],
                "else": [],
                "type": "If",
            }));
            tmp_path(&tmp)
        }
        syn::Expr::MethodCall(m) => {
            let mut m2 = m.clone();
            m2.receiver = Box::new(hoist_tries(&m.receiver, cx, in_fn, out));
            for a in m2.args.iter_mut() {
                *a = hoist_tries(a, cx, in_fn, out);
            }
            syn::Expr::MethodCall(m2)
        }
        syn::Expr::Call(c) => {
            let mut c2 = c.clone();
            for a in c2.args.iter_mut() {
                *a = hoist_tries(a, cx, in_fn, out);
            }
            syn::Expr::Call(c2)
        }
        syn::Expr::Field(f) => {
            let mut f2 = f.clone();
            f2.base = Box::new(hoist_tries(&f.base, cx, in_fn, out));
            syn::Expr::Field(f2)
        }
        syn::Expr::Index(ix) => {
            let mut ix2 = ix.clone();
            ix2.expr = Box::new(hoist_tries(&ix.expr, cx, in_fn, out));
            ix2.index = Box::new(hoist_tries(&ix.index, cx, in_fn, out));
            syn::Expr::Index(ix2)
        }
        syn::Expr::Binary(b) => {
            let mut b2 = b.clone();
            b2.left = Box::new(hoist_tries(&b.left, cx, in_fn, out));
            b2.right = Box::new(hoist_tries(&b.right, cx, in_fn, out));
            syn::Expr::Binary(b2)
        }
        syn::Expr::Paren(p) => {
            let mut p2 = p.clone();
            p2.expr = Box::new(hoist_tries(&p.expr, cx, in_fn, out));
            syn::Expr::Paren(p2)
        }
        other => other.clone(),
    }
}

fn lower_local(
    local: &syn::Local,
    cx: &Cx,
    in_fn: bool,
    out: &mut Vec<Value>,
) -> Option<Value> {
    // `let x: i64 = ...` parses as Pat::Type wrapping Pat::Ident — the
    // annotation is type-checker information, erased here (the store is
    // dynamically typed).
    let pat: &syn::Pat = match &local.pat {
        syn::Pat::Type(pt) => &pt.pat,
        p => p,
    };
    if let Some(init) = &local.init {
        let plain_ident = matches!(pat, syn::Pat::Ident(pi) if pi.subpat.is_none() && pi.ident != "None");
        if init.diverge.is_some() || !plain_ident {
            return let_pattern_stmt(pat, init, cx, in_fn, out);
        }
    }
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
    // `let g = NAME.lock().unwrap();` binds a HANDLE on the static — not a
    // copy — so every later use of `g` reads/writes `NAME`. No statement
    // is emitted (the guard's only effect is holding the lock, which a
    // single-threaded runtime does not model).
    if let Some((sname, inner)) = guard_handle(&init.expr, cx) {
        if is_scalar_store_ty(&inner) || matches!(inner, Ty::Set(..) | Ty::Map(..)) {
            cx.guards.borrow_mut().insert(name.clone(), (sname, inner));
            return None;
        }
    }
    // `let mut m = HashMap::new()` / `HashSet::from([..])` — a NAMED
    // associative store; an explicit type annotation supplies K/V.
    if let Some((mut ty, entries)) = map_ctor(&init.expr, cx) {
        if let syn::Pat::Type(pt) = &local.pat {
            let at = ty_from_type(&pt.ty, cx);
            if matches!(at, Ty::Map(..) | Ty::Set(..)) {
                ty = at;
            }
        }
        out.push(assoc_empty_stmt(&name));
        for (k, v) in entries {
            out.push(assoc_set_stmt(&name, k, v));
        }
        cx.vars.borrow_mut().insert(name.clone(), ty);
        return None;
    }
    // `let ok = COLL.any(|e| COND)` / `.all(..)` — a bounded loop that
    // binds the boolean result (empty any=false, empty all=true).
    if let Some((all, src, cl)) = quantifier_parts(&init.expr, cx) {
        let r = quantifier_stmts(all, src, cl, cx, out);
        cx.vars.borrow_mut().insert(name.clone(), Ty::Bool);
        return Some(store_stmt(&name, get_var(&r)));
    }
    // if-as-value: bind the target in EVERY branch (exact — rustc
    // guarantees all paths produce the value)
    // A chain COLLECTED INTO A MAP: `let m: BTreeMap<K,V> = SRC.iter()
    // .enumerate().map(|(i, n)| (K, V)).collect();`. Each element inserts
    // one pair, so the fill order does not matter (a map has no order of
    // its own). The source may be ANY array-valued expression — a field
    // array is copied ONCE into a temp exactly like array iteration — and
    // the bound is the runtime-trusted arrayLen, so an unproven-length
    // source is fine here (unlike the ordered accumulator below).
    let decl_map = match &local.pat {
        syn::Pat::Type(pt) => matches!(ty_from_type(&pt.ty, cx), Ty::Map(..)),
        _ => false,
    };
    if decl_map && as_method_call(&init.expr, "collect").is_some() {
        if let Some((src, iname, vname, key, val)) = map_collect_parts(&init.expr, cx) {
            let t = fresh_tmp(cx, "a");
            match src {
                // a plain array variable: copy its ELEMENTS (a getVar read
                // would arrive as one joined string — see array_items_call)
                syn::Expr::Path(p) if p.qself.is_none() && single_path(p).is_some() => {
                    let n = single_path(p).unwrap().to_string();
                    out.push(store_stmt(&t, array_items_call(&n)));
                }
                other => out.push(store_stmt(&t, value_expr(other, cx))),
            }
            cx.vars
                .borrow_mut()
                .insert(t.clone(), Ty::Arr(None, None));
            let idxv = fresh_tmp(cx, "i");
            let lenv = fresh_tmp(cx, "l");
            out.push(assign_stmt(&lenv, array_len_call(&t)));
            cx.vars.borrow_mut().insert(lenv.clone(), Ty::Int);
            cx.vars.borrow_mut().insert(idxv.clone(), Ty::Int);
            let elem_ty = match infer_ty(src, cx) {
                Ty::Arr(Some(e), _) => *e,
                _ => Ty::Str,
            };
            let mut body = Vec::new();
            cx.vars.borrow_mut().insert(iname.clone(), Ty::Int);
            body.push(assign_stmt(&iname, get_var(&idxv)));
            cx.vars.borrow_mut().insert(vname.clone(), elem_ty);
            body.push(assign_stmt(
                &vname,
                raw_elem(&t, json!({"type": "Var", "name": idxv, "sigil": null})),
            ));
            body.push(assoc_set_stmt(&name, value_expr(key, cx), value_expr(val, cx)));
            body.push(assign_stmt(
                &idxv,
                arith_bin("+", arith_var(&idxv), arith_num(1)),
            ));
            out.push(assoc_empty_stmt(&name));
            cx.vars.borrow_mut().insert(
                name.clone(),
                Ty::Map(Box::new(Ty::Str), Box::new(Ty::Int), MapOrder::Hash),
            );
            out.push(json!({
                "type": "While",
                "cond": test_call(&format!("${idxv} -lt ${lenv}")),
                "body": body,
            }));
            return None;
        }
    }
    // `std::env::args().collect()` — the source IS the runtime's argument
    // vector, so the binding is that value: a FRESH array (argv[0] then the
    // positionals), not a copy of the live positional store.
    if let Some(m) = as_method_call(&init.expr, "collect") {
        if m.args.is_empty() {
            if let syn::Expr::Call(c) = &*m.receiver {
                if is_argv_call(c) {
                    cx.vars.borrow_mut().insert(
                        name.clone(),
                        Ty::Arr(Some(Box::new(Ty::Str)), None),
                    );
                    let v = value_expr(&m.receiver, cx);
                    return Some(store_stmt(&name, v));
                }
            }
        }
    }
    // iterator chains (`SRC.iter().map(F).collect()`) desugar to
    // accumulator + index loop + setArrayAppend
    if as_method_call(&init.expr, "collect").is_some() {
        let parsed = parse_collect_chain(&init.expr, cx);
        if let Some((src_expr, steps)) = parsed {
            return collect_chain_stmt(&name, src_expr, steps, cx, out);
        }
        refuse(
            "iterator chain (only SRC.iter()[.map(closure-or-fn)].collect())",
            init.expr.span(),
        );
    }
    // NESTED `?` inside a larger expression (`a()?.b()?`): hoist EVERY
    // `?` into its own temp + early return, then evaluate the rewritten
    // expression. This is the same propagation the outermost form uses,
    // just applied at the point each `?` occurs.
    if matches!(&*init.expr, syn::Expr::Try(_)) && has_nested_try(&init.expr) {
        let hoisted = hoist_tries(&init.expr, cx, in_fn, out);
        let ty = infer_ty(&init.expr, cx);
        cx.vars.borrow_mut().insert(name.clone(), ty);
        return Some(assign_stmt(&name, value_expr(&hoisted, cx)));
    }
    // `let x = expr?;` — Option propagation under None≡"": an EMPTY
    // value returns "" from the enclosing fn (propagation); otherwise x
    // binds the unwrapped value. Exact when callers treat "" as None.
    if let syn::Expr::Try(t) = &*init.expr {
        let v = value_expr(&t.expr, cx);
        out.push(assign_stmt(&name, v));
        cx.vars.borrow_mut().insert(name.clone(), Ty::Unknown);
        out.push(json!({
            "cond": test_call(&format!("${name} = \"\"")),
            "then": [json!({"type": "Exit", "value": null})],
            "elsifs": [],
            "else": [],
            "type": "If",
        }));
        return Some(assign_stmt(&name, get_var(&name)));
    }
    // array literals STORE via setArray (native array, no coercion)
    if let Some(elems) = array_literal_elems(&init.expr) {
        let elements = elems.iter().map(|e| value_expr(e, cx)).collect::<Vec<_>>();
        let first_ty = elems
            .first()
            .map(|e| infer_ty(e, cx))
            .unwrap_or(Ty::Unknown);
        cx.vars.borrow_mut().insert(
            name.clone(),
            Ty::Arr(Some(Box::new(first_ty)), Some(elems.len() as u64)),
        );
        return Some(set_array_stmt(&name, elements));
    }
    if let syn::Expr::Match(m) = &*init.expr {
        // The bound name exists after the match (all arms assign or
        // return): register it so later `format!("{name}")` captures and
        // typed reads see it. The arm result type is not tracked.
        //
        // `in_fn` is passed THROUGH: an arm of a value-position match is
        // still the function's body, so `return None;` inside an arm is the
        // function's early exit (carrying a value), not a top-level `return`.
        cx.vars.borrow_mut().insert(name.clone(), Ty::Unknown);
        return Some(match_value_node(m, BranchTarget::Var(&name), cx, in_fn, out));
    }
    if let syn::Expr::If(ie) = &*init.expr {
        let v = if_value_node(ie, BranchTarget::Var(&name), cx, in_fn, out);
        let ty = branch_ty(ie, cx);
        cx.vars.borrow_mut().insert(name.clone(), ty);
        return Some(v);
    }
    // NOTE: infer BEFORE taking the write guard (infer_ty reads vars).
    // An EXPLICIT annotation (`let names: Vec<String> = Vec::new()`) is a
    // type-checker fact and beats the shape of the initializer.
    let mut init_ty = infer_ty(&init.expr, cx);
    if let syn::Pat::Type(pt) = &local.pat {
        let dt = ty_from_type(&pt.ty, cx);
        if !matches!(dt, Ty::Unknown) {
            init_ty = dt;
        }
    }
    // arithmetic over field reads: hoist the pure reads into temps
    if arith_contains_field(&init.expr) {
        let mut ctr = cx.tmp.borrow_mut();
        let ast = hoisted_arith(&init.expr, cx, out, &mut ctr);
        drop(ctr);
        cx.vars.borrow_mut().insert(name.clone(), Ty::Int);
        return Some(assign_stmt(&name, arith_value(ast)));
    }
    cx.vars.borrow_mut().insert(name.clone(), init_ty);
    // A `let` whose initializer is a call with `&mut` arguments bound under
    // other names: the call and its readback are the whole statement, so the
    // order is Rust's (bind a temp, then the copies).
    {
        let (done, v) = call_mut_readback(&init.expr, Some(&name), cx, out, false);
        if done {
            return v;
        }
    }
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
            // a char is a one-character string in the store
            syn::Lit::Char(_) => Ty::Str,
            _ => Ty::Unknown,
        },
        syn::Expr::Macro(m) => {
            let name = m.mac.path.segments.last().map(|s| s.ident.to_string()).unwrap_or_default();
            if name == "vec" {
                // literal args give the EXACT element type (first arg)
                // and LENGTH — the loop bound needs both
                struct VecElems(syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>);
                impl syn::parse::Parse for VecElems {
                    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
                        Ok(VecElems(
                            syn::punctuated::Punctuated::parse_terminated(input)?,
                        ))
                    }
                }
                match syn::parse2::<VecElems>(m.mac.tokens.clone()) {
                    Ok(VecElems(elems)) => {
                        let elem = elems
                            .first()
                            .map(|e| Box::new(infer_ty(e, cx)))
                            .unwrap_or_else(|| Box::new(Ty::Unknown));
                        Ty::Arr(Some(elem), Some(elems.len() as u64))
                    }
                    Err(_) => Ty::Arr(None, None),
                }
            } else {
                Ty::Arr(None, None)
            }
        }
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            if matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_) | And(_) | Or(_)) {
                Ty::Bool
            } else {
                Ty::Int
            }
        }
        syn::Expr::Paren(p) => infer_ty(&p.expr, cx),
        syn::Expr::Reference(r) => infer_ty(&r.expr, cx),
        // deref reads pass through: the store holds values
        syn::Expr::Unary(u) if matches!(u.op, syn::UnOp::Deref(_)) => {
            infer_ty(&u.expr, cx)
        }
        syn::Expr::Array(ar) => Ty::Arr(Some(Box::new(
            ar.elems.first().map(|e| infer_ty(e, cx)).unwrap_or(Ty::Unknown),
        )), Some(ar.elems.len() as u64)),
        // a struct literal HAS its named record type (receiver dispatch)
        syn::Expr::Struct(se) => {
            if let Some((en, _)) = enum_variant_of_path(&se.path, cx) {
                return Ty::Enum(en);
            }
            let seg = se.path.segments.last();
            match seg {
                Some(s) => Ty::Struct(s.ident.to_string()),
                None => Ty::Unknown,
            }
        }
        // a TUPLE literal is an array of the first element's proven type
        // REGISTERED-fn calls carry their declared return Ty
        syn::Expr::Call(c) => {
            if let syn::Expr::Path(p2) = &*c.func {
                if let Some((en, _)) = enum_variant_of_path(&p2.path, cx) {
                    return Ty::Enum(en);
                }
                if is_env_var_path(p2) && env_var_lit(&c.args).is_some() {
                    return Ty::Opt(Box::new(Ty::Str));
                }
                // the process argument VECTOR is an array of strings
                if is_argv_call(c) {
                    return Ty::Arr(Some(Box::new(Ty::Str)), None);
                }
                // `String::new()` / `String::with_capacity(n)` / `String::from(x)`
                // — the store value is a string in every case.
                if p2.qself.is_none()
                    && p2.path.leading_colon.is_none()
                    && p2.path.segments.len() == 2
                    && p2.path.segments[0].ident == "String"
                    && matches!(
                        p2.path.segments[1].ident.to_string().as_str(),
                        "new" | "with_capacity" | "from"
                    )
                {
                    return Ty::Str;
                }
                // `Self::m(..)` inside an impl block returns the
                // enclosing type's registered method's declared Ty
                if p2.qself.is_none()
                    && p2.path.leading_colon.is_none()
                    && p2.path.segments.len() == 2
                    && p2.path.segments[0].ident == "Self"
                {
                    if let Some(t) = cx.self_ty.borrow().as_deref() {
                        let mangled = format!("{}_{}", t, p2.path.segments[1].ident);
                        if let Some(rt) = cx.fn_rets.get(&mangled) {
                            return rt.clone();
                        }
                    }
                }
                if let Some(id) = single_path(p2) {
                    // Some(v) constructor -> Opt(type of v), under the
                    // None≡"" convention (the option methods then collapse
                    // to the plain variable read).
                    if id == "Some" && c.args.len() == 1 {
                        return Ty::Opt(Box::new(infer_ty(&c.args[0], cx)));
                    }
                    if let Some(t) = cx.fn_rets.get(&id.to_string()) {
                        return t.clone();
                    }
                }
            }
            Ty::Unknown
        }
        // method-call result types (the small proven family)
        syn::Expr::MethodCall(m) => {
            let rty = infer_ty(&m.receiver, cx);
            // a static guard / an interior-mutability wrapper: the
            // single-threaded runtime erases the wrapper
            if let Some((_, inner)) = static_guard(&m.receiver, cx) {
                match (&inner, m.method.to_string().as_str()) {
                    (Ty::Set(..), "contains" | "insert" | "remove" | "is_empty")
                    | (Ty::Map(..), "contains_key" | "is_empty") => return Ty::Bool,
                    (Ty::Map(_, v, _), "get" | "insert" | "remove") => {
                        return Ty::Opt(v.clone())
                    }
                    (Ty::Set(..), "len") | (Ty::Map(..), "len") => return Ty::Int,
                    _ => {}
                }
            }
            if let Ty::Cel(t) = &rty {
                match m.method.to_string().as_str() {
                    "lock" | "read" | "write" | "borrow" | "borrow_mut" => {
                        return Ty::Guard(t.clone())
                    }
                    "get" | "replace" | "set" | "take" => return (**t).clone(),
                    _ => {}
                }
            }
            if let Ty::Guard(t) = &rty {
                if m.method == "unwrap" {
                    return (**t).clone();
                }
            }
            match m.method.to_string().as_str() {
                // identity-shaped reads keep the receiver's option-ness
                "as_deref" => rty,
                "is_some" | "is_none" | "is_ok" | "is_err" => Ty::Bool,
                "get" => match &rty {
                    Ty::Map(_, v, _) => Ty::Opt(v.clone()),
                    _ => Ty::Unknown,
                },
                "keys" => match &rty {
                    Ty::Map(k, _, _) => Ty::Arr(Some(k.clone()), None),
                    _ => Ty::Unknown,
                },
                "values" => match &rty {
                    Ty::Map(_, v, _) => Ty::Arr(Some(v.clone()), None),
                    _ => Ty::Unknown,
                },
                "split" => Ty::List,
                // string-to-string methods keep the Str type (a proven
                // receiver makes the result proven too)
                "trim" | "trim_start" | "trim_end" | "to_lowercase" | "to_uppercase"
                | "to_string" | "strip_prefix" | "strip_suffix" | "as_str" | "replace"
                    if matches!(rty, Ty::Str) =>
                {
                    Ty::Str
                }
                "iter" | "iter_mut" => rty,
                "any" | "all" => Ty::Bool,
                // Rust's char/str predicates and the membership family
                "is_ascii_alphanumeric" | "is_ascii_digit" | "is_ascii"
                | "is_ascii_whitespace" | "is_alphanumeric" | "is_empty"
                | "contains" | "contains_key" | "starts_with" | "ends_with" => Ty::Bool,
                "unwrap_or" => match m.args.first() {
                    Some(a) => infer_ty(a, cx),
                    None => Ty::Unknown,
                },
                // combinators answer the CLOSURE BODY's shape; the
                // Option-returning ones wrap it
                "map" | "map_or" | "and_then" | "or_else" | "unwrap_or_else" => {
                    let (aget, a) = match (rty.clone(), m.args.last()) {
                        (Ty::Opt(inner), Some(a)) => (*inner, a),
                        (_, Some(a)) => (Ty::Unknown, a),
                        (_, None) => return Ty::Unknown,
                    };
                    let body_ty = match a {
                        syn::Expr::Closure(cl) => closure_ret_ty(cl, aget, cx),
                        other => infer_ty(other, cx),
                    };
                    match m.method.to_string().as_str() {
                        "map" => Ty::Opt(Box::new(body_ty)),
                        "and_then" | "or_else" => match body_ty {
                            Ty::Opt(_) => body_ty,
                            t => Ty::Opt(Box::new(t)),
                        },
                        _ => body_ty,
                    }
                }
                "is_some_and" | "is_ok_and" => Ty::Bool,
                "flatten" => match rty {
                    Ty::Opt(inner) => *inner,
                    _ => Ty::Unknown,
                },
                // Option shape-preserving reads
                "copied" | "cloned" | "as_ref" | "ok" | "clone" => rty,
                "unwrap" | "expect" => match rty {
                    Ty::Opt(t) => *t,
                    _ => Ty::Unknown,
                },
                // `v.first()` / `v.last()` answer Option<&elem> — the
                // element or the absent marker
                "first" | "last" if m.args.is_empty() => match &rty {
                    Ty::Arr(Some(t), _) => Ty::Opt(Box::new((**t).clone())),
                    Ty::List => Ty::Opt(Box::new(Ty::Str)),
                    _ => Ty::Unknown,
                },
                // `s.chars()` / `s.lines()` are string sequences; their
                // `next()` is the Option of one element (a one-char string).
                "chars" | "lines" => Ty::List,
                "next" => Ty::Opt(Box::new(Ty::Str)),
                _ => Ty::Unknown,
            }
        }
        syn::Expr::Tuple(tu) => Ty::Arr(
            Some(Box::new(
                tu.elems.first().map(|e| infer_ty(e, cx)).unwrap_or(Ty::Unknown),
            )),
            Some(tu.elems.len() as u64),
        ),
        // `expr?` propagates the absent case; the VALUE has the inner type.
        syn::Expr::Try(t) => infer_ty(&t.expr, cx),

        // a cast to an integer type is an integer (a float/pointer cast is
        // not something the store types)
        syn::Expr::Cast(c) => match int_ty_bits(&c.ty) {
            Some(_) => Ty::Int,
            None => Ty::Unknown,
        },

        // `v[i]` on a PROVEN array carries the element type; a strSplit
        // list's elements are strings.
        syn::Expr::Index(ix) => match infer_ty(&ix.expr, cx) {
            Ty::Arr(Some(t), _) => *t,
            Ty::List => Ty::Str,
            _ => Ty::Unknown,
        },

        // `.N` on a proven Arr(tuple) reads an element of the elem type

        syn::Expr::Field(f)
            if matches!(&f.member, syn::Member::Unnamed(_))
                && matches!(infer_ty(&f.base, cx), Ty::Arr(..)) =>
        {
            match infer_ty(&f.base, cx) {
                Ty::Arr(Some(t), _) => *t,
                _ => Ty::Unknown,
            }
        }

        syn::Expr::Field(f) => {
            // `base.field` where base is a KNOWN struct: the field's
            // declared type (`prog.var_types` -> Arr)
            let base_ty = infer_ty(&f.base, cx);
            if let Ty::Struct(sname) = &base_ty {
                if let syn::Member::Named(ident) = &f.member {
                    let fields = cx.structs.borrow();
                    if let Some(fs) = fields.get(sname) {
                        if let Some((_, ft)) =
                            fs.iter().find(|(n, _)| n.as_str() == ident.to_string().as_str())
                        {
                            return ft.clone();
                        }
                    }
                }
            }
            Ty::Unknown
        }
        syn::Expr::Path(p) => {
            // a registered-FN item used as a value carries its name — but
            // ONLY when it is not shadowed by a tracked variable
            if p.qself.is_none()
                && !p.path.leading_colon.is_none() == false
                && p.path.segments.iter().all(|s| s.arguments.is_none())
            {
                let last = p.path.segments.last().unwrap().ident.to_string();
                let is_var = single_path(p)
                    .map(|id| {
                        cx.vars.borrow().contains_key(&id.to_string())
                            || cx.statics.borrow().contains_key(&id.to_string())
                    })
                    .unwrap_or(false);
                if !is_var && cx.fns.iter().any(|f| *f == last) {
                    return Ty::Fn(last);
                }
            }
            // `Color::Red` — a unit-variant VALUE has its enum's type
            if p.qself.is_none() && p.path.segments.len() == 2 {
                let e = p.path.segments[0].ident.to_string();
                if cx.enums.iter().any(|x| *x == e) {
                    return Ty::Enum(e);
                }
            }
            // bare `None` — the absent marker's inner type is unknowable
            // from the constructor alone (an `Option<&str>` annotation
            // would pin it). Sound because the option methods collapse to
            // the plain variable read regardless of the inner type.
            if p.qself.is_none()
                && p.path.leading_colon.is_none()
                && p.path.segments.len() == 1
                && p.path.segments[0].arguments.is_none()
                && p.path.segments[0].ident == "None"
            {
                return Ty::Opt(Box::new(Ty::Unknown));
            }
            single_path(p)
                .and_then(|id| {
                    cx.vars.borrow().get(&id.to_string()).cloned().or_else(|| {
                        cx.statics.borrow().get(&id.to_string()).cloned()
                    })
                })
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
                AddAssign(_)
                    | SubAssign(_)
                    | MulAssign(_)
                    | DivAssign(_)
                    | RemAssign(_)
                    | BitOrAssign(_)
                    | BitAndAssign(_)
            ) {
                Some(compound_assign(b, cx, out))
            } else {
                refuse("binary-operator statement (only compound assigns)", b.op.span())
            }
        }
        // `std::process::exit(N)` — end the process with status N. A1's Exit
        // statement IS this (`process.exit(Number(N))`), the same node a
        // top-level `return;` lowers to; the status is the only observable, so
        // there is no value to model. (Value position cannot occur: the call's
        // type is `!`, so it never supplies a `let` initializer.)
        syn::Expr::Call(c) if is_exit_call(c) => {
            let status = c
                .args
                .first()
                .unwrap_or_else(|| refuse("`std::process::exit` without a status", c.span()));
            if c.args.len() > 1 {
                refuse("`std::process::exit` with extra arguments", c.span());
            }
            Some(json!({"type": "Exit", "value": value_expr(status, cx)}))
        }
        syn::Expr::If(ie) => Some(if_stmt(ie, cx, in_fn, out)),
        syn::Expr::While(w) => Some(while_stmt(w, cx, in_fn, out)),
        syn::Expr::ForLoop(fl) => Some(for_stmt(fl, cx, in_fn, out)),
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
            // IMPURE statement-position methods first:
            // `flag.store(v, Ordering::..)` on an AtomicBool static — a
            // plain assignment (single-threaded runtime: exact).
            if m.method == "store" {
                // an atomic BOOL or INT static: single-threaded, so the
                // store IS the assignment (the ordering arg is unused)
                if matches!(
                    infer_ty(&m.receiver, cx),
                    Ty::AtomicBool | Ty::Int
                ) {
                    let Some(syn::Expr::Path(p)) = Some(m.receiver.as_ref()) else {
                        refuse("atomic store target path", m.receiver.span());
                    };
                    let Some(name) = single_path(p) else {
                        refuse("atomic store target path", p.span());
                    };
                    let mut it = m.args.iter();
                    let (Some(val), Some(_ord)) = (it.next(), it.next()) else {
                        refuse("store expects (value, ordering)", m.span());
                    };
                    return Some(assign_stmt(
                        &name.to_string(),
                        value_expr(val, cx),
                    ));
                }
            }
            // `NAME.lock().unwrap().clear();` — an empty assoc store
            if m.method == "clear" && m.args.is_empty() {
                if let Some((sname, inner)) = static_guard(&m.receiver, cx) {
                    if matches!(inner, Ty::Set(..) | Ty::Map(..)) {
                        return Some(assoc_empty_stmt(&sname));
                    }
                    refuse("clear on a non-collection static", m.span());
                }
            }
            // `NAME.with(|c| c.set(v));` — the Cell write
            if m.method == "with" && m.args.len() == 1 {
                if let syn::Expr::Path(p) = &*m.receiver {
                    if let Some(n) = single_path(p).map(|i| i.to_string()) {
                        if let Some(Ty::Cel(inner)) = cx.statics.borrow().get(&n).cloned() {
                            if let Some(v) = cell_with(&n, &inner, &m.args[0], cx) {
                                return Some(expr_stmt(v));
                            }
                        }
                    }
                }
            }
            // IMPURE statement-position mutators. Both are exactly
            // representable because the mutator's effect on the store
            // value is the same operation the A1 builtins perform:
            //   arr.push(v)   -> setArrayAppend(arr, [v])   (native append)
            //   s.push_str(p) -> s = s + p                  (string concat)
            // Both require a PLAIN VARIABLE receiver (the builtin takes
            // the variable NAME); any other receiver refuses, and `push`
            // additionally requires a PROVEN array (pushing onto a string
            // appends a char — a different value).
            let recv_ty = infer_ty(&m.receiver, cx);
            let plain_recv = match m.receiver.as_ref() {
                syn::Expr::Path(p) if p.qself.is_none() => single_path(p),
                _ => None,
            };
            if matches!(m.method.to_string().as_str(), "push" | "push_str") {
                let method = m.method.to_string();
                let Some(id) = plain_recv else {
                    refuse(
                        "push target (a plain variable receiver is required)",
                        m.receiver.span(),
                    );
                };
                let name = id.to_string();
                if method == "push" {
                    // String::push(c) appends exactly ONE char (rustc types
                    // the argument as `char`), so the value-level concat is
                    // the same string — no measurement, no ambiguity.
                    if matches!(recv_ty, Ty::Str) {
                        if m.args.len() != 1 {
                            refuse("push expects exactly one argument", m.span());
                        }
                        let rhs = match &m.args[0] {
                            syn::Expr::Reference(r) => value_expr(&r.expr, cx),
                            other => value_expr(other, cx),
                        };
                        return Some(assign_stmt(&name, str_concat(get_var(&name), rhs)));
                    }
                    if !matches!(recv_ty, Ty::Arr(..)) {
                        refuse(
                            "`push` on an unproven (non-array) receiver",
                            m.method.span(),
                        );
                    }
                    if m.args.len() != 1 {
                        refuse("push expects exactly one argument", m.span());
                    }
                    let v = &m.args[0];
                    let vv = value_expr(v, cx);
                    // A Vec of SCALARS also keeps a parallel ASSOC SET, so
                    // `contains` can answer membership exactly (an ordered
                    // array alone cannot). Only pushes into a Vec that
                    // `contains` may later consult need the shadow.
                    let shadow = matches!(recv_ty, Ty::Arr(Some(ref e), _) if matches!(**e, Ty::Str | Ty::Int));
                    let mut stmts = vec![expr_stmt(json!({
                        "args": [
                            str_lit(&name),
                            json!({"elements": vec![vv.clone()], "type": "Array"}),
                        ],
                        "func": "setArrayAppend", "purity": "Emulable", "type": "Call"
                    }))];
                    if shadow {
                        stmts.push(assoc_set_stmt(
                            &format!("{name}__set"),
                            vv,
                            str_lit("1"),
                        ));
                    }
                    if stmts.len() == 1 {
                        return Some(stmts.pop().unwrap_or(serde_json::Value::Null));
                    }
                    return Some(json!({"type": "Block", "body": stmts}));
                }
                // String::push_str(&p) — Rust appends; the store value is a
                // string, so `s = s + p` is the same value. Rust's
                // push_str takes &str; the borrow erases (a shared borrow
                // cannot mutate through the original).
                if m.args.len() != 1 {
                    refuse("push_str expects exactly one argument", m.span());
                }
                let rhs = match &m.args[0] {
                    syn::Expr::Reference(r) => value_expr(&r.expr, cx),
                    other => value_expr(other, cx),
                };
                return Some(assign_stmt(&name, str_concat(get_var(&name), rhs)));
            }
            // MAP/SET mutators are IMPURE: `insert`/`remove` must be
            // EMITTED (value_expr yields the assocSet/assocDel call), not
            // dropped as a discarded pure read.
            if matches!(recv_ty, Ty::Map(..) | Ty::Set(..))
                && matches!(m.method.to_string().as_str(), "insert" | "remove")
            {
                return Some(expr_stmt(value_expr(e, cx)));
            }
            // everything else in statement position: only PURE methods
            // lower at all (the whitelist in value_expr), and a discarded
            // pure call is a no-op — drop it. Anything impure/unknown
            // already refuses inside value_expr.
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
                refuse(
                    &format!("call to unknown function `{}`", path_text(c.func.as_ref())),
                    c.func.span(),
                );
            };
            // A statement call with `&mut` arguments bound under other names
            // emits its own copy-in/call/copy-out sequence here.
            if call_mut_readback(e, None, cx, out, true).0 {
                return None;
            }
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
    // `*NAME.lock().unwrap() = v` — a write through a guard writes the
    // static (the lock is vacuous single-threaded). Collections have no
    // value form, so only scalar statics take this path.
    if let syn::Expr::Unary(u) = &*a.left {
        if matches!(u.op, syn::UnOp::Deref(_)) {
            if let Some((sname, inner)) = static_guard(&u.expr, cx) {
                if !is_scalar_store_ty(&inner) {
                    refuse(
                        "static collection has no value form (use its operations)",
                        a.left.span(),
                    );
                }
                return assign_stmt(&sname, value_expr(&a.right, cx));
            }
        }
    }
    if let syn::Expr::Index(ix) = &*a.left {
        let Some(syn::Expr::Path(p)) = Some(&*ix.expr) else {
            refuse("element assignment on a non-variable array", ix.expr.span());
        };
        let Some(name) = single_path(p) else {
            refuse("element assignment on a non-variable array", p.span());
        };
        if !matches!(infer_ty(&ix.expr, cx), Ty::Arr(..)) {
            refuse("element assignment on an unproven array", ix.expr.span());
        }
        let Some(key) = index_key_expr(&ix.index) else {
            refuse("array index (only integer literals and variables)", ix.index.span());
        };
        return index_set_stmt(&name.to_string(), key, value_expr(&a.right, cx));
    }
    // `*p = v` — a write through a `&mut` parameter (references erase to the
    // slot, v0.40), so the pointee IS the variable: `p = v`. cuda_backend's
    // `*tmp += 1` / `*qlab += 1` are the shapes that need this.
    if let syn::Expr::Unary(u) = &*a.left {
        if matches!(u.op, syn::UnOp::Deref(_)) {
            if let syn::Expr::Path(p) = &*u.expr {
                if let Some(name) = single_path(p) {
                    let name = name.to_string();
                    let ty = infer_ty(&u.expr, cx);
                    if !is_scalar_store_ty(&ty) {
                        refuse(
                            "write through a `&mut` collection reference (collections live in the store by name, not as values)",
                            a.left.span(),
                        );
                    }
                    return assign_stmt(&name, value_expr(&a.right, cx));
                }
            }
        }
    }
    let syn::Expr::Path(p) = &*a.left else {
        refuse("assignment target (only plain variables are supported)", a.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    // A Vec that carries the `contains` shadow set (`<v>__set`, filled at
    // every push) cannot be REBOUND: the set would keep the old elements
    // and `contains` would silently answer wrong. Refuse rather than let a
    // stale set lie.
    if matches!(infer_ty(&a.left, cx), Ty::Arr(Some(ref e), _) if matches!(**e, Ty::Str | Ty::Int))
    {
        refuse(
            "assignment to a Vec that carries the contains shadow set",
            a.left.span(),
        );
    }
    // `ok = COLL.any(|e| COND)` / `.all(..)`
    if let Some((all, src, cl)) = quantifier_parts(&a.right, cx) {
        let r = quantifier_stmts(all, src, cl, cx, out);
        cx.vars.borrow_mut().insert(name.to_string(), Ty::Bool);
        return store_stmt(&name.to_string(), get_var(&r));
    }
    // `dst = std::env::args().collect()` — the argument vector as a value
    if let Some(m) = as_method_call(&a.right, "collect") {
        if m.args.is_empty() {
            if let syn::Expr::Call(c) = &*m.receiver {
                if is_argv_call(c) {
                    cx.vars
                        .borrow_mut()
                        .insert(name.to_string(), Ty::Arr(Some(Box::new(Ty::Str)), None));
                    let v = value_expr(&m.receiver, cx);
                    return store_stmt(&name.to_string(), v);
                }
            }
        }
    }
    // iterator chains in assignment position
    if as_method_call(&a.right, "collect").is_some() {
        if let Some((src_expr, steps)) = parse_collect_chain(&a.right, cx) {
            let node =
                collect_chain_stmt(&name.to_string(), src_expr, steps, cx, out);
            return match node {
                Some(v) => v,
                None => refuse("iterator chain produced no loop", a.right.span()),
            };
        }
        refuse(
            "iterator chain (only SRC.iter()[.map(closure-or-fn)].collect())",
            a.right.span(),
        );
    }
    // array literal RHS stores via setArray (native, no coercion)
    if let Some(elems) = array_literal_elems(&a.right) {
        let elements = elems.iter().map(|e| value_expr(e, cx)).collect::<Vec<_>>();
        let first_ty = elems
            .first()
            .map(|e| infer_ty(e, cx))
            .unwrap_or(Ty::Unknown);
        cx.vars.borrow_mut().insert(
            name.to_string(),
            Ty::Arr(Some(Box::new(first_ty)), Some(elems.len() as u64)),
        );
        return set_array_stmt(&name.to_string(), elements);
    }
    // `x = if c {A} else {B}` — bind x in every branch
    if let syn::Expr::If(ie) = &*a.right {
        return if_value_node(ie, BranchTarget::Var(&name.to_string()), cx, false, out);
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

fn compound_assign(b: &syn::ExprBinary, cx: &Cx, out: &mut Vec<Value>) -> Value {
    // `*NAME.lock().unwrap() += e` — the arithmetic on the static's slot
    if let syn::Expr::Unary(u) = &*b.left {
        if matches!(u.op, syn::UnOp::Deref(_)) {
            if let Some((sname, inner)) = static_guard(&u.expr, cx) {
                if !is_scalar_store_ty(&inner) {
                    refuse(
                        "static collection has no value form (use its operations)",
                        b.left.span(),
                    );
                }
                use syn::BinOp::*;
                let op = match b.op {
                    AddAssign(_) => "+",
                    SubAssign(_) => "-",
                    MulAssign(_) => "*",
                    DivAssign(_) => "/",
                    RemAssign(_) => "%",
                    _ => refuse(
                        "compound assignment operator on a static guard",
                        b.op.span(),
                    ),
                };
                let nv = arith_bin(op, arith_var(&sname), arith_expr(&b.right, cx));
                return assign_stmt(&sname, nv);
            }
        }
    }
    // `*p += e` — compound write through a `&mut` parameter: the pointee is
    // the slot, so `p += e` (v0.40).
    if let syn::Expr::Unary(u) = &*b.left {
        if matches!(u.op, syn::UnOp::Deref(_)) {
            if let syn::Expr::Path(p) = &*u.expr {
                if let Some(name) = single_path(p) {
                    let name = name.to_string();
                    let ty = infer_ty(&u.expr, cx);
                    if !is_scalar_store_ty(&ty) {
                        refuse(
                            "write through a `&mut` collection reference (collections live in the store by name, not as values)",
                            b.left.span(),
                        );
                    }
                    use syn::BinOp::*;
                    let op = match b.op {
                        AddAssign(_) => "+",
                        SubAssign(_) => "-",
                        MulAssign(_) => "*",
                        DivAssign(_) => "/",
                        RemAssign(_) => "%",
                        _ => refuse(
                            "compound assignment operator through a `&mut` reference",
                            b.op.span(),
                        ),
                    };
                    let nv = arith_bin(op, arith_var(&name), arith_expr(&b.right, cx));
                    return assign_stmt(&name, nv);
                }
            }
        }
    }
    if let syn::Expr::Index(ix) = &*b.left {
        use syn::BinOp::*;
        let op = match b.op {
            AddAssign(_) => "+",
            SubAssign(_) => "-",
            MulAssign(_) => "*",
            DivAssign(_) => "/",
            RemAssign(_) => "%",
            _ => refuse("compound assignment operator", b.op.span()),
        };
        let Some(syn::Expr::Path(p)) = Some(&*ix.expr) else {
            refuse("element assignment on a non-variable array", ix.expr.span());
        };
        let Some(aname) = single_path(p) else {
            refuse("element assignment on a non-variable array", p.span());
        };
        if !matches!(infer_ty(&ix.expr, cx), Ty::Arr(..)) {
            refuse("element assignment on an unproven array", ix.expr.span());
        }
        let Some(key) = index_key_expr(&ix.index) else {
            refuse("array index (only integer literals and variables)", ix.index.span());
        };
        let cur = json!({
            "type": "Index", "var": aname.to_string(), "key": arith_expr(&ix.index, cx)
        });
        return index_set_stmt(
            &aname.to_string(),
            key,
            arith_bin(op, cur, arith_expr(&b.right, cx)),
        );
    }
    let syn::Expr::Path(p) = &*b.left else {
        refuse("assignment target (only plain variables are supported)", b.left.span());
    };
    let Some(name) = single_path(p) else {
        refuse("assignment target path", p.span());
    };
    let name = name.to_string();
    use syn::BinOp::*;
    // `|=` / `&=` on a PROVEN boolean are Rust's logical OR / AND
    // (`BitOr`/`BitAnd` on bool). They are NOT short-circuiting, so the
    // RHS always evaluates exactly once — hoisting it into a temp is the
    // exact evaluation order. A bitwise lowering would be silently wrong:
    // the store holds the strings "true"/"false", which `|` coerces to
    // NaN → 0. Any non-bool receiver refuses (REFUSE > GUESS).
    if matches!(b.op, BitOrAssign(_) | BitAndAssign(_)) {
        let lhs_ty = cx
            .vars
            .borrow()
            .get(&name)
            .cloned()
            .or_else(|| cx.statics.borrow().get(&name).cloned())
            .unwrap_or(Ty::Unknown);
        if !matches!(lhs_ty, Ty::Bool | Ty::AtomicBool) {
            refuse(
                "bitwise compound assignment on a non-boolean receiver",
                b.op.span(),
            );
        }
        let mut ctr = cx.tmp.borrow_mut();
        *ctr += 1;
        let temp = format!("__sh2b{}", ctr);
        drop(ctr);
        out.push(assign_stmt(&temp, value_expr(&b.right, cx)));
        cx.vars.borrow_mut().insert(temp.clone(), Ty::Bool);
        let op = if matches!(b.op, BitOrAssign(_)) { "-o" } else { "-a" };
        let cond = format!("${name} = \"true\" {op} ${temp} = \"true\"");
        return assign_stmt(&name, test_call(&cond));
    }
    let op = match b.op {
        AddAssign(_) => "+",
        SubAssign(_) => "-",
        MulAssign(_) => "*",
        DivAssign(_) => "/",
        RemAssign(_) => "%",
        _ => refuse("compound assignment operator", b.op.span()),
    };
    // `i += 1` lowers to `i = i + 1` — the c-sh-go compound shape. A call
    // (or field read) on the right is hoisted first, so it runs exactly once,
    // before the arithmetic — Rust evaluates the RHS of `+=` exactly once.
    // (`+=` IS arithmetic, so its RHS is an arithmetic spine by definition:
    // a bare call RHS must be hoisted too, exactly once, before the add)
    if arith_contains_field(&b.right) || arith_operand_needs_hoist(&b.right) {
        let mut ctr = cx.tmp.borrow_mut();
        let ast = hoisted_arith(&b.right, cx, out, &mut ctr);
        drop(ctr);
        return assign_stmt(&name, arith_bin(op, arith_var(&name), ast));
    }
    assign_stmt(&name, arith_bin(op, arith_var(&name), arith_expr(&b.right, cx)))
}

/// The `if let` pattern conversion: bind the payload (writing any binding
/// into `out`, so it is visible to both branches) and return the A1
/// condition node. Shared by the statement form (`if_stmt`) and the
/// value/tail form (`if_value_node`) so the two can never disagree.
///
/// Under the None≡"" convention (the same one `?`, Ty::Opt and the option
/// methods use) Some(v) IS v and None IS "". Only `Some(NAME)`, `None`
/// and `_` are representable; anything else refuses — a payload the
/// convention cannot distinguish from None (`Some("")`) would be a GUESS,
/// and refusing is the standing rule.
///
/// The condition QUOTES the expansion (`"$v" = ""`): an unquoted empty
/// expansion vanishes under word-splitting, so `[ $v = "" ]` would parse
/// as `[ = "" ]` and never fire. bash emits the quoted form for exactly
/// this reason.
fn iflet_cond(tmp: &str, pat: &syn::Pat, cx: &Cx, out: &mut Vec<Value>) -> Value {
    let mut pre = Vec::new();
    let mut binds = Vec::new();
    let cond = destructure(tmp, pat, cx, &mut pre, &mut binds);
    out.extend(pre);
    out.extend(binds);
    test_call(&cond.unwrap_or_else(|| "1 -eq 1".to_string()))
}

fn if_stmt(ie: &syn::ExprIf, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Value {
    // `if let PAT = EXPR { .. } [else { .. }]` — the Option layer's None≡""
    // convention. The scrutinee hoists to a temp so it evaluates ONCE
    // (Rust evaluates an if condition once too), and the binding hoists
    // into `out` so it is visible to both branches.
    if let syn::Expr::Let(l) = &*ie.cond {
        let n = {
            let mut t = cx.tmp.borrow_mut();
            *t += 1;
            *t
        };
        let tmp = format!("__sh2if{n}");
        out.push(store_stmt(&tmp, value_expr(&l.expr, cx)));
        let cond = iflet_cond(&tmp, &l.pat, cx, out);
        let mut then = Vec::new();
        lower_block(&ie.then_branch, cx, in_fn, &mut then);
        let else_stmts: Vec<Value> = match &ie.else_branch {
            None => vec![],
            Some((_, inner)) => match &**inner {
                syn::Expr::If(inner_if) => vec![if_stmt(inner_if, cx, in_fn, out)],
                syn::Expr::Block(blk) => {
                    let mut v = Vec::new();
                    lower_block(&blk.block, cx, in_fn, &mut v);
                    v
                }
                other => refuse("else form", other.span()),
            },
        };
        return json!({"cond": cond, "then": then, "elsifs": [], "else": else_stmts, "type": "If"});
    }
    // method-call leaves of the condition hoist to temps (If conditions
    // evaluate once — exact); the rest is test-grammar text
    let cond_expr = hoist_cond_calls(&ie.cond, cx, out);
    let cond = test_call(&test_string(&cond_expr, cx));
    let mut then = Vec::new();
    lower_block(&ie.then_branch, cx, in_fn, &mut then);
    let else_stmts: Vec<Value> = match &ie.else_branch {
        None => vec![],
        Some((_, inner)) => match &**inner {
            // `else if` lowers to a NESTED If in the else position — the
            // exact shape the shell frontend emits for elif chains (the
            // `elsifs` field stays empty).
            syn::Expr::If(inner_if) => {
                vec![if_stmt(inner_if, cx, in_fn, out)]
            }
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

fn while_stmt(w: &syn::ExprWhile, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Value {
    if w.label.is_some() {
        refuse("labeled while", w.span());
    }
    // the condition's hoists are STATEMENTS that must run before the loop
    let cond_expr = hoist_cond_calls(&w.cond, cx, out);
    let cond = test_call(&test_string(&cond_expr, cx));
    let mut body = Vec::new();
    lower_block(&w.body, cx, in_fn, &mut body);
    json!({"cond": cond, "body": body, "type": "While"})
}

fn for_stmt(fl: &syn::ExprForLoop, cx: &Cx, in_fn: bool, out: &mut Vec<Value>) -> Value {
    if fl.label.is_some() {
        refuse("labeled for", fl.span());
    }
    // STRING iteration: `for c in s.chars()` / `for line in s.lines()`.
    // The runtime already carries the two ops with Rust's exact semantics:
    // strChars yields Unicode SCALAR VALUES (a non-BMP char is ONE element,
    // where split("") would give two UTF-16 halves) and strLines is Rust's
    // `lines()` (\r\n stripped, a final newline adds no empty line). Both
    // return a native array, adopted through setArray's Array.isArray
    // splice (setVar would nest it as ONE element). The iterator borrows the
    // source immutably, so the temp snapshot cannot change under the loop:
    // reading the length once is exact.
    if let syn::Expr::MethodCall(m) = &*fl.expr {
        let fname = match m.method.to_string().as_str() {
            "chars" => Some("strChars"),
            "lines" => Some("strLines"),
            _ => None,
        };
        if let Some(fname) = fname {
            if m.args.is_empty() && matches!(infer_ty(&m.receiver, cx), Ty::Str) {
                let tmp = fresh_tmp(cx, "s");
                out.push(expr_stmt(json!({
                    "type": "Call", "func": "setArray", "purity": "Emulable",
                    "args": [
                        str_lit(&tmp),
                        json!({"type": "Array", "elements": [json!({
                            "type": "Call", "func": fname, "purity": "PureCpu",
                            "args": [value_expr(&m.receiver, cx)],
                        })]}),
                    ],
                })));
                cx.vars
                    .borrow_mut()
                    .insert(tmp.clone(), Ty::Arr(Some(Box::new(Ty::Str)), None));
                let mut fl2 = fl.clone();
                fl2.label = None;
                fl2.expr = Box::new(
                    syn::parse_str::<syn::Expr>(&tmp)
                        .unwrap_or_else(|_| refuse("temporary name", fl.span())),
                );
                return for_stmt(&fl2, cx, in_fn, out);
            }
        }
    }
    // ARRAY iteration: `for PAT in v` / `&v` / `v.iter()` — an index
    // while-loop over the trusted runtime length. Rust's borrow checker
    // forbids mutating the array during iteration, so one length read is
    // exact. Elements read RAW (`arrayItems(v).at(i)`) so tuple/record
    // elements survive destructuring.
    // MAP / SET iteration: keys come from assocKeys (a native array),
    // values from assocGet per key.
    //
    // ORDER is part of the program's OUTPUT for a `BTreeMap`: rustc
    // enumerates it in KEY order (Ord on the key type), while the store's
    // assoc keeps insertion order. A BTreeMap receiver therefore SORTS the
    // key array before iterating — `assocKeys(m).sort()`, the same
    // lexicographic array sort the shell path emits for `$(… | sort)`.
    // `HashMap`/`HashSet` promise no order at all, so their insertion
    // order is a valid enumeration and nothing about them changes.
    //
    // Exactness domain of `.sort()`: JS compares UTF-16 code units and
    // Rust compares UTF-8 BYTES, which agree for every key whose
    // characters are all in U+0000..U+FFFF (all ASCII, Latin-1, CJK, …);
    // they can differ only when one key's first difference involves an
    // astral (supplementary) character against one in U+E000..U+FFFF.
    // Measured on `t124_btreemap_order` (ASCII + Latin-1 + CJK keys).
    if let Some((vname, mode, order)) = map_iter_mode(&fl.expr, cx) {
        let ks = fresh_tmp(cx, "mk");
        let keys = assoc_call("assocKeys", vec![str_lit(&vname)]);
        let keys = if order == MapOrder::BTree {
            json!({
                "type": "MethodCall",
                "object": keys,
                "method": "sort",
                "args": []
            })
        } else {
            keys
        };
        out.push(store_stmt(&ks, keys));
        let idx = fresh_tmp(cx, "i");
        let lenv = fresh_tmp(cx, "l");
        out.push(assign_stmt(&lenv, array_len_call(&ks)));
        cx.vars.borrow_mut().insert(lenv.clone(), Ty::Int);
        cx.vars.borrow_mut().insert(idx.clone(), Ty::Int);
        let elem = raw_elem(&ks, json!({"type": "Var", "name": idx, "sigil": null}));
        let mut body_all = Vec::new();
        match (mode, fl.pat.as_ref()) {
            ("keys" | "set", syn::Pat::Ident(pi)) if pi.subpat.is_none() => {
                let name = pi.ident.to_string();
                cx.vars.borrow_mut().insert(name.clone(), Ty::Str);
                body_all.push(assign_stmt(&name, elem));
            }
            ("values", syn::Pat::Ident(pi)) if pi.subpat.is_none() => {
                let name = pi.ident.to_string();
                let k = fresh_tmp(cx, "k");
                let vt = map_value_ty(&vname, cx);
                cx.vars.borrow_mut().insert(k.clone(), Ty::Str);
                cx.vars.borrow_mut().insert(name.clone(), vt.clone());
                body_all.push(assign_stmt(&k, elem));
                let read = assoc_call("assocGet", vec![str_lit(&vname), get_var(&k)]);
                if is_composite_ty(&vt) {
                    body_all.push(store_stmt(&name, read));
                } else {
                    body_all.push(assign_stmt(&name, read));
                }
            }
            ("entries", syn::Pat::Tuple(t)) if t.elems.len() == 2 => {
                let syn::Pat::Ident(ki) = &t.elems[0] else {
                    refuse("map iteration key pattern", t.elems[0].span());
                };
                if ki.subpat.is_some() {
                    refuse("map iteration key subpattern", ki.span());
                }
                let kname = ki.ident.to_string();
                cx.vars.borrow_mut().insert(kname.clone(), Ty::Str);
                body_all.push(assign_stmt(&kname, elem));
                match &t.elems[1] {
                    syn::Pat::Ident(vi) if vi.subpat.is_none() => {
                        let vname2 = vi.ident.to_string();
                        let vt = map_value_ty(&vname, cx);
                        cx.vars.borrow_mut().insert(vname2.clone(), vt.clone());
                        let read = assoc_call(
                            "assocGet",
                            vec![str_lit(&vname), get_var(&kname)],
                        );
                        if is_composite_ty(&vt) {
                            body_all.push(store_stmt(&vname2, read));
                        } else {
                            body_all.push(assign_stmt(&vname2, read));
                        }
                    }
                    syn::Pat::Wild(_) => {}
                    other => refuse("map iteration value pattern", other.span()),
                }
            }
            (_, pat) => refuse("map/set for-loop pattern", pat.span()),
        }
        lower_block(&fl.body, cx, in_fn, &mut body_all);
        body_all.push(assign_stmt(
            &idx,
            arith_bin("+", arith_var(&idx), arith_num(1)),
        ));
        return json!({
            "type": "While",
            "cond": test_call(&format!("${idx} -lt ${lenv}")),
            "body": body_all,
        });
    }
    // `.iter().enumerate()`: the loop index IS the position (the walk visits
    // elements in order), so `for (i, x) in v.iter().enumerate()` binds i to
    // the counter and x to the element.
    let mut enumerate_iter = false;
    let mut iter_root: &syn::Expr = &fl.expr;
    if let syn::Expr::MethodCall(m) = iter_root {
        if m.method == "enumerate" && m.args.is_empty() {
            if let syn::Expr::MethodCall(m2) = &*m.receiver {
                if (m2.method == "iter" || m2.method == "iter_mut") && m2.args.is_empty() {
                    enumerate_iter = true;
                    iter_root = &m2.receiver;
                }
            }
        }
    }
    let iter_arr: Option<&syn::Expr> = match iter_root {
        syn::Expr::MethodCall(m)
            if (m.method == "iter" || m.method == "iter_mut") && m.args.is_empty() =>
        {
            Some(&m.receiver)
        }
        e if matches!(infer_ty(e, cx), Ty::Arr(..)) => Some(e),
        _ => None,
    };
    if let Some(arr_expr) = iter_arr {
        // `for x in &v` / `&mut v`: the borrow has no store form; the
        // iteration reads the same array (Rust forbids mutating it here).
        let arr_expr = match arr_expr {
            syn::Expr::Reference(r) => &*r.expr,
            e => e,
        };
        let (vname, elem_ty, known_len) = match arr_expr {
            syn::Expr::Path(p) => {
                let Some(vname) = single_path(p) else {
                    refuse("array iteration target path", p.span());
                };
                let (t, l) = match infer_ty(arr_expr, cx) {
                    Ty::Arr(Some(t), l) => (*t.clone(), l),
                    Ty::Arr(_, l) => (Ty::Unknown, l),
                    _ => (Ty::Unknown, None),
                };
                (vname.to_string(), t, l)
            }
            // A FIELD array (`for x in &self.items`) is a record member,
            // not a store array: copy it ONCE through setVar (which
            // registers native arrays) and iterate the temp.
            other if matches!(infer_ty(other, cx), Ty::Arr(..)) => {
                let t = fresh_tmp(cx, "a");
                out.push(store_stmt(&t, value_expr(other, cx)));
                let (et, l) = match infer_ty(other, cx) {
                    Ty::Arr(Some(et), l) => (*et.clone(), l),
                    Ty::Arr(_, l) => (Ty::Unknown, l),
                    _ => (Ty::Unknown, None),
                };
                (t, et, l)
            }
            other => refuse("array iteration target path", other.span()),
        };
        let idx = fresh_tmp(cx, "i");
        let lenv = fresh_tmp(cx, "l");
        // The bound is ALWAYS a variable — the while-condition grammar
        // reads bare numbers as positionals ($3!).
        match known_len {
            Some(n) => out.push(assign_stmt(&lenv, str_lit(&n.to_string()))),
            None => out.push(assign_stmt(
                &lenv,
                json!({
                    "args": [str_lit(&vname)],
                    "func": "arrayLen", "purity": "PureCpu", "type": "Call"
                }),
            )),
        }
        cx.vars.borrow_mut().insert(lenv.clone(), Ty::Int);
        cx.vars.borrow_mut().insert(idx.clone(), Ty::Int);
        let elem = raw_elem(
            &vname,
            json!({"type": "Var", "name": idx, "sigil": null}),
        );
        let mut body_all = Vec::new();
        match fl.pat.as_ref() {
            syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                let name = pi.ident.to_string();
                cx.vars.borrow_mut().insert(name.clone(), elem_ty.clone());
                // SCALARS bind natively (arith/other native reads see the
                // value); composite elements must go through setVar so a
                // tuple/record survives raw.
                if matches!(
                    elem_ty,
                    Ty::Struct(_) | Ty::Tuple(_) | Ty::Arr(..) | Ty::Enum(_)
                ) {
                    body_all.push(store_stmt(&name, elem));
                } else {
                    body_all.push(assign_stmt(&name, elem));
                }
            }
            // `for (i, x) in v.iter().enumerate()` — the pair is never
            // materialized: i is the loop counter, x the element.
            syn::Pat::Tuple(t) if enumerate_iter && t.elems.len() == 2 => {
                let syn::Pat::Ident(ii) = &t.elems[0] else {
                    refuse("enumerate loop index pattern", t.elems[0].span());
                };
                if ii.subpat.is_some() {
                    refuse("enumerate loop index subpattern", ii.span());
                }
                let iname = ii.ident.to_string();
                cx.vars.borrow_mut().insert(iname.clone(), Ty::Int);
                body_all.push(assign_stmt(&iname, get_var(&idx)));
                match &t.elems[1] {
                    syn::Pat::Ident(vi) if vi.subpat.is_none() => {
                        let vname2 = vi.ident.to_string();
                        cx.vars.borrow_mut().insert(vname2.clone(), elem_ty.clone());
                        if matches!(
                            elem_ty,
                            Ty::Struct(_) | Ty::Tuple(_) | Ty::Arr(..) | Ty::Enum(_)
                        ) {
                            body_all.push(store_stmt(&vname2, elem));
                        } else {
                            body_all.push(assign_stmt(&vname2, elem));
                        }
                    }
                    syn::Pat::Wild(_) => {}
                    other => refuse("enumerate loop element pattern", other.span()),
                }
            }
            pat => {
                let elt = fresh_tmp(cx, "e");
                body_all.push(store_stmt(&elt, elem));
                let mut pre = Vec::new();
                let mut binds = Vec::new();
                if destructure(&elt, pat, cx, &mut pre, &mut binds).is_some() {
                    refuse("refutable for-loop pattern", pat.span());
                }
                body_all.extend(pre);
                body_all.extend(binds);
            }
        }
        lower_block(&fl.body, cx, in_fn, &mut body_all);
        body_all.push(assign_stmt(
            &idx,
            arith_bin("+", arith_var(&idx), arith_num(1)),
        ));
        return json!({
            "type": "While",
            "cond": test_call(&format!("${idx} -lt ${lenv}")),
            "body": body_all,
        });
    }
    // LIST iteration: `for x in s.split(',')` (strSplit) and any other
    // list-valued expression. listLen is the runtime-trusted length;
    // listGet returns RAW elements (tuples/records survive).
    if matches!(infer_ty(&fl.expr, cx), Ty::List) {
        let src = match &*fl.expr {
            syn::Expr::Reference(r) => &*r.expr,
            e => e,
        };
        let id = match src {
            syn::Expr::Path(p) if matches!(infer_ty(src, cx), Ty::List) => {
                let Some(n) = single_path(p) else {
                    refuse("list iteration target path", p.span());
                };
                n.to_string()
            }
            other => {
                let t = fresh_tmp(cx, "q");
                out.push(store_stmt(&t, value_expr(other, cx)));
                t
            }
        };
        let idx = fresh_tmp(cx, "i");
        let lenv = fresh_tmp(cx, "l");
        out.push(assign_stmt(
            &lenv,
            json!({
                "args": [get_var(&id)],
                "func": "listLen", "purity": "PureCpu", "type": "Call"
            }),
        ));
        cx.vars.borrow_mut().insert(lenv.clone(), Ty::Int);
        cx.vars.borrow_mut().insert(idx.clone(), Ty::Int);
        let elem = json!({
            "args": [get_var(&id), json!({"type": "Var", "name": idx, "sigil": null})],
            "func": "listGet", "purity": "PureCpu", "type": "Call"
        });
        let mut body_all = Vec::new();
        match fl.pat.as_ref() {
            syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                let name = pi.ident.to_string();
                cx.vars.borrow_mut().insert(name.clone(), Ty::Str);
                body_all.push(assign_stmt(&name, elem));
            }
            pat => {
                let elt = fresh_tmp(cx, "e");
                body_all.push(store_stmt(&elt, elem));
                let mut pre = Vec::new();
                let mut binds = Vec::new();
                if destructure(&elt, pat, cx, &mut pre, &mut binds).is_some() {
                    refuse("refutable for-loop pattern", pat.span());
                }
                body_all.extend(pre);
                body_all.extend(binds);
            }
        }
        lower_block(&fl.body, cx, in_fn, &mut body_all);
        body_all.push(assign_stmt(
            &idx,
            arith_bin("+", arith_var(&idx), arith_num(1)),
        ));
        return json!({
            "type": "While",
            "cond": test_call(&format!("${idx} -lt ${lenv}")),
            "body": body_all,
        });
    }
    // RANGES need a plain binding (a range element has no structure).
    // `for _ in 0..n` is the SAME loop with nobody reading the counter, so it
    // binds an unread temp: writing it is unobservable, the iteration count
    // and order are identical.
    let name = match fl.pat.as_ref() {
        syn::Pat::Ident(pi) => pi.ident.to_string(),
        syn::Pat::Wild(_) => fresh_tmp(cx, "fw"),
        _ => refuse("for-loop pattern (destructuring not supported)", fl.pat.span()),
    };
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
        other => refuse(
            "for-loop iterator (ranges and Vec/array iteration only)",
            other.span(),
        ),
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
/// Every CALL target name an item list mentions: the last path segment
/// (unqualified free fn) and, for `mod::fn`, the mangled
/// `mod_fn` spelling. Method calls are not resolvable by name alone.
fn collect_calls_in_items(items: &[syn::Item], out: &mut HashSet<String>) {
    use syn::visit::Visit;
    struct C<'a>(&'a mut HashSet<String>);
    impl<'ast> Visit<'ast> for C<'_> {
        fn visit_expr_call(&mut self, c: &'ast syn::ExprCall) {
            if let syn::Expr::Path(p) = &*c.func {
                let segs: Vec<String> = p
                    .path
                    .segments
                    .iter()
                    .map(|s| s.ident.to_string())
                    .collect();
                // std/core/alloc paths are library calls, never crate
                // functions; treating their last segment as a name could
                // splice an unrelated same-named item.
                if matches!(segs.first().map(|s| s.as_str()), Some("std" | "core" | "alloc"))
                {
                    syn::visit::visit_expr_call(self, c);
                    return;
                }
                if let Some(last) = segs.last() {
                    self.0.insert(last.clone());
                    if segs.len() >= 2 {
                        self.0
                            .insert(format!("{}_{}", segs[segs.len() - 2], last));
                    }
                }
            }
            syn::visit::visit_expr_call(self, c);
        }
    }
    let mut c = C(out);
    for it in items {
        // calls inside a `#[cfg(test)]` subtree are not part of a normal
        // build — they must not pull anything into the demand set
        if cfg_test_only(item_attrs(it)) {
            continue;
        }
        c.visit_item(it);
    }
}

/// Free fn names and mangled impl-method names defined by an item list.
fn defined_names(items: &[syn::Item]) -> HashSet<String> {
    fn walk(items: &[syn::Item], out: &mut HashSet<String>) {
        for it in items {
            // a test-only definition is not callable from a normal build
            if cfg_test_only(item_attrs(it)) {
                continue;
            }
            match it {
                syn::Item::Fn(f) => {
                    out.insert(f.sig.ident.to_string());
                }
                syn::Item::Impl(i) => {
                    if let Some(t) = type_ident(&i.self_ty) {
                        for ii in &i.items {
                            if let syn::ImplItem::Fn(m) = ii {
                                out.insert(format!("{}_{}", t, m.sig.ident));
                            }
                        }
                    }
                }
                syn::Item::Mod(m) => {
                    if let Some((_, inner)) = &m.content {
                        walk(inner, out);
                    }
                }
                _ => {}
            }
        }
    }
    let mut out = HashSet::new();
    walk(items, &mut out);
    out
}

/// Files named by `mod x;` declarations reachable from `items` (not the
/// whole crate: the demand pass must stay proportional to the entry).
fn collect_mod_files(items: &[syn::Item], base: &Path, out: &mut Vec<PathBuf>) {
    for it in items {
        let syn::Item::Mod(m) = it else {
            continue;
        };
        if let Some((_, inner)) = &m.content {
            collect_mod_files(inner, base, out);
            continue;
        }
        // `#[path = "..."]` wins over the name-derived candidates.
        let path_attr = m.attrs.iter().find_map(|a| {
            if !a.path().is_ident("path") {
                return None;
            }
            let syn::Meta::NameValue(nv) = &a.meta else {
                return None;
            };
            let syn::Expr::Lit(l) = &nv.value else {
                return None;
            };
            let syn::Lit::Str(s) = &l.lit else {
                return None;
            };
            Some(base.join(s.value()))
        });
        let cands = [
            path_attr.unwrap_or_else(|| base.join(format!("{}.rs", m.ident))),
            base.join(m.ident.to_string()).join("mod.rs"),
        ];
        let Some(path) = cands.iter().find(|p| p.exists()) else {
            continue;
        };
        if out.contains(path) {
            continue;
        }
        out.push(path.clone());
        if let Ok(text) = std::fs::read_to_string(path) {
            if let Ok(f) = syn::parse_file(&text) {
                collect_mod_files(&f.items, path.parent().unwrap_or(base), out);
            }
        }
    }
}

/// The attribute list of any item kind (empty for kinds that carry none).
fn item_attrs(item: &syn::Item) -> &[syn::Attribute] {
    match item {
        syn::Item::Const(i) => &i.attrs,
        syn::Item::Enum(i) => &i.attrs,
        syn::Item::Fn(i) => &i.attrs,
        syn::Item::Impl(i) => &i.attrs,
        syn::Item::Mod(i) => &i.attrs,
        syn::Item::Static(i) => &i.attrs,
        syn::Item::Struct(i) => &i.attrs,
        syn::Item::Trait(i) => &i.attrs,
        syn::Item::Type(i) => &i.attrs,
        syn::Item::Use(i) => &i.attrs,
        _ => &[],
    }
}

fn flatten_items<'a>(items: &'a [syn::Item], out: &mut Vec<&'a syn::Item>) {
    for item in items {
        // `#[cfg(test)]`-gated items exist only under `cargo test`; a
        // normal build drops them exactly as rustc does — including the
        // whole inline `mod` subtree (its children must not re-enter the
        // flat list as standalone items).
        if cfg_test_only(item_attrs(item)) {
            continue;
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
///
/// LIFETIME-ONLY arguments erase (`TestParser<'a, 'r>`, `PyChild<'_>`,
/// `Walker<'a>`): lifetimes have no runtime representation, exactly like
/// the v0.14 function-level lifetime erasure. Type/const generics still
/// refuse — they can change a method's runtime shape.
fn type_ident(ty: &syn::Type) -> Option<String> {
    if let syn::Type::Path(tp) = ty {
        if tp.qself.is_none() && tp.path.leading_colon.is_none() {
            if let Some(seg) = tp.path.segments.last() {
                let lifetimes_only = match &seg.arguments {
                    syn::PathArguments::None => true,
                    syn::PathArguments::AngleBracketed(a) => a
                        .args
                        .iter()
                        .all(|arg| matches!(arg, syn::GenericArgument::Lifetime(_))),
                    syn::PathArguments::Parenthesized(_) => false,
                };
                if lifetimes_only {
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
    let mut saw_test = false;
    for a in attrs {
        if a.path().is_ident("cfg")
            && a.meta
                .require_list()
                .map(|l| l.tokens.to_string())
                .map(|t| t.trim() == "test")
                .unwrap_or(false)
        {
            saw_test = true;
            continue;
        }
        // Inert metadata carries no configuration: a doc comment (`///`
        // arrives here as `#[doc = "..."]`) or `#[test]` beside the gate
        // does not change WHICH build the item belongs to.
        let name = a
            .path()
            .segments
            .last()
            .map(|s| s.ident.to_string())
            .unwrap_or_default();
        if matches!(
            name.as_str(),
            "test" | "macro_export" | "allow" | "inline" | "deprecated" | "doc"
        ) {
            continue;
        }
        return false;
    }
    saw_test
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
/// A plain shell/store identifier: getVar parses `#name` as a length and
/// `name[...]` as a subscript, so anything else is not a readable name.
fn valid_env_name(n: &str) -> bool {
    let mut cs = n.chars();
    match cs.next() {
        Some(c) if c.is_ascii_alphabetic() || c == '_' => {}
        _ => return false,
    }
    cs.all(|c| c.is_ascii_alphanumeric() || c == '_')
}

/// True for `std::env::var` / `std::env::var_os` (also `env::var` after
/// `use std::env;`). Only the two-argument-free, literal-name reads are
/// accepted at the call site.
/// `env::args()` / `std::env::args()` — the process argument vector. The
/// A1 `Argv` node names it as one array value (argv[0] = program name as
/// invoked, then the arguments in OS order).
fn is_argv_call(c: &syn::ExprCall) -> bool {
    if !c.args.is_empty() {
        return false;
    }
    let syn::Expr::Path(p) = &*c.func else {
        return false;
    };
    if p.qself.is_some() || p.path.leading_colon.is_some() {
        return false;
    }
    let segs: Vec<String> = p.path.segments.iter().map(|s| s.ident.to_string()).collect();
    matches!(segs.join("::").as_str(), "env::args" | "std::env::args")
}

/// `std::process::exit(..)` / `process::exit(..)` — the terminating call whose
/// only observable is the process status.
fn is_exit_call(c: &syn::ExprCall) -> bool {
    let syn::Expr::Path(p) = &*c.func else {
        return false;
    };
    if p.qself.is_some() || p.path.leading_colon.is_some() {
        return false;
    }
    let segs: Vec<String> = p.path.segments.iter().map(|s| s.ident.to_string()).collect();
    matches!(segs.join("::").as_str(), "process::exit" | "std::process::exit")
}

fn is_env_var_path(p: &syn::ExprPath) -> bool {
    if p.qself.is_some() || p.path.leading_colon.is_some() {
        return false;
    }
    let segs: Vec<&syn::PathSegment> = p.path.segments.iter().collect();
    if segs.iter().any(|s| !s.arguments.is_none()) {
        return false;
    }
    let Some(last) = segs.last() else {
        return false;
    };
    if last.ident != "var" && last.ident != "var_os" {
        return false;
    }
    if segs.len() == 2 {
        return segs[0].ident == "env";
    }
    segs.len() >= 3
        && segs[segs.len() - 2].ident == "env"
        && segs[segs.len() - 3].ident == "std"
}

fn env_var_lit<'a>(
    args: &'a syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
) -> Option<&'a syn::LitStr> {
    if args.len() != 1 {
        return None;
    }
    match args.first()? {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Str(s) => Some(s),
            _ => None,
        },
        _ => None,
    }
}

/// `std::env::var("NAME")` -> `getVar("NAME")`.
///
/// The runtime's getVar falls back to `process.env[name]` for names the
/// program never assigned, so the value is the environment's. var/var_os
/// differ only for non-UTF-8 values, which the string store cannot hold;
/// Option vs Result is collapsed by the store's None≡"" convention. The
/// two exactness preconditions are enforced here: a PLAIN identifier name
/// (getVar interprets `#`/`[...]`), and no same-named program binding
/// (getVar would read the store first).
fn env_var_value(
    p: &syn::ExprPath,
    args: &syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
    cx: &Cx,
) -> Value {
    if args.len() != 1 {
        refuse("env var read expects exactly one name", p.span());
    }
    let Some(syn::Expr::Lit(l)) = args.first() else {
        refuse("env var name must be a string literal", args.span());
    };
    let syn::Lit::Str(ls) = &l.lit else {
        refuse("env var name must be a string literal", l.span());
    };
    let name = ls.value();
    if !valid_env_name(&name) {
        refuse("env var name is not a plain shell identifier", ls.span());
    }
    if cx.bindings.contains(&name) {
        refuse(
            &format!("env var `{name}` is shadowed by a program binding"),
            ls.span(),
        );
    }
    get_var(&name)
}

/// `Some(v)`/`Ok(v)` <=> the store value is not ""; `None`/`Err` <=> it is.
/// A1 BinOp compares exactly like the test grammar's string equality, and
/// the store is string-typed, so this is the same marker test as
/// `if let Some(x)`.
fn opt_present(recv: &syn::Expr, present: bool, cx: &Cx) -> Value {
    json!({
        "type": "BinOp",
        "op": if present { "Ne" } else { "Eq" },
        "lhs": value_expr(recv, cx),
        "rhs": str_lit(""),
    })
}

fn std_ctor_value(
    p: &syn::ExprPath,
    args: &syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>,
    cx: &Cx,
) -> Option<Value> {
    if p.qself.is_some() {
        return None;
    }
    let nsegs = p.path.segments.len();
    let f = p
        .path
        .segments
        .last()
        .map(|s| s.ident.to_string())
        .unwrap_or_default();
    // BARE option constructors: `Some(expr)` / `None` — unambiguous
    // (registered fns were resolved before this runs)
    if nsegs == 1 && !p.path.leading_colon.is_some() {
        match f.as_str() {
            "Some" if args.len() == 1 => return Some(value_expr(&args[0], cx)),
            "None" if args.is_empty() => return Some(str_lit("")),
            _ => return None,
        }
    }
    if nsegs < 2 {
        return None;
    }
    // match the LAST two segments (`std::collections::HashMap::new`)
    let t = p.path.segments[nsegs - 2].ident.to_string();
    match (t.as_str(), f.as_str()) {
        // OPTION constructors under the None≡"" convention:
        // Some(v) IS v (payload passthrough); None IS "".
        ("AtomicBool", "new") if args.len() == 1 => Some(value_expr(&args[0], cx)),
        ("Some", _) if args.len() == 1 => Some(value_expr(&args[0], cx)),
        ("None", _) if args.is_empty() => Some(str_lit("")),
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

/// A FUNCTION ITEM used as a VALUE (`let f = transforms::g;`): its
/// store value is the REGISTERED lowered name. Sound because the only
/// operation on a fn value is an indirect call, which dispatches by that
/// name (the runtime resolves sh2.functions entries by string).
fn fn_item_value(p: &syn::ExprPath, cx: &Cx) -> Option<Value> {
    if p.qself.is_some() {
        return None;
    }
    let last = p.path.segments.last()?.ident.to_string();
    if cx.fns.iter().any(|f| *f == last) && p.path.segments.len() >= 2 {
        return Some(str_lit(&last));
    }
    None
}

fn last_len_in_fns(last: &str, cx: &Cx) -> bool {
    cx.fns.iter().any(|f| f == last)
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
    // a VARIABLE of proven Fn(type) dispatches to its registered name
    // (compile-time resolution: the tracker follows reassignments)
    if segs.len() == 1 && segs[0].arguments.is_none() {
        let n = segs[0].ident.to_string();
        let vt = cx
            .vars
            .borrow()
            .get(&n)
            .cloned()
            .or_else(|| cx.statics.borrow().get(&n).cloned());
        if let Some(Ty::Fn(reg)) = vt {
            return Some(reg);
        }
    }
    // `Self::m` inside an impl block names the enclosing type's
    // associated fn — the same item the explicit `Type::m` form names,
    // so it resolves to the identical mangled registration.
    if segs.len() == 2 && segs[0].ident == "Self" && segs[1].arguments.is_none() {
        if let Some(t) = cx.self_ty.borrow().as_deref() {
            let mangled = format!("{}_{}", t, segs[1].ident);
            if known(&mangled) {
                return Some(mangled);
            }
        }
    }
    // exactly-2 segments: the `Type::method` mangling wins first
    if segs.len() == 2 && segs.iter().all(|s| s.arguments.is_none()) {
        let mangled = format!("{}_{}", segs[0].ident, segs[1].ident);
        if known(&mangled) {
            return Some(mangled);
        }
    }
    // deeper crate/mod paths (`crate::transforms::builtin::f`) resolve by
    // their LAST segment — consistent with the inline-mod flattening that
    // already registers nested fns under their bare names
    if segs.len() >= 2 && segs.iter().all(|s| s.arguments.is_none()) {
        let last = segs.last().unwrap().ident.to_string();
        if known(&last) {
            return Some(last);
        }
        return None;
    }
    match segs.len() {
        1 if segs[0].arguments.is_none() => {
            let n = segs[0].ident.to_string();
            if known(&n) {
                Some(n)
            } else {
                // a HOISTED nested `fn` of the enclosing function — and only
                // if it lowered (a refused definition must make every call
                // site refuse, never emit a call to a missing function)
                cx.fn_alias.borrow().get(&n).cloned().filter(|m| known(m))
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

/// The same call node over already-lowered A1 argument values.
fn fn_call_expr_a1(name: &str, args: Vec<Value>, func: &str) -> Value {
    json!({
        "args": [
            str_lit(name),
            json!({"elements": args, "type": "Array"}),
        ],
        "func": func, "purity": "Emulable", "type": "Call"
    })
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
            json!({"elements": args.into_iter().enumerate().map(|(i, a)| call_arg_expr(&a, i, name, cx)).collect::<Vec<_>>(), "type": "Array"}),
        ],
        "func": func, "purity": "Emulable", "type": "Call"
    })
}

/// Parameter names, positionally, for the `&mut`-argument rule below. A
/// receiver keeps its positional slot so a method's `self` lines up with
/// argument 0.
/// Is each parameter declared `&mut`? (Syntactic: `&mut T`.)
fn param_muts(sig: &syn::Signature) -> Vec<bool> {
    sig.inputs
        .iter()
        .map(|a| match a {
            syn::FnArg::Receiver(_) => false,
            syn::FnArg::Typed(pt) => {
                matches!(&*pt.ty, syn::Type::Reference(r) if r.mutability.is_some())
            }
        })
        .collect()
}

fn param_names(sig: &syn::Signature) -> Vec<String> {
    sig.inputs
        .iter()
        .map(|a| match a {
            syn::FnArg::Receiver(_) => "self".to_string(),
            syn::FnArg::Typed(pt) => match &*pt.pat {
                syn::Pat::Ident(pi) => pi.ident.to_string(),
                _ => String::new(),
            },
        })
        .collect()
}

/// A CALL ARGUMENT. Shared borrows already erase to the value; a `&mut x`
/// argument needs the v0.40 exactness rule:
///
/// The store keys ONE flat location per NAME — scalars in `vars`, arrays in
/// the native-array registry, sets/maps in their assoc stores — and
/// references are erased (`&mut T` and `T` are the same `Ty`), so inside the
/// callee `*p` reads and `*p = v` writes the location named `p`, and
/// `p.push`/`p.insert` write the array/set registered under `p`.
///
/// That IS the caller's variable exactly when the argument's name is the
/// parameter's name: one name, one location, the same one the caller reads
/// after the call returns. Records alias too (JS objects, passed as the
/// object by `record_param_bind`), but a name-keyed value does NOT: passing
/// the value under a new name creates a second location, so the writes
/// vanish (measured: `&mut lines` bound as `out` pushed into a fresh array
/// and the caller still read 0 elements). A different name therefore
/// REFUSES rather than silently dropping the mutation.
fn call_arg_expr(a: &syn::Expr, idx: usize, callee: &str, cx: &Cx) -> Value {
    let syn::Expr::Reference(r) = a else {
        // A bare path handed to a `&mut` parameter is a REBORROW: exact only
        // when the names match (then the caller's slot IS the callee's).
        if let syn::Expr::Path(p) = a {
            if let Some(n) = single_path(p) {
                let pmut = cx
                    .fn_param_mut
                    .get(callee)
                    .and_then(|m| m.get(idx))
                    .copied()
                    .unwrap_or(false);
                if pmut {
                    let same = cx
                        .fn_params
                        .get(callee)
                        .and_then(|ps| ps.get(idx))
                        .map(|p| *p == n.to_string())
                        .unwrap_or(false);
                    if !same {
                        refuse(
                            &format!(
                                "reborrow into `{callee}`'s parameter bound under a different \
                                 name (only an explicit `&mut x` argument gets a readback)"
                            ),
                            a.span(),
                        );
                    }
                }
            }
        }
        return value_expr(a, cx);
    };
    if r.mutability.is_none() {
        return value_expr(a, cx);
    }
    let Some(name) = (match &*r.expr {
        syn::Expr::Path(p) => single_path(p).map(|s| s.to_string()),
        _ => None,
    }) else {
        refuse(
            "`&mut` of a non-variable (the store has no location for a field/element reference)",
            r.expr.span(),
        );
    };
    let pointee = infer_ty(&r.expr, cx);
    let same_name = cx
        .fn_params
        .get(callee)
        .and_then(|ps| ps.get(idx))
        .map(|p| *p == name)
        .unwrap_or(false);
    if !same_name {
        // A different name is a DIFFERENT location. It becomes exact only
        // through the readback statements `call_mut_readback` emits around
        // the call (v0.41), which exist for a whole `let` initializer or a
        // statement-position call — anywhere else the write would be lost,
        // so it REFUSES rather than lowering.
        if !*cx.mut_rw.borrow() {
            refuse(
                &format!(
                    "`&mut` argument to `{callee}` bound under a different name where the \
                     readback cannot be emitted (only a whole `let` initializer or a \
                     statement-position call)"
                ),
                a.span(),
            );
        }
        // The POSITIONAL is unused for a `&mut` parameter (the prologue does
        // not bind it): the call site writes the callee's slot itself, so the
        // argument slot only has to keep the positional numbering aligned.
        match pointee {
            Ty::Int | Ty::Str | Ty::Bool => return str_lit(""),
            _ if arr_of_scalar(&pointee) => return str_lit(""),
            _ => refuse(
                "`&mut` argument of a collection type bound under a different name \
                 (only scalars and Vec<scalar> have an exact readback)",
                a.span(),
            ),
        }
    }
    value_expr(&r.expr, cx)
}

/// Does e contain a field read inside its ARITHMETIC spine? Field reads
/// cannot be Arith operands (the A1 arith AST has no field form), so such
/// exprs need temporary hoisting.
/// Does this arithmetic expression have a leaf the raw arith AST cannot
/// spell — a FIELD read (the AST has no field operand) or a CALL (which must
/// run exactly once, in Rust's left-to-right operand order)? Those leaves are
/// hoisted into temporaries by `hoisted_arith` and read back as variables.
fn arith_contains_field(e: &syn::Expr) -> bool {
    match e {
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            if matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_) | And(_) | Or(_)) {
                false // comparison spine lowers via the test grammar
            } else {
                arith_operand_needs_hoist(&b.left) || arith_operand_needs_hoist(&b.right)
            }
        }
        syn::Expr::Unary(u) => arith_contains_field(&u.expr),
        syn::Expr::Paren(p) => arith_contains_field(&p.expr),
        syn::Expr::Unary(u) => {
            if matches!(u.op, syn::UnOp::Deref(_)) {
                arith_contains_field(&u.expr)
            } else {
                false
            }
        }
        // a bare CALL at the top of an initializer (`let v = Vec::new()`) is
        // NOT arithmetic — it must keep its own value/type form. Only a call
        // INSIDE an arithmetic spine is a hoistable operand.
        syn::Expr::Field(_) => true,
        _ => false,
    }
}

/// The same question one level INSIDE an arithmetic spine: there a CALL is
/// also a leaf that must be hoisted (it has to run exactly once, at Rust's
/// left-to-right operand order), and so is a field read.
fn arith_operand_needs_hoist(e: &syn::Expr) -> bool {
    match e {
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            if matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_) | And(_) | Or(_)) {
                false
            } else {
                arith_operand_needs_hoist(&b.left) || arith_operand_needs_hoist(&b.right)
            }
        }
        syn::Expr::Unary(u) => arith_operand_needs_hoist(&u.expr),
        syn::Expr::Paren(p) => arith_operand_needs_hoist(&p.expr),
        syn::Expr::Field(_) | syn::Expr::Call(_) => true,
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
        // A CALL as an arithmetic operand: hoisted into a temp FIRST, so the
        // call happens exactly once, at Rust's left-to-right operand order.
        // Exact for any Rust program that compiles: Rust's arithmetic
        // operators need numeric operands, so a call here cannot yield a bool
        // or a text value whose store form would differ. The callee's value
        // form is the same one a `let` initializer uses (`value_expr`), so an
        // impure/unknown callee still refuses there.
        syn::Expr::Call(_) => {
            let v = value_expr(e, cx);
            *ctr += 1;
            let t = format!("__sh2a{}", ctr);
            cx.vars.borrow_mut().insert(t.clone(), Ty::Int);
            stmts.push(assign_stmt(&t, v));
            arith_var(&t)
        }
        other => refuse("expression in arithmetic", other.span()),
    }
}

/// An array-literal STORE: the setArray builtin keeps a NATIVE JS array
/// in the named variable (a plain Assign would stringify — "10,20,30").
/// The exact shape debashc emits for bash `a=(...)`.
fn set_array_stmt(name: &str, elements: Vec<Value>) -> Value {
    // `setVar(name, [..])`, NOT `setArray`: setArray is the SHELL literal
    // path and SPLICES nested arrays (field-splitting). Rust values are
    // data: a Vec of tuples must keep each tuple as one element. The
    // runtime setVar registers the native array and preserves objects.
    expr_stmt(json!({
        "args": [
            str_lit(name),
            json!({"elements": elements, "type": "Array"}),
        ],
        "func": "setVar", "purity": "Emulable", "type": "Call"
    }))
}

// ── maps and sets (named associative stores) ───────────────────────

fn assoc_call(func: &str, args: Vec<Value>) -> Value {
    json!({"type": "Call", "func": func, "purity": "Emulable", "args": args})
}

/// `declare -A`-equivalent: an EMPTY named associative store.
fn assoc_empty_stmt(name: &str) -> Value {
    expr_stmt(assoc_call(
        "setArray",
        vec![
            str_lit(name),
            json!({"type": "Array", "elements": []}),
            json!({"type": "Bool", "value": true}),
        ],
    ))
}

fn assoc_set_stmt(name: &str, key: Value, val: Value) -> Value {
    expr_stmt(assoc_call("assocSet", vec![str_lit(name), key, val]))
}

/// A COPY of a named array's elements (`sh2.arrayItems(name)`). Use this —
/// never `getVar(name)` — when the value must STAY an array: the emitter
/// inlines a store read as `sh2.vars.x ?? ""`, and the runtime stringifies
/// that on the way in, so a nested getVar would arrive as one joined string.
fn array_items_call(name: &str) -> Value {
    json!({
        "type": "Call", "func": "arrayItems", "purity": "PureCpu",
        "args": [str_lit(name)]
    })
}

fn array_len_call(name: &str) -> Value {
    json!({
        "type": "Call", "func": "arrayLen", "purity": "PureCpu",
        "args": [str_lit(name)]
    })
}

/// A receiver with NO side effect: a literal, a store read, a field/index
/// read, or a PURE method chain over those (store reads like
/// `map.get(k).copied()`). Evaluating one twice — a marker test and the
/// value branch — is the same read twice, so the result is identical.
// ── interior-mutability statics (static / lazy_static! / thread_local!) ──
//
// JS is single-threaded, so `Mutex`/`RwLock`/`Cell`/`RefCell` are vacuous
// wrappers and an atomic is a plain number: the static IS a mutable
// global, which is exactly what the A1 store is. Every access shape below
// is the store/assoc operation Rust performs — `insert` still answers
// "was it absent", `remove`/`get` still answer the previous value, `len`
// is the store's key count — so nothing is guessed. What has NO exact
// form (iteration order of a HashSet, a clone of a named assoc, a Vec
// static's build) refuses at its own site.

/// Peel `const { e }` / `X::new(arg)` down to the initializer's value.
fn static_init_value(e: &syn::Expr, cx: &Cx) -> Option<Value> {
    match e {
        syn::Expr::Call(c) => {
            let syn::Expr::Path(p) = &*c.func else { return None };
            let last = p.path.segments.last()?.ident.to_string();
            // `Mutex::new(v)` / `Cell::new(v)` / `AtomicUsize::new(0)`:
            // the wrapper's state is the argument
            if last == "new" && c.args.len() == 1 {
                return static_init_value(&c.args[0], cx);
            }
            None
        }
        syn::Expr::Block(b) => b
            .block
            .stmts
            .last()
            .and_then(|s| match s {
                syn::Stmt::Expr(e, _) => Some(e),
                _ => None,
            })
            .and_then(|e| static_init_value(e, cx)),
        syn::Expr::Const(c) => c
            .block
            .stmts
            .last()
            .and_then(|s| match s {
                syn::Stmt::Expr(e, _) => Some(e),
                _ => None,
            })
            .and_then(|e| static_init_value(e, cx)),
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Bool(b) => Some(str_lit(if b.value() { "true" } else { "false" })),
            syn::Lit::Int(i) => Some(str_lit(&int_text(i))),
            syn::Lit::Str(s) => Some(str_lit(&s.value())),
            _ => None,
        },
        syn::Expr::Path(p) => match single_path(p).map(|i| i.to_string()) {
            Some(n) if n == "None" => Some(str_lit("")),
            _ => None,
        },
        syn::Expr::Paren(p) => static_init_value(&p.expr, cx),
        _ => None,
    }
}

/// The type's zero value — what `Default::default()` means for a static.
fn static_zero(ty: &Ty) -> Value {
    match ty {
        Ty::Bool | Ty::AtomicBool => str_lit("false"),
        Ty::Int => str_lit("0"),
        _ => str_lit(""),
    }
}

/// `Option<T>` statics: `None` is the store's empty value, `Some(lit)` is
/// the literal (the None≡"" convention every option read already uses).
fn static_init_scalar(e: &syn::Expr, ty: &Ty, cx: &Cx) -> Option<Value> {
    if matches!(ty, Ty::Opt(_)) {
        if let syn::Expr::Call(c) = e {
            if let syn::Expr::Path(p) = &*c.func {
                if p.path.segments.last().map(|s| s.ident == "Some").unwrap_or(false)
                    && c.args.len() == 1
                {
                    return static_init_value(&c.args[0], cx);
                }
            }
        }
    }
    static_init_value(e, cx)
}

/// The program-start statement that gives a static its initial value.
fn static_init_stmt(name: &str, ty: &Ty, init: Option<&syn::Expr>, cx: &Cx) -> Value {
    let inner = match ty {
        Ty::Cel(t) => t.as_ref(),
        t => t,
    };
    match inner {
        // a collection lives in the NAMED associative store, never in the
        // scalar value slot
        Ty::Set(..) | Ty::Map(..) => assoc_empty_stmt(name),
        // a Vec static is a native array (setVar preserves the array)
        Ty::Arr(..) => set_array_stmt(name, Vec::new()),
        _ => {
            let v = init
                .and_then(|e| static_init_scalar(e, inner, cx))
                .unwrap_or_else(|| static_zero(inner));
            assign_stmt(name, v)
        }
    }
}

/// Types whose value the scalar store can carry (a guard read is the read).
fn is_scalar_store_ty(t: &Ty) -> bool {
    matches!(t, Ty::Int | Ty::Str | Ty::Bool | Ty::AtomicBool | Ty::Opt(_))
}

/// `Vec<String>` (the only element type Rust's `join` accepts).
fn arr_of_str(t: &Ty) -> bool {
    matches!(t, Ty::Arr(e, _) if matches!(e.as_deref(), Some(Ty::Str)))
}

/// `Vec<scalar>` — the element types the array copy-in/copy-out channel
/// carries exactly (elements stay their own values; no objects to alias).
fn arr_of_scalar(t: &Ty) -> bool {
    matches!(t, Ty::Arr(e, _) if matches!(e.as_deref(), Some(Ty::Int | Ty::Str | Ty::Bool)))
}

/// The static behind a guard expression: `NAME.lock().unwrap()`,
/// `NAME.borrow_mut()`, `*GUARD`, or a guard ALIAS bound earlier.
/// Returns (static store name, inner Ty).
fn static_guard(e: &syn::Expr, cx: &Cx) -> Option<(String, Ty)> {
    if let syn::Expr::Unary(u) = e {
        if matches!(u.op, syn::UnOp::Deref(_)) {
            return static_guard(&u.expr, cx);
        }
    }
    guard_handle(e, cx)
}

/// The guard HANDLE itself (not a `*guard` read): an alias variable or a
/// `NAME.lock().unwrap()` / `NAME.borrow_mut()` chain.
fn guard_handle(e: &syn::Expr, cx: &Cx) -> Option<(String, Ty)> {
    if let syn::Expr::Path(p) = e {
        let n = single_path(p)?.to_string();
        return cx.guards.borrow().get(&n).cloned();
    }
    let syn::Expr::MethodCall(m) = e else { return None };
    // `guard.unwrap()` peels the guard wrapper
    if m.method == "unwrap" && m.args.is_empty() {
        return guard_handle(&m.receiver, cx);
    }
    if matches!(
        m.method.to_string().as_str(),
        "lock" | "read" | "write" | "borrow" | "borrow_mut"
    ) && m.args.is_empty()
    {
        if let syn::Expr::Path(p) = &*m.receiver {
            let n = single_path(p)?.to_string();
            if let Some(Ty::Cel(inner)) = cx.statics.borrow().get(&n).cloned() {
                return Some((n, *inner));
            }
        }
    }
    None
}

/// `&x` / `x` as a VALUE expression (the assoc helpers take values).
fn ref_arg(e: &syn::Expr, cx: &Cx) -> Value {
    match e {
        syn::Expr::Reference(r) => value_expr(&r.expr, cx),
        other => value_expr(other, cx),
    }
}

/// A store temp holding `expr` (evaluates ONCE — Rust evaluates an
/// argument once, and every helper that needs the key twice must too).
fn hoist_value(expr: &syn::Expr, prefix: &str, cx: &Cx) -> String {
    let t = fresh_tmp(cx, prefix);
    cx.pending.borrow_mut().push(store_stmt(&t, value_expr(expr, cx)));
    cx.vars.borrow_mut().insert(t.clone(), Ty::Str);
    t
}

/// A method ON a static guard: the static's own store/assoc operation.
/// None means "no exact form" — the caller falls through to the normal
/// method table (which refuses).
fn static_guard_method(name: &str, inner: &Ty, m: &syn::ExprMethodCall, cx: &Cx) -> Option<Value> {
    let meth = m.method.to_string();
    let assoc_get = |key: Value| assoc_call("assocGet", vec![str_lit(name), key]);
    match inner {
        Ty::Set(..) => match meth.as_str() {
            "contains" if m.args.len() == 1 => {
                let key = ref_arg(&m.args[0], cx);
                let g = fresh_tmp(cx, "k");
                cx.pending.borrow_mut().push(assign_stmt(&g, assoc_get(key)));
                cx.vars.borrow_mut().insert(g.clone(), Ty::Str);
                Some(cond_value(
                    "c",
                    format!("\"${g}\" != \"\""),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                ))
            }
            "insert" if m.args.len() == 1 => {
                let kt = hoist_value(ref_arg_src(&m.args[0]), "q", cx);
                let g = fresh_tmp(cx, "k");
                cx.pending
                    .borrow_mut()
                    .push(assign_stmt(&g, assoc_get(get_var(&kt))));
                cx.vars.borrow_mut().insert(g.clone(), Ty::Str);
                cx.pending.borrow_mut().push(assoc_set_stmt(
                    name,
                    get_var(&kt),
                    str_lit("1"),
                ));
                // newly inserted <=> the key was absent before
                Some(cond_value(
                    "c",
                    format!("\"${g}\" = \"\""),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                ))
            }
            "remove" if m.args.len() == 1 => {
                let kt = hoist_value(ref_arg_src(&m.args[0]), "q", cx);
                let g = fresh_tmp(cx, "k");
                cx.pending
                    .borrow_mut()
                    .push(assign_stmt(&g, assoc_get(get_var(&kt))));
                cx.vars.borrow_mut().insert(g.clone(), Ty::Str);
                cx.pending.borrow_mut().push(expr_stmt(assoc_call(
                    "assocDel",
                    vec![str_lit(name), get_var(&kt)],
                )));
                Some(cond_value(
                    "c",
                    format!("\"${g}\" != \"\""),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                ))
            }
            "len" if m.args.is_empty() => Some(array_len_call(name)),
            "is_empty" if m.args.is_empty() => {
                let g = fresh_tmp(cx, "n");
                cx.pending
                    .borrow_mut()
                    .push(assign_stmt(&g, array_len_call(name)));
                cx.vars.borrow_mut().insert(g.clone(), Ty::Int);
                Some(cond_value(
                    "c",
                    format!("${g} -eq 0"),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                ))
            }
            _ => None,
        },
        Ty::Map(..) => match meth.as_str() {
            "contains_key" if m.args.len() == 1 => {
                let key = ref_arg(&m.args[0], cx);
                let g = fresh_tmp(cx, "k");
                cx.pending.borrow_mut().push(assign_stmt(&g, assoc_get(key)));
                cx.vars.borrow_mut().insert(g.clone(), Ty::Str);
                Some(cond_value(
                    "c",
                    format!("\"${g}\" != \"\""),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                ))
            }
            // HashMap::get answers Option<&V>: the stored text, or the
            // absent marker (the None≡"" convention)
            "get" if m.args.len() == 1 => Some(assoc_get(ref_arg(&m.args[0], cx))),
            // insert/remove answer the PREVIOUS value (Option<V>)
            "insert" if m.args.len() == 2 => {
                let kt = hoist_value(ref_arg_src(&m.args[0]), "q", cx);
                let vt = hoist_value(ref_arg_src(&m.args[1]), "v", cx);
                let old = fresh_tmp(cx, "p");
                cx.pending
                    .borrow_mut()
                    .push(assign_stmt(&old, assoc_get(get_var(&kt))));
                cx.vars.borrow_mut().insert(old.clone(), Ty::Str);
                cx.pending.borrow_mut().push(assoc_set_stmt(
                    name,
                    get_var(&kt),
                    get_var(&vt),
                ));
                Some(get_var(&old))
            }
            "remove" if m.args.len() == 1 => {
                let kt = hoist_value(ref_arg_src(&m.args[0]), "q", cx);
                let old = fresh_tmp(cx, "p");
                cx.pending
                    .borrow_mut()
                    .push(assign_stmt(&old, assoc_get(get_var(&kt))));
                cx.vars.borrow_mut().insert(old.clone(), Ty::Str);
                cx.pending.borrow_mut().push(expr_stmt(assoc_call(
                    "assocDel",
                    vec![str_lit(name), get_var(&kt)],
                )));
                Some(get_var(&old))
            }
            "len" if m.args.is_empty() => Some(array_len_call(name)),
            _ => None,
        },
        _ => None,
    }
}

/// `&x` unwrapped for hoisting (the borrow erases).
fn ref_arg_src(e: &syn::Expr) -> &syn::Expr {
    match e {
        syn::Expr::Reference(r) => &r.expr,
        other => other,
    }
}

/// `NAME.with(|c| c.get() / c.set(v) / c.replace(v))` on a Cell static.
fn cell_with(name: &str, inner: &Ty, arg: &syn::Expr, cx: &Cx) -> Option<Value> {
    let syn::Expr::Closure(cl) = arg else { return None };
    if cl.inputs.len() != 1 {
        return None;
    }
    let syn::Pat::Ident(pi) = &cl.inputs[0] else { return None };
    let pname = pi.ident.to_string();
    let body: &syn::Expr = match &*cl.body {
        syn::Expr::Block(b) => {
            let mut only: Option<&syn::Expr> = None;
            for s in &b.block.stmts {
                match s {
                    syn::Stmt::Expr(e, _) => {
                        if only.is_some() {
                            return None;
                        }
                        only = Some(e);
                    }
                    _ => return None,
                }
            }
            only?
        }
        e => e,
    };
    let syn::Expr::MethodCall(m) = body else { return None };
    let syn::Expr::Path(rp) = &*m.receiver else { return None };
    if single_path(rp).map(|i| i.to_string()).as_deref() != Some(pname.as_str()) {
        return None;
    }
    // INSIDE the closure the param IS the Cell: `c.get()` reads the
    // static, and any other use resolves through the same handle
    cx.guards
        .borrow_mut()
        .insert(pname.clone(), (name.to_string(), inner.clone()));
    let out = match m.method.to_string().as_str() {
        "get" if m.args.is_empty() => Some(get_var(name)),
        "set" if m.args.len() == 1 => {
            let v = value_expr(&m.args[0], cx);
            cx.pending.borrow_mut().push(assign_stmt(name, v));
            Some(str_lit(""))
        }
        "replace" if m.args.len() == 1 => {
            // the NEW value evaluates first, then the old one is read
            let vt = hoist_value(&m.args[0], "v", cx);
            let old = fresh_tmp(cx, "p");
            cx.pending
                .borrow_mut()
                .push(assign_stmt(&old, get_var(name)));
            cx.vars.borrow_mut().insert(old.clone(), Ty::Str);
            cx.pending.borrow_mut().push(assign_stmt(name, get_var(&vt)));
            Some(get_var(&old))
        }
        _ => None,
    };
    cx.guards.borrow_mut().remove(&pname);
    out
}

fn pure_receiver(e: &syn::Expr) -> bool {
    match e {
        syn::Expr::Lit(_) => true,
        syn::Expr::Path(p) => p.qself.is_none() && single_path(p).is_some(),
        syn::Expr::Field(f) => pure_receiver(&f.base),
        syn::Expr::Index(ix) => pure_receiver(&ix.expr) && pure_receiver(&ix.index),
        syn::Expr::Paren(p) => pure_receiver(&p.expr),
        syn::Expr::Reference(r) => pure_receiver(&r.expr),
        syn::Expr::Cast(c) => pure_receiver(&c.expr),
        syn::Expr::Try(t) => pure_receiver(&t.expr),
        syn::Expr::Unary(u) => matches!(u.op, syn::UnOp::Deref(_)) && pure_receiver(&u.expr),
        syn::Expr::MethodCall(m) => {
            matches!(
                m.method.to_string().as_str(),
                "get" | "copied" | "cloned" | "as_ref" | "as_deref" | "as_str"
                    | "to_string" | "ok" | "first" | "last"
            ) && pure_receiver(&m.receiver)
                && m.args.iter().all(pure_receiver)
        }
        _ => false,
    }
}

/// The plain variable a method receiver reads (`m`, `&m`). Maps/sets are
/// NAMED associative stores, so a field or nested receiver has no exact
/// representation yet.
fn recv_var_name(e: &syn::Expr) -> Option<String> {
    match e {
        syn::Expr::Path(p) => single_path(p).map(|i| i.to_string()),
        syn::Expr::Reference(r) => recv_var_name(&r.expr),
        syn::Expr::Paren(p) => recv_var_name(&p.expr),
        _ => None,
    }
}

fn array_elems_ref(e: &syn::Expr) -> Option<&syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>> {
    match e {
        syn::Expr::Reference(r) => array_elems_ref(&r.expr),
        syn::Expr::Array(a) => Some(&a.elems),
        syn::Expr::Paren(p) => array_elems_ref(&p.expr),
        _ => None,
    }
}

/// `HashMap::new()`, `BTreeMap::new()`, `HashSet::new()`, `...::with_capacity(n)`,
/// `HashMap::from([(k, v), ..])`, `HashSet::from([e, ..])` → the Ty plus
/// the initial entries. `None` for any other expression.
fn map_ctor(e: &syn::Expr, cx: &Cx) -> Option<(Ty, Vec<(Value, Value)>)> {
    let (last, prev) = match e {
        syn::Expr::Path(p) => {
            let n = p.path.segments.len();
            if n < 2 {
                return None;
            }
            (
                p.path.segments[n - 1].ident.to_string(),
                p.path.segments[n - 2].ident.to_string(),
            )
        }
        syn::Expr::Call(c) => {
            let syn::Expr::Path(p) = &*c.func else {
                return None;
            };
            let n = p.path.segments.len();
            if n < 2 {
                return None;
            }
            let last = p.path.segments[n - 1].ident.to_string();
            let prev = p.path.segments[n - 2].ident.to_string();
            let is_map = matches!(prev.as_str(), "HashMap" | "BTreeMap");
            let is_set = matches!(prev.as_str(), "HashSet" | "BTreeSet");
            let order = if prev == "BTreeMap" {
                MapOrder::BTree
            } else {
                MapOrder::Hash
            };
            if !is_map && !is_set {
                return None;
            }
            if last == "new" || last == "with_capacity" {
                return Some(if is_map {
                    (
                        Ty::Map(Box::new(Ty::Unknown), Box::new(Ty::Unknown), order),
                        Vec::new(),
                    )
                } else {
                    (Ty::Set(Box::new(Ty::Unknown), order), Vec::new())
                });
            }
            if last != "from" {
                return None;
            }
            let arr = array_elems_ref(c.args.first()?)?;
            let mut out = Vec::new();
            if is_set {
                for el in arr {
                    out.push((value_expr(el, cx), str_lit("1")));
                }
                return Some((Ty::Set(Box::new(Ty::Unknown), order), out));
            }
            for el in arr {
                let syn::Expr::Tuple(t) = el else {
                    return None;
                };
                if t.elems.len() != 2 {
                    return None;
                }
                out.push((value_expr(&t.elems[0], cx), value_expr(&t.elems[1], cx)));
            }
            return Some((
                Ty::Map(Box::new(Ty::Unknown), Box::new(Ty::Unknown), order),
                out,
            ));
        }
        _ => return None,
    };
    let is_map = matches!(prev.as_str(), "HashMap" | "BTreeMap");
    let is_set = matches!(prev.as_str(), "HashSet" | "BTreeSet");
    if !is_map && !is_set {
        return None;
    }
    if last != "new" && last != "with_capacity" {
        return None;
    }
    let order = if prev == "BTreeMap" {
        MapOrder::BTree
    } else {
        MapOrder::Hash
    };
    Some(if is_map {
        (
            Ty::Map(Box::new(Ty::Unknown), Box::new(Ty::Unknown), order),
            Vec::new(),
        )
    } else {
        (Ty::Set(Box::new(Ty::Unknown), order), Vec::new())
    })
}

/// `for` over a map/set: the source variable and what each item is.
/// Only NAMED map/set variables have an associative-store representation.
fn map_value_ty(name: &str, cx: &Cx) -> Ty {
    match cx.vars.borrow().get(name) {
        Some(Ty::Map(_, v, _)) => (**v).clone(),
        _ => Ty::Unknown,
    }
}

/// Types whose store value is a composite (needs the raw setVar path).
fn is_composite_ty(t: &Ty) -> bool {
    matches!(
        t,
        Ty::Struct(_) | Ty::Tuple(_) | Ty::Arr(..) | Ty::Enum(_) | Ty::Map(..) | Ty::Set(..)
    )
}

fn map_iter_mode(e: &syn::Expr, cx: &Cx) -> Option<(String, &'static str, MapOrder)> {
    let base = match e {
        syn::Expr::Reference(r) => &*r.expr,
        e => e,
    };
    let (n, explicit) = match base {
        syn::Expr::MethodCall(m) => {
            let n = recv_var_name(&m.receiver)?;
            let mode = match m.method.to_string().as_str() {
                "iter" => None,
                "keys" => Some("keys"),
                "values" => Some("values"),
                _ => return None,
            };
            (n, mode)
        }
        other => (recv_var_name(other)?, None),
    };
    let rt = cx.vars.borrow().get(&n).cloned()?;
    match (rt, explicit) {
        // `BTreeSet` has the same key-order promise as `BTreeMap`; the
        // store keeps sets as assoc arrays too, so the SAME sort applies.
        (Ty::Set(_, order), None) => Some((n, "set", order)),
        (Ty::Set(..), Some(_)) => None,
        (Ty::Map(_, _, order), Some(m)) => Some((n, m, order)),
        (Ty::Map(_, _, order), None) => Some((n, "entries", order)),
        _ => None,
    }
}

/// If e is an ARRAY LITERAL (vec![..] or [..]), its elements — else None.
fn array_literal_elems(e: &syn::Expr) -> Option<Vec<syn::Expr>> {
    match e {
        syn::Expr::Macro(m) => {
            let name = m.mac.path.segments.last().map(|s| s.ident.to_string())?;
            if name == "vec" {
                struct VecElems(syn::punctuated::Punctuated<syn::Expr, syn::Token![,]>);
                impl syn::parse::Parse for VecElems {
                    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
                        Ok(VecElems(
                            syn::punctuated::Punctuated::parse_terminated(input)?,
                        ))
                    }
                }
                let VecElems(elems) = syn::parse2(m.mac.tokens.clone()).ok()?;
                Some(elems.into_iter().collect())
            } else {
                None
            }
        }
        syn::Expr::Array(ar) => Some(ar.elems.iter().cloned().collect()),
        // tuples ARE native arrays in the store model
        syn::Expr::Tuple(tu) => Some(tu.elems.iter().cloned().collect()),
        _ => None,
    }
}

/// The per-element transform of a `.map(F)`: a closure (inlined) or a
/// registered fn name (dispatched via fnValue).
enum MapFn<'a> {
    Closure(&'a syn::ExprClosure),
    Fn(String),
}

/// The first input pattern of a map's closure/fn, if it is a closure.
fn c_inputs_first<'a>(f: &'a MapFn) -> Option<&'a syn::Pat> {
    match f {
        MapFn::Closure(c) => c.inputs.first(),
        MapFn::Fn(_) => None,
    }
}

/// One pipeline step between the source array and `.collect()`.
enum Step<'a> {
    /// `.iter()` / `.iter_mut()` — enters the pipeline
    Iter,
    /// `.map(F)` — F is an inline closure or a registered fn name
    Map(MapFn<'a>),
    /// `.filter(|x| bool-expr)` — skips elements whose proven-bool
    /// predicate is false
    Filter(&'a syn::ExprClosure),
    /// `.take(N)` — N a literal int (bounds the loop)
    Take(u64),
    /// `.skip(K)` — K a literal int (shifts the iteration window)
    Skip(u64),
    /// `.enumerate()` — pairs (position, element); positions are the
    /// LOOP INDEX (exact: the pipeline visits elements in order)
    Enumerate,
    /// `.rev()` — reverse visitation order
    Rev,
}

/// `SRC.iter().enumerate().map(|(i, n)| (K, V)).collect()` -> its parts:
/// the source expression, the two closure binding names (position and
/// element) and the KEY / VALUE expressions. Nothing else matches.
fn map_collect_parts<'a>(
    e: &'a syn::Expr,
    cx: &Cx,
) -> Option<(&'a syn::Expr, String, String, &'a syn::Expr, &'a syn::Expr)> {
    let collect = as_method_call(e, "collect")?;
    if !collect.args.is_empty() {
        return None;
    }
    let map = as_method_call(&collect.receiver, "map")?;
    if map.args.len() != 1 {
        return None;
    }
    let syn::Expr::Closure(cl) = &map.args[0] else {
        return None;
    };
    if cl.inputs.len() != 1 {
        return None;
    }
    let Some(syn::Pat::Tuple(pt)) = cl.inputs.first() else {
        return None;
    };
    if pt.elems.len() != 2 {
        return None;
    }
    let (syn::Pat::Ident(ii), syn::Pat::Ident(nn)) = (&pt.elems[0], &pt.elems[1]) else {
        return None;
    };
    if ii.subpat.is_some() || nn.subpat.is_some() {
        return None;
    }
    let syn::Expr::Tuple(tu) = &*cl.body else {
        return None;
    };
    if tu.elems.len() != 2 {
        return None;
    }
    let en = as_method_call(&map.receiver, "enumerate")?;
    if !en.args.is_empty() {
        return None;
    }
    let src: &syn::Expr = match &*en.receiver {
        syn::Expr::MethodCall(m)
            if (m.method == "iter" || m.method == "iter_mut") && m.args.is_empty() =>
        {
            &m.receiver
        }
        other => other,
    };
    if !matches!(infer_ty(src, cx), Ty::Arr(..) | Ty::List) {
        return None;
    }
    Some((
        src,
        ii.ident.to_string(),
        nn.ident.to_string(),
        &tu.elems[0],
        &tu.elems[1],
    ))
}

fn as_method_call<'a>(e: &'a syn::Expr, name: &str) -> Option<&'a syn::ExprMethodCall> {
    match e {
        syn::Expr::MethodCall(m) if m.method == name => Some(m),
        _ => None,
    }
}

/// Parse `SRC.<steps...>.collect()` into (SRC, ordered steps). Supported
/// steps: iter/iter_mut (entering), map(F: closure or registered fn),
/// filter(bool-closure), take(literal N). Anything else returns None —
/// the caller refuses.
fn parse_collect_chain<'a>(
    e: &'a syn::Expr,
    cx: &Cx,
) -> Option<(&'a syn::Expr, Vec<Step<'a>>)> {
    let collect = as_method_call(e, "collect")?;
    if !collect.args.is_empty() || collect.turbofish.is_some() && false {
        // turbofished collect is allowed (the dst annotation types it)
    }
    let mut rev: Vec<Step> = Vec::new();
    let mut node: &syn::Expr = &*collect.receiver;
    loop {
        match node {
            syn::Expr::MethodCall(mc) => {
                let mname = mc.method.to_string();
                match mname.as_str() {
                    "enumerate" => {
                        if !mc.args.is_empty() || mc.turbofish.is_some() {
                            return None;
                        }
                        rev.push(Step::Enumerate);
                        node = &mc.receiver;
                    }
                    "rev" => {
                        if !mc.args.is_empty() || mc.turbofish.is_some() {
                            return None;
                        }
                        rev.push(Step::Rev);
                        node = &mc.receiver;
                    }
                    "iter" | "iter_mut" => {
                        if !mc.args.is_empty() || mc.turbofish.is_some() {
                            return None;
                        }
                        rev.push(Step::Iter);
                        node = &mc.receiver;
                    }
                    "map" => {
                        if mc.args.len() != 1 {
                            return None;
                        }
                        let f = match &mc.args[0] {
                            syn::Expr::Closure(c) => MapFn::Closure(c),
                            syn::Expr::Path(p) => {
                                let name = single_path(p)?.to_string();
                                if !cx.fns.iter().any(|f| *f == name) {
                                    return None;
                                }
                                MapFn::Fn(name)
                            }
                            _ => return None,
                        };
                        rev.push(Step::Map(f));
                        node = &mc.receiver;
                    }
                    "filter" => {
                        if mc.args.len() != 1 {
                            return None;
                        }
                        match &mc.args[0] {
                            syn::Expr::Closure(c) => rev.push(Step::Filter(c)),
                            _ => return None,
                        }
                        node = &mc.receiver;
                    }
                    "skip" => {
                        if mc.args.len() != 1 {
                            return None;
                        }
                        match mc.args.first() {
                            Some(syn::Expr::Lit(l)) => match &l.lit {
                                syn::Lit::Int(li) => {
                                    let n: u64 = li.base10_parse().ok()?;
                                    rev.push(Step::Skip(n));
                                    node = &mc.receiver;
                                }
                                _ => return None,
                            },
                            _ => return None,
                        }
                    }
                    "take" => {
                        if mc.args.len() != 1 {
                            return None;
                        }
                        match mc.args.first() {
                            Some(syn::Expr::Lit(l)) => match &l.lit {
                                syn::Lit::Int(li) => {
                                    let n: u64 = li.base10_parse().ok()?;
                                    rev.push(Step::Take(n));
                                    node = &mc.receiver;
                                }
                                _ => return None,
                            },
                            _ => return None,
                        }
                    }
                    _ => return None,
                }
            }
            syn::Expr::Path(_) => break, // the SOURCE reached
            other => return None,
        }
    }
    // rev holds steps BACKWARD; reverse first, then require Iter to open
    // the pipeline and a plain-path source behind it
    rev.reverse();
    match (rev.first(), node) {
        (Some(Step::Iter), syn::Expr::Path(p)) if p.qself.is_none() => {}
        _ => return None,
    }
    Some((node, rev))
}

/// Lower `let D = SRC.iter().map(F).collect();` — accumulator + index
/// loop + setArrayAppend, all existing A1 shapes:
///   setArray(D, [])
///   __n <- param(len, SRC); __i = 0
///   while $__i < $__n:
///     it <- arrayIndex(SRC, __i); [P <- it;]
///     setArrayAppend(D, [VALUE])
///     __i += 1
fn collect_chain_stmt(
    dst: &str,
    src_expr: &syn::Expr,
    steps: Vec<Step>,
    cx: &Cx,
    out: &mut Vec<Value>,
) -> Option<Value> {
    // the SOURCE must be a plain variable holding an array
    let Some(syn::Expr::Path(sp)) = Some(src_expr) else {
        refuse("iterator source path", src_expr.span());
    };
    let Some(src_name) = single_path(sp) else {
        refuse("iterator source path", sp.span());
    };
    let src_name = src_name.to_string();
    let elem0_ty = match infer_ty(src_expr, cx) {
        Ty::Arr(Some(t), _) => t.as_ref().clone(),
        _ => Ty::Unknown,
    };

    // KNOWN length only (literal-built sources): Take(n) caps it at n.
    // The param(len) fallback miscounts arrays (core request
    // rust-frontend-20260824-param-len-arrays.md) — refuses otherwise.
    let mut known_len: Option<u64> = match infer_ty(src_expr, cx) {
        Ty::Arr(_, l) => l,
        _ => None,
    };
    let mut enumerated = false;
    let mut reversed = false;
    for step in &steps {
        match step {
            Step::Take(n) => {
                known_len = Some(match known_len {
                    Some(l) if l > *n => *n,
                    Some(l) => l,
                    None => *n, // unproven source + take(n): EXACT bound
                });
            }
            Step::Skip(k) => {
                // skip past a proven-length source shrinks the window;
                // skipping an UNPROVEN source keeps it unproven (the
                // param-len fallback would miscount)
                known_len = known_len.map(|l| l.saturating_sub(*k));
            }
            Step::Enumerate => enumerated = true,
            Step::Rev => reversed = !reversed,
            _ => {}
        }
    }
    let Some(bound) = known_len else {
        refuse(
            "iterator source length unproven (param-len arrays core request)",
            src_expr.span(),
        );
    };

    // result type: last Map's output (or the element type)
    let mut res_ty = elem0_ty.clone();
    for step in &steps {
        match step {
            Step::Map(f) => {
                res_ty = match f {
                    MapFn::Closure(c) => infer_ty(&c.body, cx),
                    MapFn::Fn(name) => cx.fn_rets.get(name).cloned().unwrap_or(Ty::Unknown),
                };
            }
            // enumerate wraps elements in (position, element) pairs — the
            // pair is never materialized (bindings read position/element
            // directly), so the RESULT element type is unchanged
            Step::Enumerate | Step::Rev | Step::Take(_) | Step::Skip(_)
            | Step::Iter | Step::Filter(_) => {}
        }
    }

    let idx = format!("__sh2i{}", { let mut c = cx.tmp.borrow_mut(); *c += 1; c });
    let lenv = format!("__sh2l{}", { let mut c = cx.tmp.borrow_mut(); *c += 1; c });
    let cur = format!("__sh2v{}", { let mut c = cx.tmp.borrow_mut(); *c += 1; c });

    // pre-loop: fresh accumulator + hoisted bound + zeroed index
    out.push(set_array_stmt(dst, vec![]));
    cx.vars.borrow_mut().insert(
        dst.to_string(),
        Ty::Arr(Some(Box::new(res_ty.clone())), None),
    );
    out.push(assign_stmt(
        &lenv,
        str_lit(&bound.to_string()),
    ));
    cx.vars.borrow_mut().insert(lenv.clone(), Ty::Int);
    out.push(assign_stmt(&idx, arith_value(arith_num(0))));
    cx.vars.borrow_mut().insert(idx.clone(), Ty::Int);

    // per-iteration body:
    //   __cur <- arrayIndex(SRC, POSITION)   (position = idx, or
    //   bound-1-idx under .rev(); enumerate exposes the position itself)
    //   per step: Map rebinds __cur via closure/fnValue; Filter skips on
    //   a proven-false predicate (Continue)
    let skip_k: u64 = steps
        .iter()
        .map(|s| match s {
            Step::Skip(k) => *k,
            _ => 0,
        })
        .sum();
    let enumerated_any = steps.iter().any(|s| matches!(s, Step::Enumerate));
    let pos_ast = if reversed {
        json!({
            "lhs": {"type": "Num", "value": bound.saturating_sub(1)},
            "op": "-",
            "rhs": {"name": idx, "type": "Var"},
            "type": "Bin"
        })
    } else {
        json!({"name": idx, "type": "Var"})
    };
    let mut loop_body = Vec::new();
    loop_body.push(assign_stmt(
        &cur,
        json!({
            "args": [str_lit(&src_name),
                     {"type": "Arith", "ast": {
                        "lhs": pos_ast.clone(),
                        "op": "+",
                        "rhs": {"type": "Num", "value": skip_k},
                        "type": "Bin"
                     }}],
            "func": "arrayIndex", "purity": "PureCpu", "type": "Call"
        }),
    ));
    cx.vars.borrow_mut().insert(cur.clone(), elem0_ty.clone());
    for step in steps.iter().skip(1) {
        match step {
            Step::Iter => {}
            Step::Take(_) => {} // folded into the hoisted bound
            Step::Skip(_) => {} // folded into the hoisted bound + start offset
            // enumerate pairs (position, element): the position IS the
            // loop index and the element is arrayIndex(src, idx') — the
            // pair is never materialized. rev flips the element read.
            Step::Enumerate => {}
            Step::Rev => {}
            Step::Skip(_) => {} // folded into the hoisted bound
            Step::Map(f) => {
                // (the rebound holder carries the STEP RESULT's proven
                // type so later filters see it)
                // `.enumerate().map(|(i, e)| ..)` — the pair is never
                // materialized: the position IS the loop index and the
                // element is arrayIndex(src, position).
                let enum_tuple: Option<Vec<syn::Ident>> =
                    if enumerated_any {
                        match c_inputs_first(f).as_deref() {
                            Some(syn::Pat::Tuple(tp)) if tp.elems.len() == 2 => {
                                let mut ids = Vec::new();
                                let mut ok = true;
                                for sub in &tp.elems {
                                    match sub {
                                        syn::Pat::Ident(pi) => {
                                            ids.push(pi.ident.clone())
                                        }
                                        _ => ok = false,
                                    }
                                }
                                if ok { Some(ids) } else { None }
                            }
                            _ => None,
                        }
                    } else {
                        None
                    };
                let idents = enum_tuple.as_deref();
                match (f, idents) {
                    (
                        MapFn::Closure(c),
                        Some([i_name, e_name]),
                    ) => {
                        let i_val = arith_value(pos_ast.clone());
                        loop_body.push(assign_stmt(&i_name.to_string(), i_val));
                        cx.vars.borrow_mut().insert(i_name.to_string(), Ty::Int);
                        let e_val = json!({
                            "args": [str_lit(&src_name),
                                     {"type": "Arith", "ast": pos_ast.clone()}],
                            "func": "arrayIndex", "purity": "PureCpu", "type": "Call"
                        });
                        loop_body.push(assign_stmt(&e_name.to_string(), e_val));
                        cx.vars.borrow_mut().insert(e_name.to_string(), elem0_ty.clone());
                        let v = value_expr(&c.body, cx);
                        let v_ty = infer_ty(&c.body, cx);
                        loop_body.push(assign_stmt(&cur, v));
                        cx.vars.borrow_mut().insert(cur.clone(), v_ty);
                    }
                    (MapFn::Closure(c), _) => {
                        let Some(syn::Pat::Ident(pi)) = c.inputs.first() else {
                            refuse("closure parameter pattern", c.inputs.span());
                        };
                        let pname = pi.ident.to_string();
                        // param adopts the INCOMING element type
                        let in_ty = cx
                            .vars
                            .borrow()
                            .get(&cur)
                            .cloned()
                            .unwrap_or(Ty::Unknown);
                        cx.vars.borrow_mut().insert(pname.clone(), in_ty);
                        loop_body.push(assign_stmt(&pname, get_var(&cur)));
                        let v = value_expr(&c.body, cx);
                        let v_ty = infer_ty(&c.body, cx);
                        loop_body.push(assign_stmt(&cur, v));
                        cx.vars.borrow_mut().insert(cur.clone(), v_ty);
                    }
                    (MapFn::Fn(name), _) => {
                        let v = fn_call_expr_a1(
                            name,
                            vec![get_var(&cur)],
                            "fnValue",
                        );
                        // fn return types come from the registration map
                        let v_ty = cx.fn_rets.get(name).cloned().unwrap_or(Ty::Unknown);
                        loop_body.push(assign_stmt(&cur, v));
                        cx.vars.borrow_mut().insert(cur.clone(), v_ty);
                    }
                }
            }
            Step::Filter(c) => {
                // bind the closure param FIRST (the predicate's proof may
                // need it: `|b| *b` derefs through to the element type),
                // then require a PROVEN bool — JS "" is falsy but Rust
                // values are never "falsy", so guessing would diverge
                let Some(syn::Pat::Ident(pi)) = c.inputs.first() else {
                    refuse("closure parameter pattern", c.inputs.span());
                };
                let pname = pi.ident.to_string();
                // param ADOPTS the holder's proven type (`|b| *b` derefs
                // through to it)
                let cur_ty = cx.vars.borrow().get(&cur).cloned().unwrap_or(Ty::Unknown);
                cx.vars.borrow_mut().insert(pname.clone(), cur_ty);
                loop_body.push(assign_stmt(&pname, get_var(&cur)));
            if !matches!(infer_ty(&c.body, cx), Ty::Bool) {
                refuse(
                    "filter predicate (must be a proven boolean)",
                    c.body.span(),
                );
            }
                let ftemp = format!(
                    "__sh2f{}",
                    { let mut ctr = cx.tmp.borrow_mut(); *ctr += 1; ctr }
                );
                loop_body.push(assign_stmt(&ftemp, value_expr(&c.body, cx)));
                cx.vars.borrow_mut().insert(ftemp.clone(), Ty::Bool);
                loop_body.push(json!({
                    "cond": test_call(&format!("${ftemp} = \"true\"")),
                    "then": [],
                    "elsifs": [],
                    "else": [{"type": "Continue"}],
                    "type": "If",
                }));
            }
        }
    }
    // Collect: append the final element value
    loop_body.push(expr_stmt(json!({
        "args": [
            str_lit(dst),
            json!({"elements": vec![get_var(&cur)], "type": "Array"}),
        ],
        "func": "setArrayAppend", "purity": "Emulable", "type": "Call"
    })));
    loop_body.push(assign_stmt(
        &idx,
        arith_bin("+", arith_var(&idx), arith_num(1)),
    ));

    Some(json!({
        "type": "While",
        "cond": test_call(&format!("${idx} -lt ${lenv}")),
        "body": loop_body,
    }))
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

/// A primitive integer type's associated `MAX`/`MIN` constant
/// (`i128::MAX`, `usize::MIN`, …) -> its EXACT decimal text. The store is
/// string-typed, so the full i128/u128 range survives verbatim; only
/// arithmetic uses would need i64 and refuse there.
fn int_assoc_const_path(p: &syn::ExprPath) -> Option<String> {
    if p.qself.is_some() || p.path.leading_colon.is_some() || p.path.segments.len() != 2 {
        return None;
    }
    let ty = p.path.segments[0].ident.to_string();
    let name = p.path.segments[1].ident.to_string();
    let (bits, signed) = match ty.as_str() {
        "i8" => (8u32, true),
        "i16" => (16, true),
        "i32" => (32, true),
        "i64" => (64, true),
        "i128" => (128, true),
        "isize" => (64, true),
        "u8" => (8, false),
        "u16" => (16, false),
        "u32" => (32, false),
        "u64" => (64, false),
        "u128" => (128, false),
        "usize" => (64, false),
        _ => return None,
    };
    match name.as_str() {
        "MAX" => {
            if signed {
                Some(((1i128 << (bits - 1)) - 1).to_string())
            } else if bits == 128 {
                Some(u128::MAX.to_string())
            } else {
                Some(((1u128 << bits) - 1).to_string())
            }
        }
        "MIN" => {
            if signed {
                Some((-(1i128 << (bits - 1))).to_string())
            } else {
                Some("0".to_string())
            }
        }
        _ => None,
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
            if cx.ambiguous.contains(&e) {
                return None;
            }
            if cx.enums.iter().any(|x| *x == e) {
                return Some(format!("{e}::{v}"));
            }
        }
    }
    None
}

/// A match arm's test condition: string equality on the hoisted
/// scrutinee, or-patterns join with `-o`. Literal patterns are exact
/// (`$t = "str"` for strings/bools, `$t -eq N` for integers); a bare
/// `None` is the Option layer's absent marker under None≡"".
/// The EXACT NEGATION of `arm_cond`: unit variants, literals and or-
/// patterns invert to `!=` (or-chains invert to and-chains); `_` is
/// always false. Used for `!matches!(..)` conditions.
fn arm_cond_neg(temp: &str, pat: &syn::Pat, cx: &Cx) -> Option<String> {
    fn one_neg(temp: &str, p: &syn::Pat, cx: &Cx) -> Option<String> {
        if let Some(tag) = unit_variant_pat(p, cx) {
            return Some(format!("\"${temp}\" != \"{tag}\""));
        }
        match p {
            syn::Pat::Wild(_) => Some("0 -eq 1".into()),
            syn::Pat::Ident(pi) if pi.ident == "None" && pi.subpat.is_none() => {
                Some(format!("\"${temp}\" != \"\""))
            }
            syn::Pat::Lit(l) => match &l.lit {
                syn::Lit::Int(li) => Some(format!("${temp} -ne {}", int_text(li))),
                syn::Lit::Str(ls) => {
                    let s = ls.value();
                    if s.contains('"') || s.contains('\\') {
                        return None;
                    }
                    Some(format!("\"${temp}\" != \"{s}\""))
                }
                syn::Lit::Bool(lb) => Some(format!(
                    "\"${temp}\" != \"{}\"",
                    if lb.value() { "true" } else { "false" }
                )),
                _ => None,
            },
            // `!matches!(x, Enum::Variant { .. })` — the TAG inequality
            // (same wildcard-only sub-pattern rule as the positive form).
            syn::Pat::Struct(ps) => {
                if !ps
                    .fields
                    .iter()
                    .all(|f| matches!(&*f.pat, syn::Pat::Wild(_) | syn::Pat::Ident(_)))
                {
                    return None;
                }
                enum_variant_of_path(&ps.path, cx)
                    .map(|(en, vn)| format!("\"${temp}\" != \"{en}::{vn}\""))
            }
            syn::Pat::TupleStruct(ts) => {
                if !ts.elems.iter().all(|p| {
                    matches!(p, syn::Pat::Wild(_))
                        || matches!(p, syn::Pat::Ident(pi) if pi.subpat.is_none())
                }) {
                    return None;
                }
                let last = ts
                    .path
                    .segments
                    .last()
                    .map(|s| s.ident.to_string())
                    .unwrap_or_default();
                if ts.path.segments.len() == 1 && last == "Some" {
                    return Some(format!("\"${temp}\" = \"\""));
                }
                if ts.path.segments.len() == 1 && last == "None" {
                    return Some(format!("\"${temp}\" != \"\""));
                }
                enum_variant_of_path(&ts.path, cx)
                    .map(|(en, vn)| format!("\"${temp}\" != \"{en}::{vn}\""))
            }
            _ => None,
        }
    }
    match pat {
        syn::Pat::Or(o) => {
            let mut parts = Vec::new();
            for p2 in &o.cases {
                parts.push(one_neg(temp, p2, cx)?);
            }
            Some(parts.join(" -a "))
        }
        other => one_neg(temp, other, cx),
    }
}

fn arm_cond(temp: &str, pat: &syn::Pat, cx: &Cx) -> Option<String> {
    fn one(temp: &str, p: &syn::Pat, cx: &Cx) -> Option<String> {
        if let Some(tag) = unit_variant_pat(p, cx) {
            return Some(format!("${temp} = \"{tag}\""));
        }
        match p {
            // `None` parses as a BINDING ident in pattern position (syn
            // has no variant knowledge); it is the absent marker, not a
            // fresh binding. Same convention as `if let None`.
            syn::Pat::Ident(pi) if pi.ident == "None" && pi.subpat.is_none() => {
                Some(format!("\"${temp}\" = \"\""))
            }
            syn::Pat::Lit(l) => match &l.lit {
                syn::Lit::Int(li) => Some(format!("${temp} -eq {}", int_text(li))),
                syn::Lit::Str(ls) => {
                    let s = ls.value();
                    // the test grammar is shell-shaped: a quote or
                    // backslash inside the literal would need the same
                    // escaping bash applies — refuse rather than guess.
                    if s.contains('"') || s.contains('\\') {
                        return None;
                    }
                    Some(format!("\"${temp}\" = \"{s}\""))
                }
                syn::Lit::Bool(lb) => Some(format!(
                    "\"${temp}\" = \"{}\"",
                    if lb.value() { "true" } else { "false" }
                )),
                _ => None,
            },
            // `matches!(x, Enum::Variant { .. })` / `Variant(_)`: the TAG
            // test. Only patterns whose sub-patterns are wildcards or
            // plain bindings are accepted (a payload constraint would need
            // the full destructure, not just the tag).
            syn::Pat::Struct(ps) => {
                if !ps
                    .fields
                    .iter()
                    .all(|f| matches!(&*f.pat, syn::Pat::Wild(_) | syn::Pat::Ident(_)))
                {
                    return None;
                }
                enum_variant_of_path(&ps.path, cx)
                    .map(|(en, vn)| format!("${temp} = \"{en}::{vn}\""))
            }
            syn::Pat::TupleStruct(ts) => {
                if !ts.elems.iter().all(|p| {
                    matches!(p, syn::Pat::Wild(_))
                        || matches!(p, syn::Pat::Ident(pi) if pi.subpat.is_none())
                }) {
                    return None;
                }
                let last = ts
                    .path
                    .segments
                    .last()
                    .map(|s| s.ident.to_string())
                    .unwrap_or_default();
                if ts.path.segments.len() == 1 && last == "Some" {
                    return Some(format!("\"${temp}\" != \"\""));
                }
                if ts.path.segments.len() == 1 && last == "None" {
                    return Some(format!("\"${temp}\" = \"\""));
                }
                enum_variant_of_path(&ts.path, cx)
                    .map(|(en, vn)| format!("${temp} = \"{en}::{vn}\""))
            }
            _ => None,
        }
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

fn fresh_tmp(cx: &Cx, prefix: &str) -> String {
    let mut t = cx.tmp.borrow_mut();
    *t += 1;
    format!("__sh2{prefix}{t}")
}

/// `E::V` where E is a registered enum carrying variant V.
fn enum_variant_of_path(path: &syn::Path, cx: &Cx) -> Option<(String, String)> {
    if path.leading_colon.is_some() || path.segments.len() < 2 {
        return None;
    }
    if path
        .segments
        .iter()
        .any(|s| !matches!(s.arguments, syn::PathArguments::None))
    {
        return None;
    }
    // A QUALIFIED variant path (`crate::ir::IrExpr::Call`) names the same
    // variant as the short `IrExpr::Call`; the last two segments are the
    // enum and variant. Module paths carry no runtime name.
    let n = path.segments.len();
    let en = path.segments[n - 2].ident.to_string();
    let vn = path.segments[n - 1].ident.to_string();
    if cx.ambiguous.contains(&en) {
        return None;
    }
    if cx.variants.get(&en)?.contains_key(&vn) {
        Some((en, vn))
    } else {
        None
    }
}

fn record_field(obj: Value, name: &str) -> Value {
    json!({"type": "FieldRead", "object": obj, "name": name})
}

/// A native tuple is an ARRAY: read element `ix` through arrayIndex (the
/// scalar view of an array is not its elements).
fn index_key_expr(e: &syn::Expr) -> Option<Value> {
    match e {
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => Some(str_lit(&int_text(li))),
            _ => None,
        },
        syn::Expr::Path(p) => single_path(p)
            .map(|id| json!({"type": "Var", "name": id.to_string(), "sigil": null})),
        _ => None,
    }
}

/// Element write through the runtime `setVar`'s bracket-name channel
/// (`v[idx]`): it writes the native array (`this.arrays`) and keeps the
/// scalar view in sync. A plain A1 Assign target drops its indices.
fn index_set_stmt(name: &str, key: Value, value: Value) -> Value {
    expr_stmt(json!({
        "args": [
            {"type": "Interpolate", "parts": [
                {"kind": "lit", "text": format!("{name}[")},
                {"kind": "expr", "expr": key},
                {"kind": "lit", "text": "]"}
            ]},
            value
        ],
        "func": "setVar", "purity": "Emulable", "type": "Call"
    }))
}

/// RAW array element read: `arrayItems(name).at(i)` returns the stored
/// value (objects/arrays survive), unlike `arrayIndex`, which is the
/// scalar view and String()s every element.
fn raw_elem(name: &str, key: Value) -> Value {
    json!({
        "type": "MethodCall",
        "object": {
            "type": "Call", "func": "arrayItems", "purity": "PureCpu",
            "args": [str_lit(name)]
        },
        "method": "at",
        "args": [key]
    })
}

/// A plain `Var` value node (a store read by name).
fn var_node(name: &str) -> Value {
    json!({"type": "Var", "name": name, "sigil": null})
}

/// Statements that rebuild the Vec `contains` shadow set (`<v>__set`, an
/// assoc store keyed by VARIABLE NAME) from a copied array's elements:
/// `for i in 0..arrayLen(arr) { assocSet(set, arr[i], "1") }`. Keying by
/// name is why a Vec that crosses a call boundary under a new name needs
/// this: the callee's pushes fill `<param>__set`, never the caller's set.
fn shadow_fill_stmts(cx: &Cx, set_name: &str, arr_name: &str) -> Vec<Value> {
    let i = fresh_tmp(cx, "sfi");
    let n = fresh_tmp(cx, "sfn");
    let body = vec![
        assoc_set_stmt(set_name, raw_elem(arr_name, var_node(&i)), str_lit("1")),
        assign_stmt(&i, arith_bin("+", arith_var(&i), arith_num(1))),
    ];
    vec![
        assign_stmt(&n, array_len_call(arr_name)),
        assign_stmt(&i, str_lit("0")),
        json!({
            "type": "While",
            "cond": test_call(&format!("${i} -lt ${n}")),
            "body": body,
        }),
    ]
}

/// `arr.join(sep)` for a `Vec<String>`, loop-composed (v0.41). The store has
/// no join member and none is invented: the accumulator is built with the
/// Interpolate concat, the separator goes BETWEEN elements, and each element
/// contributes its stored (raw) text — which is exactly Rust's `join` for the
/// only element type it accepts (`String`/`&str`, via `Borrow<str>`).
fn join_value(cx: &Cx, arr: &str, sep: Value) -> Value {
    let n = fresh_tmp(cx, "jn");
    let i = fresh_tmp(cx, "ji");
    let acc = fresh_tmp(cx, "js");
    // Temps are written with `assign_stmt` (the emitter's JS binding), never
    // `store_stmt`: a `setVar` write lands in `sh2.vars` while the generated
    // reads use the hoisted binding, and the two would diverge.
    let mut stmts = vec![
        assign_stmt(&n, array_len_call(arr)),
        assign_stmt(&i, str_lit("0")),
        assign_stmt(&acc, str_lit("")),
    ];
    let body = vec![
        json!({
            "type": "If",
            "cond": test_call(&format!("${i} -gt 0")),
            "then": [assign_stmt(&acc, str_concat(get_var(&acc), sep))],
            "elsifs": [],
            "else": [],
        }),
        assign_stmt(&acc, str_concat(get_var(&acc), raw_elem(arr, var_node(&i)))),
        assign_stmt(&i, arith_bin("+", arith_var(&i), arith_num(1))),
    ];
    stmts.push(json!({
        "type": "While",
        "cond": test_call(&format!("${i} -lt ${n}")),
        "body": body,
    }));
    for s in stmts {
        cx.pending.borrow_mut().push(s);
    }
    var_node(&acc)
}

/// `n.is_power_of_two()` (v0.41) — exact without bitwise ops, which the
/// store does not have: a positive integer is a power of two iff halving it
/// while it is even lands on exactly 1. `0` never enters the loop and stays
/// false, and so does every negative value (Rust's own answer). Exact for
/// every width: the store's numbers are IEEE doubles, so the integers it can
/// hold are |n| < 2^53, and powers of two above that halve exactly as well.
fn pow2_value(cx: &Cx, recv: &syn::Expr) -> Value {
    let v = fresh_tmp(cx, "pt");
    let m = fresh_tmp(cx, "pm");
    let r = fresh_tmp(cx, "pr");
    let mut stmts = vec![
        assign_stmt(&v, value_expr(recv, cx)),
        assign_stmt(&r, str_lit("false")),
    ];
    let then = vec![
        assign_stmt(&m, arith_bin("%", arith_var(&v), arith_num(2))),
        json!({
            "type": "While",
            "cond": test_call(&format!("${m} -eq 0")),
            "body": [
                assign_stmt(&v, arith_bin("/", arith_var(&v), arith_num(2))),
                assign_stmt(&m, arith_bin("%", arith_var(&v), arith_num(2))),
            ],
        }),
        json!({
            "type": "If",
            "cond": test_call(&format!("${v} -eq 1")),
            "then": [assign_stmt(&r, str_lit("true"))],
            "elsifs": [],
            "else": [],
        }),
    ];
    stmts.push(json!({
        "type": "If",
        "cond": test_call(&format!("${v} -gt 0")),
        "then": then,
        "elsifs": [],
        "else": [],
    }));
    for s in stmts {
        cx.pending.borrow_mut().push(s);
    }
    var_node(&r)
}

/// `&mut` READBACK for a call whose arguments are bound under DIFFERENT
/// names than the callee's parameters (v0.41). The store keys a location by
/// name, so the callee's `p` and the caller's `x` are two locations:
///
///   * copy-in — a `Vec` argument is passed as `arrayItems(x)` (the elements,
///     which the callee's prologue registers under `p`), and `<p>__set` is
///     rebuilt from those elements so the callee's `contains` sees them;
///   * the call;
///   * copy-out — `x := p` for a scalar, `x := arrayItems(p)` plus a rebuilt
///     `<x>__set` for a Vec. rustc's borrow rules make `&mut` non-aliasing,
///     so the caller observing the final state at return IS the borrow's
///     observable semantics.
///
/// Only a whole `let` initializer or a statement-position call qualifies
/// (`bind`/`stmt`), where the call is the statement's own value: the
/// statements cannot reorder anything, so the order is Rust's. Returns
/// `(handled, binding statement)`.
fn call_mut_readback(
    e: &syn::Expr,
    bind: Option<&str>,
    cx: &Cx,
    out: &mut Vec<Value>,
    stmt: bool,
) -> (bool, Option<Value>) {
    let syn::Expr::Call(c) = e else {
        return (false, None);
    };
    let syn::Expr::Path(p) = &*c.func else {
        return (false, None);
    };
    let Some(callee) = single_path(p).map(|s| s.to_string()) else {
        return (false, None);
    };
    let Some(params) = cx.fn_params.get(&callee).cloned() else {
        return (false, None);
    };
    let mut rw: Vec<(String, String, Ty)> = Vec::new();
    for (i, a) in c.args.iter().enumerate() {
        let syn::Expr::Reference(r) = a else { continue };
        if r.mutability.is_none() {
            continue;
        }
        let syn::Expr::Path(ap) = &*r.expr else { continue };
        let Some(an) = single_path(ap).map(|s| s.to_string()) else {
            continue;
        };
        let Some(pn) = params.get(i).cloned() else { continue };
        if pn == an {
            continue; // the same name IS the same location: nothing to copy
        }
        rw.push((an, pn, infer_ty(&r.expr, cx)));
    }
    if rw.is_empty() {
        return (false, None);
    }
    // COPY-IN: write the callee's slot from the caller's location. A `&mut`
    // parameter is not bound from its positional at all, so this is the only
    // write that puts the caller's value where the callee reads it.
    for (an, pn, ty) in &rw {
        if matches!(ty, Ty::Arr(..)) {
            out.push(assign_stmt(pn, array_items_call(an)));
            for s in shadow_fill_stmts(cx, &format!("{pn}__set"), an) {
                out.push(s);
            }
        } else {
            out.push(assign_stmt(pn, get_var(an)));
        }
    }
    if !stmt {
        // The hoisted result rides the JS binding (`assign_stmt`); a call
        // returning a collection would need the array channel, so it refuses.
        if matches!(
            cx.fn_rets.get(&callee),
            Some(Ty::Arr(..)) | Some(Ty::Struct(_)) | Some(Ty::Enum(_)) | Some(Ty::Set(..)) | Some(Ty::Map(..))
        ) {
            refuse(
                "`&mut` readback call returning a collection (the hoisted result rides the scalar channel)",
                e.span(),
            );
        }
    }
    let saved = cx.mut_rw.replace(true);
    let call_v = fn_call_expr(&callee, &c.args, if stmt { "fnCall" } else { "fnValue" }, cx);
    cx.mut_rw.replace(saved);
    let t = fresh_tmp(cx, "mr");
    out.push(if stmt { expr_stmt(call_v) } else { assign_stmt(&t, call_v) });
    for (an, pn, ty) in rw {
        if matches!(ty, Ty::Arr(..)) {
            out.push(assign_stmt(&an, array_items_call(&pn)));
            for s in shadow_fill_stmts(cx, &format!("{an}__set"), &an) {
                out.push(s);
            }
        } else {
            out.push(assign_stmt(&an, get_var(&pn)));
        }
    }
    if stmt {
        (true, None)
    } else {
        (true, Some(assign_stmt(bind.unwrap_or(""), var_node(&t))))
    }
}

fn tuple_elem(name: &str, ix: usize) -> Value {
    raw_elem(name, str_lit(&ix.to_string()))
}

/// The tag test of a payload variant: hoist the `tag` field into a temp
/// (the test grammar reads only $vars/literals) and compare exactly.
/// A unit-variant value is a plain string, so its `.tag` is empty and the
/// test is false — matching the representation, not guessing it.
fn tag_cond(temp: &str, en: &str, vn: &str, cx: &Cx, pre: &mut Vec<Value>) -> String {
    let t = fresh_tmp(cx, "g");
    pre.push(store_stmt(&t, record_field(get_var(temp), "tag")));
    format!("\"${{{t}}}\" = \"{en}::{vn}\"")
}

/// Destructure `pat` against the value held in variable `temp`.
///
/// Returns the REFUTABLE part as a test-grammar condition, or None when
/// the pattern is irrefutable. Pure hoists (tag/element reads) go into
/// `pre` (they must run before the condition); bindings go into `binds`
/// (they belong on the matched path). Recursive patterns hoist each
/// intermediate into a fresh temp, so a failed outer tag test only ever
/// reads fields that are absent on that variant (undefined), never
/// evaluates user code twice.
///
/// Representations: unit variants are strings (`"E::V"`), tuple/struct
/// payload variants are tagged objects (`{tag: "E::V", 0: ..}`), tuples
/// are native arrays, structs are untagged objects, and Option keeps the
/// None≡"" convention.
fn destructure(
    temp: &str,
    pat: &syn::Pat,
    cx: &Cx,
    pre: &mut Vec<Value>,
    binds: &mut Vec<Value>,
) -> Option<String> {
    match pat {
        syn::Pat::Wild(_) => None,
        syn::Pat::Ident(pi) if pi.subpat.is_none() => {
            if pi.ident == "None" {
                return Some(format!("\"${temp}\" = \"\""));
            }
            binds.push(store_stmt(&pi.ident.to_string(), get_var(temp)));
            cx.vars
                .borrow_mut()
                .insert(pi.ident.to_string(), Ty::Unknown);
            None
        }
        a @ syn::Pat::Ident(pi) => {
            // `name @ subpat`
            let _ = a;
            let name = pi.ident.to_string();
            binds.push(store_stmt(&name, get_var(temp)));
            cx.vars.borrow_mut().insert(name, Ty::Unknown);
            pi.subpat
                .as_ref()
                .and_then(|(_, p)| destructure(temp, p, cx, pre, binds))
        }
        // FIXED-LENGTH slice/array pattern (`let [a, b] = ..`): the length
        // test plus one destructure per element. A rest (`[a, ..]`) needs
        // variable-length binding and keeps refusing.
        syn::Pat::Slice(sl) => {
            if sl.elems.iter().any(|p| matches!(p, syn::Pat::Rest(_))) {
                refuse("slice pattern with a rest (fixed length only)", sl.span());
            }
            let n = sl.elems.len();
            let is_list = matches!(cx.vars.borrow().get(temp), Some(Ty::List));
            let l = fresh_tmp(cx, "sl");
            pre.push(assign_stmt(
                &l,
                if is_list {
                    json!({"type": "Call", "func": "listLen", "purity": "PureCpu", "args": [get_var(temp)]})
                } else {
                    array_len_call(temp)
                },
            ));
            cx.vars.borrow_mut().insert(l.clone(), Ty::Int);
            let mut cond = format!("${l} -eq {n}");
            for (i, p) in sl.elems.iter().enumerate() {
                let et = fresh_tmp(cx, "se");
                let key = json!({"type": "Arith", "ast": {"type": "Num", "value": i as i64}});
                let elem = if is_list {
                    json!({"type": "Call", "func": "listGet", "purity": "PureCpu", "args": [get_var(temp), key]})
                } else {
                    raw_elem(temp, key)
                };
                pre.push(store_stmt(&et, elem));
                if let Some(c) = destructure(&et, p, cx, pre, binds) {
                    cond.push_str(&format!(" -a {c}"));
                }
            }
            Some(cond)
        }
        syn::Pat::Lit(_) | syn::Pat::Path(_) | syn::Pat::Or(_) => arm_cond(temp, pat, cx),
        syn::Pat::Paren(p) => destructure(temp, &p.pat, cx, pre, binds),
        syn::Pat::Type(t) => destructure(temp, &t.pat, cx, pre, binds),
        syn::Pat::Reference(r) => destructure(temp, &r.pat, cx, pre, binds),
        syn::Pat::TupleStruct(ts) => {
            let last = ts
                .path
                .segments
                .last()
                .map(|s| s.ident.to_string())
                .unwrap_or_default();
            if ts.path.segments.len() == 1 && last == "Some" {
                let Some(inner) = ts.elems.first() else {
                    refuse("`Some()` pattern with no binding", ts.span());
                };
                let c = format!("\"${temp}\" != \"\"");
                match inner {
                    syn::Pat::Wild(_) => Some(c),
                    syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                        binds.push(store_stmt(&pi.ident.to_string(), get_var(temp)));
                        cx.vars
                            .borrow_mut()
                            .insert(pi.ident.to_string(), Ty::Unknown);
                        Some(c)
                    }
                    other => {
                        let ft = fresh_tmp(cx, "o");
                        binds.push(store_stmt(&ft, get_var(temp)));
                        Some(match destructure(&ft, other, cx, pre, binds) {
                            Some(ic) => format!("{c} -a {ic}"),
                            None => c,
                        })
                    }
                }
            } else if ts.path.segments.len() == 1 && last == "None" {
                Some(format!("\"${temp}\" = \"\""))
            } else if let Some((en, vn)) = enum_variant_of_path(&ts.path, cx) {
                let shape = cx
                    .variants
                    .get(&en)
                    .and_then(|m| m.get(&vn))
                    .cloned()
                    .unwrap_or_else(|| refuse("unknown enum variant", ts.span()));
                let VariantShape::Tuple(tys) = shape else {
                    refuse("tuple-struct pattern for a non-tuple variant", ts.span());
                };
                if tys.len() != ts.elems.len() {
                    refuse("enum variant pattern arity", ts.span());
                }
                let mut conds = vec![tag_cond(temp, &en, &vn, cx, pre)];
                for (i, sub) in ts.elems.iter().enumerate() {
                    match sub {
                        syn::Pat::Wild(_) => {}
                        syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                            binds.push(store_stmt(
                                &pi.ident.to_string(),
                                record_field(get_var(temp), &i.to_string()),
                            ));
                            cx.vars
                                .borrow_mut()
                                .insert(pi.ident.to_string(), tys[i].clone());
                        }
                        other => {
                            let ft = fresh_tmp(cx, "f");
                            pre.push(store_stmt(
                                &ft,
                                record_field(get_var(temp), &i.to_string()),
                            ));
                            if let Some(c) = destructure(&ft, other, cx, pre, binds) {
                                conds.push(c);
                            }
                        }
                    }
                }
                Some(conds.join(" -a "))
            } else {
                refuse(
                    "tuple-struct pattern (only Option, enum tuple variants, and tuples)",
                    ts.span(),
                )
            }
        }
        syn::Pat::Struct(ps) => {
            let mut conds = Vec::new();
            let mut field_tys: HashMap<String, Ty> = HashMap::new();
            if let Some((en, vn)) = enum_variant_of_path(&ps.path, cx) {
                conds.push(tag_cond(temp, &en, &vn, cx, pre));
                if let Some(VariantShape::Struct(fs)) =
                    cx.variants.get(&en).and_then(|m| m.get(&vn))
                {
                    for (n, t) in fs {
                        field_tys.insert(n.clone(), t.clone());
                    }
                }
            } else if ps.path.segments.len() != 1 {
                refuse("struct pattern path", ps.path.span());
            }
            for fp in &ps.fields {
                let key = match &fp.member {
                    syn::Member::Named(id) => id.to_string(),
                    syn::Member::Unnamed(ix) => {
                        refuse("tuple-struct positional pattern", ix.span())
                    }
                };
                match &*fp.pat {
                    syn::Pat::Wild(_) => {}
                    syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                        binds.push(store_stmt(
                            &pi.ident.to_string(),
                            record_field(get_var(temp), &key),
                        ));
                        let t = field_tys.get(&key).cloned().unwrap_or(Ty::Unknown);
                        cx.vars.borrow_mut().insert(pi.ident.to_string(), t);
                    }
                    other => {
                        let ft = fresh_tmp(cx, "f");
                        pre.push(store_stmt(&ft, record_field(get_var(temp), &key)));
                        if let Some(c) = destructure(&ft, other, cx, pre, binds) {
                            conds.push(c);
                        }
                    }
                }
            }
            if conds.is_empty() {
                None
            } else {
                Some(conds.join(" -a "))
            }
        }
        syn::Pat::Tuple(tp) => {
            let mut conds = Vec::new();
            for (i, sub) in tp.elems.iter().enumerate() {
                match sub {
                    syn::Pat::Wild(_) => {}
                    syn::Pat::Ident(pi) if pi.subpat.is_none() => {
                        binds.push(store_stmt(&pi.ident.to_string(), tuple_elem(temp, i)));
                        cx.vars
                            .borrow_mut()
                            .insert(pi.ident.to_string(), Ty::Unknown);
                    }
                    other => {
                        let ft = fresh_tmp(cx, "t");
                        pre.push(store_stmt(&ft, tuple_elem(temp, i)));
                        if let Some(c) = destructure(&ft, other, cx, pre, binds) {
                            conds.push(c);
                        }
                    }
                }
            }
            if conds.is_empty() {
                None
            } else {
                Some(conds.join(" -a "))
            }
        }
        other => refuse(
            "pattern kind (slice/range/box patterns need runtime support)",
            other.span(),
        ),
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
    // scrutinee hoist — any expression, read once (exact: Rust moves/
    // borrows the scrutinee once too)
    let n = cx.tmp.borrow_mut();
    let temp = format!("__sh2m{n}");
    drop(n);
    // ORDER: the scrutinee's value can need hoisted temps (a guarded index, a
    // branch value). `value_expr` QUEUES those on `cx.pending`, so they must be
    // flushed BEFORE the statement that consumes their result — otherwise the
    // binding reads the temp before the temp is computed (argv `nth(1)` in a
    // match scrutinee was exactly that bug).
    let scrut = value_expr(&m.expr, cx);
    let hoisted: Vec<Value> = cx.pending.borrow_mut().drain(..).collect();
    out.extend(hoisted);
    out.push(store_stmt(&temp, scrut));
    let mut else_: Vec<Value> = Vec::new();
    let last_ix = m.arms.len().saturating_sub(1);
    for (ix, arm) in m.arms.iter().enumerate().rev() {
        let mut then = Vec::new();
        // Irrefutable arms (`_` and a plain binding) are the FALL-THROUGH.
        // rustc only permits them last (later arms would be unreachable);
        // any other position refuses rather than silently dropping code.
        let cond_s = match &arm.pat {
            syn::Pat::Wild(_) => {
                if ix != last_ix {
                    refuse("`_` match arm before later arms", arm.pat.span());
                }
                None
            }
            syn::Pat::Ident(pi) if pi.subpat.is_none() && pi.ident != "None" => {
                if ix != last_ix {
                    refuse("binding match arm before later arms", arm.pat.span());
                }
                // `other => body` binds the scrutinee value to `other`
                // (the hoisted temp is read once, so the snapshot is exact).
                then.push(assign_stmt(&pi.ident.to_string(), get_var(&temp)));
                let bind_ty = infer_ty(&m.expr, cx);
                cx.vars.borrow_mut().insert(pi.ident.to_string(), bind_ty);
                None
            }
            pat => {
                let mut pre = Vec::new();
                let c = destructure(&temp, pat, cx, &mut pre, &mut then);
                out.extend(pre);
                if c.is_none() && ix != last_ix {
                    refuse("irrefutable match arm before later arms", pat.span());
                }
                c
            }
        };
        // The arm's bindings (if any) precede the body: a guard reads them.
        let binds_len = then.len();
        match &*arm.body {
            syn::Expr::Block(blk) => {
                push_branch_block(&blk.block, target, cx, in_fn, &mut then)
            }
            body_e => match target {
                BranchTarget::Var(name) => {
                    // `let x = match .. { _ => return None }`: the arm
                    // RETURNS; the assignment never runs on that path.
                    if let syn::Expr::Return(r) = body_e {
                        let v = match &r.expr {
                            Some(e) => value_expr(e, cx),
                            None => serde_json::Value::Null,
                        };
                        then.push(json!({"type": "Return", "value": v}));
                    } else {
                        then.push(assign_stmt(name, value_expr(body_e, cx)));
                    }
                }
                BranchTarget::Ret => {
                    // `PAT => return None` in a value match: the arm's own
                    // return IS the tail (never wrap it in a second Return).
                    if let syn::Expr::Return(r) = body_e {
                        let v = match &r.expr {
                            Some(e) => value_expr(e, cx),
                            None => serde_json::Value::Null,
                        };
                        then.push(json!({"type": "Return", "value": v}));
                    } else {
                        then.push(json!({"type": "Return", "value": value_expr(body_e, cx)}));
                    }
                }
                BranchTarget::Unit => {
                    if let Some(v) = lower_expr_stmt(body_e, cx, in_fn, &mut then) {
                        then.push(v);
                    }
                }
            },
        }
        // `PAT if GUARD => body`: the pattern's bindings are in scope, so the
        // guard becomes a nested If whose FALSE branch is the remaining
        // arms (the same arms the pattern's own failure branch runs).
        if let Some(g) = &arm.guard {
            let body: Vec<Value> = then.split_off(binds_len);
            then.push(json!({
                "cond": test_call(&test_string(&g.1, cx)),
                "then": body,
                "elsifs": [],
                "else": else_.clone(),
                "type": "If",
            }));
        }
        else_ = match cond_s {
            Some(cond_s) => vec![json!({
                "cond": test_call(&cond_s),
                "then": then,
                "elsifs": [],
                "else": else_,
                "type": "If",
            })],
            // irrefutable arm: the body IS the remaining else. The
            // always-true guard keeps the one-node shape the caller
            // expects (and is exact — no value is inspected).
            None => vec![json!({
                "cond": test_call("1 -eq 1"),
                "then": then,
                "elsifs": [],
                "else": [],
                "type": "If",
            })],
        };
    }
    else_.remove(0)
}

/// `COLL.any(|e| COND)` / `COLL.all(|e| COND)` over an ARRAY or a LIST
/// (strSplit result). Returns (is_all, collection expr, closure). Only a
/// PROVEN collection receiver is accepted: `any`/`all` also exist on
/// Option/Result, whose store representation is different.
fn quantifier_parts<'a>(
    e: &'a syn::Expr,
    cx: &Cx,
) -> Option<(bool, &'a syn::Expr, &'a syn::ExprClosure)> {
    let syn::Expr::MethodCall(m) = e else {
        return None;
    };
    let all = match m.method.to_string().as_str() {
        "any" => false,
        "all" => true,
        _ => return None,
    };
    let syn::Expr::Closure(cl) = m.args.first()? else {
        return None;
    };
    if cl.inputs.len() != 1 {
        return None;
    }
    if !matches!(infer_ty(&m.receiver, cx), Ty::Arr(..) | Ty::List) {
        return None;
    }
    Some((all, &m.receiver, cl))
}

/// Lower a quantifier into setup + a bounded while-loop, returning the
/// fresh variable that holds "true"/"false". The loop reads the runtime
/// length ONCE and raw elements (listGet / arrayItems.at), so tuples and
/// records survive. The closure parameter must not shadow an existing
/// store binding (the loop would clobber it).
fn quantifier_stmts(
    all: bool,
    src: &syn::Expr,
    cl: &syn::ExprClosure,
    cx: &Cx,
    out: &mut Vec<Value>,
) -> String {
    let (coll, is_list) = match src {
        syn::Expr::MethodCall(m) if m.method == "split" => (src, true),
        syn::Expr::MethodCall(m)
            if (m.method == "iter" || m.method == "iter_mut") && m.args.is_empty() =>
        {
            (&*m.receiver, matches!(infer_ty(&m.receiver, cx), Ty::List))
        }
        e => (e, matches!(infer_ty(e, cx), Ty::List)),
    };
    let elem_ty = match infer_ty(coll, cx) {
        Ty::Arr(Some(t), _) => (*t).clone(),
        Ty::List => Ty::Str,
        _ => Ty::Unknown,
    };
    let id = match coll {
        syn::Expr::Path(p) => {
            let Some(n) = single_path(p) else {
                refuse("quantifier collection path", p.span());
            };
            n.to_string()
        }
        other => {
            let t = fresh_tmp(cx, "q");
            out.push(store_stmt(&t, value_expr(other, cx)));
            t
        }
    };
    let n = fresh_tmp(cx, "qn");
    let i = fresh_tmp(cx, "qi");
    let r = fresh_tmp(cx, "qr");
    let param = match &cl.inputs[0] {
        syn::Pat::Ident(pi) if pi.subpat.is_none() => pi.ident.to_string(),
        other => refuse("quantifier closure parameter (plain binding)", other.span()),
    };
    if cx.vars.borrow().contains_key(&param) || cx.statics.borrow().contains_key(&param) {
        refuse(
            "quantifier closure parameter shadows a store binding",
            cl.inputs[0].span(),
        );
    }
    let saved = cx.vars.borrow().clone();
    {
        let mut vars = cx.vars.borrow_mut();
        vars.insert(param.clone(), elem_ty);
        vars.insert(n.clone(), Ty::Int);
        vars.insert(i.clone(), Ty::Int);
        vars.insert(r.clone(), Ty::Bool);
    }
    let len_expr = if is_list {
        json!({
            "args": [get_var(&id)],
            "func": "listLen", "purity": "PureCpu", "type": "Call"
        })
    } else {
        json!({
            "args": [str_lit(&id)],
            "func": "arrayLen", "purity": "PureCpu", "type": "Call"
        })
    };
    out.push(assign_stmt(&n, len_expr));
    out.push(assign_stmt(&i, str_lit("0")));
    out.push(store_stmt(&r, str_lit(if all { "true" } else { "false" })));
    let elem = if is_list {
        json!({
            "args": [get_var(&id), json!({"type": "Var", "name": i, "sigil": null})],
            "func": "listGet", "purity": "PureCpu", "type": "Call"
        })
    } else {
        raw_elem(&id, json!({"type": "Var", "name": i, "sigil": null}))
    };
    let mut body = vec![store_stmt(&param, elem)];
    let body_expr = match &*cl.body {
        syn::Expr::Block(b) => block_value(&b.block)
            .unwrap_or_else(|| refuse("quantifier closure block value", b.block.span())),
        e => e,
    };
    let mut pre = Vec::new();
    let cond_expr = hoist_cond_calls(body_expr, cx, &mut pre);
    body.extend(pre);
    // `.all` tests the EXACT NEGATION (operator inversion), not a
    // runtime `! ( .. )` wrapper: the test grammar's negation form is
    // not trusted for every operator chain.
    let cond = if all {
        test_string_neg(&cond_expr, cx)
    } else {
        test_string(&cond_expr, cx)
    };
    body.push(json!({
        "cond": test_call(&cond),
        "then": [
            store_stmt(&r, str_lit(if all { "false" } else { "true" })),
            json!({"type": "Break"})
        ],
        "elsifs": [],
        "else": [],
        "type": "If",
    }));
    body.push(assign_stmt(
        &i,
        arith_bin("+", arith_var(&i), arith_num(1)),
    ));
    *cx.vars.borrow_mut() = saved;
    cx.vars.borrow_mut().insert(r.clone(), Ty::Bool);
    out.push(json!({
        "type": "While",
        "cond": test_call(&format!("${i} -lt ${n}")),
        "body": body,
    }));
    r
}

/// Hoist METHOD-CALL leaves of an If CONDITION into fresh temporaries
/// (the test grammar holds only $vars and literals). Exact for If: the
/// condition evaluates ONCE. While conditions re-evaluate every
/// iteration and do NOT hoist (calls there keep refusing).
fn hoist_cond_calls(e: &syn::Expr, cx: &Cx, out: &mut Vec<Value>) -> syn::Expr {
    match e {
        syn::Expr::MethodCall(_) if quantifier_parts(e, cx).is_some() => {
            let (all, src, cl) = quantifier_parts(e, cx)
                .unwrap_or_else(|| refuse("quantifier", e.span()));
            let r = quantifier_stmts(all, src, cl, cx, out);
            syn::parse_str::<syn::Expr>(&r)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        syn::Expr::MethodCall(m) if m.method == "is_empty" => {
            // Arr: `${#v} == 0` via a hoisted param(len) read; Str: the
            // EXACT equality `$s = ""` (no encoding questions at all).
            let recv_ty = infer_ty(&m.receiver, cx);
            // a static guard's `is_empty` is the STATIC's key count
            let name = match &*m.receiver {
                syn::Expr::Path(p) => single_path(p).map(|i| i.to_string()),
                _ => None,
            }
            .or_else(|| static_guard(&m.receiver, cx).map(|(s, _)| s));
            match (recv_ty, name.clone()) {
                (Ty::Arr(_, _), Some(vname)) => {
                    out.push(assign_stmt(
                        "__sh2e",
                        json!({
                            "args": [str_lit(&vname)],
                            "func": "arrayLen", "purity": "PureCpu", "type": "Call"
                        }),
                    ));
                    cx.vars.borrow_mut().insert("__sh2e".to_string(), Ty::Int);
                    syn::parse_str::<syn::Expr>("__sh2e == 0")
                        .unwrap_or_else(|_| refuse("temporary name", e.span()))
                }
                (Ty::Str, Some(vname)) => {
                    syn::parse_str::<syn::Expr>(&format!("{vname} == \"\""))
                        .unwrap_or_else(|_| refuse("temporary name", e.span()))
                }
                // A FIELD array / any array-valued expression: copy it
                // ONCE to a temp (setVar registers the native array), then
                // read its trusted runtime length.
                (Ty::Arr(_, _), None) => {
                    let t = fresh_tmp(cx, "a");
                    out.push(store_stmt(&t, value_expr(&m.receiver, cx)));
                    out.push(assign_stmt(
                        "__sh2e",
                        json!({
                            "args": [str_lit(&t)],
                            "func": "arrayLen", "purity": "PureCpu", "type": "Call"
                        }),
                    ));
                    cx.vars.borrow_mut().insert("__sh2e".to_string(), Ty::Int);
                    syn::parse_str::<syn::Expr>("__sh2e == 0")
                        .unwrap_or_else(|_| refuse("temporary name", e.span()))
                }
                // LIST (strSplit result): listLen is the trusted length.
                (Ty::List, _) => {
                    let id = match name {
                        Some(n) => n,
                        None => {
                            let t = fresh_tmp(cx, "q");
                            out.push(store_stmt(&t, value_expr(&m.receiver, cx)));
                            t
                        }
                    };
                    out.push(assign_stmt(
                        "__sh2e",
                        json!({
                            "args": [get_var(&id)],
                            "func": "listLen", "purity": "PureCpu", "type": "Call"
                        }),
                    ));
                    cx.vars.borrow_mut().insert("__sh2e".to_string(), Ty::Int);
                    syn::parse_str::<syn::Expr>("__sh2e == 0")
                        .unwrap_or_else(|_| refuse("temporary name", e.span()))
                }
                // MAP / SET: the associative store's key count.
                (Ty::Map(..) | Ty::Set(..), Some(vname)) => {
                    out.push(assign_stmt("__sh2e", array_len_call(&vname)));
                    cx.vars.borrow_mut().insert("__sh2e".to_string(), Ty::Int);
                    syn::parse_str::<syn::Expr>("__sh2e == 0")
                        .unwrap_or_else(|_| refuse("temporary name", e.span()))
                }
                _ => refuse("is_empty on unproven receiver", m.method.span()),
            }
        }
        // a method ON a static guard (`X.lock().unwrap().contains(&k)`):
        // the store operation evaluates ONCE into a temp, so the condition
        // stays a plain test on that temp
        syn::Expr::MethodCall(m) if static_guard(&m.receiver, cx).is_some() => {
            let t = fresh_tmp(cx, "t");
            let v = value_expr(e, cx);
            let mut p: Vec<Value> = cx.pending.borrow_mut().drain(..).collect();
            out.append(&mut p);
            out.push(store_stmt(&t, v));
            let ty = infer_ty(e, cx);
            cx.vars.borrow_mut().insert(t.clone(), ty);
            syn::parse_str::<syn::Expr>(&t)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        // `matches!(EXPR, PAT)` with a COMPOUND subject: evaluate the
        // subject ONCE into a temp, then test the pattern on that temp.
        // Exact for If (the condition evaluates once); While conditions
        // never hoist, so they keep refusing.
        syn::Expr::Macro(m)
            if m.mac
                .path
                .segments
                .last()
                .map(|s| s.ident == "matches")
                .unwrap_or(false) =>
        {
            let (subj, pat, guard) = parse_matches_args(m.mac.tokens.clone(), m.span());
            // `matches!` is an IF-LET in disguise: destructure the subject
            // once, bind the pattern's names, then test the optional guard
            // against those bindings. Exact for If (evaluated once).
            let t = fresh_tmp(cx, "m");
            out.push(store_stmt(&t, value_expr(&subj, cx)));
            let r = fresh_tmp(cx, "mr");
            out.push(store_stmt(&r, str_lit("false")));
            let mut pre = Vec::new();
            let mut binds = Vec::new();
            let c = destructure(&t, &pat, cx, &mut pre, &mut binds);
            let mut then = pre;
            then.extend(binds);
            match guard {
                Some(g) => then.push(json!({
                    "cond": test_call(&test_string(&g, cx)),
                    "then": [store_stmt(&r, str_lit("true"))],
                    "elsifs": [],
                    "else": [],
                    "type": "If",
                })),
                None => then.push(store_stmt(&r, str_lit("true"))),
            }
            out.push(json!({
                "cond": test_call(c.as_deref().unwrap_or("1 -eq 1")),
                "then": then,
                "elsifs": [],
                "else": [],
                "type": "If",
            }));
            cx.vars.borrow_mut().insert(r.clone(), Ty::Bool);
            syn::parse_str::<syn::Expr>(&r)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        // An INDEX or FIELD read in a condition (`v[i] == "x"`,
        // `self.kind == "A::B"`): the test grammar reads only $vars, so
        // snapshot the value ONCE into a temp that carries its proven type
        // (a Str/Enum temp makes the following equality textual).
        syn::Expr::Index(_) | syn::Expr::Field(_) => {
            let v = value_expr(e, cx);
            let temp = fresh_tmp(cx, "c");
            let ty = infer_ty(e, cx);
            cx.vars.borrow_mut().insert(temp.clone(), ty);
            out.push(store_stmt(&temp, v));
            syn::parse_str::<syn::Expr>(&temp)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        // A CALL as the WHOLE condition (`if pred(x) {`): evaluated ONCE
        // into a temp, then tested as the store's boolean — exactly what
        // `let b = pred(x); if b` already lowers to (same value, same test).
        // Under `&&`/`||` the RIGHT operand is skipped by Rust when the left
        // decides the answer, so a call there REFUSES instead of running
        // unconditionally.
        syn::Expr::Call(_) => {
            if cx.sc_depth.get() > 0 {
                refuse(
                    "call condition inside a short-circuit chain (Rust skips it; \
                     a hoisted call would always run)",
                    e.span(),
                );
            }
            let t = fresh_tmp(cx, "cd");
            out.push(store_stmt(&t, value_expr(e, cx)));
            cx.vars.borrow_mut().insert(t.clone(), Ty::Bool);
            syn::parse_str::<syn::Expr>(&t)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        syn::Expr::MethodCall(m) => {
            let v = value_expr(e, cx);
            let mut ctr = cx.tmp.borrow_mut();
            *ctr += 1;
            let temp = format!("__sh2c{}", ctr);
            drop(ctr);
            // the temp carries the method's result type so a following
            // test_string sees a PROVEN bool (`contains`) etc.
            let ty = match m.method.to_string().as_str() {
                "contains" | "contains_key" | "starts_with" | "ends_with" => Ty::Bool,
                "len" => Ty::Int,
                _ => infer_ty(e, cx),
            };
            cx.vars.borrow_mut().insert(temp.clone(), ty);
            out.push(assign_stmt(&temp, v));
            syn::parse_str::<syn::Expr>(&temp)
                .unwrap_or_else(|_| refuse("temporary name", e.span()))
        }
        syn::Expr::Binary(b) => {
            use syn::BinOp::*;
            let cmp = matches!(b.op, Eq(_) | Ne(_) | Lt(_) | Le(_) | Gt(_) | Ge(_));
            // a COMPARISON operand the test grammar cannot spell (`n % 2`,
            // `f(x)`, `a.len()`) becomes a temp first: the condition still
            // evaluates it exactly once, in Rust's operand order, and the
            // comparison then reads a $variable.
            let mut hoist_operand = |e: &syn::Expr, cx: &Cx, out: &mut Vec<Value>| -> syn::Expr {
                if cmp && arith_operand_needs_hoist(e) {
                    if cx.sc_depth.get() > 0 && contains_call(e) {
                        refuse(
                            "call operand inside a short-circuit chain (Rust \
                             skips it; a hoisted call would always run)",
                            e.span(),
                        );
                    }
                    let t = fresh_tmp(cx, "op");
                    let ty = infer_ty(e, cx);
                    let ty = if matches!(ty, Ty::Unknown) { Ty::Int } else { ty };
                    out.push(store_stmt(&t, value_expr(e, cx)));
                    cx.vars.borrow_mut().insert(t.clone(), ty);
                    return syn::parse_str::<syn::Expr>(&t)
                        .unwrap_or_else(|_| refuse("temporary name", e.span()));
                }
                e.clone()
            };
            let mut b2 = b.clone();
            b2.left = Box::new(hoist_operand(&b.left, cx, out));
            let saved = cx.sc_depth.get();
            if matches!(b.op, And(_) | Or(_)) {
                cx.sc_depth.set(saved + 1);
            }
            b2.right = Box::new(hoist_operand(&b.right, cx, out));
            cx.sc_depth.set(saved);
            b2.left = Box::new(hoist_cond_calls(&b2.left, cx, out));
            cx.sc_depth.set(saved);
            if matches!(b.op, And(_) | Or(_)) {
                cx.sc_depth.set(saved + 1);
            }
            b2.right = Box::new(hoist_cond_calls(&b2.right, cx, out));
            cx.sc_depth.set(saved);
            syn::Expr::Binary(b2)
        }
        syn::Expr::Unary(u) => {
            let mut u2 = u.clone();
            u2.expr = Box::new(hoist_cond_calls(&u.expr, cx, out));
            syn::Expr::Unary(u2)
        }
        syn::Expr::Paren(p) => {
            let mut p2 = p.clone();
            p2.expr = Box::new(hoist_cond_calls(&p.expr, cx, out));
            syn::Expr::Paren(p2)
        }
        other => other.clone(),
    }
}

/// Lower `if c {A} else {B}` used as a VALUE into the equivalent A1 If:
/// each branch lowers its statements then binds/returns the branch value.
/// else-if chains recurse (nested If in the else position). A missing
/// else refuses (rustc rejects it too — never reached for valid code).
/// A conditional VALUE as a STATEMENT `If` (the A1 `Ternary` node is
/// Perl-only IR — the ESTree renderer rejects it). One branch runs, exactly
/// like Rust's `if`; the arms are pure values, so the order is Rust's own.
// ── Option/Result combinators (the absent-marker algebra) ───────────
//
// The store's Option model is value-or-absent(""): Some(v)/Ok(v) IS v and
// None/Err IS "". Every combinator below is that algebra with RUST's own
// evaluation order:
//   unwrap_or(d), map_or(d, f)   evaluate d EAGERLY (it is an argument),
//                                then run f only when present;
//   unwrap_or_else(f)            runs f only when absent;
//   map, and_then, is_some_and   run the closure only when present.
// A combinator closure is INLINED with its parameter renamed to a fresh
// store temp, so the binding can neither capture nor clobber an outer
// variable of the same name (the store has ONE namespace; Rust gives a
// closure its own).

/// A store temp holding `e`, with its proven Ty (evaluated ONCE).
fn recv_tmp(e: &syn::Expr, cx: &Cx) -> (String, Ty) {
    let t = fresh_tmp(cx, "o");
    let ty = infer_ty(e, cx);
    cx.vars.borrow_mut().insert(t.clone(), ty.clone());
    let v = value_expr(e, cx);
    cx.pending.borrow_mut().push(store_stmt(&t, v));
    (t, ty)
}

/// `if COND { then } else { else }` as a VALUE. Each branch keeps its OWN
/// hoisted statements: Rust runs a combinator closure only on the branch
/// that takes it, so hoisting them in front of the If would run a side
/// effect the native program never runs.
fn branch_value(
    hint: &str,
    cond: String,
    then_f: impl FnOnce() -> Value,
    else_f: impl FnOnce() -> Value,
    cx: &Cx,
) -> Value {
    let base = cx.pending.borrow().len();
    let r = fresh_tmp(cx, hint);
    let tv = then_f();
    let mut then_stmts: Vec<Value> = cx.pending.borrow_mut().split_off(base);
    then_stmts.push(store_stmt(&r, tv));
    let ev = else_f();
    let mut else_stmts: Vec<Value> = cx.pending.borrow_mut().split_off(base);
    else_stmts.push(store_stmt(&r, ev));
    cx.pending.borrow_mut().push(json!({
        "cond": test_call(&cond),
        "then": then_stmts,
        "elsifs": [],
        "else": else_stmts,
        "type": "If",
    }));
    get_var(&r)
}

/// `|..| BODY` / `|x| BODY` only — and only with a single trailing
/// expression, because a statement body can carry `return`/`break` that
/// would bind to the CALLER's function, not to the closure.
fn closure_body(cl: &syn::ExprClosure) -> syn::Expr {
    match &*cl.body {
        syn::Expr::Block(b) => {
            let mut only: Option<&syn::Expr> = None;
            for st in &b.block.stmts {
                match st {
                    syn::Stmt::Expr(e, None) => {
                        if only.is_some() {
                            refuse("combinator closure with several statements", b.span());
                        }
                        only = Some(e);
                    }
                    other => refuse("combinator closure with a statement body", other.span()),
                }
            }
            match only {
                Some(e) => e.clone(),
                None => refuse("empty combinator closure body", b.span()),
            }
        }
        e => e.clone(),
    }
}

/// Rename every single-segment use of a closure parameter.
struct RenameIdent {
    from: String,
    to: String,
}

impl syn::visit_mut::VisitMut for RenameIdent {
    fn visit_expr_mut(&mut self, e: &mut syn::Expr) {
        if let syn::Expr::Path(pth) = e {
            if pth.qself.is_none() && pth.path.segments.len() == 1 {
                let seg = &mut pth.path.segments[0];
                if seg.ident == self.from && seg.arguments.is_none() {
                    seg.ident = syn::Ident::new(&self.to, seg.ident.span());
                }
            }
        }
        syn::visit_mut::visit_expr_mut(self, e);
    }
}

fn pat_binds(p: &syn::Pat, name: &str) -> bool {
    match p {
        syn::Pat::Ident(pi) => pi.ident == name,
        syn::Pat::Tuple(t) => t.elems.iter().any(|e| pat_binds(e, name)),
        syn::Pat::TupleStruct(ts) => ts.elems.iter().any(|e| pat_binds(e, name)),
        syn::Pat::Struct(st) => st.fields.iter().any(|f| pat_binds(&f.pat, name)),
        syn::Pat::Reference(r) => pat_binds(&r.pat, name),
        syn::Pat::Type(t) => pat_binds(&t.pat, name),
        syn::Pat::Or(o) => o.cases.iter().any(|c| pat_binds(c, name)),
        syn::Pat::Slice(sl) => sl.elems.iter().any(|e| pat_binds(e, name)),
        _ => false,
    }
}

/// Does the body introduce a NEW binding of `name` (a `let`, a nested
/// closure parameter)? Then the rename would capture it — refuse instead
/// of silently renaming the inner binding's uses.
struct Rebinds<'a> {
    name: &'a str,
    hit: bool,
}

impl<'a> syn::visit::Visit<'a> for Rebinds<'a> {
    fn visit_local(&mut self, l: &'a syn::Local) {
        if pat_binds(&l.pat, self.name) {
            self.hit = true;
        }
        syn::visit::visit_local(self, l);
    }
    fn visit_expr_closure(&mut self, c: &'a syn::ExprClosure) {
        if c.inputs.iter().any(|i| pat_binds(i, self.name)) {
            self.hit = true;
        }
        syn::visit::visit_expr_closure(self, c);
    }
    fn visit_item_fn(&mut self, _f: &'a syn::ItemFn) {
        self.hit = true;
    }
}

fn rebinds(body: &syn::Expr, name: &str) -> bool {
    let mut r = Rebinds { name, hit: false };
    syn::visit::Visit::visit_expr(&mut r, body);
    r.hit
}

/// Any `?` in the body? In Rust that returns from the CLOSURE, so inlining
/// it would return from the caller — a different program.
struct HasTry {
    hit: bool,
}

impl<'ast> syn::visit::Visit<'ast> for HasTry {
    fn visit_expr_try(&mut self, t: &'ast syn::ExprTry) {
        self.hit = true;
        syn::visit::visit_expr_try(self, t);
    }
}

fn closure_has_try(body: &syn::Expr) -> bool {
    let mut h = HasTry { hit: false };
    syn::visit::Visit::visit_expr(&mut h, body);
    h.hit
}

fn as_closure(e: &syn::Expr) -> &syn::ExprClosure {
    match e {
        syn::Expr::Closure(c) => c,
        other => refuse(
            "combinator argument (only a closure is representable)",
            other.span(),
        ),
    }
}

/// Inline a combinator closure: bind its parameter to a FRESH store temp
/// and return (temp, renamed body). `move`/async closures, patterns,
/// bodies that rebind the parameter and bodies using `?` refuse.
fn inline_closure(cl: &syn::ExprClosure, arg_ty: Ty, cx: &Cx) -> (String, syn::Expr) {
    use syn::visit_mut::VisitMut as _;
    if cl.asyncness.is_some() || cl.movability.is_some() || cl.capture.is_some() {
        refuse(
            "`move`/async combinator closure (capture semantics differ from an inlined body)",
            cl.span(),
        );
    }
    let mut body = closure_body(cl);
    if closure_has_try(&body) {
        refuse(
            "`?` inside a combinator closure (it returns from the CLOSURE, not the caller)",
            body.span(),
        );
    }
    let name = fresh_tmp(cx, "c");
    match cl.inputs.len() {
        0 => (name, body),
        1 => {
            let syn::Pat::Ident(pi) = &cl.inputs[0] else {
                refuse(
                    "closure parameter pattern (only a plain name is representable)",
                    cl.inputs[0].span(),
                );
            };
            if pi.by_ref.is_some() || pi.subpat.is_some() {
                refuse("closure parameter pattern", pi.span());
            }
            let pname = pi.ident.to_string();
            if rebinds(&body, &pname) {
                refuse(
                    "combinator closure that rebinds its own parameter",
                    body.span(),
                );
            }
            RenameIdent {
                from: pname,
                to: name.clone(),
            }
            .visit_expr_mut(&mut body);
            cx.vars.borrow_mut().insert(name.clone(), arg_ty);
            (name, body)
        }
        _ => refuse(
            "combinator closure with two parameters",
            cl.inputs[1].span(),
        ),
    }
}

/// The closure body's Ty, with the parameter bound (used by `infer_ty`).
fn closure_ret_ty(cl: &syn::ExprClosure, arg_ty: Ty, cx: &Cx) -> Ty {
    let (_, body) = inline_closure(cl, arg_ty, cx);
    infer_ty(&body, cx)
}

fn cond_value(hint: &str, cond: String, then_v: Value, else_v: Value, cx: &Cx) -> Value {
    let r = fresh_tmp(cx, hint);
    cx.pending.borrow_mut().push(json!({
        "cond": test_call(&cond),
        "then": [assign_stmt(&r, then_v)],
        "elsifs": [],
        "else": [assign_stmt(&r, else_v)],
        "type": "If",
    }));
    get_var(&r)
}

fn if_value_node(
    ie: &syn::ExprIf,
    target: BranchTarget,
    cx: &Cx,
    in_fn: bool,
    out: &mut Vec<Value>,
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
    // `if let` in a value/tail position: the pattern test replaces the
    // condition, and the scrutinee + binding hoist FIRST into `out` (so
    // both branches can see them — the same shape the statement form
    // emits).
    let cond = if let syn::Expr::Let(l) = &*ie.cond {
        let n = {
            let mut t = cx.tmp.borrow_mut();
            *t += 1;
            *t
        };
        let tmp = format!("__sh2if{n}");
        out.push(store_stmt(&tmp, value_expr(&l.expr, cx)));
        iflet_cond(&tmp, &l.pat, cx, out)
    } else {
        // the condition may need a hoist (`self.items.is_empty()`, a method
        // call) before the test grammar can read it — the same pre-pass the
        // statement form runs. An if evaluates its condition ONCE.
        let hoisted = hoist_cond_calls(&ie.cond, cx, out);
        test_call(&test_string(&hoisted, cx))
    };
    let mut then = Vec::new();
    push_branch(&ie.then_branch, target, cx, in_fn, &mut then);
    let else_ = match &ie.else_branch {
        None => refuse("if-expression without else", ie.span()),
        Some((_, inner)) => match &**inner {
            syn::Expr::If(inner_if) => vec![if_value_node(inner_if, target, cx, in_fn, out)],
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
/// Every `fn` item nested in `block` — at any depth of `if`/`while`/`for`/
/// `loop`/`match`/block — collected with the mangled name it is hoisted
/// under. A Rust `fn` item captures nothing, so hoisting it to the top level
/// preserves its semantics exactly; only the NAME has to be kept unique,
/// which the `<parent>__<local>` path does.
fn collect_nested_fns<'a>(
    block: &'a syn::Block,
    parent: &str,
    inherited: &HashMap<String, String>,
    acc: &mut Vec<(String, &'a syn::ItemFn)>,
    scopes: &mut HashMap<String, HashMap<String, String>>,
) {
    // every `fn` item of THIS block (at any nesting depth of if/for/…): they
    // are all visible to the whole block, so they share one scope
    let mut mine: Vec<String> = Vec::new();
    collect_fn_items(block, parent, acc, &mut mine);
    let mut scope = inherited.clone();
    for m in &mine {
        let local = m.rsplit_once("__").map(|x| x.1).unwrap_or(m).to_string();
        scope.insert(local, m.clone());
    }
    scopes.entry(parent.to_string()).or_insert_with(HashMap::new);
    scopes.insert(parent.to_string(), scope.clone());
    // each hoisted def's own body inherits this scope and adds its children
    let children: Vec<(String, &'a syn::ItemFn)> = acc
        .iter()
        .filter(|(n, _)| n.rsplit_once("__").map(|x| x.0 == parent).unwrap_or(false))
        .map(|(n, f)| (n.clone(), *f))
        .collect();
    for (name, f) in children {
        collect_nested_fns(&f.block, &name, &scope, acc, scopes);
    }
}

fn collect_fn_items<'a>(
    block: &'a syn::Block,
    parent: &str,
    acc: &mut Vec<(String, &'a syn::ItemFn)>,
    mine: &mut Vec<String>,
) {
    for stmt in &block.stmts {
        match stmt {
            syn::Stmt::Item(syn::Item::Fn(f)) => {
                let name = format!("{}__{}", parent, f.sig.ident);
                mine.push(name.clone());
                acc.push((name, f));
            }
            syn::Stmt::Expr(e, _) => collect_fn_items_expr(e, parent, acc, mine),
            syn::Stmt::Local(l) => {
                if let Some(init) = &l.init {
                    collect_fn_items_expr(&init.expr, parent, acc, mine);
                }
            }
            _ => {}
        }
    }
}

fn collect_fn_items_expr<'a>(
    e: &'a syn::Expr,
    parent: &str,
    acc: &mut Vec<(String, &'a syn::ItemFn)>,
    mine: &mut Vec<String>,
) {
    match e {
        syn::Expr::If(i) => {
            collect_fn_items(&i.then_branch, parent, acc, mine);
            if let Some((_, el)) = &i.else_branch {
                collect_fn_items_expr(el, parent, acc, mine);
            }
        }
        syn::Expr::While(w) => collect_fn_items(&w.body, parent, acc, mine),
        syn::Expr::ForLoop(f) => collect_fn_items(&f.body, parent, acc, mine),
        syn::Expr::Loop(l) => collect_fn_items(&l.body, parent, acc, mine),
        syn::Expr::Block(b) => collect_fn_items(&b.block, parent, acc, mine),
        syn::Expr::Match(m) => {
            for arm in &m.arms {
                collect_fn_items_expr(&arm.body, parent, acc, mine);
            }
        }
        _ => {}
    }
}

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
    // the enclosing impl type for `Self::m` resolution (None in a free fn)
    *cx.self_ty.borrow_mut() = self_ty.map(|s| s.to_string());
    // hoisted nested `fn`s visible in THIS body (its own plus the siblings
    // inherited from the enclosing scope): local name -> mangled def name
    {
        let frame = cx.fn_scopes.borrow().get(&name).cloned().unwrap_or_default();
        *cx.fn_alias.borrow_mut() = frame;
    }
    // LIFETIME generics erase unconditionally — they are borrow-checker
    // metadata with zero runtime meaning (`fn f<'a>(x: &'a T)` IS
    // `fn f(x: &T)` at runtime). Type/const generics refuse.
    let has_type_generics = sig.generics.params.iter().any(|p| {
        matches!(p, syn::GenericParam::Type(_) | syn::GenericParam::Const(_))
    });
    if has_type_generics {
        refuse("generic function (type/const parameters)", sig.generics.params.span());
    }
    if sig.generics.where_clause.is_some() {
        refuse("where clause on a function", sig.generics.where_clause.span());
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
    cx.guards.borrow_mut().clear();
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
        let pty = ty_from_type(&pt.ty, cx);
        // A `&mut` parameter is NOT bound from its positional (v0.41): the
        // caller owns the location — it writes the callee's slot before the
        // call (copy-in) and reads it back after (copy-out) — so binding here
        // would clobber it with the argument's text.
        if matches!(&*pt.ty, syn::Type::Reference(r) if r.mutability.is_some()) {
            cx.vars.borrow_mut().insert(pname.clone(), pty);
            continue;
        }
        // positional binding: param i <- getVar("i+1"), read as a VALUE for
        // a record parameter (`record_param_bind`) and as text otherwise.
        body.push(if is_record_ty(&pty) {
            record_param_bind(&pname, i + 1)
        } else {
            assign_stmt(&pname, get_var(&(i + 1).to_string()))
        });
        cx.vars.borrow_mut().insert(pname.clone(), pty);
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
                            syn::Expr::If(ie) => {
                                let mut hoist = Vec::new();
                                let node =
                                    if_value_node(ie, BranchTarget::Ret, cx, true, &mut hoist);
                                body.extend(hoist);
                                body.push(node);
                            }
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
                    lower_fn_stmt(stmt, cx, &mut body);
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
                    // the tail expression's hoisted statements (an
                    // atomic RMW's read-then-write, a guard read) run
                    // BEFORE the return that reads their temp
                    let tv = value_expr(e, cx);
                    let mut tp: Vec<Value> = cx.pending.borrow_mut().drain(..).collect();
                    body.append(&mut tp);
                    body.push(json!({"type": "Return", "value": tv}));
                } else {
                    lower_fn_stmt(stmt, cx, &mut body);
                }
                continue;
            }
        }
        lower_fn_stmt(stmt, cx, &mut body);
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

fn format_value(mac: &syn::Macro, cx: &Cx) -> Value {
    let fmt_args: FmtArgs = syn::parse2(mac.tokens.clone())
        .unwrap_or_else(|e| refuse(&format!("`format!` arguments: {e}"), mac.span()));
    let fmt = fmt_args.fmt.value();
    let mut parts: Vec<Value> = Vec::new();
    let mut lit = String::new();
    let mut ph = 0usize;
    let mut it = fmt.chars().peekable();
    while let Some(c) = it.next() {
        match c {
            '{' => {
                if it.peek() == Some(&'{') {
                    it.next();
                    lit.push('{');
                    continue;
                }
                let mut spec = String::new();
                let mut closed = false;
                for c2 in it.by_ref() {
                    if c2 == '}' {
                        closed = true;
                        break;
                    }
                    spec.push(c2);
                }
                if !closed {
                    refuse("unbalanced `{` in format!", fmt_args.fmt.span());
                }
                if !lit.is_empty() {
                    parts.push(json!({"kind": "lit", "text": std::mem::take(&mut lit)}));
                }
                let positional = spec.is_empty() || spec == ":?";
                if positional {
                    let a = fmt_args.args.get(ph).unwrap_or_else(|| {
                        refuse(
                            "fewer format! arguments than placeholders",
                            fmt_args.fmt.span(),
                        )
                    });
                    ph += 1;
                    if spec == ":?" && !matches!(infer_ty(a, cx), Ty::Int | Ty::Unknown) {
                        refuse(
                            "{:?} on a non-proven-integer in format! (Debug != Display)",
                            fmt_args.fmt.span(),
                        );
                    }
                    parts.push(json!({"kind": "expr", "expr": value_expr(a, cx)}));
                } else if !spec.contains(':') && spec.chars().all(|c| c.is_alphanumeric() || c == '_')
                {
                    if !cx.vars.borrow().contains_key(&spec) {
                        refuse("format! named argument (only local captures)", fmt_args.fmt.span());
                    }
                    parts.push(json!({"kind": "expr", "expr": get_var(&spec)}));
                } else {
                    refuse(
                        "format! spec (only {}, {:?} on ints, and {name} captures)",
                        fmt_args.fmt.span(),
                    );
                }
            }
            '}' => {
                if it.peek() == Some(&'}') {
                    it.next();
                    lit.push('}');
                } else {
                    refuse("unbalanced `}` in format!", fmt_args.fmt.span());
                }
            }
            other => lit.push(other),
        }
    }
    if ph != fmt_args.args.len() {
        refuse("format! argument count mismatch", fmt_args.fmt.span());
    }
    if !lit.is_empty() {
        parts.push(json!({"kind": "lit", "text": lit}));
    }
    if parts.is_empty() {
        return str_lit("");
    }
    json!({"type": "Interpolate", "parts": parts})
}

fn lower_print_macro(mac: &syn::Macro, cx: &Cx) -> Value {
    let (newline, to_stderr) =
        if mac.path.segments.len() == 1 && mac.path.leading_colon.is_none() {
            match mac.path.segments[0].ident.to_string().as_str() {
                "println" => (true, false),
                "print" => (false, false),
                "eprintln" => (true, true),
                // `eprint!` is the same channel WITHOUT the trailing newline,
                // and the runtime's only stderr member (`sh2.warn`) always
                // appends one; a raw `process.stderr.write` would break the
                // rule that rendered callees are whitelisted `sh2.<name>`
                // members. Refused rather than given a newline it does not
                // emit.
                "eprint" => refuse(
                    "`eprint!` (stderr with NO trailing newline: the runtime's `sh2.warn` always appends one)",
                    mac.path.span(),
                ),
                other => refuse(&format!("macro invocation `{other}!`"), mac.path.span()),
            }
        } else {
            refuse("qualified macro path", mac.path.span());
        };
    // stderr is a SEPARATE typed channel: the operand is the same formatted
    // value a stdout print would build, and `ErrLine` renders to the
    // runtime's `sh2.warn` (one line on stderr, stdout untouched).
    if to_stderr {
        let v = format_value(mac, cx);
        return expr_stmt(json!({"type": "ErrLine", "value": v}));
    }
    let fmt_args: FmtArgs = syn::parse2(mac.tokens.clone())
        .unwrap_or_else(|e| refuse(&format!("macro arguments: {e}"), mac.span()));
    let fmt = fmt_args.fmt.value();
    let (translated, n) =
        translate_format(&fmt).unwrap_or_else(|e| refuse(&e, fmt_args.fmt.span()));
    let args = &fmt_args.args;
    // {:?} placeholders are EXACT when their argument is a PROVEN integer
    // (Rust Debug == Display == decimal digits for ints). Strings gain
    // quotes/escapes under Debug — those refuse. Walk the format string,
    // mapping each placeholder group to its argument in order.
    {
        let mut chars = fmt.char_indices().peekable();
        let mut ph_index = 0usize;
        while let Some((i, c)) = chars.next() {
            match c {
                '{' => {
                    // capture the spec up to the matching '}'
                    let mut spec = String::new();
                    let mut closed = false;
                    for (_, c2) in chars.by_ref() {
                        if c2 == '}' {
                            closed = true;
                            break;
                        }
                        if c2 == '{' {
                            // "{{" escape inside scan: treat as literal
                            spec.clear();
                            closed = false;
                            break;
                        }
                        spec.push(c2);
                    }
                    if !closed {
                        break; // unbalanced; translate_format refuses later
                    }
                    if spec.is_empty() || spec == ":?" {
                        let arg_ty = args
                            .get(ph_index)
                            .map(|a| infer_ty(a, cx))
                            .unwrap_or(Ty::Unknown);
                        if spec == ":?" && !matches!(arg_ty, Ty::Int | Ty::Unknown) {
                            refuse(
                                "{:?} on a non-proven-integer argument (Debug != Display)",
                                fmt_args.fmt.span(),
                            );
                        }
                        if spec.is_empty() {
                            // plain {}: any proven type renders via %s;
                            // unproven args were already the norm here
                        }
                        ph_index += 1;
                    } else if spec.starts_with(':') {
                        // other typed specs (width/precision/fill) refuse
                        // EXCEPT :? handled above
                        refuse(
                            &format!("format specifier '{{:{}}}' unsupported", spec),
                            fmt_args.fmt.span(),
                        );
                    }
                }

                _ => {}
            }
        }
    }
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
                    if inner.is_empty() || inner == ":?" {
                        // {} and {:?} both position into the arg list;
                        // {:?} renders EXACTLY like {} when the argument
                        // is a proven integer (Debug == Display == decimal)
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
            // a char is a ONE-CHARACTER string in the store
            syn::Lit::Char(lc) => str_lit(&lc.value().to_string()),
            other => refuse("literal type (only integers, strings, chars, bools)", other.span()),
        },
        syn::Expr::Path(p) => {
            // `Color::Red` — a UNIT-variant value lowers to its qualified
            // tag string (matching is string equality). Tuple/struct
            // variants carry payloads and refuse until record storage.
            if p.qself.is_none() && p.path.leading_colon.is_none() && p.path.segments.len() == 2 {
                let e = p.path.segments[0].ident.to_string();
                let v = p.path.segments[1].ident.to_string();
                if cx.ambiguous.contains(&e) {
                    refuse("ambiguous enum name (declared with different shapes)", p.span());
                }
                if cx.enums.iter().any(|x| *x == e) {
                    if !p.path.segments[1].arguments.is_none() {
                        refuse("enum variant with payload", p.path.segments[1].span());
                    }
                    return str_lit(&format!("{e}::{v}"));
                }
            }
            // a FUNCTION ITEM used as a value (`transforms::g`) lowers
            // to its registered name string — indirect calls dispatch
            // by that name
            if p.qself.is_none()
                && p.path.leading_colon.is_none()
                && p.path.segments.len() >= 2
                && p.path.segments.iter().all(|s| s.arguments.is_none())
            {
                let last = p.path.segments.last().unwrap().ident.to_string();
                if cx.fns.iter().any(|f| *f == last) {
                    return str_lit(&last);
                }
            }
            // `i64::MAX` / `u128::MIN` … — primitive associated constants
            // are compile-time integers. The store is a decimal string, so
            // the EXACT value is emitted (i128/u128 exceed i64; any later
            // arithmetic use refuses at its own site rather than wrap).
            if let Some(v) = int_assoc_const_path(p) {
                return str_lit(&v);
            }
            let Some(name) = single_path(p) else {
                refuse("path expression", p.span());
            };
            // a guard ALIAS reads the static's slot (a scalar; a
            // collection has no value form)
            if let Some((s, inner)) = cx.guards.borrow().get(&name.to_string()).cloned() {
                if is_scalar_store_ty(&inner) {
                    return get_var(&s);
                }
                refuse(
                    "static collection has no value form (use its operations)",
                    p.span(),
                );
            }
            // BARE `None` is the Option layer's ABSENT marker, whose store
            // value is the empty string (the None≡"" convention — the same
            // one `?`, Ty::Opt and the option methods use). It is NOT a
            // variable read: `getVar("None")` would return "" only by
            // accident of an unset name, and would silently read a user
            // variable called `None` if one existed.
            if name == "None" {
                return str_lit("");
            }
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
            } else if arith_contains_field(e) {
                // a call operand must run ONCE, before the arithmetic; its
                // temp goes to `pending`, which the enclosing statement
                // lowering flushes ahead of the statement that reads it
                let mut buf: Vec<Value> = Vec::new();
                let ast = {
                    let mut ctr = cx.tmp.borrow_mut();
                    hoisted_arith(e, cx, &mut buf, &mut ctr)
                };
                cx.pending.borrow_mut().extend(buf);
                arith_value(ast)
            } else {
                // multi-operator arithmetic composes RAW nodes; the outer
                // Arith wrapper goes here exactly once
                arith_value(arith_from_binary(b, cx))
            }
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => test_call(&test_string(e, cx)),
            // `*r` — a deref READ: the store holds values (no pointers),
            // so the deref is the value itself
            syn::UnOp::Deref(_) => value_expr(&u.expr, cx),
            _ => arith_value(arith_from_unary(u, cx)),
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
            // `matches!(v, PAT)` in VALUE position is a bool. Lower it to
            // a test call over the pattern — the SAME conversion the
            // condition path and the match lowering use (matches_cond /
            // arm_cond), so they can never disagree. The result is a
            // native JS boolean via the test builtin, which prints like
            // Rust's `{}` for bool.
            if name == "matches" {
                let cond_s = matches_cond(m.mac.tokens.clone(), m.span(), cx).unwrap_or_else(|| {
                    refuse(
                        "`matches!` pattern (only unit variants, or-patterns of them, and `_`)",
                        m.span(),
                    )
                });
                return test_call(&cond_s);
            }
            // `format!("..")` in VALUE position is a string built from
            // literal segments and argument values (the Interpolate node
            // `$a$b` lowers to). Exact for `{}`, `{:?}` on proven ints and
            // `{name}` captures of locals; width/alignment specs refuse.
            if name == "format" {
                return format_value(&m.mac, cx);
            }
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
            let enum_variant = enum_variant_of_path(&se.path, cx);
            let mut props: Vec<Value> = Vec::new();
            if let Some((en, vn)) = &enum_variant {
                props.push(json!({
                    "key": "tag",
                    "value": str_lit(&format!("{en}::{vn}")),
                }));
                match cx.variants.get(en).and_then(|m| m.get(vn)) {
                    Some(VariantShape::Struct(fs)) => {
                        for f in &se.fields {
                            if let syn::Member::Named(id) = &f.member {
                                if !fs.iter().any(|(n, _)| n == &id.to_string()) {
                                    refuse("unknown enum variant field", id.span());
                                }
                            }
                        }
                    }
                    _ => refuse("struct-literal form for a non-struct variant", se.span()),
                }
            }
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
        syn::Expr::Tuple(tup) => {
            // `(a, b)` -> a native array (the store's tuple value);
            // arity is tracked for `.N` bound checks
            json!({
                "type": "Array",
                "elements": tup.elems.iter().map(|e| value_expr(e, cx)).collect::<Vec<_>>(),
            })
        }
        syn::Expr::Index(ix) => {
            // `m[&k]` on a MAP reads the associative store.
            if matches!(infer_ty(&ix.expr, cx), Ty::Map(..)) {
                let Some(vname) = recv_var_name(&ix.expr) else {
                    refuse("map index on a non-variable map", ix.expr.span());
                };
                return assoc_call(
                    "assocGet",
                    vec![str_lit(&vname), value_expr(&ix.index, cx)],
                );
            }
            // `v[i]` on a proven native-array variable: an A1 Index expr,
            // which the core renders as the trusted arrayIndex read
            // (getVar would give the scalar view).
            if !matches!(infer_ty(&ix.expr, cx), Ty::Arr(..)) {
                refuse("indexed read on an unproven array", ix.expr.span());
            }
            let Some(key) = index_key_expr(&ix.index) else {
                refuse("array index (only integer literals and variables)", ix.index.span());
            };
            match &*ix.expr {
                syn::Expr::Path(p) => {
                    let Some(name) = single_path(p) else {
                        refuse("indexed read on a non-variable array", p.span());
                    };
                    json!({"type": "Index", "var": name.to_string(), "key": key})
                }
                // A FIELD array / other composite expression: a computed
                // native member read on the raw value (FieldRead returns
                // the actual native array, so `arr[i]` is its element).
                other => json!({
                    "type": "ElementRead",
                    "coll": value_expr(other, cx),
                    "key": key,
                }),
            }
        }
        syn::Expr::Field(f) => {
            // NUMERIC members (`t.0`) on a tracked ARRAY variable read
            // elements via the arrayIndex builtin — getVar returns the
            // scalar view of arrays, so a plain read would be wrong.
            // NAMED members use the FieldRead ext node (property read).
            if let syn::Member::Unnamed(ix) = &f.member {
                let recv_ty = infer_ty(&f.base, cx);
                if matches!(recv_ty, Ty::Arr(..)) {
                    if let Some(syn::Expr::Path(p)) = Some(&*f.base) {
                        if let Some(vname) = single_path(p) {
                            return json!({
                                "args": [
                                    str_lit(&vname.to_string()),
                                    {"type": "Arith", "ast": {"type": "Num", "value": ix.index as i64}},
                                ],
                                "func": "arrayIndex", "purity": "PureCpu", "type": "Call"
                            });
                        }
                    }
                }
                refuse("tuple-index read on an untracked receiver", f.span());
            }
            let name = match &f.member {
                syn::Member::Named(ident) => ident.to_string(),
                syn::Member::Unnamed(_) => unreachable!("handled above"),
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
            // A static's VALUE through its guard (`*NOCASEMATCH.lock().
            // unwrap()`): the store read of the static's slot. A static
            // COLLECTION has no value form (it is a named assoc store).
            if let Some((sname, inner)) = static_guard(e, cx) {
                if is_scalar_store_ty(&inner) {
                    return get_var(&sname);
                }
                refuse(
                    "static collection has no value form (use its operations)",
                    m.span(),
                );
            }
            // `SRC.iter().any(|e| COND)` / `.all(..)` in VALUE position:
            // the bounded loop's setup + iterations hoist into `pending`
            // (evaluated once, in source order, before the statement that
            // reads the result)
            if let Some((all, src, cl)) = quantifier_parts(e, cx) {
                let mut pre: Vec<Value> = Vec::new();
                let r = quantifier_stmts(all, src, cl, cx, &mut pre);
                for st in pre {
                    cx.pending.borrow_mut().push(st);
                }
                return get_var(&r);
            }
            // a method ON a static guard is the static's OWN operation
            // (insert answers "was absent", remove the previous value)
            if let Some((sname, inner)) = static_guard(&m.receiver, cx) {
                // a scalar guard's `get()` (a Cell closure param, a
                // MutexGuard read) is the store read
                if rust_name == "get" && m.args.is_empty() && is_scalar_store_ty(&inner) {
                    return get_var(&sname);
                }
                if let Some(v) = static_guard_method(&sname, &inner, m, cx) {
                    return v;
                }
            }
            // `NAME.with(|c| ..)` on a Cell static
            if rust_name == "with" && m.args.len() == 1 {
                if let syn::Expr::Path(p) = &*m.receiver {
                    if let Some(n) = single_path(p) {
                        let n = n.to_string();
                        if let Some(Ty::Cel(inner)) = cx.statics.borrow().get(&n).cloned() {
                            if let Some(v) = cell_with(&n, &inner, &m.args[0], cx) {
                                return v;
                            }
                        }
                    }
                }
            }
            // ATOMIC statics: single-threaded, so a load IS the store read
            // and an RMW IS read-then-assign (the previous value returned)
            if let syn::Expr::Path(p) = &*m.receiver {
                if let Some(n) = single_path(p).map(|i| i.to_string()) {
                    if cx.statics.borrow().contains_key(&n) {
                        match rust_name.as_str() {
                            "load" => return get_var(&n),
                            "fetch_add" | "fetch_sub" if !m.args.is_empty() => {
                                let old = fresh_tmp(cx, "p");
                                cx.pending.borrow_mut().push(assign_stmt(&old, get_var(&n)));
                                cx.vars.borrow_mut().insert(old.clone(), Ty::Int);
                                let op = if rust_name == "fetch_add" { "+" } else { "-" };
                                let nv = arith_bin(op, arith_var(&n), arith_expr(&m.args[0], cx));
                                cx.pending.borrow_mut().push(assign_stmt(&n, nv));
                                return get_var(&old);
                            }
                            "swap" if !m.args.is_empty() => {
                                let old = fresh_tmp(cx, "p");
                                cx.pending.borrow_mut().push(assign_stmt(&old, get_var(&n)));
                                cx.vars.borrow_mut().insert(old.clone(), Ty::Int);
                                let nv = value_expr(&m.args[0], cx);
                                cx.pending.borrow_mut().push(assign_stmt(&n, nv));
                                return get_var(&old);
                            }
                            _ => {}
                        }
                    }
                }
            }
            // MAP / SET methods on a NAMED associative store. `insert`
            // returns Option in Rust; it is lowered to the assocSet call
            // (exact when used as a statement, which is how it appears).
            if matches!(recv_ty, Ty::Map(..) | Ty::Set(..)) {
                // a COLLECTION has no value form: `.clone()` of a named
                // assoc would need a deep copy under a new name, and
                // `.iter()`'s order is the store's, not the set's
                if rust_name == "clone" || rust_name == "iter" {
                    refuse(
                        "clone/iter of a collection (a named assoc has no exact value copy, and its order is not the set's)",
                        m.span(),
                    );
                }
                let Some(vname) = recv_var_name(&m.receiver) else {
                    refuse("map/set method on a non-variable receiver", m.receiver.span());
                };
                let is_set = matches!(recv_ty, Ty::Set(..));
                match rust_name.as_str() {
                    "insert" if is_set => {
                        if m.args.len() != 1 {
                            refuse("set insert arity", m.span());
                        }
                        return assoc_call(
                            "assocSet",
                            vec![
                                str_lit(&vname),
                                value_expr(&m.args[0], cx),
                                str_lit("1"),
                            ],
                        );
                    }
                    "insert" => {
                        if m.args.len() != 2 {
                            refuse("map insert arity", m.span());
                        }
                        return assoc_call(
                            "assocSet",
                            vec![
                                str_lit(&vname),
                                value_expr(&m.args[0], cx),
                                value_expr(&m.args[1], cx),
                            ],
                        );
                    }
                    "get" if !is_set => {
                        if m.args.len() != 1 {
                            refuse("map get arity", m.span());
                        }
                        return assoc_call(
                            "assocGet",
                            vec![str_lit(&vname), value_expr(&m.args[0], cx)],
                        );
                    }
                    "contains_key" if !is_set => {
                        if m.args.len() != 1 {
                            refuse("contains_key arity", m.span());
                        }
                        return assoc_call(
                            "assocHas",
                            vec![str_lit(&vname), value_expr(&m.args[0], cx)],
                        );
                    }
                    "contains" if is_set => {
                        if m.args.len() != 1 {
                            refuse("set contains arity", m.span());
                        }
                        return assoc_call(
                            "assocHas",
                            vec![str_lit(&vname), value_expr(&m.args[0], cx)],
                        );
                    }
                    "remove" => {
                        if m.args.len() != 1 {
                            refuse("map/set remove arity", m.span());
                        }
                        return assoc_call(
                            "assocDel",
                            vec![str_lit(&vname), value_expr(&m.args[0], cx)],
                        );
                    }
                    "len" => return array_len_call(&vname),
                    "is_empty" => {
                        let g = fresh_tmp(cx, "n");
                        cx.pending
                            .borrow_mut()
                            .push(assign_stmt(&g, array_len_call(&vname)));
                        cx.vars.borrow_mut().insert(g.clone(), Ty::Int);
                        return cond_value(
                            "c",
                            format!("${g} -eq 0"),
                            str_lit("true"),
                            str_lit("false"),
                            cx,
                        );
                    }
                    "keys" | "values" | "iter" => refuse(
                        "map/set iterator outside a for-loop",
                        m.span(),
                    ),
                    _ => refuse(
                        &format!("map/set method `{}`", rust_name),
                        m.method.span(),
                    ),
                }
            }
            // `s.split(SEP)` — the runtime strSplit returns a LIST id
            // (raw elements via listGet/listLen). The separator must be a
            // literal char/string: a dynamic separator would need the
            // runtime's regex/word-splitting rules.
            if rust_name == "split" {
                if !matches!(recv_ty, Ty::Str) {
                    refuse("split on a non-string receiver", m.receiver.span());
                }
                if m.args.len() != 1 {
                    refuse("split with an argument count other than one", m.span());
                }
                let sep = match &m.args[0] {
                    syn::Expr::Lit(l) => match &l.lit {
                        syn::Lit::Str(s) => s.value(),
                        syn::Lit::Char(c) => c.value().to_string(),
                        _ => refuse("split separator (literal string or char)", l.span()),
                    },
                    other => refuse("split separator (literal string or char)", other.span()),
                };
                return json!({
                    "args": [value_expr(&m.receiver, cx), str_lit(&sep)],
                    "func": "strSplit", "purity": "PureCpu", "type": "Call"
                });
            }
            if rust_name == "clone" {
                // x.clone() under the Rust Clone bound NEVER aliases —
                // an independent deep copy is exact for every store
                // value (the CloneDeep drop-in; structuredClone in JS).
                if !m.args.is_empty() {
                    refuse("clone with arguments", m.span());
                }
                return json!({
                    "type": "CloneDeep",
                    "value": value_expr(&m.receiver, cx),
                });
            }
            // `.contains(&x)` on a LITERAL const array -> an exact
            // OR-equality chain over the known contents ($x = ".." -o ..)
            let recv_name = match &*m.receiver {
                syn::Expr::Path(p) => single_path(p).map(|i| i.to_string()),
                _ => None,
            };
            if rust_name == "contains" {
                if let (Some(_), Some(lits)) = (
                    recv_name.as_ref(),
                    recv_name.as_ref().and_then(|n| cx.const_arrays.get(n)),
                ) {
                    if !m.args.is_empty() {
                        let arg_val = match &m.args[0] {
                            syn::Expr::Reference(r) => &r.expr,
                            a => a,
                        };
                        let opnd = operand(arg_val, cx, true);
                        let cond = lits
                            .iter()
                            .map(|lit| format!("{opnd} = \"{}\"", lit))
                            .collect::<Vec<_>>()
                            .join(" -o ");
                        return test_call(&cond);
                    }
                }
            }
            // Vec::len() on a PROVEN native-array variable: the trusted
            // runtime length (arrayLen reads `this.arrays`, unlike the
            // scalar param("len") fold that miscounted an array value).
            // Result::ok() / Option::as_ref() / String::as_str() — the
            // store has one absent marker (`""`) for both Result and
            // Option, so these conversions are the value itself. The same
            // holds for `to_string`: Display of every store type IS its
            // stored text (a record has no Display in Rust, so it cannot
            // appear here).
            if matches!(
                rust_name.as_str(),
                "ok" | "as_str" | "as_deref" | "as_ref" | "as_slice" | "to_string"
            ) && m.args.is_empty()
            {
                return value_expr(&m.receiver, cx);
            }
            // `v.get(i)` — Some(element) or the absent marker; arrayIndex
            // is exactly `(arr[i] !== undefined ? arr[i] : "")`.
            if rust_name == "get" && m.args.len() == 1 && matches!(recv_ty, Ty::Arr(..)) {
                let key = value_expr(&m.args[0], cx);
                if let Some(name) = recv_var_name(&m.receiver) {
                    return json!({
                        "type": "Call", "func": "arrayIndex", "purity": "PureCpu",
                        "args": [str_lit(&name), key]
                    });
                }
                return json!({
                    "type": "ElementRead",
                    "coll": value_expr(&m.receiver, cx),
                    "key": key,
                });
            }
            // `v.first()` — the first element or the absent marker.
            if rust_name == "first" && m.args.is_empty() {
                if matches!(recv_ty, Ty::Arr(..)) {
                    let zero = json!({"type": "Arith", "ast": {"type": "Num", "value": 0}});
                    if let Some(name) = recv_var_name(&m.receiver) {
                        return json!({
                            "type": "Call", "func": "arrayIndex", "purity": "PureCpu",
                            "args": [str_lit(&name), zero]
                        });
                    }
                    return json!({
                        "type": "ElementRead",
                        "coll": value_expr(&m.receiver, cx),
                        "key": zero,
                    });
                }
            }
            // `v.last()` — mirrors `first`: the last element when the
            // array is non-empty, the absent marker when it is empty (the
            // length is read ONCE and the read is guarded, so no index can
            // run past either end).
            if rust_name == "last" && m.args.is_empty() {
                if matches!(recv_ty, Ty::Arr(..)) {
                    let rname = match recv_var_name(&m.receiver) {
                        Some(name) => name,
                        None => {
                            let t = fresh_tmp(cx, "av");
                            let v = value_expr(&m.receiver, cx);
                            cx.pending.borrow_mut().push(assign_stmt(&t, v));
                            cx.vars.borrow_mut().insert(
                                t.clone(),
                                Ty::Arr(Some(Box::new(Ty::Str)), None),
                            );
                            t
                        }
                    };
                    let n = fresh_tmp(cx, "ln");
                    let len_v = array_len_call(&rname);
                    cx.pending.borrow_mut().push(assign_stmt(&n, len_v));
                    cx.vars.borrow_mut().insert(n.clone(), Ty::Int);
                    let idx = fresh_tmp(cx, "li");
                    cx.pending.borrow_mut().push(assign_stmt(
                        &idx,
                        arith_bin("-", arith_var(&n), arith_num(1)),
                    ));
                    cx.vars.borrow_mut().insert(idx.clone(), Ty::Int);
                    let elem = json!({
                        "type": "Call", "func": "arrayIndex", "purity": "PureCpu",
                        "args": [str_lit(&rname), get_var(&idx)]
                    });
                    return branch_value(
                        "lv",
                        format!("${n} -gt 0"),
                        || elem,
                        || str_lit(""),
                        cx,
                    );
                }
            }
            // `it.nth(K)` — on the process-argument snapshot, the only
            // Arr-typed "iterator" this frontend produces (a Vec has no
            // `.nth`, so a compiling program that reaches here is indexing the
            // immutable argument vector). A CONSTANT K is a guarded index: the
            // length is read once and the element read only when `K < len`,
            // with the absent marker otherwise — no iterator position exists
            // to consume, because the snapshot is a fresh array.
            if rust_name == "nth" && m.args.len() == 1 && matches!(recv_ty, Ty::Arr(..)) {
                match &m.args[0] {
                    lit @ syn::Expr::Lit(syn::ExprLit {
                        lit: syn::Lit::Int(_), ..
                    }) => {
                        let k = int_literal(lit, cx);
                        // arrayLen/arrayIndex read the store's ARRAY REGISTRY by
                        // name, not a value, so a snapshot expression is bound to
                        // a temp first (assigning an array value registers it,
                        // which is what makes `let args = env::args(); args.len()`
                        // work).
                        let name = match recv_var_name(&m.receiver) {
                            Some(name) => name,
                            None => {
                                let t = fresh_tmp(cx, "av");
                                let v = value_expr(&m.receiver, cx);
                                cx.pending.borrow_mut().push(assign_stmt(&t, v));
                                cx.vars.borrow_mut().insert(t.clone(), Ty::Arr(Some(Box::new(Ty::Str)), None));
                                t
                            }
                        };
                        let n = fresh_tmp(cx, "nn");
                        let len_v = array_len_call(&name);
                        cx.pending.borrow_mut().push(assign_stmt(&n, len_v));
                        cx.vars.borrow_mut().insert(n.clone(), Ty::Int);
                        let elem = json!({
                            "type": "Call", "func": "arrayIndex", "purity": "PureCpu",
                            "args": [str_lit(&name), str_lit(&k.to_string())]
                        });
                        return branch_value(
                            "nth",
                            format!("${n} -gt {k}"),
                            || elem,
                            || str_lit(""),
                            cx,
                        );
                    }
                    other => refuse(
                        "`nth(K)` with a non-constant K (only a literal index has a proven guard)",
                        other.span(),
                    ),
                }
            }
            // `s.is_empty()` — exact for every encoding: a string is empty
            // iff it carries no bytes, which is iff its stored text is "".
            if rust_name == "is_empty" && m.args.is_empty() && matches!(recv_ty, Ty::Str) {
                let t = fresh_tmp(cx, "se");
                let sv = value_expr(&m.receiver, cx);
                cx.pending.borrow_mut().push(store_stmt(&t, sv));
                let r = cond_value(
                    "ie",
                    format!("\"${t}\" = \"\""),
                    str_lit("true"),
                    str_lit("false"),
                    cx,
                );
                cx.vars.borrow_mut().insert(t, Ty::Str);
                return r;
            }
            if rust_name == "len" && m.args.is_empty() {
                if matches!(recv_ty, Ty::Arr(..)) {
                    if let syn::Expr::Path(p) = &*m.receiver {
                        if let Some(n) = single_path(p) {
                            return json!({
                                "args": [str_lit(&n.to_string())],
                                "func": "arrayLen", "purity": "PureCpu", "type": "Call"
                            });
                        }
                    }
                    // A FIELD array / slice expression: `.length` on the
                    // raw native array is the same trusted length.
                    return json!({
                        "type": "FieldRead",
                        "object": value_expr(&m.receiver, cx),
                        "name": "length",
                    });
                }
                refuse("len on a non-array receiver", m.method.span());
            }
            if rust_name == "load" {
                // `flag.load(Ordering::..)` on an AtomicBool — a plain
                // variable read (single-threaded runtime: exact)
                if let Ty::AtomicBool = recv_ty {
                    let Some(syn::Expr::Path(p)) = Some(m.receiver.as_ref()) else {
                        refuse("atomic load target path", m.receiver.span());
                    };
                    let Some(name) = single_path(p) else {
                        refuse("atomic load target path", p.span());
                    };
                    return get_var(&name.to_string());
                }
            }
            if let Ty::Struct(ty_name) = &recv_ty {
                // inherent method dispatch: `obj.m(args)` ->
                // fnValue("Type_m", [obj, args...]) — the mangled def's
                // first positional IS the receiver object (its `self`).
                // NOTE: an ENUM receiver stays refused below: a by-value
                // enum method would bind `self` into the shared store
                // (FRONTEND.md "KNOWN LIMITATION: params share the global
                // store"), and every method call inside the callee would
                // then clobber the caller's `self` — not exact.
                let target = format!("{}_{}", ty_name, rust_name);
                if !cx.fns.iter().any(|f| *f == target) {
                    refuse(&format!("no method `{}` on struct `{}`", rust_name, ty_name),
                           m.method.span());
                }
                let mut call_args: Vec<syn::Expr> = vec![(*m.receiver).clone()];
                call_args.extend(m.args.iter().cloned());
                return fn_call_expr_values(&target, call_args, "fnValue", cx);
            }
            // OPTION methods: under the None≡"" store convention,
            // as_deref / unwrap_or("") / unwrap_or_default are ALL the
            // plain variable read (Some(v) -> v, None -> ""). Exact when
            // no code distinguishes Some("") from None.
            // a CALL receiver whose registered fn returns Opt also
            // carries the option type (`echo(1).unwrap_or("")`)
            let recv_ty = match (&recv_ty, &*m.receiver) {
                (Ty::Unknown, syn::Expr::Call(c)) => match &*c.func {
                    syn::Expr::Path(p2) => single_path(p2)
                        .and_then(|id| cx.fn_rets.get(&id.to_string()).cloned())
                        .unwrap_or(Ty::Unknown),
                    _ => Ty::Unknown,
                },
                _ => recv_ty,
            };
            if matches!(recv_ty, Ty::Opt(_)) {
                match rust_name.as_str() {
                    // `unwrap()` is the same value read as the family
                    // above: Some(v) -> v. The None branch PANICS in Rust
                    // (no defined stdout), so the store's absent marker
                    // ("") never has to model an output.
                    "as_deref" | "unwrap" => {
                        if !m.args.is_empty() {
                            refuse("option method with arguments", m.span());
                        }
                        return value_expr(&m.receiver, cx);
                    }
                    // `Option<&T>::copied()` is the Option<T> of the same
                    // value: the store has no reference layer, so the read
                    // is the value.
                    "copied" | "cloned" => {
                        if !m.args.is_empty() {
                            refuse("option method with arguments", m.span());
                        }
                        return value_expr(&m.receiver, cx);
                    }
                    // `x.unwrap_or(d)` — d is an ARGUMENT, so Rust
                    // evaluates it eagerly, present or not. The receiver
                    // evaluates ONCE into a temp; the choice is a branch.
                    "unwrap_or" if m.args.len() == 1 => {
                        let (t, _) = recv_tmp(&m.receiver, cx);
                        let d = fresh_tmp(cx, "d");
                        let dv = value_expr(&m.args[0], cx);
                        cx.pending.borrow_mut().push(store_stmt(&d, dv));
                        return branch_value(
                            "u",
                            format!("\"${t}\" != \"\""),
                            || get_var(&t),
                            || get_var(&d),
                            cx,
                        );
                    }
                    // `x.unwrap_or_else(|| d)` — the closure runs ONLY when
                    // absent, so its statements stay in the else branch
                    "unwrap_or_else" if m.args.len() == 1 => {
                        let cl = as_closure(&m.args[0]);
                        // On a RESULT this closure receives the ERROR
                        // payload (on an Option it takes no argument):
                        // that value is not in the store model, so it
                        // refuses rather than dropping it.
                        if !cl.inputs.is_empty() {
                            refuse(
                                "unwrap_or_else closure with a parameter (a Result's ERROR payload is not representable)",
                                cl.span(),
                            );
                        }
                        let (t, _) = recv_tmp(&m.receiver, cx);
                        let (_, body) = inline_closure(cl, Ty::Unknown, cx);
                        return branch_value(
                            "u",
                            format!("\"${t}\" != \"\""),
                            || get_var(&t),
                            || value_expr(&body, cx),
                            cx,
                        );
                    }
                    // `x.unwrap_or_default()` — the TYPE's zero value (an
                    // `Option<i64>` is 0, not the absent marker)
                    "unwrap_or_default" if m.args.is_empty() => {
                        let Ty::Opt(inner) = &recv_ty else {
                            refuse("unwrap_or_default on a non-Option", m.span());
                        };
                        let z = static_zero(inner);
                        let (t, _) = recv_tmp(&m.receiver, cx);
                        return branch_value(
                            "u",
                            format!("\"${t}\" != \"\""),
                            || get_var(&t),
                            || z,
                            cx,
                        );
                    }
                    // `x.map(f)` — only the present branch runs f
                    "map" if m.args.len() == 1 => {
                        let (t, rty) = recv_tmp(&m.receiver, cx);
                        let Ty::Opt(inner) = rty else {
                            refuse("map on a non-Option receiver", m.span());
                        };
                        let cl = as_closure(&m.args[0]);
                        let (p, body) = inline_closure(cl, (*inner).clone(), cx);
                        return branch_value(
                            "m",
                            format!("\"${t}\" != \"\""),
                            || {
                                cx.pending.borrow_mut().push(store_stmt(&p, get_var(&t)));
                                value_expr(&body, cx)
                            },
                            || str_lit(""),
                            cx,
                        );
                    }
                    // `x.and_then(f)` — f answers an Option, so its absent
                    // marker propagates unchanged
                    "and_then" if m.args.len() == 1 => {
                        let (t, rty) = recv_tmp(&m.receiver, cx);
                        let Ty::Opt(inner) = rty else {
                            refuse("and_then on a non-Option receiver", m.span());
                        };
                        let cl = as_closure(&m.args[0]);
                        let (p, body) = inline_closure(cl, (*inner).clone(), cx);
                        return branch_value(
                            "m",
                            format!("\"${t}\" != \"\""),
                            || {
                                cx.pending.borrow_mut().push(store_stmt(&p, get_var(&t)));
                                value_expr(&body, cx)
                            },
                            || str_lit(""),
                            cx,
                        );
                    }
                    // `x.or_else(|| alt)` — lazy: the closure is the ABSENT
                    // branch, and it answers an Option
                    "or_else" if m.args.len() == 1 => {
                        let (t, _) = recv_tmp(&m.receiver, cx);
                        let cl = as_closure(&m.args[0]);
                        let (_, body) = inline_closure(cl, Ty::Unknown, cx);
                        return branch_value(
                            "u",
                            format!("\"${t}\" != \"\""),
                            || get_var(&t),
                            || value_expr(&body, cx),
                            cx,
                        );
                    }
                    // `x.map_or(d, f)` — d eagerly (an argument), then f
                    // only when present: exactly `map(f).unwrap_or(d)`
                    "map_or" if m.args.len() == 2 => {
                        let (t, rty) = recv_tmp(&m.receiver, cx);
                        let Ty::Opt(inner) = rty else {
                            refuse("map_or on a non-Option receiver", m.span());
                        };
                        let d = fresh_tmp(cx, "d");
                        let dv = value_expr(&m.args[0], cx);
                        cx.pending.borrow_mut().push(store_stmt(&d, dv));
                        let cl = as_closure(&m.args[1]);
                        let (p, body) = inline_closure(cl, (*inner).clone(), cx);
                        return branch_value(
                            "m",
                            format!("\"${t}\" != \"\""),
                            || {
                                cx.pending.borrow_mut().push(store_stmt(&p, get_var(&t)));
                                value_expr(&body, cx)
                            },
                            || get_var(&d),
                            cx,
                        );
                    }
                    // `x.is_some_and(f)` — present AND f(v): the predicate
                    // evaluates only in the present branch
                    "is_some_and" | "is_ok_and" if m.args.len() == 1 => {
                        let (t, rty) = recv_tmp(&m.receiver, cx);
                        let Ty::Opt(inner) = rty else {
                            refuse("is_some_and on a non-Option receiver", m.span());
                        };
                        let cl = as_closure(&m.args[0]);
                        let (p, body) = inline_closure(cl, (*inner).clone(), cx);
                        return branch_value(
                            "p",
                            format!("\"${t}\" != \"\""),
                            || {
                                cx.pending.borrow_mut().push(store_stmt(&p, get_var(&t)));
                                let mut pre: Vec<Value> = Vec::new();
                                let ce = hoist_cond_calls(&body, cx, &mut pre);
                                for st in pre {
                                    cx.pending.borrow_mut().push(st);
                                }
                                cond_value(
                                    "b",
                                    test_string(&ce, cx),
                                    str_lit("true"),
                                    str_lit("false"),
                                    cx,
                                )
                            },
                            || str_lit("false"),
                            cx,
                        );
                    }
                    // `x.flatten()` — Option<Option<T>> collapses to the
                    // inner absent marker (identity under the convention)
                    "flatten" if m.args.is_empty() => {
                        let Ty::Opt(inner) = &recv_ty else {
                            refuse("flatten on a non-Option receiver", m.span());
                        };
                        if !matches!(**inner, Ty::Opt(_)) {
                            refuse("flatten on a non-nested Option", m.span());
                        }
                        return value_expr(&m.receiver, cx);
                    }
                    // PRESENCE predicates are the absent-marker test:
                    // Some(v)/Ok(v) <=> the store value is not "". Exact
                    // for the string-typed store under the documented
                    // Some("") == None convention (same convention as
                    // `if let Some(x)`, which tests the same marker).
                    "is_some" | "is_ok" => {
                        if !m.args.is_empty() {
                            refuse("option predicate with arguments", m.span());
                        }
                        return opt_present(&m.receiver, true, cx);
                    }
                    "is_none" | "is_err" => {
                        if !m.args.is_empty() {
                            refuse("option predicate with arguments", m.span());
                        }
                        return opt_present(&m.receiver, false, cx);
                    }
                    _ => {}
                }
            }
            // `s.strip_prefix(p)` / `s.strip_suffix(p)` — Option<&str>:
            // the stripped value or the absent marker. Exact by anchoring
            // the test and slicing; the receiver/prefix are PURE reads, so
            // reading them twice is observationally identical.
            if matches!(rust_name.as_str(), "strip_prefix" | "strip_suffix")
                && m.args.len() == 1
                && matches!(recv_ty, Ty::Str)
            {
                // PURE reads (no side effects) so evaluating twice — once in
                // the test, once in the slice — is observational identity.
                fn pure_read(e: &syn::Expr) -> bool {
                    match e {
                        syn::Expr::Lit(_) => true,
                        syn::Expr::Path(p) if p.qself.is_none() => single_path(p).is_some(),
                        syn::Expr::Field(f) => matches!(&f.member, syn::Member::Named(_)),
                        // `p.words[0]` — an element read of a field array is
                        // pure too (no side effects, same value twice).
                        syn::Expr::Index(ix) => pure_read(&ix.expr) && pure_read(&ix.index),
                        syn::Expr::Try(t) => pure_read(&t.expr),
                        syn::Expr::Paren(p) => pure_read(&p.expr),
                        syn::Expr::MethodCall(m) => {
                            matches!(
                                m.method.to_string().as_str(),
                                "trim"
                                    | "trim_start"
                                    | "trim_end"
                                    | "to_string"
                                    | "as_str"
                                    | "to_lowercase"
                                    | "to_uppercase"
                                    | "strip_prefix"
                                    | "strip_suffix"
                            ) && pure_read(&m.receiver)
                                && m.args.iter().all(pure_read)
                        }
                        _ => false,
                    }
                }
                if pure_read(&m.receiver) && pure_read(&m.args[0]) {
                    let s = value_expr(&m.receiver, cx);
                    let p = value_expr(&m.args[0], cx);
                    let plen = json!({"type": "MethodCall", "object": p.clone(), "method": "length", "args": []});
                    let slen = json!({"type": "MethodCall", "object": s.clone(), "method": "length", "args": []});
                    let zero = json!({"type": "Arith", "ast": {"type": "Num", "value": 0}});
                    if rust_name == "strip_prefix" {
                        return json!({
                            "type": "Ternary",
                            "cond": {"type": "MethodCall", "object": s.clone(), "method": "startsWith", "args": [p]},
                            "then": {"type": "MethodCall", "object": s, "method": "slice", "args": [plen]},
                            "else": str_lit("")
                        });
                    }
                    return json!({
                        "type": "Ternary",
                        "cond": {"type": "MethodCall", "object": s.clone(), "method": "endsWith", "args": [p]},
                        "then": {
                            "type": "MethodCall", "object": s, "method": "substring",
                            "args": [zero, json!({"type": "BinOp", "lhs": slen, "op": "-", "rhs": plen})]
                        },
                        "else": str_lit("")
                    });
                }
            }
            // `s.chars()` / `s.lines()` in VALUE position: the runtime ops
            // return a native array of Unicode SCALARS / Rust's exact lines.
            if matches!(recv_ty, Ty::Str)
                && m.args.is_empty()
                && matches!(rust_name.as_str(), "chars" | "lines")
            {
                let f = if rust_name == "chars" { "strChars" } else { "strLines" };
                return json!({
                    "type": "Call", "func": f, "purity": "PureCpu",
                    "args": [value_expr(&m.receiver, cx)],
                });
            }
            // `.next()` of a FRESH char/line iterator is its FIRST element
            // (or the absent marker for an empty sequence — Rust's None). A
            // cursor over a STORED iterator is a stateful, different shape
            // and does not match. The op is pure, so the empty test and the
            // element read are the same array twice.
            if rust_name == "next" && m.args.is_empty() {
                if let syn::Expr::MethodCall(rm) = &*m.receiver {
                    if (rm.method == "chars" || rm.method == "lines")
                        && rm.args.is_empty()
                        && matches!(infer_ty(&rm.receiver, cx), Ty::Str)
                    {
                        let f = if rm.method == "chars" { "strChars" } else { "strLines" };
                        // snapshot the sequence ONCE, then its length, then
                        // read element 0 only when it exists (None -> "").
                        let t = fresh_tmp(cx, "s");
                        let arr = json!({
                            "type": "Call", "func": f, "purity": "PureCpu",
                            "args": [value_expr(&rm.receiver, cx)],
                        });
                        cx.pending.borrow_mut().push(store_stmt(&t, arr));
                        cx.vars.borrow_mut().insert(
                            t.clone(),
                            Ty::Arr(Some(Box::new(Ty::Str)), None),
                        );
                        let n = fresh_tmp(cx, "n");
                        cx.pending.borrow_mut().push(assign_stmt(&n, array_len_call(&t)));
                        cx.vars.borrow_mut().insert(n.clone(), Ty::Int);
                        return cond_value(
                            "c",
                            format!("${n} -gt 0"),
                            raw_elem(&t, str_lit("0")),
                            str_lit(""),
                            cx,
                        );
                    }
                }
            }
            // `is_ascii_alphanumeric` / `is_ascii_digit` on a CHAR: Rust's
            // str:: predicates are char-only, so the receiver is ONE char,
            // and an anchored bash bracket glob over the same ASCII set is
            // the identical predicate (the runtime matcher is an anchored
            // regex class; both sets contain both cases, and `[0-9]` has no
            // case at all, so nocasematch cannot change the answer).
            if matches!(recv_ty, Ty::Str)
                && m.args.is_empty()
                && matches!(
                    rust_name.as_str(),
                    "is_ascii_alphanumeric" | "is_ascii_digit"
                )
            {
                let pat = if rust_name == "is_ascii_digit" {
                    "[0-9]"
                } else {
                    "[0-9A-Za-z]"
                };
                // the A1 call carries (value, pattern); the emitter swaps
                // to the runtime's globMatch(pattern, value).
                return json!({
                    "type": "Call", "func": "globMatch", "purity": "PureCpu",
                    "args": [value_expr(&m.receiver, cx), str_lit(pat)],
                });
            }
            // `v.contains(&x)` on a Vec of SCALARS: the ordered array cannot
            // answer membership, but every `push` also maintained a parallel
            // ASSOC SET (`<v>__set`), so membership is a key lookup. Exact
            // because push is the ONLY mutation path this frontend admits on
            // a Vec (remove/pop/retain/sort/extend refuse, element writes
            // need a plain-variable target) and a whole-variable
            // REASSIGNMENT of a shadowed Vec refuses in plain_assign — a
            // stale set would answer wrong, so it is never produced.
            if let Ty::Arr(Some(ref elem), _) = recv_ty {
                if rust_name == "contains"
                    && m.args.len() == 1
                    && matches!(**elem, Ty::Str | Ty::Int)
                {
                    if let Some(n) = recv_var_name(&m.receiver) {
                        let needle = match &m.args[0] {
                            syn::Expr::Reference(r) => value_expr(&r.expr, cx),
                            other => value_expr(other, cx),
                        };
                        // key PRESENT in the shadow set <=> the value is an
                        // element. The lookup answers in the store's TEXT
                        // vocabulary ("false" is a real string, not a falsy
                        // JS boolean that prints as ""), so the result folds
                        // into "true"/"false" through an exact test.
                        let g = fresh_tmp(cx, "h");
                        cx.pending.borrow_mut().push(assign_stmt(
                            &g,
                            assoc_call(
                                "assocGet",
                                vec![str_lit(&format!("{n}__set")), needle],
                            ),
                        ));
                        cx.vars.borrow_mut().insert(g.clone(), Ty::Str);
                        return cond_value(
                            "k",
                            format!("\"${g}\" != \"\""),
                            str_lit("true"),
                            str_lit("false"),
                            cx,
                        );
                    }
                }
            }
            // `a.join(sep)` on a `Vec<String>` (v0.41): loop-composed with the
            // Interpolate concat — no join member exists and none is invented.
            // The separator goes BETWEEN elements and each contributes its
            // stored text, which is byte-identical to Rust's `join`.
            if rust_name.as_str() == "join" {
                if arr_of_str(&recv_ty) {
                    if m.args.len() != 1 {
                        refuse("join with a non-single separator", m.span());
                    }
                    let sep = &m.args[0];
                    if !matches!(infer_ty(sep, cx), Ty::Str) {
                        refuse("join separator (only a proven string)", sep.span());
                    }
                    let arr = match &*m.receiver {
                        syn::Expr::Path(rp) => single_path(rp).map(|s| s.to_string()),
                        _ => None,
                    };
                    let Some(arr) = arr else {
                        refuse(
                            "join on a non-variable receiver (the element reads are name-keyed)",
                            m.receiver.span(),
                        );
                    };
                    return join_value(cx, &arr, value_expr(sep, cx));
                }
            }
            // `n.is_power_of_two()` (v0.41): halve-while-even then `== 1`,
            // exact for 0 and every width — see `pow2_value`.
            if let (Ty::Int, "is_power_of_two") = (&recv_ty, rust_name.as_str()) {
                if !m.args.is_empty() {
                    refuse("is_power_of_two with arguments", m.span());
                }
                return pow2_value(cx, &m.receiver);
            }
            let js_name_raw = match (&recv_ty, rust_name.as_str()) {
                (Ty::Str, "contains") => "includes",
                // NOTE: Vec::len() does NOT map to .length here — the
                // canonical A1 read is the param("len", name) call
                // (${#a}); handled before this table.
                (Ty::Str, "starts_with") => "startsWith",
                (Ty::Str, "ends_with") => "endsWith",
                (Ty::Str, "trim") => "trim",
                (Ty::Str, "trim_start") => "trimStart",
                (Ty::Str, "trim_end") => "trimEnd",
                // Rust's `replace` replaces EVERY non-overlapping match;
                // JS `replaceAll` with a STRING pattern is literal and
                // global — the same operation (no regex interpretation).
                (Ty::Str, "replace") => "replaceAll",
                (Ty::Str, "to_lowercase") => "toLowerCase",
                (Ty::Str, "to_uppercase") => "toUpperCase",
                // strings are immutable JS values — to_string is identity
                (Ty::Str, "to_string") => "<identity>",
                _ => refuse(
                    &format!(
                        "method `{}` on {}receiver (no proven JS-equivalent yet)",
                        rust_name,
                        match recv_ty {
                            Ty::Int => "int ",
                            Ty::Str => "string ",
                            Ty::Bool => "bool ",
                            Ty::Arr(..) => "array ",
                            // Struct/enum receivers normally dispatch to a
                            // native shape earlier; one that reaches here has
                            // NO proven lowering, so it REFUSES like every
                            // other unmapped receiver — never a panic (a
                            // hoisted if-condition can legitimately reach it).
                            Ty::Struct(_) => "struct ",
                            Ty::Enum(_) => "enum ",
                            Ty::AtomicBool => "",
                            Ty::Tuple(_) => "tuple ",
                            Ty::List => "list ",
                            Ty::Map(..) => "map ",
                            Ty::Set(..) => "set ",
                            Ty::Fn(_) => "",
                            Ty::Opt(_) => "option ",
                            Ty::Cel(_) => "interior-mutable ",
                            Ty::Guard(_) => "guard ",
                            Ty::Unknown => "",
                        }
                    ),
                    m.method.span(),
                ),
            };
            if js_name_raw == "<identity>" {
                return value_expr(&m.receiver, cx);
            }
            let js_name = js_name_raw;
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
            if is_env_var_path(p) {
                return env_var_value(p, &c.args, cx);
            }
            // `env::args()` — the runtime already owns the vector
            // (`sh2.argv0` + `sh2.positional`); the node snapshots it into
            // a FRESH array, so a later write to the binding cannot reach
            // the positional store.
            if is_argv_call(c) {
                return json!({"type": "Argv", "purity": "PureCpu"});
            }
            // `Ok(x)` is x under the value-or-"" Result model; `Err(e)` is
            // the absent marker (the payload still evaluates for effects).
            if p.path.segments.len() == 1 && c.args.len() == 1 {
                match p.path.segments[0].ident.to_string().as_str() {
                    "Ok" => return value_expr(&c.args[0], cx),
                    "Err" => {
                        // the payload is an ARGUMENT: it evaluates EAGERLY
                        // (for its effects), and the value is the absent
                        // marker. (It used to be wrapped in a thunk, which
                        // deferred a side effect Rust performs at once.)
                        let ev = value_expr(&c.args[0], cx);
                        cx.pending.borrow_mut().push(expr_stmt(ev));
                        return str_lit("");
                    }
                    _ => {}
                }
            }
            if let Some((en, vn)) = enum_variant_of_path(&p.path, cx) {
                let shape = cx
                    .variants
                    .get(&en)
                    .and_then(|m| m.get(&vn))
                    .cloned()
                    .unwrap_or_else(|| refuse("unknown enum variant", c.func.span()));
                match shape {
                    VariantShape::Unit if c.args.is_empty() => {
                        return str_lit(&format!("{en}::{vn}"));
                    }
                    VariantShape::Tuple(tys) => {
                        if tys.len() != c.args.len() {
                            refuse("enum variant constructor arity", c.span());
                        }
                        let mut props = vec![json!({
                            "key": "tag",
                            "value": str_lit(&format!("{en}::{vn}")),
                        })];
                        for (i, a) in c.args.iter().enumerate() {
                            props.push(json!({
                                "key": i.to_string(),
                                "value": value_expr(a, cx)
                            }));
                        }
                        return json!({"type": "Object", "properties": props});
                    }
                    _ => refuse("enum variant construction form", c.func.span()),
                }
            }
            // `Box::new(x)` is an ALLOCATOR wrapper: the store has no box
            // layer, so the value is x itself (`Box::from(x)` likewise).
            if p.path.segments.len() >= 2 {
                let n = p.path.segments.len();
                let ty = p.path.segments[n - 2].ident.to_string();
                let f = p.path.segments[n - 1].ident.to_string();
                if (ty == "Box" || ty == "Rc" || ty == "Arc")
                    && (f == "new" || f == "from")
                    && c.args.len() == 1
                {
                    return value_expr(&c.args[0], cx);
                }
            }
            if let Some(v) = std_ctor_value(p, &c.args, cx) {
                return v;
            }
            let Some(target) = call_target(p, cx) else {
                refuse(
                    &format!("call to unknown function `{}`", path_text(c.func.as_ref())),
                    c.func.span(),
                );
            };
            fn_call_expr(&target, &c.args, "fnValue", cx)
        }
        // `if c { A } else { B }` as a VALUE: the branches are STATEMENTS, so
        // the whole if lowers into the pending buffer (drained before the
        // statement that consumes the temp) and the value IS that temp. Rust
        // evaluates exactly one branch and the A1 If does the same.
        syn::Expr::If(ie) => {
            let t = fresh_tmp(cx, "f");
            let ty = branch_ty(ie, cx);
            cx.vars.borrow_mut().insert(t.clone(), ty);
            let mut buf: Vec<Value> = Vec::new();
            let node = if_value_node(ie, BranchTarget::Var(&t), cx, false, &mut buf);
            buf.push(node);
            cx.pending.borrow_mut().extend(buf);
            get_var(&t)
        }
        // `as` between integer types is a compile-time FACT when the operand
        // is a constant: Rust yields the same value whenever it fits the
        // target's range, so the exact text IS the constant. Everything else
        // refuses — a runtime value can wrap (a u64 wrap is unrepresentable
        // in the decimal store) and a float/pointer cast has no store value.
        syn::Expr::Cast(c) => {
            let Some((text, val)) = const_int_cast_operand(&c.expr, cx) else {
                refuse(
                    "cast (`as`) of a non-constant expression (only compile-time integer values are exact)",
                    c.span(),
                )
            };
            let Some((bits, signed)) = int_ty_bits(&c.ty) else {
                refuse(
                    "cast (`as`) to a non-integer type (no exact JS equivalent yet)",
                    c.ty.span(),
                )
            };
            let ok = match val {
                Some(v) => int_cast_range_ok(v, bits, signed),
                // the only constant never seen as i128 is a u128-range
                // associated constant (`u128::MAX`): verbatim ONLY into an
                // unsigned 128-bit target.
                None => !signed && bits >= 128,
            };
            if !ok {
                refuse(
                    "cast (`as`) that would wrap (value outside the target type's range)",
                    c.span(),
                );
            }
            str_lit(&text)
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
            // a guard ALIAS (`*g` in arithmetic) is the static's slot
            if let Some((s, _)) = cx.guards.borrow().get(&name.to_string()).cloned() {
                return arith_var(&s);
            }
            arith_var(&name.to_string())
        }
        // `GUARD.get()` (a Cell closure param, a guard alias) is the
        // static's own slot in arithmetic
        syn::Expr::MethodCall(m) if m.method == "get" && m.args.is_empty() => {
            match static_guard(&m.receiver, cx) {
                Some((s, inner)) if is_scalar_store_ty(&inner) => arith_var(&s),
                Some(_) => refuse(
                    "static collection has no value form (use its operations)",
                    m.span(),
                ),
                None => refuse("method call in arithmetic", m.span()),
            }
        }
        syn::Expr::Binary(b) => arith_from_binary(b, cx),
        syn::Expr::Unary(u) => arith_from_unary(u, cx),
        syn::Expr::Paren(p) => arith_expr(&p.expr, cx),
        other => refuse("expression in arithmetic", other.span()),
    }
}

/// Raw arith AST node (NOT wrapped in the Arith value) — for use as an
/// operand of another node or inside arith_value(..).
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
    json!({
        "lhs": arith_expr(&b.left, cx),
        "op": op,
        "rhs": arith_expr(&b.right, cx),
        "type": "Bin",
    })
}

fn arith_from_unary(u: &syn::ExprUnary, cx: &Cx) -> Value {
    // scalar deref read = the value itself (runtime arith coerces the
    // decimal string)
    if let syn::UnOp::Deref(_) = u.op {
        return arith_expr(&u.expr, cx);
    }
    match u.op {
        syn::UnOp::Neg(_) => json!({
            "lhs": arith_num(0),
            "op": "-",
            "rhs": arith_expr(&u.expr, cx),
            "type": "Bin"
        }),
        _ => refuse("unary operator", u.op.span()),
    }
}

/// Integer literal -> its DECIMAL text (hex/bin/octal and `_` separators
/// normalized, suffixes refused). The store is decimal-string-typed.
fn int_text(li: &syn::LitInt) -> String {
    // `1i64` / `0u8` / `3usize` carry their TYPE, not their value: the
    // store is decimal-text typed, so the suffix is dropped.
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

/// Lower `matches!(subject, PAT)` to an exact test-grammar condition
/// string. Shared by the value-position path (test_call) and the
/// condition path (test_string) so the two can never disagree. The
/// subject must be a PLAIN VARIABLE (the test grammar reads `$var`; a
/// compound subject would be re-evaluated — not exact), and the pattern
/// the same unit-variant/or/`_` set the match lowering accepts.
struct MatchesArgs(syn::Expr, syn::Pat, Option<syn::Expr>);
impl syn::parse::Parse for MatchesArgs {
    fn parse(input: syn::parse::ParseStream) -> syn::Result<Self> {
        let e: syn::Expr = input.parse()?;
        input.parse::<syn::Token![,]>()?;
        // or-patterns (`A | B`) are Pat::Or, not parse_single
        let p: syn::Pat = syn::Pat::parse_multi_with_leading_vert(input)?;
        // optional match guard: `PAT if COND`
        let g = if input.peek(syn::Token![if]) {
            input.parse::<syn::Token![if]>()?;
            Some(input.parse::<syn::Expr>()?)
        } else {
            None
        };
        Ok(MatchesArgs(e, p, g))
    }
}

fn parse_matches_args(
    tokens: proc_macro2::TokenStream,
    span: proc_macro2::Span,
) -> (syn::Expr, syn::Pat, Option<syn::Expr>) {
    let MatchesArgs(subj, pat, guard) = syn::parse2(tokens)
        .unwrap_or_else(|e| refuse(&format!("`matches!` argument form: {e}"), span));
    (subj, pat, guard)
}

/// Does this expression contain a CALL (something that can have effects)?
/// Used to keep condition hoists exact under `&&`/`||`, where Rust skips the
/// right operand entirely and a hoisted call would always run.
fn contains_call(e: &syn::Expr) -> bool {
    match e {
        syn::Expr::Call(_) => true,
        syn::Expr::MethodCall(m) => m.args.iter().any(contains_call) || contains_call(&m.receiver),
        syn::Expr::Binary(b) => contains_call(&b.left) || contains_call(&b.right),
        syn::Expr::Unary(u) => contains_call(&u.expr),
        syn::Expr::Paren(p) => contains_call(&p.expr),
        syn::Expr::Field(f) => contains_call(&f.base),
        syn::Expr::Index(i) => contains_call(&i.expr) || contains_call(&i.index),
        syn::Expr::Reference(r) => contains_call(&r.expr),
        syn::Expr::Cast(c) => contains_call(&c.expr),
        syn::Expr::Macro(_) => true,
        _ => false,
    }
}

/// The `matches!` SUBJECT as a variable name the test grammar can read. A
/// plain path is already one; a compound subject (`matches!(f.as_str(), ..)`)
/// is evaluated ONCE into a temp — Rust evaluates the scrutinee exactly once,
/// and the temp is what the test then reads. The hoist goes on `cx.pending`,
/// which the enclosing statement lowering flushes BEFORE the statement whose
/// condition reads it.
fn matches_subject(subj: &syn::Expr, cx: &Cx) -> Option<String> {
    match subj {
        syn::Expr::Path(p) if p.qself.is_none() => single_path(p).map(|i| i.to_string()),
        _ => {
            if cx.sc_depth.get() > 0 && contains_call(subj) {
                refuse(
                    "`matches!` subject with a call inside a short-circuit chain \
                     (Rust skips it; a hoisted call would always run)",
                    subj.span(),
                );
            }
            let ty = infer_ty(subj, cx);
            let v = value_expr(subj, cx);
            let t = fresh_tmp(cx, "ms");
            cx.vars.borrow_mut().insert(t.clone(), ty);
            cx.pending.borrow_mut().push(assign_stmt(&t, v));
            Some(t)
        }
    }
}

fn matches_cond_neg(
    tokens: proc_macro2::TokenStream,
    span: proc_macro2::Span,
    cx: &Cx,
) -> Option<String> {
    let (subj, pat, guard) = parse_matches_args(tokens, span);
    if guard.is_some() {
        return None;
    }
    let Some(sv) = matches_subject(&subj, cx) else {
        refuse("`matches!` subject", span);
    };
    arm_cond_neg(&sv, &pat, cx)
}

fn matches_cond(
    tokens: proc_macro2::TokenStream,
    span: proc_macro2::Span,
    cx: &Cx,
) -> Option<String> {
    let (subj, pat, guard) = parse_matches_args(tokens, span);
    if guard.is_some() {
        return None;
    }
    let Some(sv) = matches_subject(&subj, cx) else {
        refuse("`matches!` subject", span);
    };
    arm_cond(&sv, &pat, cx)
}

/// A boolean condition -> the `test` STRING grammar the runtime parses
/// (`$x -gt 1`, `-a`/`-o` chains, `!`, parens) — the c-sh-go shapes.
fn test_string(e: &syn::Expr, cx: &Cx) -> String {
    // a BOOL static read through its guard is the plain store test
    if let Some((s, inner)) = static_guard(e, cx) {
        if matches!(inner, Ty::Bool | Ty::AtomicBool) {
            return format!("${s} = \"true\"");
        }
    }
    match e {
        // `if matches!(x, PAT)` / `while matches!(..)` — the same exact
        // conversion the value-position path uses (shared helper)
        syn::Expr::Macro(m)
            if m.mac
                .path
                .segments
                .last()
                .map(|s| s.ident == "matches")
                .unwrap_or(false) =>
        {
            matches_cond(m.mac.tokens.clone(), m.span(), cx).unwrap_or_else(|| {
                refuse(
                    "`matches!` pattern (only unit variants, or-patterns of them, and `_`)",
                    m.span(),
                )
            })
        }
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
                // `&&`/`||` become `-a`/`-o`, and the TEST evaluates both
                // sides: a hoisted operand with a CALL would run where Rust's
                // short-circuit would not. `sc_depth` makes the hoists refuse
                // on the right instead of quietly changing behaviour.
                And(_) => {
                    let l = test_string(&b.left, cx);
                    let saved = cx.sc_depth.get();
                    cx.sc_depth.set(saved + 1);
                    let r = test_string(&b.right, cx);
                    cx.sc_depth.set(saved);
                    format!("{} -a {}", l, r)
                }
                Or(_) => {
                    let l = test_string(&b.left, cx);
                    let saved = cx.sc_depth.get();
                    cx.sc_depth.set(saved + 1);
                    let r = test_string(&b.right, cx);
                    cx.sc_depth.set(saved);
                    format!("{} -o {}", l, r)
                }
                _ => refuse("binary operator in condition", b.op.span()),
            }
        }
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => test_string_neg(&u.expr, cx),
            // `*x` / `**x` on an iterator item: the store value is the
            // item itself (no reference layer).
            syn::UnOp::Deref(_) => test_string(&u.expr, cx),
            _ => refuse("unary operator in condition", u.op.span()),
        },
        syn::Expr::Paren(p) => format!("( {} )", test_string(&p.expr, cx)),
        // a bare boolean VARIABLE is not representable in the test
        // grammar: `$b` alone parses as a nonempty-string/file test, not
        // a bool read. Refuse until a bool-var contract lands.
        syn::Expr::Path(p) => {
            // a PROVEN-bool variable has an exact condition form: it is
            // true iff its store value stringifies to "true"
            let name = single_path(p);
            let is_bool = name
                .and_then(|id| {
                    cx.vars.borrow().get(&id.to_string()).cloned().or_else(|| {
                        cx.statics.borrow().get(&id.to_string()).cloned()
                    })
                })
                .map(|t| matches!(t, Ty::Bool | Ty::AtomicBool))
                .unwrap_or(false);
            if let (Some(name), true) = (name, is_bool) {
                return format!("${name} = \"true\"");
            }
            refuse(
                "boolean variable as a condition (type not proven)",
                p.span(),
            )
        }
        other => refuse("condition expression", other.span()),
    }
}

/// The EXACT NEGATION of a condition, by inverting its top-level
/// operator (`=`↔`!=`, `-lt`↔`-ge`, `-a`↔`-o`, `!` removal). The test
/// grammar's own `!` wrapper is avoided: inverting the operator is
/// exact by construction and needs no parser support.
fn test_string_neg(e: &syn::Expr, cx: &Cx) -> String {
    use syn::BinOp::*;
    if let Some((s, inner)) = static_guard(e, cx) {
        if matches!(inner, Ty::Bool | Ty::AtomicBool) {
            return format!("${s} != \"true\"");
        }
    }
    match e {
        syn::Expr::Binary(b) => match b.op {
            Eq(_) => bin_test("!=", &b.left, &b.right, cx),
            Ne(_) => bin_test("=", &b.left, &b.right, cx),
            Lt(_) => bin_test("-ge", &b.left, &b.right, cx),
            Le(_) => bin_test("-gt", &b.left, &b.right, cx),
            Gt(_) => bin_test("-le", &b.left, &b.right, cx),
            Ge(_) => bin_test("-lt", &b.left, &b.right, cx),
            And(_) => {
                let l = test_string_neg(&b.left, cx);
                let saved = cx.sc_depth.get();
                cx.sc_depth.set(saved + 1);
                let r = test_string_neg(&b.right, cx);
                cx.sc_depth.set(saved);
                format!("{} -o {}", l, r)
            }
            Or(_) => {
                let l = test_string_neg(&b.left, cx);
                let saved = cx.sc_depth.get();
                cx.sc_depth.set(saved + 1);
                let r = test_string_neg(&b.right, cx);
                cx.sc_depth.set(saved);
                format!("{} -a {}", l, r)
            }
            _ => refuse("negated binary condition operator", b.op.span()),
        },
        syn::Expr::Unary(u) => match u.op {
            syn::UnOp::Not(_) => test_string(&u.expr, cx),
            syn::UnOp::Deref(_) => test_string_neg(&u.expr, cx),
            _ => refuse("negated unary condition", u.op.span()),
        },
        syn::Expr::Paren(p) => test_string_neg(&p.expr, cx),
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Bool(lb) => {
                if lb.value() {
                    "0 -eq 1".into()
                } else {
                    "1 -eq 1".into()
                }
            }
            _ => refuse("negated literal condition", l.span()),
        },
        // a PROVEN-bool variable: its condition form is `$x = "true"`
        syn::Expr::Path(p) => {
            let Some(name) = single_path(p) else {
                refuse("negated condition path", p.span());
            };
            let is_bool = cx
                .vars
                .borrow()
                .get(&name.to_string())
                .cloned()
                .or_else(|| cx.statics.borrow().get(&name.to_string()).cloned())
                .map(|t| matches!(t, Ty::Bool | Ty::AtomicBool))
                .unwrap_or(false);
            if is_bool {
                return format!("${name} != \"true\"");
            }
            refuse("negated condition path (type not proven)", p.span())
        }
        // `!matches!(x, PAT)` — the exact inverse of matches_cond.
        syn::Expr::Macro(m)
            if m.mac
                .path
                .segments
                .last()
                .map(|s| s.ident == "matches")
                .unwrap_or(false) =>
        {
            matches_cond_neg(m.mac.tokens.clone(), m.span(), cx)
                .unwrap_or_else(|| refuse("negated `matches!` pattern", m.span()))
        }
        other => refuse("negated condition expression", other.span()),
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
        // `*x` is a reference DEREF: the store has no references, so the
        // value is the variable itself (`**x` nests).
        syn::Expr::Unary(u) if matches!(u.op, syn::UnOp::Deref(_)) => {
            operand(&u.expr, cx, str_eq)
        }
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
            // a guard ALIAS compares the STATIC it holds
            if let Some((s, _)) = cx.guards.borrow().get(&name.to_string()).cloned() {
                return format!("${s}");
            }
            format!("${name}")
        }
        // A LENGTH READ as a comparison operand (`v.len() > 1`): hoisted into
        // a temp so the test grammar can read a $variable. Exact only for
        // what the store counts exactly — an ARRAY's element count. A
        // STRING's `.len()` stays refused: Rust counts bytes, the store's
        // text is UTF-16 code units (the v0.35 `as_bytes` wall). The temp is
        // queued on `cx.pending`, which `lower_block`/`lower_fn_stmt` flush
        // BEFORE the statement that reads it.
        syn::Expr::MethodCall(m)
            if m.method == "len"
                && m.args.is_empty()
                && matches!(infer_ty(&m.receiver, cx), Ty::Arr(..)) =>
        {
            let t = fresh_tmp(cx, "cl");
            let v = value_expr(e, cx);
            cx.pending.borrow_mut().push(assign_stmt(&t, v));
            cx.vars.borrow_mut().insert(t.clone(), Ty::Int);
            format!("${t}")
        }
        syn::Expr::Lit(l) => match &l.lit {
            syn::Lit::Int(li) => int_text(li),
            // booleans stringify exactly ("true"/"false") — exact under
            // string equality; numeric -eq would misparse both as 0/NaN
            syn::Lit::Bool(lb) if str_eq => {
                format!("\"{}\"", if lb.value() { "true" } else { "false" })
            }
            syn::Lit::Str(ls) if str_eq => format!("\"{}\"", ls.value()),
            // a CHAR is a one-character string in the store (the same value
            // `"{}"` prints), so character equality is string equality.
            syn::Lit::Char(lc) if str_eq => format!("\"{}\"", lc.value()),
            other => refuse("literal in condition operand", other.span()),
        },
        // a PARENTHESIZED operand is the operand itself
        syn::Expr::Paren(p) => operand(&p.expr, cx, str_eq),
        // An ARITHMETIC operand in a NUMERIC comparison (`n % 2 == 0`,
        // `f(x) > 3`, `a.len() + 1 < b`): hoisted into a temp, so it runs
        // exactly once, in Rust's left-to-right operand order, and the test
        // grammar reads a $variable. Deliberately NOT for STRING equality:
        // there the compared value must be the expression's own string form,
        // which a value lowering need not reproduce.
        other
            if !str_eq
                && matches!(
                    other,
                    syn::Expr::Binary(_)
                        | syn::Expr::Unary(_)
                        | syn::Expr::Call(_)
                        | syn::Expr::MethodCall(_)
                ) =>
        {
            if cx.sc_depth.get() > 0 && contains_call(other) {
                refuse(
                    "call operand inside a short-circuit chain (Rust skips it; \
                     a hoisted call would always run)",
                    other.span(),
                );
            }
            let v = value_expr(other, cx);
            let t = fresh_tmp(cx, "op");
            cx.vars.borrow_mut().insert(t.clone(), Ty::Int);
            cx.pending.borrow_mut().push(assign_stmt(&t, v));
            format!("${t}")
        }
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
            syn::Expr::Lit(l2) => {
                matches!(l2.lit, syn::Lit::Str(_) | syn::Lit::Bool(_))
            }
            syn::Expr::Reference(r2) => textual(&r2.expr, cx),
            syn::Expr::Unary(u2) if matches!(u2.op, syn::UnOp::Deref(_)) => {
                textual(&u2.expr, cx)
            }
            syn::Expr::Paren(p) => textual(&p.expr, cx),
            syn::Expr::Path(p) => {
                if p.qself.is_none() && p.path.segments.len() == 2 {
                    let en = p.path.segments[0].ident.to_string();
                    if cx.enums.iter().any(|x| *x == en) {
                        return true;
                    }
                }
                single_path(p)
                    .and_then(|id| {
                        cx.vars.borrow().get(&id.to_string()).cloned().or_else(|| {
                            cx.statics.borrow().get(&id.to_string()).cloned()
                        })
                    })
                    .map(|t| matches!(t, Ty::Str | Ty::Enum(_) | Ty::Bool | Ty::AtomicBool))
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
