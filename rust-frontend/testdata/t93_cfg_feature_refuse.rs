// `cfg` that is not `cfg(test)` selects code for a build configuration we
// cannot know (feature flags, target platforms), so it refuses rather than
// guessing which arm a real build would take.
#[cfg(feature = "gpu")]
fn accel(x: i64) -> i64 {
    x * 2
}

fn main() {
    println!("{}", accel(3));
}
