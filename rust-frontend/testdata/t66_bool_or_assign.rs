// t66_bool_or_assign — `|=` / `&=` on a PROVEN bool are Rust's logical
// OR / AND (BitOr/BitAnd on bool). They are NOT short-circuiting, so the
// RHS always evaluates once; the frontend hoists it into a temp and
// assigns a `test` result (a native JS boolean — never a bitwise op,
// which would coerce the store's "true"/"false" strings to NaN → 0).
fn flip(a: bool, b: bool) -> bool {
    a || b
}

fn main() {
    let mut changed = false;
    changed |= flip(true, false);
    println!("{}", changed);
    changed &= flip(true, false);
    println!("{}", changed);
    changed |= flip(false, false);
    println!("{}", changed);
}
