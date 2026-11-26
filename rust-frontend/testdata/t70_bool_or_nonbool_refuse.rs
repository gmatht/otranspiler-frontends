// t70_bool_or_nonbool_refuse — `|=` on a non-bool receiver is a BITWISE
// op in Rust; the frontend refuses instead of guessing (the JS `|` is
// 32-bit and the store is string-typed — neither matches i64 semantics).
fn main() {
    let mut n = 1;
    n |= 2;
    println!("{}", n);
}
