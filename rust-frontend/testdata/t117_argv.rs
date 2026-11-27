// Process arguments as ONE array value: `std::env::args()` is the runtime's
// argument vector — argv[0] (the program name as invoked) then the
// arguments in OS order. `collect()` binds that array, so `len`, indexing
// and iteration are the ordinary array operations.
//
// The gate runs this with NO arguments on both sides, so the output is
// argv-length 1 / "true" / no per-argument lines / count 1 — deterministic.
// The argument-CARRYING case is proven separately with the runner's
// `--name`/`--args` (FRONTEND.md): argv[0] itself differs by construction
// between a compiled binary path and the runner's --name, so nothing here
// prints argv[0].
fn main() {
    let args: Vec<String> = std::env::args().collect();
    let nargs = args.len();
    println!("{}", nargs);
    println!("{}", nargs > 0);
    let mut i = 0;
    for a in args.iter() {
        if i > 0 {
            println!("{}", a);
        }
        i = i + 1;
    }
    println!("{}", i);
}
