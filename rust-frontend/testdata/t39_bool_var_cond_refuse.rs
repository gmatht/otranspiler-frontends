// t39_bool_var_cond_refuse — a bare boolean VARIABLE as a condition
// refuses: `$b` alone parses in the test grammar as a nonempty-string /
// file-existence test, NOT a bool read. Until a bool-var contract node
// lands, lowering would guess — so it refuses (REFUSE > GUESS).
fn main() {
    let b = true;
    if b {
        println!("taken");
    }
}
