// HashMap/BTreeMap and HashSet/BTreeSet lowering.
use std::collections::{HashMap, HashSet};
fn main() {
    let mut counts: HashMap<String, i64> = HashMap::new();
    counts.insert("a".to_string(), 1);
    counts.insert("b".to_string(), 2);
    counts.insert("a".to_string(), 3);
    println!("{} {}", counts.len(), counts["a"]);
    if counts.contains_key("b") {
        println!("has b");
    }
    if counts.is_empty() {
        println!("empty");
    } else {
        println!("nonempty");
    }
    let mut total = 0;
    for (_k, v) in &counts {
        total += *v;
    }
    println!("{}", total);
    let mut seen: HashSet<String> = HashSet::new();
    seen.insert("x".to_string());
    seen.insert("y".to_string());
    seen.insert("x".to_string());
    println!("{}", seen.len());
    if seen.contains("y") {
        println!("has y");
    }
    let mut n = 0;
    for _k in &seen {
        n += 1;
    }
    println!("{}", n);
}
