// t16_match — literal patterns, `|` or-patterns and `_` lower to the
// test-grammar equality on a hoisted scrutinee (the v0.21 subset).
fn main() {
    let x = 3;
    match x {
        1 => println!("one"),
        _ => println!("other"),
    }
}
