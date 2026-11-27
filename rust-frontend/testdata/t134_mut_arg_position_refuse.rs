// A `&mut` argument bound under another name is only read back when the call
// is a whole `let` initializer or a statement — inside a larger expression the
// copies cannot be emitted in Rust's order, so it refuses instead of losing
// the write.
fn bump(t: &mut u32) {
    *t += 1;
}

fn main() {
    let mut n: u32 = 1;
    let v = 1 + bump2(&mut n);
    println!("{} {}", v, n);
}

fn bump2(t: &mut u32) -> u32 {
    *t += 1;
    *t
}
