// `Vec<String>::join(sep)` is loop-composed (no join member exists): the
// separator goes BETWEEN elements, each contributes its stored text, and an
// empty vector is the empty string — byte-identical to rustc.
fn main() {
    let mut v: Vec<String> = Vec::new();
    println!("[{}]", v.join(", "));
    v.push("a".to_string());
    println!("[{}]", v.join(", "));
    v.push("b".to_string());
    v.push("c".to_string());
    println!("[{}]", v.join("-"));
    let parts = vec!["x".to_string(), "y".to_string()];
    println!("{}|{}", parts.join(""), parts.join(" :: "));
}
