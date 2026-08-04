// perl-sh-go: Perl source -> shIR JSON (A1 contract), initial
// ANTLR4+Go frontend. INITIAL VERSION (per the plan): the worker
// (run_worker.sh -> pi/deepseek-v4-flash) handles correctness and
// grammar expansion. The grammar file (grammars/Perl.g4) is a TODO
// stub for the worker to flesh out; the hand-rolled stub parser in
// main.go handles the v1 shell-flavored subset.
package main

// Placeholder. The actual custom .g4 grammar for the v1 shell-flavored
// Perl subset lives in grammars/Perl.g4 (initially a minimal stub; the
// worker will expand it). The hand-rolled stub parser in main.go is
// the initial v1 implementation that makes the frontend buildable and
// runnable without the grammar being complete.
