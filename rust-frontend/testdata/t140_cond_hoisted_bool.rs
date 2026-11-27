// The divmod shape: the RIGHT operand of `&&` is a method call hoisted into a
// temp and then tested as a bare boolean Var. That temp must carry the
// method's genuine type (Bool); before v0.43 `infer_ty` recorded nothing for
// `is_power_of_two`, the temp was Unknown, and this exact condition refused
// with "boolean variable as a condition (type not proven)".
fn main() {
    let vals: Vec<u32> = vec![1, 2, 3, 4, 8, 12, 16];
    let mut i = 0;
    let mut hits = 0;
    while i < vals.len() {
        let n = vals[i];
        if n > 0 && n.is_power_of_two() {
            hits += 1;
        }
        i += 1;
    }
    println!("{}", hits);
}
