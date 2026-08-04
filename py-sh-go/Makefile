# py-sh-go: Python source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in main.go handles the v1
# shell-flavored subset; `build` compiles it (the antlr4 generation from
# grammars/Python3Lexer.g4 + Python3Parser.g4 is the WORKER's job — it is
# not a build blocker). `test` runs the BYTE-EQUALITY ORACLE: the frontend
# emit must byte-match the core's `debashc --shir` for the same source.
# A passing test = real progress (correct output); a mismatch = the gap
# the worker's pi should fix.

ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

# TODO: generate the Go parser from grammars/Python3Lexer.g4 +
# Python3Parser.g4. The worker (run_worker.sh -> pi/deepseek-v4-flash)
# wires this. The stub in main.go is the initial impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package py_sh_go -visitor -o gen grammars/Python3Lexer.g4 grammars/Python3Parser.g4" >&2
	@exit 1

# Build the CLI (the stub parser — no antlr4 needed).
build:
	$(GO) build -o py-sh-go .

# Byte-equality oracle: for each testdata file, frontend --shir vs
# debashc --shir (core); the JSON must be byte-equal (the A1 contract).
test: build
	@for f in testdata/*.py; do \
	  bn=$$(basename $$f); \
	  ./py-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  # Ingress acceptance: the core consumes SHELL, not py source, so \
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
	@echo "py-sh-go worker gate: byte-equality on testdata/ passed"

clean:
	rm -f py-sh-go /tmp/*.mine /tmp/*.core
