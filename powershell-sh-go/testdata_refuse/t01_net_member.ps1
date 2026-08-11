# t01_net_member: .NET member access — pinned REFUSE
# (PLAN_POWERSHELL_F.md: the .NET interop surface is out of the v1
# subset).
$x = "abc"
$len = $x.Length
Write-Output $len
