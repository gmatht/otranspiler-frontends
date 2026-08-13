# posix-sh-go: POSIX shell source -> shIR JSON (A1 contract), ANTLR4+Go.
# WORKER REWRITE (2026-08-05): main.go is a faithful hand-rolled port of the
# core frontend (lexer+parser in main.go, lowering in lowering.go, A2
# var-type analysis + A1 JSON emitter in analysis.go). `test` runs the
# BYTE-EQUALITY ORACLE vs the core's `debashc --shir FILE --raw` — the
# emitted JSON must be byte-identical (the A1 contract). A passing test =
# real progress (correct output); a mismatch = the gap to fix.

ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

# TODO: generate the Go parser from grammars/POSIX.g4 (the worker wires
# this via pi/deepseek-v4-flash). The stub in main.go is the initial impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package posix_sh_go -visitor -o gen grammars/POSIX.g4" >&2
	@exit 1

build:
	# Atomic publish: build to a temp file, then rename over the target, so a
	# CONCURRENT gate run (the worker loop and the coverage step both run
	# `make test` in this dir and they can overlap) never observes the binary
	# missing or half-written mid-gate (2026-08-13 15:17: 4 tests failed
	# "frontend emit: ./posix-sh-go: No such file or directory" while the
	# byte-equal phase of the same run had passed). The old `rm -f` +
	# in-place `go build -o` left a window with NO binary; the in-place
	# write left one with a PARTIAL binary. mv(1) is atomic: any concurrent
	# exec sees either the previous complete binary or the new one.
	# Self-healing (a stale/corrupt OUTPUT used to make `go build -o` refuse
	# to overwrite it and wedge the gate) now guards the temp file only, so
	# a corrupt published binary can never block a rebuild either.
	rm -f .posix-sh-go.tmp
	$(GO) build -o .posix-sh-go.tmp .
	mv -f .posix-sh-go.tmp posix-sh-go

test: build
	@for f in testdata/*.sh; do \
	  bn=$$(basename $$f); \
	  ./posix-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend)"; exit 1; }; \
	  $(DEBASHC) --shir $$f --raw > /tmp/$$bn.core 2>/dev/null || { echo "FAIL $$bn (core)"; exit 1; }; \
	  if diff -q /tmp/$$bn.mine /tmp/$$bn.core > /dev/null; then \
	    echo "OK $$bn (byte-equal)"; \
	  else \
	    echo "DIFF $$bn (frontend != core — the gap the worker fixes)"; \
	    diff /tmp/$$bn.mine /tmp/$$bn.core | head -5; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" sh ./posix-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "posix-sh-go worker gate: byte-equality + executed-stdout passed"

clean:
	rm -f posix-sh-go .posix-sh-go.tmp /tmp/*.mine /tmp/*.core
