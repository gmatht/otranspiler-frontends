// A SELF-REFERENTIAL enum (`Vec<Self>` in a variant) used to read as a
// name collision: the first registration pass typed the element before the
// enum's own name existed, a later pass typed it after, and the two shapes
// differed — so the enum was dropped as ambiguous and EVERY struct-variant
// pattern on it refused. Names are declaration facts, so they are declared
// before any shape is computed; resolution is order-independent and real
// collisions stay ambiguous.
enum Stmt {
    Assign { var: String },
    If { then: Vec<Stmt>, else_: Vec<Stmt> },
    While { test: String, body: Vec<Stmt> },
}

fn main() {
    let s = Stmt::While {
        test: "x".to_string(),
        body: vec![
            Stmt::Assign { var: "ab".to_string() },
            Stmt::If { then: vec![], else_: vec![Stmt::Assign { var: "c".to_string() }] },
        ],
    };
    match s {
        Stmt::Assign { var } => println!("assign {}", var),
        Stmt::If { then, else_ } => println!("if {} {}", then.len(), else_.len()),
        Stmt::While { test, body } => {
            println!("while {} ({})", test, body.len());
            for b in body {
                match b {
                    Stmt::Assign { var } => println!("  assign {}", var),
                    Stmt::If { then, else_ } => println!("  if {} {}", then.len(), else_.len()),
                    _ => println!("  nested"),
                }
            }
        }
    }
}
