// t68_impl_lifetime — lifetime-only generic arguments ERASE on the impl
// target (`impl<'a> Marker<'a>`), exactly like the v0.14 function-level
// lifetime erasure: a lifetime has no runtime representation. The method
// is dead code at runtime (a definition-only lowering), so the program
// only pins that the impl is ACCEPTED; type generics still refuse.
struct Marker<'a> {
    name: &'a str,
}

impl<'a> Marker<'a> {
    fn tag(&self) -> i64 {
        0
    }
}

fn main() {
    println!("lifetime impl accepted");
}
