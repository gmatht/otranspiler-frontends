# powershell-sh-go: PowerShell (.ps1) source -> A1 shIR JSON.
#
# `test` runs the powershell gate: refusal pins (testdata_refuse/*.ps1
# must FAIL loudly) + ingress acceptance (the core deserializer accepts
# every expressible emit) + the executed-stdout oracle (RECORDED
# expectations — pwsh is not installed on the fleet boxes, so
# harness/frontend-stdout.sh compares the transpiled run against the
# native_limits_powershell records, the bat precedent).
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= go
SCRATCH := $(shell { [ -w $(abspath .gate-tmp) ] || rm -rf $(abspath .gate-tmp); mkdir -p $(abspath .gate-tmp) && mktemp -d $(abspath .gate-tmp)/gate.XXXXXX; } 2>/dev/null || mktemp -d /tmp/powershell-sh-go-gate.XXXXXX)

.PHONY: all build test worker clean

all: build

build:
	# Atomic + conditional rebuild (concurrent-gate safety; see cpp-sh-go)
	@if [ ! -x powershell-sh-go ] || find cmd *.go go.mod go.sum -newer powershell-sh-go 2>/dev/null | grep -q .; then \
	  echo "  building powershell-sh-go…"; \
	  $(GO) build -o powershell-sh-go.new.$$ ./cmd/powershell-sh-go \
	    && mv -f powershell-sh-go.new.$$ powershell-sh-go \
	    || { rm -f powershell-sh-go.new.$$; exit 1; }; \
	fi

$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

test: build $(DEBASHC)
	@test -n "$(SCRATCH)" || { echo "FAIL: cannot create gate scratch dir"; exit 1; }
	@echo "--- refusals: testdata_refuse/*.ps1 must FAIL loudly ---"
	@for f in testdata_refuse/*.ps1; do \
	  bn=$$(basename $$f); \
	  if ./powershell-sh-go --shir $$f --raw >/dev/null 2>$(SCRATCH)/refuse.err; then \
	    echo "FAIL $$bn (expected loud refusal, got a parse)"; exit 1; \
	  else \
	    echo "OK $$bn (refused loudly: $$(head -1 $(SCRATCH)/refuse.err))"; \
	  fi; \
	done
	@echo "--- expressible: ingress acceptance (debashc --shir-in-estree) ---"
	@for f in testdata/*.ps1; do \
	  bn=$$(basename $$f); \
	  ./powershell-sh-go --shir $$f --raw > $(SCRATCH)/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR)"; exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (recorded expectations vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" powershell ./powershell-sh-go testdata "$(DEBASHC)" || exit 1
	@rm -rf $(SCRATCH)

worker: test
	@echo "powershell-sh-go worker gate: refusals + ingress + stdout passed"

clean:
	rm -f powershell-sh-go
