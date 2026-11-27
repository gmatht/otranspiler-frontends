// An enum passed by `&` arrives as a VALUE, payload included: the callee's
// positional read must hand over the tagged object, not its text form.
enum Shape {
    Square { side: i64 },
    Rect { w: i64, h: i64 },
}

fn area(s: &Shape) -> i64 {
    match s {
        Shape::Square { side } => *side * *side,
        Shape::Rect { w, h } => *w * *h,
    }
}

fn main() {
    println!("{}", area(&Shape::Square { side: 3 }));
    println!("{}", area(&Shape::Rect { w: 4, h: 5 }));
}
