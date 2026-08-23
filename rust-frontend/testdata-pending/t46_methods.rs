// t46_methods — inherent methods with receivers: the mangled def's FIRST
// positional is the receiver object (`self` <- getVar("1")), and field
// reads on `self` go through FieldRead. All three receiver forms lower
// identically (JS objects are references: &mut self writes through —
// true; &self can't mutate — true; by-value self moves are
// use-after-move-checked by rustc).
//
// NOTE: MUTATING methods (`self.n += k`) still refuse — field WRITES
// need a contract shape (A1 Assign targets are plain vars); they share
// the record-storage core request with struct-literal round-trips.
struct Counter {
    n: i64,
}

impl Counter {
    fn get(&self) -> i64 {
        self.n
    }

    fn doubled(&self) -> i64 {
        self.n * 2
    }

    fn into_inner(self) -> i64 {
        self.n
    }
}

fn main() {
    let c = Counter { n: 10 };
    println!("{}", c.get());
    println!("{}", c.doubled());
    let d = &c;
    println!("{}", d.get());
    println!("{}", c.into_inner());
}
