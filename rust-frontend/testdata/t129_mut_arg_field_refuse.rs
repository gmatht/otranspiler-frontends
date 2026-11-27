// `&mut` of a field has no store location — a location is a NAME, and a
// field reference would need a real reference layer. Refused.
struct P {
    x: u32,
}

fn bump(t: &mut u32) {
    *t += 1;
}

fn main() {
    let mut p = P { x: 1 };
    bump(&mut p.x);
    println!("{}", p.x);
}
