// A `&mut` argument bound under a DIFFERENT name than the parameter is exact
// through the call-site readback (v0.41): the caller writes the callee's slot
// (copy-in), the call runs, and the slot is copied back — `n` observes the
// bump.
fn bump(t: &mut u32) {
    *t += 1;
}

fn main() {
    let mut n: u32 = 1;
    bump(&mut n);
    println!("{}", n);
}
