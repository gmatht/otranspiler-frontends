// Field reads THROUGH a record parameter, used as a loop bound and as a
// condition — the shapes that failed when the parameter was a string.
struct P {
    x: i64,
    y: i64,
}

fn countdown(p: &P) {
    let mut i = p.x;
    while i > 0 {
        println!("{}", i);
        i -= 1;
    }
    if p.y > 0 {
        println!("y={}", p.y);
    }
}

fn main() {
    countdown(&P { x: 3, y: 2 });
}
