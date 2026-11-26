// t74_option_predicate — is_some/is_none are the absent-marker test, the
// same convention as `if let Some(x)` (None ≡ ""). Some("x") is present,
// a bare None is absent, and the predicates work in value and condition
// position.
fn main() {
    let a = Some("x");
    let b: Option<&str> = None;
    println!(
        "some={} none={} anone={}",
        a.is_some(),
        b.is_some(),
        a.is_none()
    );
    if b.is_none() && a.is_some() {
        println!("branch");
    }
}
