// t72_env_var — std::env::var reads the PROCESS environment through
// getVar's process.env fallback: present -> its value, absent -> "" (the
// store's absent marker). HOME is present in the gate's environment; the
// other name is never set, so both branches are exercised.
fn main() {
    let home = std::env::var("HOME").unwrap_or_default();
    let missing = std::env::var("SH2_FRONTEND_PIN_UNSET_7C1").unwrap_or_default();
    println!("home-present={}", home != "");
    println!("missing-empty={}", missing == "");
    println!("home-ok={}", std::env::var("HOME").is_ok());
    println!("missing-ok={}", std::env::var("SH2_FRONTEND_PIN_UNSET_7C1").is_ok());
    println!(
        "missing-err={}",
        std::env::var("SH2_FRONTEND_PIN_UNSET_7C1").is_err()
    );
    if std::env::var("HOME").is_ok() {
        println!("home-cond=true");
    }
    if std::env::var("SH2_FRONTEND_PIN_UNSET_7C1").is_err() {
        println!("missing-cond=true");
    }
}
