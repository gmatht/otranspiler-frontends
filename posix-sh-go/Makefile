# posix-sh-go: POSIX shell source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in main.go handles the v1
# shell-flavored subset; `build` compiles it (the antlr4 generation from
# grammars/POSIX.g4 is the WORKER's job — not a build blocker). `test`
# runs the BYTE-EQUALITY ORACLE vs the core's `debashc --shir`. A passing
# test = real progress (correct output); a mismatch = the gap to fix.

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
	$(GO) build -o posix-sh-go .

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

worker: test
	@echo "posix-sh-go worker gate: byte-equality on testdata/ passed"

clean:
	rm -f posix-sh-go /tmp/*.mine /tmp/*.core
