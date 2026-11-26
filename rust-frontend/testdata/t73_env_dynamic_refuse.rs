// t73_env_dynamic_refuse — the env NAME must be a string literal. A
// computed name would make getVar read the store (and getVar's `#name` /
// `name[...]` parsing would reinterpret it), so the env-only read is not
// representable and must refuse loudly.
fn main() {
    let name = "HOME";
    let value = std::env::var(name);
    if value.is_ok() {
        println!("ok");
    }
}
