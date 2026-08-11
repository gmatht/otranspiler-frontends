# zig-sh-go: Zig source -> A1 shIR JSON.
#
# `test` runs the zig gate: refusal pins (testdata_refuse/*.zig must
# FAIL loudly) + ingress acceptance (the core deserializer accepts every
# expressible emit) + the executed-stdout oracle (native `zig run` vs
# A1->ESTree->JS, via harness/frontend-stdout.sh).
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= go
ZIG ?= /snap/zig/current/bin/zig
SCRATCH := $(shell { [ -w $(abspath .gate-tmp) ] || rm -rf $(abspath .gate-tmp); mkdir -p $(abspath .gate-tmp) && mktemp -d $(abspath .gate-tmp)/gate.XXXXXX; } 2>/dev/null || mktemp -d /tmp/zig-sh-go-gate.XXXXXX)

.PHONY: all build test worker clean

all: build

build:
	# Atomic + conditional rebuild (concurrent-gate safety; see cpp-sh-go)
	@if [ ! -x zig-sh-go ] || find cmd main.go go.mod -newer zig-sh-go 2>/dev/null | grep -q .; then \
	  echo "  building zig-sh-go…"; \
	  $(GO) build -o zig-sh-go.new.$$ ./cmd/zig-sh-go \
	    && mv -f zig-sh-go.new.$$ zig-sh-go \
	    || { rm -f zig-sh-go.new.$$; exit 1; }; \
	fi

$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

test: build $(DEBASHC)
	@test -n "$(SCRATCH)" || { echo "FAIL: cannot create gate scratch dir"; exit 1; }
	@echo "--- refusals: testdata_refuse/*.zig must FAIL loudly ---"
	@for f in testdata_refuse/*.zig; do \
	  bn=$$(basename $$f); \
	  if ./zig-sh-go --shir $$f --raw >/dev/null 2>$(SCRATCH)/refuse.err; then \
	    echo "FAIL $$bn (expected loud refusal, got a parse)"; exit 1; \
	  else \
	    echo "OK $$bn (refused loudly: $$(head -1 $(SCRATCH)/refuse.err))"; \
	  fi; \
	done
	@echo "--- expressible: ingress acceptance (debashc --shir-in-estree) ---"
	@for f in testdata/*.zig; do \
	  bn=$$(basename $$f); \
	  ./zig-sh-go --shir $$f --raw > $(SCRATCH)/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR)"; exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native zig run vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" zig ./zig-sh-go testdata "$(DEBASHC)" || exit 1
	@rm -rf $(SCRATCH)

worker: test
	@echo "zig-sh-go worker gate: refusals + ingress + stdout passed"

clean:
	rm -f zig-sh-go
