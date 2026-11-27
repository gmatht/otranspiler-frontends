// A record reached through a `&mut` borrow still refuses, for a narrower
// reason than v0.40's scalar write: the deref write added there targets a
// plain variable (the slot IS the pointee), while `p.x += 1` writes a FIELD
// of the object, and a field reference has no store location. Records cross
// a SHARED borrow (t94-t100); a `&mut` record has no exact form yet.
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
