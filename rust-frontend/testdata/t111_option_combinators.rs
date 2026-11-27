// The Option/Result combinator family under the value-or-absent("")
// model. Each one keeps RUST's evaluation order: `unwrap_or`/`map_or`
// evaluate their default eagerly (it is an argument), while `map`,
// `and_then`, `is_some_and`, `or_else` and `unwrap_or_else` run their
// closure ONLY on the branch that takes it.
fn main() {
    let a: Option<i64> = Some(5);
    let b: Option<i64> = None;
    println!("{}", a.unwrap_or(9));
    println!("{}", b.unwrap_or(9));
    let r: Result<i64, String> = Ok(7);
    println!("{}", r.unwrap_or(1));
    let e: Result<i64, String> = Err(String::from("no"));
    println!("{}", e.unwrap_or(1));
    println!("{}", a.map_or(0, |x| x * 2));
    println!("{}", b.map_or(0, |x| x * 2));
    println!("{}", a.map(|x| x + 1).unwrap_or_default());
    println!("{}", b.map(|x| x * 2).unwrap_or_default());
    println!("{}", a.is_some_and(|x| x > 3));
    println!("{}", b.is_some_and(|x| x > 3));
    let r2: Result<i64, String> = Ok(7);
    println!("{}", r2.is_ok_and(|x| x > 3));
    let e2: Result<i64, String> = Err(String::from("no"));
    println!("{}", e2.is_ok_and(|x| x > 3));
    println!("{}", b.and_then(|x| Some(x * 3)).unwrap_or(-1));
    println!("{}", a.and_then(|x| Some(x + 3)).unwrap_or(-1));
    println!("{}", a.or_else(|| Some(0)).unwrap_or(-2));
    println!("{}", b.or_else(|| Some(0)).unwrap_or(-2));
    println!("{}", a.unwrap_or_else(|| 42));
    println!("{}", b.unwrap_or_else(|| 42));
    let n: Option<Option<i64>> = Some(Some(4));
    println!("{}", n.flatten().unwrap_or(-2));
    let z: Option<Option<i64>> = Some(None);
    println!("{}", z.flatten().unwrap_or(-2));
    let s: Option<String> = Some(String::from("hi"));
    println!("{}", s.map(|v| v.to_string()).unwrap_or(String::from("-")));
}
