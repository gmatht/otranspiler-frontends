// t84_cast_nonconst_refuse — a non-constant `as` cast refuses: a runtime
// value can wrap or truncate, and the decimal-string store cannot hold the
// wrapped result (nor is a guessed truncation the same value).
fn main() {
    let n: i64 = 300;
    let b = n as u8;
    println!("{}", b);
}
