// Tuple destructuring in for loops and iteration over a struct field array.
struct S {
    items: Vec<(i64, i64)>,
}
fn main() {
    let pairs = vec![(1, 2), (3, 4)];
    for (a, b) in &pairs {
        println!("{} {}", a, b);
    }
    let s = S { items: vec![(5, 6)] };
    for (a, b) in &s.items {
        println!("{} {}", a, b);
    }
}
