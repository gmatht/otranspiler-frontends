// REFUSAL PIN: `ok_or(e)` EAGERLY evaluates `e` and answers a
// Result<T, E>. The error payload has no place in the store (Ok(v) IS v,
// Err IS the absent marker), so a lowering would have to evaluate `e` for
// its side effects and then throw the value away — which is only sound
// while nothing observes it, and the frontend cannot see the consumer.
// Refused: `ok_or` stays outside the lowered family.
fn main() {
    let b: Option<i64> = None;
    println!("{}", b.ok_or(String::from("missing")).unwrap_or(-1));
}
