// Passing a `&mut` through to another function's parameter spelled
// differently is a reborrow with no readback (only an explicit `&mut x`
// argument gets one), so it refuses rather than dropping the write.
fn inner(q: &mut u32) {
    *q += 10;
}

fn outer(t: &mut u32) {
    inner(t);
}

fn main() {
    let mut t: u32 = 1;
    outer(&mut t);
    println!("{}", t);
}
