// String splitting, any/all quantifiers, and list/array iteration.
struct S {
    items: Vec<i64>,
}
fn main() {
    let csv = "a,b,c";
    for part in csv.split(',') {
        println!("p={}", part);
    }
    let has_b = csv.split(',').any(|s| s == "b");
    let has_z = csv.split(',').any(|s| s == "z");
    let all_abc = csv.split(',').all(|s| s == "a" || s == "b" || s == "c");
    println!("{} {} {}", has_b, has_z, all_abc);
    if csv.split(',').any(|s| s == "c") {
        println!("found c");
    }
    let v = vec!["x", "y"];
    let any_y = v.iter().any(|s| *s == "y");
    let all_x = v.iter().all(|s| *s == "x");
    println!("{} {}", any_y, all_x);
    let s = S { items: vec![1, 2, 3] };
    if s.items.is_empty() {
        println!("empty");
    } else {
        println!("{}", s.items[1]);
    }
}
