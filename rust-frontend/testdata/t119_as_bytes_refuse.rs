// REFUSAL PIN: `as_bytes()` is the biggest remaining string cluster, and it
// CANNOT be lowered honestly — a Rust `&str` is a BYTE view while the
// store's text is a JS string of UTF-16 code units, so `bytes[i]` and
// `bytes.len()` differ from the code-unit read for every non-ASCII input.
// Lowering it to string indexing would be a silent mistranslation for
// exactly the inputs the corpus has (latin-1 fixtures included), so the
// byte view stays refused until the IR carries one.
fn main() {
    let s = "ab";
    let b = s.as_bytes();
    println!("{}", b.len());
}
