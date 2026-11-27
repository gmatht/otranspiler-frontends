// stderr is a typed output channel of its own: `eprintln!` writes the same
// formatted value a stdout print would build to the runtime's diagnostic
// channel (`sh2.warn`), one line per call, and touches nothing on stdout.
// Interleaved calls keep each STREAM's own order, which is all the host
// promises (and all a program can observe byte-wise once redirected).
fn main() {
    let name = String::from("file.sh");
    println!("start");
    eprintln!("usage: dump_ast <script.sh>");
    eprintln!("reading {}", name);
    println!("ok");
    eprintln!("done");
}
