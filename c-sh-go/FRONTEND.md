# c-sh-go frontend (dir: /home/llm/sh2loop/frontends/c-sh-go)

C source -> A1 shIR JSON (the shell-flavored subset of C).

Yours (in THIS dir): the lexer, parser, emitter, tests. The v1 subset
(t01–t07, all green): printf, int assignments (+=/-=), binary arith,
comparisons, if/else, while, for (lowered to the equivalent while — the
A1 For node is for value-list iteration), function signatures (skipped;
the body becomes the program), return (skipped), comments, #include.

Scope: per cxx-rust-adequacy.md, shIR is a shell process model — pointers,
structs, headers, types, arrays-by-value, stdlib calls beyond printf are
NOT expressible. REFUSE (exit 1) rather than half-parse; every new
construct lands by pinning the executable shape first (probe: gcc vs
A1->ESTree->JS) — the executed-stdout oracle in frontends-stdout.sh `c`.

Shared (do NOT fork): the core (src/shir.rs, ir.rs, estree.rs, parser/);
harness/frontend-stdout.sh (the `c` lang case); setup_backends.sh (the
fleet registration). Core needs: core-requests/.

Worker: ./run_frontend_worker.sh — failure-driven (make test -> pi
deepseek-v4-turbo -> commit/stash -> trap/escalate).
