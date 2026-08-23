// t45_structs — struct literals lower to A1 Object literals (the store's
// record value) and named field reads to the FieldRead ext node
// (shir_nodes/field_read.node: JS obj.name, Perl $obj->{name}).
struct Point {
    x: i64,
    y: i64,
}

fn main() {
    let p = Point { x: 3, y: 4 };
    println!("{} {}", p.x, p.y);
    let q = Point { x: 10, y: 20 };
    // NOTE: field reads inside ARITHMETIC (`q.x + q.y`) still refuse —
    // the A1 arith AST has no field operand (same limitation as calls).
    let a = p.x;
    println!("{}", a + 1);
}
