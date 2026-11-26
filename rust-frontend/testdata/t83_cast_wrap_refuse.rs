// t83_cast_wrap_refuse — a constant cast that would WRAP must stay loud:
// Rust truncates 300 as u8 to 44, and 44 is not the constant the source
// names, so a silent answer here would be a wrong one.
fn main() {
    let b = 300 as u8;
    println!("{}", b);
}
