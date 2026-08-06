# c-sh-go: C source -> shIR JSON (A1 contract), hand-rolled Go parser.
# v1 subset: printf, int assignments (+=/-=), binary arith, comparisons,
# if/else, while, for (lowered to while), function signatures (skipped).
# `test` runs INGRESS-ACCEPTANCE (the core deserializer accepts the
# emitted A1) + the EXECUTED-STDOUT gate (native gcc run vs the A1->
# ESTree->JS run must match). Unsupported constructs REFUSE (exit 1).
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all build test worker clean

all: build

build:
	$(GO) build -o c-sh-go .

test: build
	@for f in testdata/*.c; do \
	  bn=$$(basename $$f); \
	  ./c-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native gcc vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" c ./c-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "c-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f c-sh-go
