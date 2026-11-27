// TWO record parameters in one call: each positional is an independent
// value read, so neither arg can be shifted or stringified.
struct P {
    x: i64,
}

struct Q {
    y: i64,
}

fn mix(a: &P, b: &Q) -> i64 {
    a.x * 100 + b.y
}

fn main() {
    println!("{}", mix(&P { x: 3 }, &Q { y: 9 }));
    let p = P { x: 1 };
    let q = Q { y: 2 };
    println!("{}", mix(&p, &q));
}
