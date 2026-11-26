// t78_slice_pattern_refuse — slice patterns need runtime length + element
// tests and are not lowered yet; the boundary must stay loud.
fn main() {
    let v = [1, 2];
    match v {
        [a, ..] => println!("{}", a),
        _ => {}
    }
}
