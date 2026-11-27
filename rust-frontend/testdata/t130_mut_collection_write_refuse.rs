// A whole-value write through a `&mut` collection reference stays refused:
// collections live in the store by name, so `*v = Vec::new()` would rebind
// the callee's slot, not the caller's.
fn reset(v: &mut Vec<String>) {
    *v = Vec::new();
}

fn main() {
    let mut v: Vec<String> = Vec::new();
    v.push("a".to_string());
    reset(&mut v);
    println!("{}", v.len());
}
