// A 128-bit cast of a RUNTIME value stays refused: rustc's wrap of a
// negative operand is >= 2^127, a magnitude no store number can hold, so no
// exact lowering exists.
fn main() {
    let x: i64 = -5;
    let y = x as u128;
    println!("{}", y);
}
