// t56_enumerate_rev — .enumerate() pairs (position, element) with the
// position being the loop index itself (the pair is never materialized),
// and .rev() flips visitation order (position = bound-1-idx). Both
// compose with map/filter/take and tuple-destructuring params.
fn main() {
    let words = vec!["a", "b", "c"];
    let tagged: Vec<usize> = words
        .iter()
        .enumerate()
        .map(|(i, w)| i * 10)
        .collect();
    let mut s1: usize = 0;
    for x in tagged.iter() {
        s1 += *x;
    }
    println!("{}", s1);

    let flags: Vec<bool> = words
        .iter()
        .enumerate()
        .map(|(i, w)| i > 0)
        .filter(|b| *b)
        .collect();
    // bools print exactly ("true"/"false")
    for b in flags.iter() {
        println!("{}", b);
    }

    let nums = vec![4, 5, 6];
    // NOTE: combining the position with the ELEMENT arithmetically works
    // when both are proven integers; here the position drives the value.
    let vals: Vec<usize> = nums
        .iter()
        .rev()
        .enumerate()
        .map(|(i, _n)| i * 2 + 1)
        .collect();
    let mut s3 = 0;
    for v in vals.iter() {
        s3 += *v;
    }
    println!("{}", s3);
}
