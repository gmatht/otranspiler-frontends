# bat-sh-go: Windows batch source -> shIR JSON (A1 contract), hand-rolled Go.
# v1 subset: @echo off, rem/::, echo, set (incl. set /a), %var%, if [not]
# A==B (cmd) else (cmd), goto/:label, exit /b, for %%v in (...) do, `&`.
# `build` compiles; `test` runs the INGRESS gate: every emitted A1 must be
# accepted by the core's deserializer (--shir-in-estree) and the executed
# stdout must match the recorded expectations (frontend-stdout.sh — cmd.exe
# is Windows-only, so the native side is recorded, not executed here).

GO ?= /snap/go/current/bin/go
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)

.PHONY: all build test worker clean

all: build

build:
	# Atomic publish: build to a temp file, then rename over the target, so a
	# CONCURRENT gate run (the worker loop and the coverage step both run
	# `make test` in this dir and they can overlap) never observes the binary
	# missing or half-written mid-gate (posix-sh-go t08-t11, 2026-08-13 15:17
	# — "frontend emit: ./posix-sh-go: No such file or directory" while the
	# byte-equal phase of the same run had passed). The old in-place
	# `go build -o` write left a window with NO/PARTIAL binary; mv(1) is
	# atomic: any concurrent exec sees either the previous complete binary
	# or the new one.
	rm -f .bat-sh-go.tmp
	$(GO) build -o .bat-sh-go.tmp ./cmd/bat-sh-go
	mv -f .bat-sh-go.tmp bat-sh-go

test: build
	@for f in testdata/*.bat; do \
	  bn=$$(basename $$f); \
	  ./bat-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit: $$(./bat-sh-go --shir $$f --raw 2>&1 | head -1))"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (recorded native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" bat ./bat-sh-go testdata "$(DEBASHC)" || exit 1
	@echo "--- cross-target agreement (sh render under dash vs ESTree under node) ---"
	@for f in testdata/*.bat; do \
	  bn=$$(basename $$f); \
	  ./bat-sh-go --shir $$f --raw 2>/dev/null | $(DEBASHC) --shir-in-sh - 2>/dev/null > /tmp/$$bn.sh; \
	  ./bat-sh-go --shir $$f --raw 2>/dev/null | $(DEBASHC) --shir-in-estree - 2>/dev/null > /tmp/$$bn.e.json; \
	  dash /tmp/$$bn.sh 2>&1 > /tmp/$$bn.sh.out; \
	  node "$(abspath ../../harness/estree-runner.mjs)" /tmp/$$bn.e.json --source $$f 2>&1 > /tmp/$$bn.estree.out; \
	  if ! diff -q /tmp/$$bn.sh.out /tmp/$$bn.estree.out >/dev/null; then \
	    echo "DIFF $$bn (sh vs estree output differs)"; exit 1; \
	  fi; \
	done
	@echo "  cross-target: all sh vs estree outputs agree"

worker: test
	@echo "bat-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f bat-sh-go .bat-sh-go.tmp /tmp/*.mine
