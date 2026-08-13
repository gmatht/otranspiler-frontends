# t12_for_init_iter (REFUSE pin): the for_initializer + for_iterator
# clauses of the for_statement node — `for ($i = 0; $i -lt 3; $i++)` —
# the assignment / comparison / `++` machinery of the plan's full
# for-lowering row ("the C frontend's for-lowering (init/cond/update →
# while)", PLAN_POWERSHELL_F.md §1), outside the v1 text-closed subset
# (assignment_expression / comparison_expression / unary `++` each
# refuse on their own rungs). The t12 pin is the condition-only form
# `for (; $c; ) { B }`.
for ($i = 0; $i -lt 3; $i++) { Write-Output $i }
