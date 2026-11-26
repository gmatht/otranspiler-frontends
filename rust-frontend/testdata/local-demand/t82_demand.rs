// Demand-driven module lowering: the module is gated out wholesale, but
// main's one reachable function is clean and must be spliced.
#[path = "t82_demand/dirty.rs"]
mod dirty;
fn main() {
    let n = dirty::double(21);
    println!("{}", n);
}
