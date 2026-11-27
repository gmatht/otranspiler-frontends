// ATOMIC statics are plain store slots single-threaded: `load` reads the
// slot, `fetch_add`/`fetch_sub`/`swap` are read-then-assign returning the
// OLD value, `store` writes it. The ordering argument has no observable
// effect without threads (Rust only constrains cross-thread order), so it
// is dropped exactly where rustc can observe nothing.
use std::sync::atomic::{AtomicUsize, Ordering};

static COUNT: AtomicUsize = AtomicUsize::new(0);
static SEEN: AtomicUsize = AtomicUsize::new(0);

fn bump(by: usize) -> usize {
    COUNT.fetch_add(by, Ordering::SeqCst)
}

fn main() {
    let a = bump(3);
    println!("{}", a);
    let b = COUNT.fetch_add(4, Ordering::Relaxed);
    println!("{}", b);
    COUNT.fetch_sub(2, Ordering::Relaxed);
    SEEN.store(COUNT.load(Ordering::Relaxed), Ordering::Relaxed);
    let old = SEEN.swap(100, Ordering::Relaxed);
    println!("{}", old);
    println!("{}", SEEN.load(Ordering::Relaxed));
    println!("{}", COUNT.load(Ordering::Relaxed));
}
