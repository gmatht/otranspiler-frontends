# c-sh-go: C source -> shIR JSON (A1 contract), hand-rolled Go parser.
# v1 subset: printf, int assignments (+=/-=), binary arith, comparisons,
# if/else, while, for (lowered to while), function signatures (skipped).
# `test` runs INGRESS-ACCEPTANCE (the core deserializer accepts the
# emitted A1) + the EXECUTED-STDOUT gate (native gcc run vs the A1->
# ESTree->JS run must match). Unsupported constructs REFUSE (exit 1).
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go
# per-gate scratch dir (unique via mktemp -d): concurrent gates never
# share filenames (the shared-scratch race that caused transient FAILs).
SCRATCH := $(shell mkdir -p $(abspath .gate-tmp) && mktemp -d $(abspath .gate-tmp)/gate.XXXXXX)

.PHONY: all build test worker clean

all: build

build:
	# Atomic + conditional: rebuild ONLY when the sources changed, via a
	# temp file + rename. The old `rm -f; go build -o` raced under
	# CONCURRENT gates (each `make test` rebuilt the binary): one gate's
	# rebuild made the other's exec fail with "No such file or directory"
	# / ETXTBSY — the transient "frontend emit" FAILs. With the rename,
	# concurrent builds are atomic (last one wins) and an existing
	# up-to-date binary is never touched.
	@if [ ! -x c-sh-go ] || find cmd main.go go.mod -newer c-sh-go 2>/dev/null | grep -q .; then \
	  echo "  building c-sh-go…"; \
	  $(GO) build -o c-sh-go.new.$$ ./cmd/c-sh-go \
	    && mv -f c-sh-go.new.$$ c-sh-go \
	    || { rm -f c-sh-go.new.$$; exit 1; }; \
	fi

# Self-heal the ingress oracle too: a missing core debashc binary (fresh
# checkout, interrupted core build) wedges the gate as a confusing
# "FAIL <file> (emit is not valid A1 shIR)". Build it on demand; the
# oracle is read-only here.
$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

test: build $(DEBASHC)
	@for f in testdata/*.c; do \
	  bn=$$(basename $$f); \
	  ./c-sh-go --shir $$f --raw > $(SCRATCH)/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  python3 $(abspath ../../harness/outparam_to_returns.py) < $(SCRATCH)/$$bn.mine > $(SCRATCH)/$$bn.out.json 2>/dev/null && mv $(SCRATCH)/$$bn.out.json $(SCRATCH)/$$bn.mine; \
	  if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@rm -rf $(SCRATCH)
	@echo "--- executed-stdout comparison (native gcc vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" c ./c-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "c-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f c-sh-go
