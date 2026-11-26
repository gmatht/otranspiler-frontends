// t71_match_payload — payload enum patterns (`E::A(n)`) destructure the
// tagged record representation (unit variants stay plain tag strings).
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
