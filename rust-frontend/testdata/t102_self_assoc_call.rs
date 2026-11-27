// `Self::m(..)` inside an impl block names the SAME item `P::m` names.
// Before v0.32 the path was resolved as the mangled name `Self_m`, which
// no impl ever registers, so every associated-function helper was an
// "unknown function" refusal.
struct P {
    x: i64,
}

impl P {
    fn twice(n: i64) -> i64 {
        n * 2
    }

    fn run(&self) -> i64 {
        let t = Self::twice(self.x);
        t + 1
    }
}

fn main() {
    let p = P { x: 21 };
    println!("{}", p.run());
}
