// `first`/`last` answer Option<&elem> (element or the absent marker) with
// the length read ONCE and the index guarded, and `is_empty` is exact on
// strings (a string is empty iff its stored text is "") and on maps/sets
// in value position (their length is the same runtime count).
fn main() {
    let v = vec![String::from("a"), String::from("bb")];
    let none = String::from("none");
    println!("{}", v.first().unwrap_or(&none));
    println!("{}", v.last().unwrap_or(&none));
    let e: Vec<String> = Vec::new();
    println!("{}", e.first().unwrap_or(&none));
    println!("{}", e.last().unwrap_or(&none));
    let s = "";
    println!("{}", s.is_empty());
    let t = String::from("x");
    println!("{}", t.is_empty());
}
