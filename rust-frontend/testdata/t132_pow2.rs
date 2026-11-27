// `is_power_of_two()` (unsigned ints only in Rust) is exact by halving while
// even and testing `== 1`: 0 answers false, 1 answers true, and every value
// the store can hold keeps its own answer.
fn main() {
    let vals: Vec<u64> = vec![0, 1, 2, 3, 4, 6, 8, 64, 1024, 1099511627776, 1099511627777];
    let mut i = 0;
    while i < vals.len() {
        let n = vals[i];
        println!("{} {}", n, n.is_power_of_two());
        i += 1;
    }
    let u: u32 = 12;
    let z: u32 = 0;
    println!("{} {}", u.is_power_of_two(), z.is_power_of_two());
}
