// `iter().enumerate().map(|..| (k, v)).collect()` into a map builds the map
// with one assocSet per element (the map keeps insertion order in the store;
// see FRONTEND.md v0.29 for the BTreeMap iteration-order limitation). Lookups
// are exact: `get` -> Option<&V>, `copied()` is an identity in a store with no
// reference layer, and `unwrap_or(d)` tests the value against "" — which is
// exactly the None representation.
use std::collections::BTreeMap;

fn main() {
    let names: Vec<String> = vec!["aa".to_string(), "b".to_string(), "cc".to_string()];
    let idx: BTreeMap<String, usize> = names
        .iter()
        .enumerate()
        .map(|(i, n)| (n.clone(), i))
        .collect();
    println!("{}", idx.len());
    println!("{}", idx.get("aa").copied().unwrap_or(0));
    println!("{}", idx.get("b").copied().unwrap_or(99));
    println!("{}", idx.get("zz").copied().unwrap_or(7));
    for (i, n) in names.iter().enumerate() {
        println!("{} {}", i, n);
    }
}
