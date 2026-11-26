// This module does NOT lower as a whole (the slice pattern below refuses),
// so whole-module gating would drop it. Demand-driven splicing must still
// pull the ONE clean function the entry calls.
pub fn double(n: i64) -> i64 {
    n * 2
}
pub fn first(xs: &[i64]) -> i64 {
    let [a, ..] = xs else { return 0; };
    *a
}
