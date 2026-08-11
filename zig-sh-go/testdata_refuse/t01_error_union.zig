// t01_error_union: an error union — pinned REFUSE (PLAN_ZIG_F.md: the
// error model is a dedicated rung after v1).
fn might_fail() !void {
    return error.OutOfMemory;
}

pub fn main() void {
    might_fail() catch {};
}
