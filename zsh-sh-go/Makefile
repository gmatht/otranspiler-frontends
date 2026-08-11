# zsh-sh-go: zsh source -> shIR JSON (A1 contract), ANTLR4+Go.
# WORKER REWRITE (2026-08-06): main.go is a faithful hand-rolled port of the
# core frontend (lexer+parser in main.go, lowering in lowering.go, A2
# var-type analysis + A1 JSON emitter in analysis.go), extended for the zsh
# corpus: `[[ ]]`/`(( ))` conditions, bare `$a[2]` array indexes,
# `f() {` bodies, heredocs (`<<EOF`). `test` runs the BYTE-EQUALITY ORACLE
# vs the core's `debashc --shir FILE --raw` — the emitted JSON must be
# byte-identical (the A1 contract). A passing test = real progress
# (correct output); a mismatch = the gap to fix.

ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

# TODO: generate the Go parser from grammars/ZshLexer.g4 + ZshParser.g4
# (the worker wires this via pi). The hand-rolled parser in main.go is
# the current impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package zsh_sh_go -visitor -o gen grammars/ZshLexer.g4 grammars/ZshParser.g4" >&2
	@exit 1

build:
	# self-healing: a stale/corrupt output (e.g. an interrupted build leaves
	# a non-object file) makes `go build -o` refuse to overwrite it and the
	# gate wedges. Drop it first; the fresh binary is always rebuilt.
	rm -f zsh-sh-go
	$(GO) build -o zsh-sh-go ./cmd/zsh-sh-go

# PLAN v12: zsh arrays are 1-based but the A1 contract is canonical
# 0-based — the frontend normalizes subscripts at emit, which the core's
# bash parse cannot match byte-for-byte. Files with array subscripts skip
# byte-equality (executed-stdout is the semantic anchor; byte-equality is
# only a conformance net where the core parses faithfully). `${#x}` /
# `${#a[@]}` (length) have no subscript and stay byte-compared.
# Self-heal the byte-equality oracle too: a missing core debashc binary
# (fresh checkout, interrupted core build) wedges the gate as a confusing
# "FAIL <file> (core)". Build it on demand; the oracle is read-only here.
$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

test: build $(DEBASHC)
	@for f in testdata/*.zsh; do \
	  bn=$$(basename $$f); \
	  ./zsh-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend)"; exit 1; }; \
	  $(DEBASHC) --shir $$f --raw > /tmp/$$bn.core 2>/dev/null || { echo "FAIL $$bn (core)"; exit 1; }; \
	  if grep -qEf "$(abspath ../../harness/zsh-subscript.ere)" "$$f"; then \
	    echo "OK $$bn (array-subscript file — byte-equality waived; stdout oracle only)"; \
	  elif diff -q /tmp/$$bn.mine /tmp/$$bn.core > /dev/null; then \
	    echo "OK $$bn (byte-equal)"; \
	  else \
	    echo "DIFF $$bn (frontend != core — the gap the worker fixes)"; \
	    diff /tmp/$$bn.mine /tmp/$$bn.core | head -5; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" zsh ./zsh-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "zsh-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f zsh-sh-go /tmp/*.mine /tmp/*.core
