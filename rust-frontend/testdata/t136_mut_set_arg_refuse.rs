// A set/map lives in the store by NAME, so a `&mut` argument bound under a
// different name has no exact readback (the callee's inserts land in its own
// store); refused rather than lost.
use std::collections::BTreeSet;

fn add(s: &mut BTreeSet<String>) {
    s.insert("a".to_string());
}

fn main() {
    let mut used: BTreeSet<String> = BTreeSet::new();
    add(&mut used);
    println!("{}", used.len());
}
