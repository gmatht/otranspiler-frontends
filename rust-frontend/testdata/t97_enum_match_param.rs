// Matching THROUGH a record parameter: tag test, payload binds and a unit
// variant, on a value that crossed the call boundary.
enum Cmd {
    Quit,
    Move { dx: i64, dy: i64 },
}

fn run(c: &Cmd) {
    match c {
        Cmd::Quit => println!("quit"),
        Cmd::Move { dx, dy } => println!("move {} {}", dx, dy),
    }
}

fn main() {
    run(&Cmd::Move { dx: 2, dy: 3 });
    run(&Cmd::Quit);
    let c = Cmd::Move { dx: 9, dy: 8 };
    if let Cmd::Move { dx, dy } = &c {
        println!("peek {} {}", dx, dy);
    }
}
