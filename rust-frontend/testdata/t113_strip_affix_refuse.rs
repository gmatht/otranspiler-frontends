// REFUSAL PIN: `strip_prefix`/`strip_suffix` answer Option<&str>, but the
// JS path cannot carry that value today, so the combinators over a
// stripped value stay refused:
//   * the StripAffix lowering is a conditional EXPRESSION, and the ESTree
//     renderer has no conditional expression (only branch STATEMENTS);
//   * it reads the affix length through a `length` METHOD call, which JS
//     renders as `p.length()` (a property there);
//   * Rust's strip is BYTE-based while JS slicing counts UTF-16 code
//     units, so even a working length slice is a different string for
//     non-ASCII input — a silent mistranslation, not a lowering.
// Case (1) alone would need the JS renderer to grow a ternary/call
// property distinction; case (3) needs a byte-view model. Refused.
fn main() {
    let t = "abc";
    println!("{}", t.strip_prefix("ab").unwrap_or(t));
    println!("{}", t.strip_suffix("zz").unwrap_or("nope"));
}
