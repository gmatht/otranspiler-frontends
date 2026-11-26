// `unwrap_or` is a conditional over the receiver: a receiver with a side
// effect would run TWICE (test + value branch), so only a variable or a pure
// store read is admitted; anything else refuses.
fn grow(v: &mut Vec<i64>) -> Option<i64> {
    v.push(9);
    v.last().copied()
}
fn main() {
    let mut v: Vec<i64> = vec![1];
    println!("{}", grow(&mut v).unwrap_or(0));
}
