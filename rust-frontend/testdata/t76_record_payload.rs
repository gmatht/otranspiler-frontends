// Payload records: tuple/struct enum variants and struct destructuring.
// Representations: unit variant = "E::V"; payload variant = tagged object
// {tag:"E::V", 0:..} / {tag:"E::V", field:..}; tuple = native array.
enum E {
    A(i64, i64),
    S { x: i64 },
    U,
}
struct P {
    x: i64,
    y: i64,
}

fn main() {
    let e = E::A(3, 4);
    match e {
        E::A(a, b) => println!("{} {}", a, b),
        E::S { x } => println!("{}", x),
        E::U => println!("u"),
    }
    let s = E::S { x: 7 };
    if let E::S { x } = s {
        println!("{}", x);
    }
    let E::A(a, b) = E::A(1, 2) else {
        return;
    };
    println!("{} {}", a, b);
    let p = P { x: 5, y: 6 };
    let P { x, y } = p;
    println!("{} {}", x, y);
    let (t, u) = (8, 9);
    println!("{} {}", t, u);
}
