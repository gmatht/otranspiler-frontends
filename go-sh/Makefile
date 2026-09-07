# go-sh: Go source -> shIR JSON (A1 contract), ANTLR4+Go.
# INITIAL VERSION: the hand-rolled stub parser in go-sh.go handles the v1
# shell-flavored subset; `build` compiles it (the antlr4 generation from
# grammars/GoLexer.g4 + GoParser.g4 is the WORKER's job — not a build
# blocker). `test` runs the BYTE-EQUALITY ORACLE vs the core's
# `otranspilerl-cli --shir`. A passing test = real progress (correct output); a
# mismatch = the gap the worker's pi should fix.

ANTLR4 ?= antlr4
CLI ?= $(abspath ../../otranspilerl/target/debug/otranspilerl-cli)
BUILD_LOCK ?= $(abspath ../../harness/build-lock.sh)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test otranspilerl-cli-oracle worker clean

all: build

# The A1-ingress oracle. The main checkout's target dir is SHARED with
# the core/estree/sh workers (build-lock.sh default), and they churn or
# wipe it (cargo clean, rebuilds, lockfile revert/stale serde caches);
# the binary can be absent or unbuildable at gate time. Rebuild it
# through the sanctioned choke point (backend role: waits up to 1800s,
# never preempts) instead of redding on a missing oracle — a missing
# oracle must report "oracle unavailable", never a misleading "emit is
# not valid A1 shIR". Known stale-cache failure (serde 1.0.229 artifacts
# vs a reverted 1.0.226 lockfile) is retried once after cleaning just
# those packages; a genuinely broken core tree still fails honestly.
otranspilerl-cli-oracle:
	@if [ -x "$(CLI)" ]; then :; else \
	  echo "oracle $$(basename $(CLI)) missing — rebuilding via build-lock.sh"; \
	  if "$(BUILD_LOCK)" --role backend -- cargo build --manifest-path "$(abspath ../../otranspilerl/Cargo.toml)" --bin otranspilerl-cli; then \
	    echo "oracle rebuilt"; \
	  else \
	    echo "oracle build failed — stale serde cache? cleaning serde artifacts and retrying once"; \
	    if "$(BUILD_LOCK)" --role backend -- cargo clean --manifest-path "$(abspath ../../otranspilerl/Cargo.toml)" -p serde_derive -p serde -p serde_core \
	        && "$(BUILD_LOCK)" --role backend -- cargo build --manifest-path "$(abspath ../../otranspilerl/Cargo.toml)" --bin otranspilerl-cli; then \
	      echo "oracle rebuilt after serde cache clean"; \
	    else \
	      echo "FAIL (oracle unavailable — could not build otranspilerl-cli; the core tree may be mid-WIP)"; \
	      exit 1; \
	    fi; \
	  fi; \
	fi

# TODO: generate the Go parser from grammars/GoLexer.g4 + GoParser.g4
# (the worker wires this via pi/deepseek-v4-flash). The stub in go-sh.go
# is the initial impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package go_sh -visitor -o gen grammars/GoLexer.g4 grammars/GoParser.g4" >&2
	@exit 1

build:
	$(GO) build -o go-sh ./cmd/go-sh

test: build otranspilerl-cli-oracle
	@mkdir -p .test-tmp && trap 'rm -rf .test-tmp' EXIT; \
	for f in testdata/*.go; do \
	  bn=$$(basename $$f); \
	  ./go-sh --shir $$f --raw > .test-tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  # Ingress acceptance: the core consumes SHELL, not go source, so \
	  # byte-equality vs otranspilerl-cli --shir is impossible. The progress signal \
	  # is: the emitted A1 shIR JSON is VALID (the core's deserializer \
	  # accepts it via `- --target estree`). \
	  # A non-empty emit is required: an empty/truncated file (e.g. a full \
	  # scratch disk) must report "frontend emit", never a misleading \
	  # "not valid A1 shIR". The corpus dir is repo-local so a full /tmp \
	  # can't fake a deserializer rejection. \
	  if [ ! -s .test-tmp/$$bn.mine ]; then \
	    echo "FAIL $$bn (frontend emit — empty/truncated output)"; exit 1; \
	  fi; \
	  if $(CLI) - --target estree < .test-tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" go ./go-sh testdata "$(CLI)" || exit 1

worker: test
	@echo "go-sh worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f go-sh && rm -rf .test-tmp
