// A record reached through a `&mut` borrow stays refused: the value store
// has no reference layer, so a write through the parameter (or the borrow
// that carries it) has no exact form. Records cross a SHARED borrow now;
// this is the one shape that does not.
struct P {
    x: i64,
}

fn bump(p: &mut P) {
    p.x += 1;
}

fn main() {
    let mut p = P { x: 1 };
    bump(&mut p);
    println!("{}", p.x);
}
