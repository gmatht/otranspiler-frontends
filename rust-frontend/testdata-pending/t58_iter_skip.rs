// t58_iter_skip — .skip(K) shifts the iteration window (element reads
// offset by K); composes with .take(N) (window = K..K+N) and with
// map/filter over the shifted elements.
fn main() {
    let nums = vec![10, 20, 30, 40, 50];
    let skipped: Vec<i64> = nums.iter().skip(2).map(|n| *n).collect();
    let mut s1 = 0;
    for x in skipped.iter() {
        s1 += *x;
    }
    println!("{}", s1);

    let window: Vec<i64> = nums.iter().skip(1).take(3).map(|n| *n).collect();
    let mut s2 = 0;
    for x in window.iter() {
        s2 += *x;
    }
    println!("{}", s2);

    let doubled: Vec<i64> = nums.iter().skip(3).map(|n| n * 2).collect();
    let mut s3 = 0;
    for x in doubled.iter() {
        s3 += *x;
    }
    println!("{}", s3);
}
