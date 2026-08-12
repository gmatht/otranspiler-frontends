# frontends/coverage — parser coverage tests

Two tests measure how much of each source language the frontends' parsers
can actually ingest, over real corpora.

## parser-coverage.sh — per-frontend parser coverage

For every frontend, run `--shir <file> --raw` over a corpus of real files
in the frontend's source language and classify each file:

| class | meaning |
|---|---|
| **EMIT** | exit 0 + valid A1 JSON — the parser+lowerer fully handled the file |
| **BAD-EMIT** | exit 0 but stdout is NOT JSON — a bug: parse error printed to stderr yet exit 0 (the harness would treat it as success) |
| **REFUSE** | exit != 0 + refusal marker — the parser understood the file and refused a construct (**subset** gap, not a parser gap) |
| **PARSE-ERR** | exit != 0 + other message — the parser itself choked (**parser** gap) |
| **CRASH** | exit != 0 + panic/stack-overflow — a parser bug (unbounded recursion) |
| **SILENT** | exit != 0, empty stderr |

Corpora (real workspace code where it exists; the frontend's own testdata
only where nothing else exists):

| frontend | corpus |
|---|---|
| sh (posix-sh-go) | `sh2perl/examples/*.sh` — 532 bash examples, the ecosystem corpus |
| c (c-sh-go) | `frontends/c-sh-go/testdata/*.c` — 79 |
| cpp (cpp-sh-go) | `frontends/cpp-sh-go/testdata_cpp/*.cc` — 13 |
| go (go-sh) | the fleet's own Go code — 91 files |
| py (py-sh-go) | workspace Python + py-sh-go testdata — 76 |
| pl (perl-sh-go) | workspace Perl (scratch excluded) — 88 |
| rust (rust-frontend) | `sh2perl/src/*.rs` + `cli/src/*.rs` — 40 real Rust (library crates) |
| fish / zsh / bat | own testdata (no other corpus exists) |

Per-file results: `results/<frontend>.tsv` (class + filename).

## antlr-coverage.sh — the ANTLR4 parsers (the external source of truth)

Ground truth first: the repo's only "ANTLR4 sh parser" —
`frontends/posix-sh-go/grammars/POSIX.g4` — is a **14-line placeholder
stub**; antlr4 cannot generate a parser from it (the script shows the
error). `plan-antlr-go.md` records that grammars-v4 has *no* POSIX shell
grammar and the intended path was a custom `.g4`; this test writes that
grammar (`antlr-sh/POSIX.g4`, POSIX-sh subset, approximations documented
in the grammar header) and measures it:

- POSIX control corpus (`posix-sh-go/testdata`, 78 POSIX-flavored): parse-clean rate
- real corpus (`sh2perl/examples`, 532 bash): parse-clean rate
- bonus: the *only real* vendored grammars-v4 grammars (Python3, at
  `py-sh-go/grammars/`) over the Python corpus

### Section 3 — per-example rule coverage vs the official grammars

The frontends' own parsers are hand-rolled (or syn/tree-sitter), so the
**official grammars are the external source of truth** for "do the
examples cover the language's parser features": parse every testdata
example with the official grammar and report the grammar rules no
example enters (`Rules.java` prints `RULE-UNEXERCISED`). Each
unexercised rule is a language construct the examples don't cover;
judge expressibility against the frontend's subset + the executed-stdout
gate (REFUSE > GUESS — see GOOD_EXAMPLES.md), and add an example per
GOOD_EXAMPLES.md for the expressible ones.

| frontend | grammar (source of truth) | status |
|---|---|---|
| posix-sh-go | custom `antlr-sh/POSIX.g4` (grammars-v4 has no POSIX) | 22/22 rules, 80/80 parse-clean, none unexercised |
| py-sh-go | official grammars-v4 Python3 (vendored) | 45/119 rules, 71/71 parse-clean; unexercised = classdef/try/lambda/break/continue/from-import/extended-slice/match… — all probed by-design refusals |
| go-sh | official grammars-v4 golang (fetched) | 67/106 rules, 78/78 parse-clean; import + fixed-array covered by t76/t77; rest = const/struct/method/pointer/break/concurrency — by-design |
| c-sh-go | official grammars-v4 c (fetched; stub base classes) | 60/117 rules, 78/79 parse-clean (t26 varargs needs the preprocessor); const/enum/typedef/volatile/__LINE__ parse but their inits are silently dropped (lowering bugs), designated-init/asm/attributes/_Generic refuse |
| perl-sh-go | PPI node classes (`ppi-coverage.pl`) | 26/76 classes, 68/68 parse-clean; single-quote gap closed by t70; number/q()/$#/single-quote-$ lowerings broken (bugs-perl-sh-go.txt); use/package/qw/pod/BEGIN refuse |
| zsh/fish/bat | none exists in grammars-v4 | no external truth — the A1-node proxy (coverage-gap.sh) is the only signal |

Caveat: the c grammar's semantic superclasses (`CLexerBase`/`CParserBase`)
are stubbed (the real ones run gcc + a symbol table); the stub predicates
are lookahead-based (type-keyword checks) and parse 78/79 of the corpus —
`sizeof(type)`/`(int)` casts resolve, `va_list` (stdarg.h) does not.

Full findings: **PARSER_GAPS.md** (workspace root). The Perl check is
`ppi-coverage.pl` (PPI is the external source of truth — grammars-v4 has no
Perl grammar).

Requires java/javac + the antlr4 jar (downloaded on demand to
`.antlr4.jar`, gitignored). Java parser generated into `.work/`
(gitignored). Per-file results: `results/antlr-*.tsv`.

## Results (2026-08-11 snapshot — the workspace is live; workers churn
binaries and corpora, so re-run for current numbers)

**Per-frontend (EMIT = fully handled):**

| frontend | EMIT | BAD-EMIT | REFUSE | PARSE-ERR | CRASH |
|---|---|---|---|---|---|
| sh (posix-sh-go) | 478/532 (89.8%) | **44** | 0 | 0 | **10** |
| c (c-sh-go) | 79/79 (100%) | 0 | 0 | 0 | 0 |
| cpp (cpp-sh-go) | 6/13 (46.2%) | 0 | 6 | 1 | 0 |
| go (go-sh) | 66/91 (72.5%) | 0 | 2 | 23 | 0 |
| py (py-sh-go) | 68/76 (89.5%) | 0 | 1 | 7 | 0 |
| pl (perl-sh-go) | 66/88 (75.0%) | 0 | 2 | 20 | 0 |
| rust (rust-frontend) | 0/40 (0%) | 0 | **40** | 0 | 0 |
| fish | 74/75 (98.7%) | 0 | 0 | 1 | 0 |
| zsh | 75/75 (100%) | 0 | 0 | 0 | 0 |
| bat | 47/47 (100%) | 0 | 0 | 0 | 0 |

rust's 0 EMIT is the v0.1 subset, not the parser: every file is a clean
REFUSE at the "only `fn main`" item gate (parser engagement 100% — the
syn parser handled all 40 real files). Its own main-bearing testdata
emits 13/13 (proven by `make test`).

**ANTLR4 (parse-clean rate):** repo POSIX.g4 stub — cannot generate
(not a grammar). The custom POSIX-subset grammar: 80.8% on POSIX
control, **59.1% on the bash corpus** (314/531). Vendored Python3
grammars: **97.4%** on the py corpus (74/76).

## What the numbers say

1. **The hand-rolled sh parser beats the ANTLR4 POSIX grammar by 30
   points on the ecosystem's own corpus** (89.8% EMIT vs 59.1%
   parse-clean) — the empirical justification for the hand-rolled fleet.
   ANTLR's ceiling is the grammar's POSIX-only vocabulary + lexer
   context-freeness (keywords-as-args, heredoc modes, `#` comments).
2. **posix-sh-go has two real bugs the test surfaced**: 44 files print
   "Parse error: ..." to stderr but **exit 0** with empty stdout
   (BAD-EMIT — silent failure), and 10 command-substitution-heavy files
   **crash the parser with a Go stack overflow** (unbounded recursion).
   None of the other frontends show either.
3. **The Go/Python/Perl frontends parse real-world code at 72–90%**
   (the PARSE-ERR remainder is their shell-flavored subset walls).
4. **The vendored Python3 grammars are the real ANTLR4 assets** — 97.4%
   parse-clean — but that's parse-only; the hand-rolled py-sh-go emits
   A1 for 89.5%.
