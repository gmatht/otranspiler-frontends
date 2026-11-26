// t67_match_literal — integer/string/bool LITERAL match arms, `|` or-patterns
// over them, a plain BINDING arm (`other => ...`, the irrefutable
// fall-through), and `_`. All lower to the test-grammar equality the
// hoisted scrutinee already uses.
fn main() {
    let x = 2;
    match x {
        0 => println!("zero"),
        1 => println!("one"),
        other => println!("{}", other),
    }
    match x {
        1 | 2 => println!("small"),
        _ => println!("big"),
    }
    let s = "b";
    match s {
        "a" => println!("ay"),
        "b" => println!("bee"),
        other => println!("{}", other),
    }
}
