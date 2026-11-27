// REFUSAL PIN: `io::stderr().write_all(bytes)` writes BYTES (not text) and
// returns a Result the program may inspect; both need models this frontend
// does not have (a byte view, and the Result payload). Stays refused.
use std::io::Write;

fn main() {
    std::io::stderr().write_all(b"raw").unwrap();
}
