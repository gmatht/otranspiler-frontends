// An `if` in VALUE position lowers to a statement `If` writing a temp (the A1
// `Ternary` node is Perl-only IR, the ESTree renderer rejects it), so both a
// `let`-initializer and a `format!` argument work — and exactly one branch
// runs, as in Rust.
fn tag(b: bool) -> String {
    let s = if b { "yes" } else { "no" };
    s.to_string()
}

fn main() {
    let hi = true;
    let suffix = if hi { "=" } else { "" };
    println!("a{}", suffix);
    println!("{}", tag(false));
    let n: usize = if hi { 3 } else { 4 };
    println!("{}", n);
    println!(
        "range {}{}",
        if hi { "[" } else { "(" },
        if hi { "]" } else { ")" }
    );
}
