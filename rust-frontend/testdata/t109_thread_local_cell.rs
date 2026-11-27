// `thread_local!` with a `Cell` is a per-thread mutable slot; the program
// has ONE thread, so it is the mutable global. `with(|c| ..)` runs on that
// slot: `get` reads it, `set` writes it, `replace` returns the old value
// AFTER evaluating the new one (Rust's argument order).
use std::cell::Cell;

thread_local! {
    static DEPTH: Cell<i64> = Cell::new(0);
    static NAME: Cell<i64> = Cell::new(10);
}

fn main() {
    DEPTH.with(|c| c.set(c.get() + 1));
    DEPTH.with(|c| c.set(c.get() + 1));
    println!("{}", DEPTH.with(|c| c.get()));
    let old = NAME.with(|c| c.replace(42));
    println!("{}", old);
    println!("{}", NAME.with(|c| c.get()));
}
