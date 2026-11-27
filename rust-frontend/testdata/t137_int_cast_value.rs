// `x as uN/iN` for a RUNTIME value is Rust's modulo 2^bits, computed with
// arithmetics the store carries exactly: unsigned wraps negatives up
// (-1 as u8 -> 255), signed folds the top half down (300 as i8 -> 44,
// -300 as i8 -> -44), and a 64-bit source truncates to 32 bits.
fn main() {
    let a: i32 = -1;
    let b: i32 = 300;
    let c: i32 = -300;
    println!("{} {} {}", a as u8, b as u8, c as u8);
    println!("{} {}", a as i8, c as i8);
    let d: i64 = 4294967297;
    println!("{} {}", d as u32, d as i32);
    let e: u32 = 2147483648;
    println!("{} {}", e as i32, e as u16);
}
