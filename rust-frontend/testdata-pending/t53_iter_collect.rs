// t53_iter_collect — `SRC.iter().map(F).collect()` desugars to an
// accumulator + index loop + setArrayAppend. F is either a registered
// fn (dispatched via fnValue) or an inline closure (param bound to the
// element, body evaluated in place).
fn double(n: i64) -> i64 {
    n * 2
}

fn main() {
    // NOTE: .map(double) with a bare fn-path arg refuses today (iter()
    // yields &i64 — the fn expects i64); closures deref explicitly.
    let nums = vec![1, 2, 3];
    let _d2: Vec<i64> = Vec::new();
    let doubled: Vec<i64> = nums.iter().map(|n| double(*n)).collect();
    let mut total = 0;
    for d in doubled.iter() {
        total += *d;
    }
    println!("{}", total);

    let inc: Vec<i64> = nums.iter().map(|n| n + 1).collect();
    let mut total2 = 0;
    for x in inc.iter() {
        total2 += *x;
    }
    println!("{}", total2);

    let copy: Vec<&i64> = nums.iter().collect();
    let mut total3 = 0;
    for y in copy.iter() {
        total3 += *y;
    }
    println!("{}", total3);
}
