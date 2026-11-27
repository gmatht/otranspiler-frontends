// A `&mut` argument bound under a DIFFERENT name than the parameter stays
// refused: the store keys a location by name, so the callee's writes would
// land on a slot the caller never reads (the write would silently vanish).
fn bump(t: &mut u32) {
    *t += 1;
}

fn main() {
    let mut n: u32 = 1;
    bump(&mut n);
    println!("{}", n);
}
