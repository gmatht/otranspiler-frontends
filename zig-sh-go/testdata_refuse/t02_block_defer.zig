// t02_block_defer: a defer inside an if-block — block-scope defer is
// REFUSED (PLAN_ZIG_F §3: only function-scope defers are in the subset;
// the wrapping transform is a follow-up).
const std = @import("std");

pub fn main() void {
    if (1 > 0) {
        defer std.debug.print("x\n", .{});
    }
}
