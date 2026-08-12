# py-sh-go: Python source -> shIR JSON (A1 contract), ANTLR4+Go.
# The hand-rolled recursive-descent parser + lowerer in main.go handles the
# v1 shell-flavored Python subset (the t01-t52 language-ladder corpus);
# `build` compiles it (the antlr4 generation from grammars/Python3Lexer.g4
# + Python3Parser.g4 is the WORKER's job — it is not a build blocker).
# `test` runs the INGRESS-ACCEPTANCE oracle (the core deserializer must
# accept the emitted A1) + the EXECUTED-STDOUT gate: the transpiled
# ESTree run's stdout must match the native python3 run for every test.
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

# Build the CLI (a thin wrapper over the pylib library at the module root).
build:
	$(GO) build -o py-sh-go ./cmd/py-sh-go

# Self-heal the ingress-acceptance oracle: a missing OR STALE core
# debashc binary wedges the gate as a confusing "FAIL <file> (emit is
# not valid A1 shIR)". Rebuild it on demand whenever the checked-out
# core sources are newer than the binary (the estree worker's cargo
# build may replace target/debug/debashc mid-gate — see the retry in
# `test` below); the oracle is read-only here (mirrors zsh-sh-go/Makefile).
CORE ?= $(abspath ../../sh2perl)
CORE_SRC := $(wildcard $(CORE)/src/*.rs $(CORE)/src/parser/*.rs)
$(DEBASHC): $(CORE_SRC)
	cd $(CORE) && cargo build --bin debashc

# Ingress-acceptance oracle: the emitted A1 shIR JSON must be accepted by
# the core deserializer (--shir-in-estree); then the executed-stdout gate
# compares the transpiled run vs native python3.
test: build $(DEBASHC)
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
	    st=$$?; \
	    # One retry: a concurrent core rebuild may replace the binary \
	    # mid-gate (exec failure / crash -> exit 126/127/>=128), which \
	    # is NOT an emit defect. A genuine rejection (exit 1) fails \
	    # the retry too, so nothing is masked. \
	    sleep 3; \
	    if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	      echo "OK $$bn (valid A1 shIR — first probe exit $$st, transient oracle hiccup, retry accepted)"; \
	    else \
	      st2=$$?; \
	      # Two consecutive rejections. Before declaring an emit defect, \
	      # rule out a STALE/BROKEN oracle: the estree worker's cargo \
	      # build may leave target/debug/debashc relinked from a \
	      # transient tree (2026-08-12 12:15 t08_comment: a 12:14:37 \
	      # build rejected valid emits; the 12:23 relink from the same \
	      # committed sources accepted them). Rebuild the oracle from \
	      # the checked-out core (a no-op when the binary already \
	      # matches the tree, so a genuine rejection still fails the \
	      # re-probe), then POLL the probe: a concurrent relink can \
	      # still be settling (cargo unlinks the old binary while the \
	      # new one links; 2026-08-12 12:31 probes hit exit 127 in that \
	      # window even after a successful rebuild). \
	      (cd $(CORE) && cargo build --bin debashc > /dev/null 2>&1); \
	      probe_ok=0; \
	      for i in 1 2 3 4 5; do \
	        sleep 2; \
	        if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then probe_ok=1; break; else st3=$$?; fi; \
	      done; \
	      if [ $$probe_ok -eq 1 ]; then \
	        echo "OK $$bn (valid A1 shIR — oracle relinked, re-probe accepted; first probes $$st/$$st2)"; \
	      else \
	        echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes; probes $$st/$$st2, post-rebuild $$st3)"; \
	        exit 1; \
	      fi; \
	    fi; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" py ./py-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "py-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f py-sh-go /tmp/*.mine /tmp/*.core
