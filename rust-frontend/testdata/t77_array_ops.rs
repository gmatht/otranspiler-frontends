// Dynamic array length, indexed reads/writes, and borrow iteration.
fn main() {
    let mut v = vec![10, 20, 30];
    v[1] = 7;
    v[0] += 5;
    let mut i = 2;
    v[i] = 9;
    println!("{} {} {} {}", v[0], v[1], v[2], v.len());
    let mut s = 0;
    for x in &v {
        s += x;
    }
    println!("{}", s);
}
