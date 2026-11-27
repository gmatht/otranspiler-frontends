// A record parameter passed ONWARD to a second function: the value must
// survive both hops, so the outer binding cannot be a string.
struct P {
    x: i64,
    y: i64,
}

fn inner(p: &P) -> i64 {
    p.x * p.y
}

fn mid(p: &P) -> i64 {
    inner(p)
}

fn outer(p: &P) -> i64 {
    mid(p)
}

fn main() {
    let p = P { x: 6, y: 7 };
    println!("{}", outer(&p));
}
