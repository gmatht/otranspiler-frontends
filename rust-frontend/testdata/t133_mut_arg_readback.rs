// The readback proof for a Vec bound under another name: the callee pushes
// through `out`, the CALLER then observes the pushed elements (length, index
// and text), plus a same-name scalar bump in the same call.
fn pushit(out: &mut Vec<String>, t: &mut u32) {
    out.push("x".to_string());
    out.push("y".to_string());
    *t += 1;
}

fn main() {
    let mut t: u32 = 1;
    let mut lines: Vec<String> = Vec::new();
    pushit(&mut lines, &mut t);
    println!("{} {} {}", lines.len(), t, lines[0]);
    println!("{}", lines[1]);
}

