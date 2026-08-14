# t36 refuse pin: `Write-Output ("a" + "b")` — pwsh's `+` on strings is
# CONCATENATION (prints ab), while the A1 Arith is numeric-only — the
# plan's "concat is the one coercion trap: `"a" + "b"` must not become
# numeric arith". Divergence → refuse.
Write-Output ("a" + "b")
