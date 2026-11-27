// REFUSAL PIN: `eprint!` writes to stderr with NO trailing newline. The
// runtime's only stderr member is `sh2.warn`, which always appends one, and
// rendered callees must be whitelisted `sh2.<name>` members — so giving this
// a newline it does not emit would be a behaviour change. Stays refused.
fn main() {
    eprint!("no newline");
}
