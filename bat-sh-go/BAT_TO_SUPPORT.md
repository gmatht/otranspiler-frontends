# BAT_TO_SUPPORT — which CMD.EXE batch syntax should the transpiler support?

Scope: the `bat-sh-go` frontend (`.bat` → A1 shIR JSON → Perl/ESTree backends).
Source text reviewed: `../bat_syntax.txt` (Wikibooks, *Windows batch scripting*).
Baseline capability: `FRONTEND.md` (the landed v1/v1.1 subset) + the A1 contract
(`frontends/plan.md`, `sh2perl/src/ir.rs`).

This document is a *recommendation ledger*: for every feature of cmd.exe batch
syntax named in the source, one verdict — SUPPORT / PARTIAL / REFUSE /
PASSTHROUGH / NO-OP — and the reason. It is not a spec of how to lower each
feature; it is the answer to "should we, and why (or why not)".

## Verdict legend

| Verdict | Meaning |
|---|---|
| **SUPPORT** | Implement (now, or in the pinned v2 rung named). A testdata pin lands with it. |
| **PARTIAL** | Support a pinned subset; the divergent edges REFUSE loudly. |
| **REFUSE** | Refuse loudly, forever or until a pinned runtime rung exists. Never guess. |
| **PASSTHROUGH** | Emit as an opaque external exec; the target machine must have the command. No translation. |
| **NO-OP** | No observable effect in the transpiled model (console/OS-only). Dropped or emitted as a no-op. |
| **OUT OF SCOPE** | Not present in batch file *text*; nothing for a compiler to see. |

## Decision criteria (in priority order)

1. **Expressible in A1.** The node set is fixed by the core (`IrStmt`/`IrExpr`).
   No new node without a core change (a frontend may not extend the contract).
2. **Oracle-pinnable.** cmd.exe is Windows-only, so the gate compares against
   *recorded* expectations, never a live native run. Anything whose exact
   output we cannot pin down ourselves is unpinnable → refuse. Divergences
   from a live cmd.exe are undetectable by the gate — the frontend must be
   conservative by construction.
3. **POSIX-portable meaning.** The backends execute on Linux (bash/Perl/Node).
   Windows-essence features (registry, console, drives, ACLs) cannot have one.
4. **Corpus utility.** How often real batch files reach for it. The v1 subset
   already covers the 90% core; v2 should add what real scripts actually use
   (`shift` arg loops, `&&`/`||`, pipes, `set /p`, delayed expansion, `%~`
   forms, `setlocal/endlocal`).
5. **Refuse > guess** (the standing discipline). A wrong number is worse than
   a loud refusal; refusals are pinnable (`testdata_refuse/`), miscompiles are not.

Two verified A1 facts that several verdicts lean on (probed with
`debashc --shir`):

- `a && b` / `a || b` lower to `BinOp And/Or` between exec Calls — the exact
  shape batch conjunctions want (zero new A1 surface).
- `${x:0:1}` lowers to `param("slice", "x", 0, 1)` — and JS `slice` already
  implements cmd's substring semantics, including negative offsets
  (`%a:~-1%` = last char, `%a:~1,-2%` = drop last two). The cleanest mapping
  in the whole language.

---

## 1. How a command line is interpreted (the four-phase model)

Variable substitution → quoting → syntax → redirection is *the* parsing model,
not a feature. **SUPPORT (architecture)** — the frontend already implements all
four phases. Two notes:

- The phase *order* is pinned by the existing testdata (a `%var%` substituted
  inside a quoted string, an `&` inside quotes, a redirect after `echo`).
- The immediate-expansion phase (substitution happens when the line is read,
  not when it executes) is the one place the compiled model *cannot* be
  faithful — see §20 (delayed expansion). That is the single most important
  semantic decision in this document.

## 2. Variable substitution

| Feature | Verdict | Reason |
|---|---|---|
| `%varname%` (env var, case-insensitive) | **SUPPORT** (v1) | The core of the language; already landed. Names lowercased into the A1. |
| `%0`–`%9` | **SUPPORT** (v1) | Positionals; `%0` = the script path (the runtime has self-location). |
| `%*` | **SUPPORT** (v1) | All args; captured once — "SHIFT has no impact" is trivially true in the compiled model. |
| `%CD%` | **SUPPORT** (v2) | Maps to getcwd. The trailing-slash rule (root dir only) needs a pin, but it is expressible. |
| `%TIME%` / `%DATE%` | **REFUSE** | Locale-dependent formats; no POSIX value matches cmd's string. Unpinnable output. (The `date`/`time` commands, §26, refuse for the same reason.) |
| `%RANDOM%` | **SUPPORT** (v2) | Range 0–32767 matches bash `$RANDOM` exactly (the sh backend already renders it, even in POSIX mode via awk). Runtime read, no shim needed. |
| `%ERRORLEVEL%` | **SUPPORT** (v1) | Already mapped to `$?` (`getVar("?")`). |
| `%CMDEXTVERSION%` | **REFUSE** | A cmd.exe version constant with no POSIX meaning. Could emit `1` (extensions always on), but that is a guess. |
| `%CMDCMDLINE%` | **REFUSE** | The invoking command line of cmd.exe itself; no equivalent. Already on the NOT-in-v1 list. |
| `set errorlevel=0` (shadowing) | **REFUSE** | The documented anti-pattern. Shadowing semantics are pin-able in principle but the divergence trap (a later `%ERRORLEVEL%` read) is exactly the kind of thing the recorded-oracle gate cannot catch. Refuse loudly. |

## 3. Quoting and escaping

| Feature | Verdict | Reason |
|---|---|---|
| Double-quoted strings | **SUPPORT** (v1) | Quotes are part of the argument (echo `"hello"` prints the quotes) — already pinned. |
| Caret `^` escape of `< > \| & ^` | **SUPPORT** (v2) | Every real batch uses `echo Owner ^& son`. v1.1 has only the line-continuation form; the inline form is the obvious next rung. Pin: caret escapes only the meta set — **not** space (`attrib File^ 1.txt` documented not to work), not quotes, not `%`. |
| Caret-escaped newline (`... ^<newline>`) | **SUPPORT** (v1.1) | Already landed; the space-before-caret rule (`if 1 equ 1 ^`) needs the pin it already has. |
| Triple caret after a pipe (`echo A ^^^^ B`) | **PARTIAL** | The triple-caret rule only exists *because* of pipe-parsing phases. Land with pipes (§4); refuse until then. |
| `%%` → single `%` in batch | **SUPPORT** (v1) | Already pinned (`echo 100%% percent`). |
| `%` cannot be quoted/`^`-escaped | **SUPPORT** | A parser rule, not a feature: `^%` and `"%..."%` still start expansion. Pin it (the `echo ^%temp^%` vs `echo %%temp%%` pair). |

## 4. Syntax

| Feature | Verdict | Reason |
|---|---|---|
| Simple commands | **SUPPORT** (v1) | — |
| Pipelines `a \| b` | **SUPPORT** (v2) | The A1 `pipeline` Call is the core's exact bash `a \| b` shape (verified). Runs all stages in parallel — same as cmd. Core corpus utility. Carry the caret-tripling rule (§3) with it. |
| `&` unconditional conjunction | **SUPPORT** (v1) | Already landed (split on first top-level `&`; the spaced-`&` echo quirk pinned). |
| `&&` / `\|\|` | **SUPPORT** (v2) | The A1 `BinOp And/Or` shape is byte-identical to the core's bash conjunction (verified). cmd's exit-code semantics match POSIX. Very common in real scripts. |
| `&`-chain error level = last command's | **SUPPORT** (v2) | The runtime's natural sequential-execution exit status; pin it in the conjunction test. |
| Parenthesized compound `( ... )` | **SUPPORT** (v1) | Already landed (blocks, multi-line). |
| `@echo off` / `echo on/off` | **SUPPORT** (v1) | Echo-control directive; no-op in the compiled output. |

## 5. Redirection

| Feature | Verdict | Reason |
|---|---|---|
| `> file`, `>> file` | **SUPPORT** (v1.1) | Landed. Pin the no-space glued forms and the spaced-`>` echo quirk (`echo text >file` echoes the space — the no-space form is the portable one; already documented). |
| `< file` | **SUPPORT** (v2) | fd 0 redirect; needed by `set /p ... < file`, `find < file`, `clip < file`. The A1 Redirect takes an fd — expressible. |
| `N>` / `N>>` (fd-prefixed) | **SUPPORT** (v1.1) | Landed. Pin the digit-before-`>>` quirk: `echo 2>>file` redirects fd 2, it does not echo "2"; `(echo 2)>>file` is the workaround. This is a parser rule (bare digit adjacent to `>>` is an fd). |
| `>&h` (fd duplication) | **SUPPORT** (v2, `2>&1`) | The A1 Redirect `&N` fd-dup target already renders through `sh2.redirectSync` (the powershell t19 precedent). `2>&1` is the overwhelmingly common form. |
| `<&h` | **REFUSE** | Input duplication is rare, unpinned, and the runtime has no input-dup precedent. Refuse until a corpus case demands it. |
| NUL device | **SUPPORT** (v1.1) | → `/dev/null`. |
| CON / PRN / AUX / COM1-9 / LPT1-9 | **REFUSE** | Console/hardware devices with no POSIX meaning. `type con >file` is interactive console input — refuse. |
| Redirection before the command (`>file echo x`) | **SUPPORT** (v1.1) | Landed. |
| Group redirection `( ... ) > file` | **SUPPORT** (v1.1) | Landed (Redirect wraps the block). |
| Per-iteration loop-body redirection (`for ... do echo %i > file`) | **SUPPORT** | The natural lowering puts the Redirect *inside* the loop body → each iteration re-opens the file, exactly cmd's "starts redirection anew" behavior. Pin it. |

## 6. Batch reloading

**OUT OF SCOPE / REFUSE.** cmd re-reads the batch file after each line or
bracketed group; the transpiler compiles a static snapshot. Self-modifying
batches (the `ping & REM wait & echo A` example exists only to demonstrate the
behavior) are pathological and have no faithful compiled meaning. Document the
divergence; do not attempt a file-rewatch shim. Nothing in the source *text*
signals the intent, so there is nothing to refuse on — it is simply not
representable, and that is acceptable.

## 7. Environment variables

| Feature | Verdict | Reason |
|---|---|---|
| `set VAR=value` / `set "VAR=value"` / `set VAR=` | **SUPPORT** (v1) | Landed. The `set name = Peter` → variable `"name "` quirk is a parser pin (spaces around `=` are part of the name/value). |
| `set` (bare) / `set home` (prefix listing) | **REFUSE** | Lists the runtime environment; output is unpinnable and machine-dependent. |
| `path dirs` (set PATH) | **SUPPORT** (v2) | Just a set; `;` → `:` separator translation needs a pin. |
| `path` (bare listing) | **REFUSE** | Unpinnable output (and `PATH=` prefix differs). |
| `setx` | **REFUSE** | Registry persistence; Windows-essence. |
| `COMSPEC` | **REFUSE** | Path to cmd.exe; meaningless on POSIX. |
| `PATHEXT` | **REFUSE** | Windows extension-resolution mechanism; the transpiled script calls POSIX names directly. |
| `PROMPT` | **NO-OP** | Console prompt; the `$X` expansion table (§PROMPT in the source) is interactive UI. Setting the var is harmless; drop it. |

## 8. Switches

| Feature | Verdict | Reason |
|---|---|---|
| Case-insensitive command names and switches | **SUPPORT** (v1) | Already implemented (all names lowercased). |
| Single-letter switches on mapped builtins (`dir /b` → `ls -1`) | **SUPPORT** (v1.1) | The flag-translation table. |
| Multi-letter switch names (`taskkill /im`, `sort /reverse`) | **PARTIAL** | For *mapped* builtins only the translated flags are pinned; unknown flags REFUSE. For external passthrough commands the flags pass through verbatim (PASSTHROUGH — the command decides). |
| `/?` help | **REFUSE** | Unpinnable Windows help text. Rare in scripts (the common pattern is `if "%~1"=="/?"`). Refuse loudly on mapped builtins. |
| `sort /reve` (substring switch match) | **REFUSE** | Windows-specific switch-resolution quirk; no portable meaning. |
| Accumulation rules (`dir/b/s` works, `tree/f/a` doesn't) | **PARTIAL** | Per-command parser details. The frontend translates only what it pins per mapped builtin; unknown combinations refuse. This is the existing per-builtin table approach. |

## 9. Error level

| Feature | Verdict | Reason |
|---|---|---|
| `exit /b [N]` | **SUPPORT** (v1.1) | Landed. |
| `if errorlevel N` (≥ semantics, incl. negative) | **SUPPORT** (v1.1) | Landed (`[[ $? -ge N ]]`). |
| `if %errorlevel% equ 0` | **SUPPORT** (v1.1) | With `if` + `equ` (§26). |
| `&&` / `\|\|` tests | **SUPPORT** (v2) | See §4. |
| `cmd /c "exit /b N"` | **PARTIAL** (v2) | `cmd /c "quoted command"` runs a nested batch line. The quoted content is batch *text* — the frontend can parse it and inline it (the `cmd /c "exit /b 10"` case inlines to `Exit(10)`). Pin the single-command subset; refuse arbitrary nesting (refuse > guess). |
| `set myerrorlevel=%errorlevel%` | **SUPPORT** | A normal var copy. |
| Same-line `%errorlevel%` read after a status change | **REFUSE** | `if 1 equ 1 ( cmd /c "exit /b 1" & echo %errorlevel% )` prints `0` in cmd (expanded before execution) but `1` in the compiled model. This is the §20 immediate-expansion trap; same refusal. |

## 10. String processing

| Feature | Verdict | Reason |
|---|---|---|
| Substring `%a:~start,len%` (incl. negative indexes) | **SUPPORT** (v2) | The cleanest mapping in the language: the core's `${x:0:1}` → `param("slice", ...)` lowering, and JS `slice` already implements cmd's (start,len) semantics **including** negative offsets and negative lengths (`%a:~1,-2%` = `slice(1,-2)`). Probe the core's emission, pin the negative-index cases, done. |
| "Starts with" idiom (`if %a:~0,1%==a`) | **SUPPORT** (v2) | Falls out of substring + if. |
| Replacement `%a:pat=rep%` | **REFUSE** | cmd replaces **all** occurrences and supports the `*pat` prefix ("up to and including the first"); bash `${a/pat/rep}` is first-match-with-globs and has no `*pat` form. No A1 primitive matches. The containment trick (`if not "%a:bc=%"=="%a%"`) refuses with it. A runtime shim is possible *only* with a pinned semantics rung — defer. |
| Split by `for %%a in (%myvar%)` (space/comma/semicolon) | **SUPPORT** (v1) | Already the word-list `for` semantics; the separator set (space/comma/semicolon) needs a pin if not already there. |

Note: cmd's own limitation "string processing does not work with parameter
variables" is a cmd bug; nothing to reproduce — the compiled model is free to
be more capable where the *source* never depended on the limitation.

## 11. Command-line arguments

| Feature | Verdict | Reason |
|---|---|---|
| `%0`–`%9`, `%*` | **SUPPORT** (v1) | Landed. |
| `shift` | **SUPPORT** (v2) | The canonical "loop over all args" idiom (`:argloop … shift … goto`) is the most common pattern in real batches after echo/set/if. The runtime has the `shift` builtin and a positional stack; probe the core's bash `shift` emission and reproduce it. `%*` unaffected by shift — trivially true. |
| Argument separators (space, comma, `;`, `=`, tab; runs collapse) | **SUPPORT** (v2) | This is cmd's *invocation* parsing. It matters where the frontend binds args: `call :label a,b;c` and the runtime's argv binding. Pin the exact separator set and run-collapsing. |
| Quoted args keep their quotes | **SUPPORT** (v1) | The `%~1` family strips them (§14). |
| No-space internal-command forms (`echo.`, `cd..`, `cd\`, `dir/b/s`) | **SUPPORT** (v1.1/2) | `echo.`/`echo:`/`echo\` landed (v1.1). `cd..`, `cd\`, `dir/b/s` need per-builtin pins (the `.`/`\` is an argument separator for internal commands only — `tree.` must still fail as an unknown command). |

## 12. Wildcards

**PARTIAL — the frontend must decide per command whether to quote globs.** This
is a real semantic divergence, not a cosmetic one: cmd does *not* expand
wildcards itself — `echo *.txt` prints `*.txt` literally, and `ren *.cxx *.cpp`
hands the patterns to `ren`'s own matcher.

- Mapped builtins that glob (**dir→ls, copy→cp, del→rm, move→mv**): leave
  patterns unquoted; POSIX tools glob the same class of patterns. Documented
  approximation: the `?`-matches-no-period rule and 8.3 short-name matching
  are Windows-specific and not reproduced.
- Mapped builtins that must NOT glob (**echo, set, type…**): single-quote
  wildcard arguments so the POSIX shell leaves them literal. `echo *.txt`
  must keep printing `*.txt`.
- `ren *.cxx *.cpp` (destination pattern interpreted by ren itself) | **REFUSE**
  | cp/mv do not translate destination patterns. The bare-file rename (v1.1)
  stays supported; the pattern form refuses.
- `for %%v in (*.txt)` | **SUPPORT** (v1) | The for-wordlist file-match
  behavior is already the v1 semantics; pin the "no match → item dropped"
  rule.

## 13. User input

| Feature | Verdict | Reason |
|---|---|---|
| `set /p var=prompt` | **SUPPORT** (v2) | The A1 `read` builtin already exists (for /f uses it). Prompt printed, one line read, `set /p x < file` reads the first line (a head -1 idiom — pin). |
| `set <NUL /p=text` (prompt without newline) | **SUPPORT** (v2) | Falls out of set /p with a `/dev/null` input redirect. |
| `choice` | **REFUSE** | Interactive single-key console input + console rendering; the A1 read is line-oriented. The errorlevel mapping is the easy part, the console keypress is Windows-essence. Refuse. |
| `type con >file` | **REFUSE** | Interactive console input copy; unpinnable. |

## 14. Percent tilde `%~…`

| Feature | Verdict | Reason |
|---|---|---|
| `%~1` (strip quotes) | **SUPPORT** (v2) | Pure string transform; pin the exact strip rule. |
| `%~n1`, `%~x1`, `%~p1`, `%~dp0`, `%~nx0` (name/ext/path decompositions) | **SUPPORT** (v2) | Pure string decompositions over the *argument text* with backslashes normalized to `/`. `%~dp0` = dirname of the script path (the runtime knows its own path); `%~nx0` = basename. The values differ from Windows only in separator characters — a documented approximation, pinnable. |
| `%~d1` (drive letter) | **REFUSE** | Drives do not exist on POSIX. |
| `%~f1` (canonical full path), `%~a1` (attributes), `%~t1` (mtime), `%~z1` (size), `%~s1` (short name), `%~$PATH:1` | **REFUSE** | Filesystem/Windows-essence: attributes and 8.3 short names have no POSIX concept; sizes/mtimes are machine-dependent (unpinnable); `$PATH:` search duplicates `which` with different semantics. The `for /r … %~zi` size-filter idiom refuses with them. |
| `%~` on `%%i` loop vars | **SUPPORT** (v2) | Same decompositions on the loop var; follows from the above. |

## 15. Functions (call / labels / setlocal)

| Feature | Verdict | Reason |
|---|---|---|
| `call :label [args]` | **SUPPORT** (v1.1) | Landed (A1 Function extraction, args → positionals). |
| `goto :eof` | **SUPPORT** (v1.1) | Landed. |
| `goto label` (jump) | **SUPPORT** (v1) | Landed (inline Goto/Label; the core's `restructure_goto` pass). |
| `setlocal` / `endlocal` | **SUPPORT** (v2) | Env snapshot/restore. The famous `endlocal & set result=…` return-value idiom lowers to restore-then-set — the compiled sequence order matches cmd exactly. Needs a runtime save/restore pair (or a Subshell wrap where the setlocal/endlocal bracket a block the frontend can bound). Pin the `endlocal & set` pattern. |
| `setlocal EnableDelayedExpansion` | **SUPPORT** (v2) | See §20. |
| `call other.bat` (whole-file call) | **REFUSE** (v3 candidate) | A separate compilation unit. Inline-compiling the callee is possible in principle (parse the callee, inline its body — env changes then propagate correctly) but is a linker-scale feature. Refuse until a pinned rung exists. |
| `call notepad.exe` (undocumented exe launch) | **PASSTHROUGH** | An opaque exec of the named program. |
| `mybatch.bat` (transfer without call) | **REFUSE** | Control never returns; the compiled model cannot faithfully represent "run this other file and stop". |
| `goto` into a `for` body | **REFUSE** | cmd's "goto breaks loop bookkeeping" quirk is *not* reproduced by the core's goto restructuring. A goto whose target lies inside a for body must refuse loudly (refuse > guess). |

## 16. Calculation (`set /a`)

| Feature | Verdict | Reason |
|---|---|---|
| `+ - * / %`, parens, unary minus, `%var%` operands | **SUPPORT** (v1.1) | Landed. |
| Full operator set: `~ & \| ^ << >>`, `!` negation, comma list, `+= -= *= /= %= &= ^= \|= <<= >>=` | **PARTIAL** (v2) | The A1 arith node has these operators (the bash `$(( ))` lowering). **The catch:** cmd is 32-bit signed with wrap-around; the A1/backends are bash-faithful (64-bit). Every result must be pinned with an explicit 32-bit mask (two's complement), and the edges that can't be masked faithfully must refuse: overflow/underflow of literals (`2147483647+1` → `-2147483648`), `1<<32` → 0, negative shift counts, `0xffffffff` → `-1`. The `--true64` work in the core shows how seriously the ecosystem treats integer-width fidelity — cmd is the 32-bit side of the same problem. |
| Hex/octal literals | **PARTIAL** | cmd parses `0x`/`0` literals with unsigned-then-two's-complement; bash treats them as plain values. Pin the small-literal subset (`0xffff` = 65535, `0777` = 511); refuse literals ≥ `0x80000000` until the 32-bit rung lands. |
| `set /a num="255^127"` / `255^^127` (quote/caret forms) | **SUPPORT** (v2) | The `^`/quote handling comes with the caret rung (§3). |
| The `if 1==1 (set /a n1=(2+4)*5)` paren conflict | **Non-issue** | A cmd *parser* quirk; the compiled model has no such conflict. Nothing to reproduce. |
| `set /a` bare (no assignment, interactive echo) | **REFUSE** | Interactive-use-only form; unpinnable/pointless in a compiled program. |

## 17. Finding files

| Feature | Verdict | Reason |
|---|---|---|
| `dir /b /s` | **SUPPORT** (v1.1) | Landed. |
| `for /r` (recursive files) and `/d` (dirs) | **SUPPORT** (v2) | A1 For over a `find`-based capture (the core's `for i in $(find …)` pattern) or a runtime walk helper. The `%~zi`/`%~ti` filters inside the body stay refused until §14's fs-dependent forms land — pin the *iteration* without the metadata first. |
| `for /f` over `'command'` | **SUPPORT** (v1.1) | Landed. |
| `findstr` | **PARTIAL** (v2) | Map to `grep` with the pinned flags (`/i`→`-i`, `/s`→`-r`, `/v`→`-v`, `/c:`→`-e`, `/x`→`-x`, `/r`→`-E`, `/m`→`-l`, `/n`→`-n`, `/l`→`-F`). Document the dialect caveat: findstr's regex is a limited subset and its space-separated OR / multiple `/c:` terms differ from grep — refuse the divergent multi-term forms. |
| `find` | **SUPPORT** (v1.1) | Landed as `grep -F`. The `find /C /V ""` line-count idiom is a v2 pin (`grep -cv` equivalence needs an exact record). |
| `forfiles` | **REFUSE** | External, Windows-only, locale-dependent date matching; `find -newer` is a rewrite, not a translation. |
| `where` | **SUPPORT** (v1.1) | Landed (`where`→`which`). The `$path:`/`$windir:` query forms refuse (env-var path queries; unpinned). |

## 18. Keyboard shortcuts (F1–F9, Tab, history)

**OUT OF SCOPE.** These are console-UI affordances of the interactive
interpreter. They never appear in batch file *text*; there is nothing for a
compiler to see. State this in one line; do not spend budget on it.

## 19. Paths

| Feature | Verdict | Reason |
|---|---|---|
| `\` separators | **SUPPORT** (v1.1) | Normalized to `/` for mapped builtins only (already the rule — echo text keeps backslashes verbatim). |
| `.` `..`, relative paths, doubled `\` | **SUPPORT** (v1.1) | Survive normalization; POSIX resolves them the same. |
| Drive-qualified paths (`C:\…`, `C:`) | **REFUSE** | Windows namespace; no POSIX equivalent. Refuse loudly on mapped builtins. |
| `/` ambiguity (switch prefix vs separator) | **PARTIAL** | The existing rule (normalize only for mapped builtins) is the pin; `/` inside a path argument is a switch, exactly cmd's rule. |
| UNC `\\server\share` | **REFUSE** | No POSIX equivalent. `pushd \\server` (auto-drive creation) refuses with it. |
| NUL | **SUPPORT** (v1.1) | → `/dev/null`. |
| CON / PRN / AUX / COMx / LPTx | **REFUSE** | Devices; see §5. |
| `cd /d` (drive switch) | **PARTIAL** | The `/d` flag is meaningless on POSIX (one namespace): ignore it, refuse a drive-qualified argument. |
| Trailing-`\` folder quirks (`attrib .\System32\` fails) | **REFUSE** | Windows filesystem semantics not reproduced by cp/mv/ls; refuse the ambiguous cases rather than silently differ. |

## 20. Delayed expansion (`!var!`) and arrays

**SUPPORT (v2), with one mandatory refusal edge — and this is the deepest
semantic in the language.**

The key insight: the compiled model evaluates variable reads *when the
statement executes*. That **is** delayed expansion. So:

- `!var!` lowers to the same `getVar("var")` read as `%var%`. The two spellings
  collapse in the compiled model.
- cmd's `%var%` immediate expansion (value fixed when the line/block is read)
  is *not* what the compiled model does — the current frontend already
  implements delayed semantics for `%var%` (the FRONTEND.md semantics note).
  The divergence is observable only when a read follows a write on the same
  line or in the same block: `set a=1 & echo %a%` prints empty in cmd, `1` in
  the compiled model.
- Recommendation:
  1. **SUPPORT** `!var!` when a `setlocal EnableDelayedExpansion` is in scope
     (the frontend tracks the directive) — the compiled model is faithful by
     construction. The array idioms (`array_%i%` / `!array_%i%!`) come with it.
  2. **REFUSE** `!var!` when delayed expansion is *not* enabled — cmd would
     treat it as literal text; a silent getVar would be a miscompile.
  3. **REFUSE** the same-line/same-block `%var%` write-then-read case (a
     compile-time analysis: a `%var%` read with an earlier write in the same
     line/block → refuse loudly). This is the one place the immediate-vs-
     delayed distinction is observable, and it cannot be reproduced without
     parse-time constant expansion.

## 21–23. Perl one-liners, Unix commands, utility tasks

**PASSTHROUGH.** `perl`, `grep`, `sed`, `awk`, `wc`, `head`, `tail`, `sort`
(external), `touch` workarounds (`copy /b file+,`), `fc`/`certutil` hex hacks,
`powershell Format-Hex` — these are opaque external commands in the source.
The frontend already emits unknown commands as execs; whether they *run* is the
exec allowlist's business, not the transpiler's. No translation, no refusal.
(The Windows-only ones — `certutil -encodeHex`, `fc /b`, `debug`, `fsutil` —
are effectively REFUSE at runtime on the POSIX targets; that is a documented
consequence of PASSTHROUGH, not a frontend feature.)

## 24. Elevated privileges (`net session`)

**PASSTHROUGH.** An external command whose failure/success is the test. Emits
as an exec; the allowlist governs. Not a transpiler feature.

## 25. cmd's limitations ("no while", "no arrays", …)

**Nothing to reproduce.** These are grammar limitations of cmd.exe; the
compiled output is not limited by cmd's grammar, and the source can never
*depend* on a limitation except in the negative ("this never works"), which
the compiled output is free to exceed. The one relevant note: batch's
goto-based loops are the language's `while`, and they are already SUPPORT via
goto (`:loop … goto :loop`); the core's `restructure_goto` pass gives the
backends structured flow. `break`/`continue` do not exist in batch — nothing
to do.

## 26. Built-in commands

| Command | Verdict | Reason |
|---|---|---|
| ASSOC, FTYPE | REFUSE | Registry file-type associations; Windows-essence. |
| BREAK | NO-OP (+ redirects) | Does nothing on NT; `break > empty.txt` works because the no-op has no output — the redirect creates the file. |
| CALL | SUPPORT (v1.1 `:label`) / REFUSE (`file`) / PASSTHROUGH (exe) | See §15. |
| CD / CHDIR | SUPPORT | → `cd`/`pwd`. `cd ..`, `cd \`, wildcard `cd C:\W*` per §19 pins. |
| CHCP | REFUSE | Code pages; console encoding. |
| CLS | NO-OP (v1) | Landed. |
| COLOR | NO-OP | Console colors. |
| COPY | SUPPORT (v1.1) | → `cp`; `/y`→`-f`, `/q`…; `copy F:\File.txt` (into cwd) and `copy Dir1 Dir2` (top-level files only) need per-form pins; `copy /b file+,` touch workaround → PASSTHROUGH approximation or REFUSE (unpinned semantics — recommend REFUSE). |
| DATE / TIME | REFUSE | Locale output; the bare forms are interactive setters. |
| DEL / ERASE | SUPPORT (v1.1) | → `rm`; `/q`→`-f`, `/s`→`-r`; `/p` (confirm each) → REFUSE (interactive). |
| DIR | SUPPORT (v1.1) | → `ls`; `/b`→`-1`, `/s`→`-R`; `/a` flags and `/o` sort flags → PARTIAL (pin the `-a`/`-A`/`-t` translations; refuse unpinned combos like `/od` ordering *per directory* semantics). |
| ECHO | SUPPORT (v1) | All forms (`echo.`, `echo:`, bare, `@echo off`). The `%random%>>file` fd quirk per §5. |
| ELSE | SUPPORT (v1.1) | With if. |
| ENDLOCAL | SUPPORT (v2) | With setlocal (§15). |
| ERASE | SUPPORT | Synonym of DEL. |
| EXIT | SUPPORT (v1.1) | `exit /b [N]` landed; bare `exit` = Exit(null) (closing the console is just program exit in the compiled model). |
| FOR | SUPPORT (v1/1.1) / PARTIAL (v2) | basic, `/l` (unit step), `/f` landed. `/l` non-unit steps → PARTIAL (the A1 Range is unit-step; multi-step needs a pin or the ForInit shape — refuse until pinned). `/d`, `/r` v2. `skip=`/`eol=`/`usebackq` v3 (pinnable, rarely used). Token sets not starting at 1 REFUSE (already). |
| FTYPE | REFUSE | See ASSOC. |
| GOTO | SUPPORT (v1/1.1) | `goto :eof` landed. Goto into a for body → REFUSE (§15). |
| IF | SUPPORT (v1.1) / PARTIAL (v2) | `==`, `defined`, `exist`, `errorlevel`, `not` landed. `equ/neq/lss/leq/gtr/geq` → v2 (numeric vs string comparison is a real semantic — pin `0 equ 00` = true vs `0==00` = false). `/i` case-insensitive → v2. `cmdextversion` → REFUSE (with `%CMDEXTVERSION%`). |
| MD / MKDIR | SUPPORT (v1.1) | → `mkdir`; multi-dir form `md Dir1 Dir2` needs a pin (POSIX mkdir takes one path per call — loop or `mkdir -p` semantics differ). |
| MKLINK | REFUSE | Windows link types (junctions/symbolic); `ln -s` is a different beast with privilege requirements. |
| MOVE | SUPPORT (v1.1) | → `mv`; the dir-rename vs dir-into-dir ambiguity is a real mv/`mv Dir1 Dir2` decision — pin per form (rename when target absent, move-into when present — mv already does this). |
| PATH | PARTIAL | `path dirs` SUPPORT (v2, `;`→`:`), bare listing REFUSE (§7). |
| PAUSE | REFUSE | Interactive wait; a compiled non-interactive run has no faithful meaning (a NO-OP would silently change timing; a read would change behavior). |
| POPD / PUSHD | SUPPORT (v2) | Plain directory stack → runtime helper (bash has the same builtins — probe the core's emission). The drive-mapping forms (`pushd \\server`) REFUSE. |
| PROMPT | NO-OP | §7. |
| RD / RMDIR | SUPPORT (v1.1) | → `rmdir`; `/s /q` → `rm -rf` (PARTIAL — cmd asks once without `/q`; pin the confirmation-free form only). |
| REM / `::` | SUPPORT (v1) | Comments. The `::`-inside-parens trouble is a cmd quirk the compiled model doesn't have — nothing to reproduce. |
| REN / RENAME | SUPPORT (v1.1) | → `mv` with the destination-resolved-to-OLD's-dir rule (landed). Pattern form (`ren *.cxx *.cpp`) → REFUSE (§12). |
| SET | SUPPORT (v1) / REFUSE (listing) | §7. |
| SETLOCAL | SUPPORT (v2) | §15. |
| SHIFT | SUPPORT (v2) | §11. |
| START | PARTIAL | Async launch → the A1 `Background` node; `start /wait` → plain exec. The title-heuristic, `/low`, `start .`/`start ..`, and `mailto:` URL forms → REFUSE (console/window/URL Windows-essence). |
| TITLE | NO-OP (v1) | Landed. |
| TIME | REFUSE | §DATE/TIME. |
| TYPE | SUPPORT (v1.1) | → `cat`; `type NUL > tmp.txt` (empty file) and `type *.txt` (glob — cat globs) work; `type con` REFUSE. |
| VER | PARTIAL | → `uname` (v1.1) — but the output is a *different* version string; a documented approximation, pinnable as such. |
| VERIFY | NO-OP | No effect on NT. |
| VOL | REFUSE | Volume labels; no POSIX meaning. |

## 27. External commands

**PASSTHROUGH by default** (the frontend already emits unknown commands as
opaque execs). Per-class notes:

| Class | Verdict / notes |
|---|---|
| Network (arp, ipconfig, net, ping, tracert…) | PASSTHROUGH. Optional v2: `ping -n` → `ping -c` flag translation (the Windows ping output differs anyway; only worth it for corpus demand). |
| Registry / OS (reg, setx, schtasks, sc, systeminfo, wmic, gpresult, bcdedit, diskpart, driverquery, shutdown, rundll32) | PASSTHROUGH, effectively REFUSE at runtime on POSIX (no such command). Documented consequence, not a frontend feature. |
| File tools (attrib, xcopy, robocopy, replace, fc, comp, cipher, compact, expand, makecab, format, chkdsk, convert, recover, subst, label, tree, more, sort, clip, debug) | PASSTHROUGH; mapped exceptions: xcopy→cp, robocopy→rsync (both v1.1, documented approximations). Candidate v2 mappings: `attrib +r`→chmod (PARTIAL — attributes ≠ permission bits; refuse beyond `+r/-r`), `tree`→`ls -R`-style (PARTIAL), `fc`→`cmp` (PARTIAL), `more`→`cat` for non-interactive use (PARTIAL). `subst`/`label`/`format`/ACL/compression tools: REFUSE-style passthrough. |
| Process (tasklist, taskkill) | PASSTHROUGH. `ps`/`kill` mappings are tempting and divergent; refuse the mapping, keep passthrough. |
| `timeout` | PARTIAL (v2): `timeout /t N` → `sleep N` with `/nobreak` pinned (the "press any key" interrupt is interactive — dropped; document). |
| `choice` | REFUSE (§13). |
| `forfiles` | REFUSE (§17). |
| `where` | SUPPORT (v1.1). |
| `find` / `findstr` | SUPPORT/ PARTIAL (§17). |
| `sort` (external) | PASSTHROUGH with a documented caveat: cmd's sort is case-insensitive and non-numeric; POSIX sort differs. A `sort /r` → `sort -r` mapping is safe; locale-collation mapping is not. |
| `doskey` | NO-OP (interactive console macros/history; nothing in a batch's semantics). |
| `help` | REFUSE (help text; unpinnable). |
| `cmd` | PARTIAL (§9: `cmd /c "simple command"` inline; bare `cmd` nested-interpreter REFUSE). |
| `debug` | REFUSE (interactive DOS debugger). |

## Priority roadmap (v2 rungs, in suggested order)

1. **`&&` / `||` + `&`-chain status** — the A1 shape already exists; highest
   corpus utility. (Verified `BinOp And/Or`.)
2. **`shift` + the arg-loop idiom** — the runtime builtin exists; the single
   most common batch pattern after the v1 core.
3. **Pipelines `|`** (with the caret-tripling rule).
4. **Inline caret `^`** (meta set; not space/quotes/`%`).
5. **`set /p` + `<` redirect** (and `set <NUL /p=`).
6. **Delayed expansion `!var!`** with the EnableDelayedExpansion scope pin and
   the same-line `%var%` write-then-read refusal.
7. **Substring `%a:~i,j%`** (the exact `param("slice", …)` mapping).
8. **`setlocal`/`endlocal`** (+ the `endlocal & set` pin) and
   `setlocal EnableDelayedExpansion` (with 6).
9. **`%~` string forms** (`1`, `n`, `x`, `p`, `dp`, `nx`); fs-dependent forms
   stay REFUSE.
10. **`set /a` full operator set** with the 32-bit mask pin (the `--true64`
    twin, on the small side).
11. **`for /r` / `for /d`**, `if` numeric comparisons, `pushd`/`popd`,
    `findstr`→`grep`, `timeout`→`sleep`, per-builtin switch pins.

## Never list (final)

Registry/OS-essence (assoc, ftype, reg, setx, schtasks, sc, …); console UI
(color, title, cls, prompt, choice, pause, doskey, mode, chcp, keyboard
shortcuts); drives/UNC/volume (drive letters, `subst`, vol, label, `cd /d` +
drive args); Windows filesystem concepts (attributes, 8.3 names, `%~a/t/z/s/f`,
`mklink`); locale-dependent text (`%DATE%`/`%TIME%`, `/?`, `date`/`time`,
`forfiles /d`); cmd-only parser quirks that are unpinnable (`sort /reve`,
switch accumulation); and everything the gate cannot oracle (`%CMDCMDLINE%`,
`%CMDEXTVERSION%`, `set errorlevel=`, batch reloading, goto-into-for).

## Gate implications

Every SUPPORT above lands as: (1) a testdata pin whose recorded stdout is the
expectation, (2) a refusal pin in `testdata_refuse/` for each REFUSE edge,
(3) — for lowerings that claim byte-identity with a core shape (`&&`, `|`,
`slice`) — a byte-equality check against `debashc --shir` on the probe, per
the frontend discipline (probe → implement → oracle-confirm). The recorded-
expectation discipline means the *frontend's own record* is the oracle; that
is exactly why REFUSE > GUESS for anything we cannot pin with certainty.
