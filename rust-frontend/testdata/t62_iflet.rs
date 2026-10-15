// t62_iflet — `if let` over the Option layer's None≡"" convention.
//
// `if let Some(x) = e { .. } else { .. }` binds x to the payload and runs
// the then-branch iff the scrutinee is not the None marker "".
// `if let None = e` tests the marker directly.
// The scrutinee hoists to a temp so it evaluates ONCE (Rust evaluates an
// if condition once too).
//
// Exactness bound: Some(v) IS v and None IS "", so a payload that IS ""
// is indistinguishable from None — that is the convention's documented
// edge (the same one `?` and Ty::Opt already live with), and this pin
// stays inside it.
fn classify(v: Option<i64>) -> &'static str {
    if let Some(n) = v {
        return "some";
    } else {
        return "none";
    }
}

fn main() {
    println!("{}", classify(Some(42)));
    let absent: Option<i64> = None;
    println!("{}", classify(absent));

    let present = Some("here");
    if let Some(s) = present {
        println!("got {}", s);
    }

    if let None = absent {
        println!("absent");
    }

    // else-if chain off an if-let
    if let Some(x) = absent {
        println!("{}", x);
    } else if let Some(y) = present {
        println!("{}", y);
    } else {
        println!("neither");
    }
}
