// `&mut` SCALARS and VECTORS are exact under the name-slot rule (v0.40):
// the store keys one location per NAME, so `*p += 1` / `*p = *p + 10` write
// the parameter's slot and the caller — which passes its variable under that
// same name — reads it back; `lines.push` appends to the array registered
// under the parameter name, which is the caller's array.
fn pushit(lines: &mut Vec<String>, t: &mut u32) {
    lines.push("x".to_string());
    lines.push("y".to_string());
    *t += 1;
    *t = *t + 10;
}

fn main() {
    let mut t: u32 = 1;
    let mut lines: Vec<String> = Vec::new();
    pushit(&mut lines, &mut t);
    println!("{} {} {}", lines.len(), t, lines[0]);
}
