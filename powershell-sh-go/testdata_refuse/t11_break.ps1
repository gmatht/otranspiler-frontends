# t11_break: the `break` keyword form of the flow_control_statement
# node — pinned REFUSE. The t11 pin lands the `exit` form only; the
# A1 Break is a LOOP signal, and v1's only landed loop (the t06
# do_statement) duplicates its body OUTSIDE the loop (`do { B } while
# (C)` → `B; while (C) { B }`) — a break in B would land its first
# copy at top level, where the A1 renders a runtime signal throw while
# pwsh's script-level break halts the script and bash's is an
# error-and-continue: three different semantics, none soundly mappable
# (refuse > guess). The while/for rungs (not yet landed) host it.
break
