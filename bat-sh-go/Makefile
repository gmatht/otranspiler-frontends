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
	$(GO) build -o bat-sh-go ./cmd/bat-sh-go

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

worker: test
	@echo "bat-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f bat-sh-go /tmp/*.mine
