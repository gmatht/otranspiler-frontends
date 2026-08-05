# zsh-sh-go: zsh source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in zsh-sh-go.go handles the
# v1 zsh subset (echo, NAME=VALUE — the POSIX-shaped core of zsh);
# `build` compiles it (the antlr4 generation from a zsh grammar is the
# WORKER's job). `test` runs the INGRESS-ACCEPTANCE gate: the emitted A1
# shIR JSON must be VALID (the core's deserializer accepts it via
# --shir-in-estree). zsh is a superset of POSIX — byte-equality vs
# debashc --shir is the long-term oracle; the v1 progress signal is valid
# A1 shIR from the renderer-safe subset.
ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package zsh_sh_go -visitor -o gen grammars/ZshLexer.g4 grammars/ZshParser.g4" >&2
	@exit 1

build:
	$(GO) build -o zsh-sh-go .

test: build
	@for f in testdata/*.zsh; do \
	  bn=$$(basename $$f); \
	  ./zsh-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done

worker: test
	@echo "zsh-sh-go worker gate: ingress acceptance on testdata/ passed"

clean:
	rm -f zsh-sh-go /tmp/*.mine
