// Nested `fn` items are HOISTED to top-level definitions named
// `<parent>__<local>`: a `fn` item captures nothing, so hoisting preserves
// its semantics exactly. This pins the shapes that matter:
//   * a call TEXTUALLY ABOVE the nested definition (legal in Rust),
//   * a nested `fn` calling a sibling nested `fn`,
//   * a nested `fn` declared inside an `if` block,
//   * a nested `fn` calling back into a free function.

fn free_double(x: i32) -> i32 {
    x * 2
}

fn outer(n: i32) -> i32 {
    let first = inner(n);
    fn inner(x: i32) -> i32 {
        let y = helper(x);
        y + 1
    }
    fn helper(x: i32) -> i32 {
        free_double(x) + 10
    }
    let mut total = first;
    if n > 0 {
        fn in_block(x: i32) -> i32 {
            x * 100
        }
        let bumped = in_block(n);
        total += bumped;
    }
    total
}

fn classify_all(items: &[i32]) -> i32 {
    fn rank(v: i32) -> i32 {
        if v < 0 {
            -1
        } else if v == 0 {
            0
        } else {
            1
        }
    }
    let mut acc = 0;
    for it in items {
        acc += rank(*it);
    }
    acc
}

// An ARITHMETIC (and call) operand in a numeric comparison: the operand is
// hoisted into a temp, so it is evaluated exactly once, in Rust's operand
// order, and the test grammar reads a $variable.
fn is_even(n: i32) -> bool {
    n % 2 == 0
}

// NOTE: the calls sit OUTSIDE the `&&`: a call on the RIGHT of `&&`/`||`
// would be skipped by Rust's short-circuit but run by the flat `-a` test, so
// the frontend REFUSES that shape rather than changing behaviour.
fn near(a: i32, b: i32) -> bool {
    let ra = rank_of(a);
    let rb = rank_of(b);
    (a - b) <= 1 && ra < rb + 3
}

fn rank_of(v: i32) -> i32 {
    v / 10
}

fn main() {
    println!("{}", outer(0));
    println!("{}", outer(3));
    println!("{}", classify_all(&[1, -2, 0, 5, -7]));
    println!("{} {} {}", is_even(4), is_even(7), near(3, 4));
    println!("{}", near(30, 31));
}
