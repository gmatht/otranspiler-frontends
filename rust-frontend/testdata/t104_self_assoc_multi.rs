// An ASSOCIATED fn (no receiver) calling another `Self::` helper with two
// arguments, and the same helper fed from field reads: resolution does not
// depend on a `self` binding at all.
struct P {
    x: i64,
    y: i64,
}

impl P {
    fn add(a: i64, b: i64) -> i64 {
        a + b
    }

    fn pair(x: i64, y: i64) -> i64 {
        let s = Self::add(x, y);
        s * 2
    }

    fn coords(&self) -> i64 {
        Self::add(self.x, self.y)
    }
}

fn main() {
    let p = P { x: 1, y: 2 };
    println!("{}", P::pair(3, 4));
    println!("{}", p.coords());
}
