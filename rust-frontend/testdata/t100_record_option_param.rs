// An `Option<record>` parameter carries a payload object in its `Some`;
// `None` is the store's empty value, where a value read and a text read
// agree. Both arms must see the caller's record.
struct P {
    x: i64,
}

fn get(o: &Option<P>) -> i64 {
    match o {
        Some(p) => p.x,
        None => 0,
    }
}

fn main() {
    let p = P { x: 5 };
    println!("{}", get(&Some(p)));
    println!("{}", get(&None));
}
