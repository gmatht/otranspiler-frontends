// `any`/`all` in VALUE position (a match-arm body, a call argument), not
// only in a `let` or an `if`: the bounded loop reads the collection once
// into a temp and the predicate runs per element — the same lowering the
// let-binding form already used.
fn main() {
    let v = vec![String::from("ax"), String::from("b")];
    println!("{}", v.iter().any(|s| s.starts_with("a")));
    println!("{}", v.iter().all(|s| s.contains("x")));
    let w = vec![1, 2, 3];
    println!("{}", w.iter().any(|n| *n == 2));
    println!("{}", w.iter().all(|n| *n < 9));
    let empty: Vec<i64> = Vec::new();
    println!("{}", empty.iter().any(|n| *n > 0));
    println!("{}", empty.iter().all(|n| *n > 0));
}
