// t63_matches — `matches!` in value AND condition position.
//
// `matches!(v, PAT)` is a bool: it lowers to a test call over the SAME
// pattern conversion the match lowering uses (unit variants, or-patterns,
// `_`), so the two can never disagree. The subject must be a plain
// variable — the test grammar reads `$var`, and a compound subject would
// be evaluated twice (not exact).
enum Color {
    Red,
    Green,
    Blue,
}

fn main() {
    let c = Color::Red;
    // condition position
    if matches!(c, Color::Red) {
        println!("is red");
    }
    // value position (printed like Rust's bool `{}`)
    println!("{}", matches!(c, Color::Green));
    println!("{}", matches!(c, Color::Red | Color::Blue));
    println!("{}", matches!(c, Color::Blue));

    let g = Color::Green;
    if matches!(g, Color::Red | Color::Green) {
        println!("cool");
    }
}
