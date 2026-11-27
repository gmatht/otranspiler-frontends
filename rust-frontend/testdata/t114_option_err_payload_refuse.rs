// REFUSAL PIN: on a RESULT, `unwrap_or_else`'s closure receives the ERROR
// payload, and every other Result accessor that OBSERVES the error
// (`.err()`, `unwrap_err`, `map_err`, `Err(e)` patterns, Debug) is
// likewise refused: the store model carries Ok(v) as v and Err as the
// absent marker, so the error VALUE has no representation. Option's
// `unwrap_or_else(|| ..)` (no parameter) lowers; this refuses.
fn main() {
    let e: Result<i64, String> = Err(String::from("boom"));
    println!("{}", e.unwrap_or_else(|msg| msg.len() as i64));
}
