// The same resolution in TAIL position, with the helper returning a
// String: `Self::times16(..)` is a value-returning call whose declared
// return type is also what the enclosing method returns.
struct P {
    x: i64,
}

impl P {
    fn times16(n: i64) -> String {
        format!("{}", n * 16)
    }

    fn label(&self) -> String {
        Self::times16(self.x)
    }
}

fn main() {
    let p = P { x: 21 };
    println!("{}", p.label());
}
