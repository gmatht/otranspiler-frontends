// REFUSAL PIN: a lazy_static SET lives in the named associative store (an
// exact lowering for insert/contains/remove/len/clear/is_empty), but there
// is no exact VALUE form for the whole collection: a `.clone()` would have
// to deep-copy the assoc under a new name (assoc reads are string-valued,
// so the copy would have to be re-keyed), and `.iter()` order is not the
// source's (a HashSet has no order, the store is insertion-ordered).
// Both refuse instead of guessing.
lazy_static::lazy_static! {
    static ref NAMES: std::sync::Mutex<std::collections::HashSet<String>> =
        Default::default();
}

fn main() {
    let copy = NAMES.lock().unwrap().clone();
    println!("{}", copy.len());
}
