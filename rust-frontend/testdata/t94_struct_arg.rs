// A struct passed by `&` arrives as a VALUE. The callee's positional read is
// a value read, so its field access sees the caller's record; the text read
// it used before rendered the argument as "[object Object]" and every field
// came back empty.
struct P {
    x: i64,
    y: i64,
}

fn sum(p: &P) -> i64 {
    p.x + p.y
}

fn main() {
    let p = P { x: 5, y: 7 };
    println!("{}", sum(&p));
}
