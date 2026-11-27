// `Mutex<T>` / `MutexGuard` are vacuous single-threaded: the static IS the
// mutable global. `*g = v` writes it, `*g += n` updates it, `*g` reads it,
// and `let g = S.lock().unwrap();` binds a HANDLE on the static (not a
// copy — a write through `g` is visible to the next lock).
use std::sync::Mutex;

static COUNTER: Mutex<i64> = Mutex::new(0);
static FLAG: Mutex<bool> = Mutex::new(false);

fn main() {
    let mut c = COUNTER.lock().unwrap();
    *c += 5;
    println!("{}", *c);
    *c = 7;
    println!("{}", *c);
    let mut f = FLAG.lock().unwrap();
    *f = true;
    if *f {
        println!("on");
    }
    if *c == 7 {
        println!("seven");
    }
}
