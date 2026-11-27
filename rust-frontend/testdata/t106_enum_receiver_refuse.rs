// An ENUM value receiver stays refused: a by-value method binds `self`
// into the shared store (FRONTEND.md "KNOWN LIMITATION: params share the
// global store"), and a method that calls another method would clobber
// the caller's `self` — that is silent wrongness, not a lowering.
enum Width {
    U8,
    U64,
}

impl Width {
    fn rank(&self) -> i64 {
        match self {
            Width::U8 => 1,
            Width::U64 => 7,
        }
    }
}

fn main() {
    let w = Width::U64;
    println!("{}", w.rank());
}
