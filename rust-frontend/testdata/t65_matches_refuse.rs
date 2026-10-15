// t65_matches_refuse — `matches!` must REFUSE a compound subject in value
// position: the test grammar reads a `$variable`, and re-evaluating a
// call subject would run it twice (not exact).
enum Color {
    Red,
    Green,
}
fn probe() -> Color {
    Color::Red
}
fn main() {
    println!("{}", matches!(probe(), Color::Red));
}
