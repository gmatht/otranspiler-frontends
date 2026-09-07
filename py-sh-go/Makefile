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
CLI ?= $(abspath ../../otranspilerl/target/debug/otranspilerl-cli)
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
# otranspilerl-cli binary wedges the gate as a confusing "FAIL <file> (emit is
# not valid A1 shIR)". Rebuild it on demand whenever the checked-out
# core sources are newer than the binary (the estree worker's cargo
# build may replace target/debug/otranspilerl-cli mid-gate — see the retry in
# `test` below); the oracle is read-only here (mirrors zsh-sh-go/Makefile).
#
# Fallback (2026-08-12): a broken shared-core tree (mid-WIP, e.g. the
# GLSL backend work that references unwired modules) makes `cargo build
# --bin otranspilerl-cli` fail, which used to wedge the gate with Error 101 before
# any test ran — while the frontend itself was healthy. If the rebuild
# fails, fall back to the existing binary: it is the last known-good
# build of the checked-out core (the A1 contract files shir_json.rs /
# shir_json_in.rs have not changed since it was built, so its
# `- --target estree` ingress check is still valid), stamp it so the rule
# RE-ARMS only when a core source changes again (then the rebuild is
# retried and, once the estree worker heals the core, the oracle goes
# fresh automatically). A missing binary with a failed rebuild is still
# a hard error. The estree worker owns the core build; core-requests/
# py-sh-go-20260812-165614-glsl-wip-breaks-otranspilerl-cli-build.md tracks it.
CORE ?= $(abspath ../../sh2perl)
OTL ?= $(abspath ../../otranspilerl)
# otranspilerl-cli statically links the debashl core: a CLI OR a core
# source change both stale the oracle.
CORE_SRC := $(wildcard $(OTL)/src/*.rs $(CORE)/src/*.rs $(CORE)/src/parser/*.rs)
$(CLI): $(CORE_SRC)
	@if cd $(OTL) && cargo build --bin otranspilerl-cli 2>/dev/null; then \
	  echo "oracle: rebuilt otranspilerl-cli"; \
	elif [ -x "$@" ]; then \
	  echo "WARNING: oracle rebuild failed (core tree mid-WIP?) — falling back to the existing otranspilerl-cli binary (last known-good build of the checked-out core; A1-ingress checks still valid). The estree worker owns the core build."; \
	  touch "$@"; \
	else \
	  echo "ERROR: no otranspilerl-cli binary and the core build failed"; \
	  exit 1; \
	fi

# Ingress-acceptance oracle: the emitted A1 shIR JSON must be accepted by
# the core deserializer (`- --target estree`); then the executed-stdout gate
# compares the transpiled run vs native python3.
test: build $(CLI)
	@for f in testdata/*.py; do \
	  bn=$$(basename $$f); \
	  ./py-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  # Ingress acceptance: the core consumes SHELL, not py source, so \
	  # byte-equality vs otranspilerl-cli --shir is impossible. The progress signal \
	  # is: the emitted A1 shIR JSON is VALID (the core's deserializer \
	  # accepts it via `- --target estree`). \
	  if $(CLI) - --target estree < /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    st=$$?; \
	    # One retry: a concurrent core rebuild may replace the binary \
	    # mid-gate (exec failure / crash -> exit 126/127/>=128), which \
	    # is NOT an emit defect. A genuine rejection (exit 1) fails \
	    # the retry too, so nothing is masked. \
	    sleep 3; \
	    if $(CLI) - --target estree < /tmp/$$bn.mine > /dev/null 2>&1; then \
	      echo "OK $$bn (valid A1 shIR — first probe exit $$st, transient oracle hiccup, retry accepted)"; \
	    else \
	      st2=$$?; \
	      # Two consecutive rejections. Before declaring an emit defect, \
	      # rule out a STALE/BROKEN oracle: the estree worker's cargo \
	      # build may leave target/debug/otranspilerl-cli relinked from a \
	      # transient tree (2026-08-12 12:15 t08_comment: a 12:14:37 \
	      # build rejected valid emits; the 12:23 relink from the same \
	      # committed sources accepted them). Rebuild the oracle from \
	      # the checked-out core (a no-op when the binary already \
	      # matches the tree, so a genuine rejection still fails the \
	      # re-probe), then POLL the probe: a concurrent relink can \
	      # still be settling (cargo unlinks the old binary while the \
	      # new one links; 2026-08-12 12:31 probes hit exit 127 in that \
	      # window even after a successful rebuild). \
	      (cd $(CORE) && cargo build --bin otranspilerl-cli > /dev/null 2>&1); \
	      probe_ok=0; \
	      for i in 1 2 3 4 5; do \
	        sleep 2; \
	        if $(CLI) - --target estree < /tmp/$$bn.mine > /dev/null 2>&1; then probe_ok=1; break; else st3=$$?; fi; \
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
	@bash "$(abspath ../../harness/frontend-stdout.sh)" py ./py-sh-go testdata "$(CLI)" || exit 1

worker: test
	@echo "py-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f py-sh-go /tmp/*.mine /tmp/*.core
