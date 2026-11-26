// A scalar Vec carries the `contains` shadow set; reassigning the whole
// variable would leave that set describing the OLD contents, so the
// reassignment refuses rather than answering a later `contains` wrongly.
fn main() {
    let mut v: Vec<String> = vec!["a".to_string()];
    v.push("b".to_string());
    let w: Vec<String> = vec!["c".to_string()];
    v = w;
    println!("{}", v.contains(&"c".to_string()));
}
