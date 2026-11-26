// `Vec<scalar>::contains` is a membership test over a shadow set maintained at
// every `push` (the one admitted Vec mutation), so it stays exact even after
// repeated pushes; a whole-variable reassignment of such a Vec refuses
// (t89) because it would leave the set stale.
fn main() {
    let mut v: Vec<String> = Vec::new();
    v.push("alpha".to_string());
    v.push("beta".to_string());
    println!("{}", v.contains(&"alpha".to_string()));
    println!("{}", v.contains(&"gamma".to_string()));
    println!("{}", v.len());
    for (i, x) in v.iter().enumerate() {
        println!("{}{}", i, x);
    }
}
