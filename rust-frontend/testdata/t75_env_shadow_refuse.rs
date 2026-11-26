// t75_env_shadow_refuse — getVar prefers the STORE over process.env, so an
// env read is exact only when the program never binds the same name. Here
// the local `HOME` would shadow the environment read; the frontend must
// refuse instead of silently returning the local.
fn main() {
    let HOME = "shadow";
    println!("{}", std::env::var("HOME").unwrap_or_default());
}
