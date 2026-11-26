// Compile-time integer casts are EXACT: when the constant fits the target
// type's range, `as` yields the same value, so the store's decimal text is
// the answer. Primitive assoc consts, literals and nesting all fold.
//
// NOTE: the values here stay inside the double-exact integer range. The A1
// carries the exact decimal text either way, but the ESTree runtime parses a
// numeric-looking value as a JS number, so PRINTING one above 2^53 loses
// digits there — a runtime limitation, not a cast-lowering one.
const BIG: i128 = i32::MAX as i128;
fn main() {
    println!("{}", BIG);
    let a = (u8::MAX as u16) as i64;
    println!("{}", a);
    let b = 7 as i32;
    println!("{}", b);
    let c = 0 as usize;
    println!("{}", c);
}
