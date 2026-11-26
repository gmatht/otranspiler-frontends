// t69_assoc_const — primitive associated constants (i128::MAX, u8::MIN,
// u64::MAX, …) fold to their EXACT decimal text. The store is a decimal
// string, so the full positive i128/u128 range survives; arithmetic uses
// beyond i64 still refuse at their own site rather than wrapping.
//
// The pin stays on POSITIVE out-of-i64 values: the ESTree core parses a
// numeric-looking A1 Str, and today it (a) rounds values that fit i64
// through a JS number and (b) panics on an i128::MIN-sized negative
// decimal. Both are core-side limitations, not this frontend's emit — the
// A1 carries the exact decimal string.
const VER_INF: i128 = i128::MAX;
const ZERO_8: u8 = u8::MIN;
const BIG_U: u64 = u64::MAX;

fn main() {
    println!("{}", VER_INF);
    println!("{}", ZERO_8);
    println!("{}", BIG_U);
}
