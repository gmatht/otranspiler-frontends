// `Self::m` resolves only to an item the impl actually registers. A name
// with no registered `Type_m` stays the same "unknown function" refusal as
// before — the fix is name resolution, never a guessed target.
struct P {
    x: i64,
}

impl P {
    fn run(&self) -> i64 {
        Self::missing(self.x)
    }
}

fn main() {
    let p = P { x: 1 };
    println!("{}", p.run());
}
