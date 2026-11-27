// A `BTreeMap` ITERATES IN KEY ORDER — that order is part of the program's
// output, not an implementation detail. The store keeps an assoc in
// insertion order, so a BTreeMap receiver sorts its key array before the
// loop; a HashMap keeps its insertion order (its order is unspecified in
// Rust, so any enumeration is valid). Keys below are ASCII plus two
// non-ASCII BMP characters, where Rust's UTF-8 byte order and JS's UTF-16
// code-unit order agree: Z < ä < 日 by both.
use std::collections::BTreeMap;
use std::collections::BTreeSet;

fn main() {
    let mut m: BTreeMap<String, i64> = BTreeMap::new();
    m.insert(String::from("b"), 2);
    m.insert(String::from("a"), 1);
    m.insert(String::from("c"), 3);
    for k in m.keys() {
        println!("{}", k);
    }
    for (k, v) in &m {
        println!("{}={}", k, v);
    }
    let mut u: BTreeMap<String, i64> = BTreeMap::new();
    u.insert(String::from("ä"), 1);
    u.insert(String::from("Z"), 2);
    u.insert(String::from("日"), 3);
    for k in u.keys() {
        println!("{}", k);
    }
    let mut s: BTreeSet<String> = BTreeSet::new();
    s.insert(String::from("y"));
    s.insert(String::from("x"));
    for e in &s {
        println!("set {}", e);
    }
}
