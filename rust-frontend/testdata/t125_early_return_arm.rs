// `return V;` INSIDE a match/if arm that is itself the initializer of a
// `let` — the shape `src/pipeline_native/capabilities/cat.rs::cat_file`
// uses (`let path = match w { IrExpr::Str(s, _) => { if … { return None; }
// … } … }`). The arm is still the function body, so the return exits the
// FUNCTION with a value; before v0.38 `in_fn` was dropped at that site and
// every such unit refused, which kept `cat.rs` from lowering at all.
enum Tok {
    Lit(String),
    Other,
}

fn path_of(t: &Tok) -> Option<String> {
    let path = match t {
        Tok::Lit(s) => {
            if s.contains('*') || s.starts_with('-') {
                return None;
            }
            format!("'{}'", s)
        }
        Tok::Other => return None,
    };
    Some(path)
}

fn classify(n: i64) -> String {
    let label = if n > 0 {
        if n > 10 {
            return String::from("big");
        }
        String::from("small")
    } else {
        return String::from("nonpositive");
    };
    format!("{}:{}", label, n)
}

// A LENGTH READ as a comparison operand — the shape
// `fish_backend.rs::body_needs_newlines` uses (`stmts.len() > 1`, the whole
// function body): the count is hoisted into a temp so the test grammar can
// read a $variable, exact for an array (Rust's element count == the store's).
fn needs_separator(items: &[String]) -> bool {
    items.len() > 1
}

fn main() {
    for probe in [Tok::Lit(String::from("a.txt")), Tok::Lit(String::from("*.sh")), Tok::Other] {
        match path_of(&probe) {
            Some(p) => println!("path {}", p),
            None => println!("path none"),
        }
    }
    println!("{}", classify(3));
    println!("{}", classify(30));
    println!("{}", classify(-3));
    let none: Vec<String> = Vec::new();
    let one = vec![String::from("a")];
    let two = vec![String::from("a"), String::from("b")];
    println!("{} {} {}", needs_separator(&none), needs_separator(&one), needs_separator(&two));
    if two.len() > 1 {
        println!("two:{}", two.len());
    }
    if none.len() > 1 {
        println!("unreachable");
    } else {
        println!("none:{}", none.len());
    }
}
