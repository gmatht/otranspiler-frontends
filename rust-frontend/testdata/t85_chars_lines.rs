// Code-point iteration is exact: `chars()` yields Unicode scalar values and
// `lines()` is Rust's `lines()` (split on '\n', strip one trailing '\r', no
// trailing empty line) — both are runtime primitives, not approximations. A
// `char` is a one-character string in the store, so `push(c)` is a concat.
fn classify(s: &str) -> String {
    let mut out = String::new();
    for c in s.chars() {
        if c.is_ascii_alphanumeric() {
            out.push(c);
        }
    }
    out
}

fn main() {
    println!("{}", classify("ab-12,cd_34"));
    for c in "h\u{e9}llo".chars() {
        println!("[{}]", c);
    }
    let text = "a b\nc d\n\ne";
    let mut n = 0;
    for line in text.lines() {
        n += 1;
        println!("{}:{}", n, line);
    }
    let digits = "x1y2z3";
    let mut count = 0;
    for c in digits.chars() {
        if c.is_ascii_digit() {
            count += 1;
        }
    }
    println!("{}", count);
}
