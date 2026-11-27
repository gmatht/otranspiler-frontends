// The divmod shape: `(*d as u64).trailing_zeros()` — the receiver's width is
// pinned by the cast, and a ZERO operand answers that width, so a lowering
// that guessed the wrong width (or mishandled zero) prints a different number.
// Covers 0, 1, 2, powers of two, all-ones patterns, the high bit (u32) and a
// spread of ordinary values.
fn main() {
    let vals: Vec<i64> = vec![0, 1, 2, 3, 8, 12, 1024, 255, 1099511627776];
    let mut i = 0;
    while i < vals.len() {
        let v = vals[i];
        println!("{}", (v as u64).trailing_zeros());
        i += 1;
    }
    let u: Vec<u32> = vec![0, 1, 2, 2147483648, 4294967295];
    let mut j = 0;
    while j < u.len() {
        let w = u[j];
        println!("{}", (w as u32).trailing_zeros());
        j += 1;
    }
}
