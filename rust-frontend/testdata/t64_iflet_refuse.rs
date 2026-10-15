// t64_iflet_refuse — the `if let` boundary must REFUSE, not guess.
//
// Only `Some(name)`, `None` and `_` are representable under the None≡""
// convention. `Some(<non-name>)` (a destructuring binding) is not.
fn main() {
    let pair = Some((1, 2));
    if let Some((a, b)) = pair {
        println!("{} {}", a, b);
    }
}
