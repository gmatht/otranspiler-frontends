// `coll.iter().position(|e| COND).unwrap_or(D)` is fused into one scan: the
// FIRST matching index, or the default when nothing matches. The default must
// win when the predicate never matches, and index 0 must be distinguishable
// from "not found".
fn main() {
    let arrays: Vec<String> = vec!["a".to_string(), "b".to_string(), "c".to_string()];
    println!("{}", arrays.iter().position(|x| x == "b").unwrap_or(0));
    println!("{}", arrays.iter().position(|x| x == "zz").unwrap_or(9));
    let first: Vec<String> = vec!["q".to_string(), "r".to_string()];
    println!("{}", first.iter().position(|x| x == "q").unwrap_or(7));
}
