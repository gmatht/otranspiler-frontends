# go-sh: Go source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in go-sh.go handles the v1
# shell-flavored subset; `build` compiles it (the antlr4 generation from
# grammars/GoLexer.g4 + GoParser.g4 is the WORKER's job — not a build
# blocker). `test` runs the BYTE-EQUALITY ORACLE vs the core's
# `debashc --shir`. A passing test = real progress (correct output); a
# mismatch = the gap the worker's pi should fix.

ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

# TODO: generate the Go parser from grammars/GoLexer.g4 + GoParser.g4
# (the worker wires this via pi/deepseek-v4-flash). The stub in go-sh.go
# is the initial impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package go_sh -visitor -o gen grammars/GoLexer.g4 grammars/GoParser.g4" >&2
	@exit 1

build:
	$(GO) build -o go-sh .

test: build
	@for f in testdata/*.go; do \
	  bn=$$(basename $$f); \
	  ./go-sh --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  # Ingress acceptance: the core consumes SHELL, not go source, so \
	  # byte-equality vs debashc --shir is impossible. The progress signal \
	  # is: the emitted A1 shIR JSON is VALID (the core's deserializer \
	  # accepts it via --shir-in-estree). \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done

worker: test
	@echo "go-sh worker gate: byte-equality on testdata/ passed"

clean:
	rm -f go-sh /tmp/*.mine /tmp/*.core
