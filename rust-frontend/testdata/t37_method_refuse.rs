// t37_method_refuse — method receivers (`&self` / `&mut self`) refuse:
// there is no field-access / method-dispatch contract node yet (the
// drop-in shir_nodes work is pending), so lowering a receiver would
// guess. Inherent impl ASSOCIATED functions without receivers ARE
// supported (t36_decls.rs, `Type::method` mangling).
//
// The emit refuses at the receiver — REFUSE > GUESS.
struct Counter {
    n: i64,
}

impl Counter {
    fn get(&self) -> i64 {
        self.n
    }
}

fn main() {
    let c = Counter { n: 1 };
    println!("{}", c.get());
}
