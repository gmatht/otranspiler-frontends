// The argument snapshot is indexable with a CONSTANT `nth(K)` (fresh array,
// so no iterator position exists to consume), and `std::process::exit(N)`
// ends the process with a status — both channels and the status are
// observable, so all three are compared against native rustc:
//   no argument   -> stderr "usage: need a name", exit 2, stdout empty
//   one argument  -> stdout "name: alpha", exit 0, stderr empty
fn main() {
    match std::env::args().nth(1) {
        Some(name) => {
            println!("name: {}", name);
        }
        None => {
            eprintln!("usage: need a name");
            std::process::exit(2);
        }
    }
}
