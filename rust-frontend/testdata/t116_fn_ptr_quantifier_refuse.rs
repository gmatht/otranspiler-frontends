// REFUSAL PIN: a FUNCTION POINTER predicate — `args.iter().any(Self::f)`
// or `items.iter().all(check)` — is a fn ITEM, not a closure: inlining it
// would have to synthesise a body and bind the item's own name, which the
// IR has no form for. The bounded-loop `any`/`all` lowering accepts a
// closure literal only; a fn item refuses.
fn is_big(n: i64) -> bool {
    n > 2
}

fn main() {
    let v = vec![1, 2, 3];
    println!("{}", v.iter().any(is_big));
}
