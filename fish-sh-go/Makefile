# fish-sh-go: fish source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in fish-sh-go.go handles the
# v1 fish subset (echo, set NAME VALUE); `build` compiles it (the antlr4
# generation from a fish grammar is the WORKER's job). `test` runs the
# INGRESS-ACCEPTANCE gate: the emitted A1 shIR JSON must be VALID (the
# core's deserializer accepts it via --shir-in-estree). Fish is not
# POSIX-compatible, so byte-equality vs debashc --shir is impossible —
# the progress signal is valid A1 shIR from the renderer-safe subset.
ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package fish_sh_go -visitor -o gen grammars/FishLexer.g4 grammars/FishParser.g4" >&2
	@exit 1

build:
	$(GO) build -o fish-sh-go .

test: build
	@for f in testdata/*.fish; do \
	  bn=$$(basename $$f); \
	  ./fish-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done

worker: test
	@echo "fish-sh-go worker gate: ingress acceptance on testdata/ passed"

clean:
	rm -f fish-sh-go /tmp/*.mine
