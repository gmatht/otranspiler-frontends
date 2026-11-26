// t71_match_payload_refuse — PAYLOAD patterns (`E::A(n)`, struct patterns)
// still REFUSE: destructuring a variant payload needs record storage,
// which the A1 store does not have yet. The boundary must stay loud.
enum E {
    A(i64),
    B,
}

fn main() {
    let e = E::B;
    match e {
        E::A(n) => println!("{}", n),
        E::B => println!("b"),
    }
}
