# zsh-sh-go: zsh source -> shIR JSON (A1 contract), ANTLR4+Go.
# WORKER REWRITE (2026-08-06): main.go is a faithful hand-rolled port of the
# core frontend (lexer+parser in main.go, lowering in lowering.go, A2
# var-type analysis + A1 JSON emitter in analysis.go), extended for the zsh
# corpus: `[[ ]]`/`(( ))` conditions, bare `$a[2]` array indexes,
# `f() {` bodies, heredocs (`<<EOF`). `test` runs the BYTE-EQUALITY ORACLE
# vs the core's `otranspilerl-cli --shir FILE --raw` — the emitted JSON must be
# byte-identical (the A1 contract). A passing test = real progress
# (correct output); a mismatch = the gap to fix.

ANTLR4 ?= antlr4
CLI ?= $(abspath ../../otranspilerl/target/debug/otranspilerl-cli)
GO ?= /snap/go/current/bin/go
# Per-gate oracle snapshot (see harness/snapshot-otranspilerl-cli.sh): concurrent
# workers rebuild target/debug/otranspilerl-cli mid-gate (estree worker core
# changes; other gates' self-heal rules), and a torn binary wedges ONE
# test with a misleading FAIL (t71_var_name_mods, 2026-08-12 20:55:24).
# The byte-equality phase and the stdout harness both run the stable
# verified copy below, never the shared binary.
# GATE_TMP is created LAZILY — only the test/worker goals need the oracle
# snapshot. An eager `$(shell mktemp -d …)` at parse time made EVERY make
# invocation (build/all/clean/gen) leak one ~20MB dir; the worker only
# runs `make test` (trap-cleaned below), but a manual `make build` leaked
# one dir per run. $(if) expands only the taken branch, so a non-test
# goal never runs mktemp. (GATE_CLI is a `:=` after this, so the
# value is fixed at parse for test/worker runs.)
GATE_TMP := $(if $(filter test worker,$(MAKECMDGOALS)),$(shell mktemp -d /tmp/zsh-sh-go-gate.XXXXXX),/nonexistent-gate-tmp)
GATE_CLI := $(GATE_TMP)/otranspilerl-cli

$(GATE_CLI): $(CLI)
	@bash "$(abspath ../../harness/snapshot-otranspilerl-cli.sh)" $(CLI) $@ \
	  || { echo "FAIL: no verified otranspilerl-cli snapshot (concurrent rebuild?)"; exit 1; }

.PHONY: all gen build test worker clean

all: build

# TODO: generate the Go parser from grammars/ZshLexer.g4 + ZshParser.g4
# (the worker wires this via pi). The hand-rolled parser in main.go is
# the current impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package zsh_sh_go -visitor -o gen grammars/ZshLexer.g4 grammars/ZshParser.g4" >&2
	@exit 1

build:
	# Atomic + conditional rebuild (concurrent-gate safety; see cpp-sh-go):
	# rebuild ONLY when the sources changed, via a PID-unique temp file +
	# rename. The old `rm -f; go build -o` left a window with NO binary —
	# the 2026-08-14 01:45:44 triage sweep hit it mid-rebuild and
	# mis-recorded t16-t20 as FAIL-FRONTEND-EMIT (frontend refused).
	# A fixed-name temp is NOT enough: two concurrent `make build`s
	# interleave writes to it and publish a partial binary (observed
	# 2026-08-14), so the temp is PID-unique. Note the FOUR-dollar temp:
	# make collapses dollar-pairs, so four reach the shell as two (the
	# shell PID); two would reach it as one literal dollar and collide
	# again. An up-to-date binary is never touched; concurrent
	# builds are atomic (last COMPLETE one wins); mv(1) means any
	# concurrent reader (triage sweep, coverage step) sees either the
	# previous complete binary or the new one.
	@if [ ! -x zsh-sh-go ] || find cmd *.go go.mod -newer zsh-sh-go 2>/dev/null | grep -q .; then \
	  echo "  building zsh-sh-go…"; \
	  rm -f .zsh-sh-go.tmp.$$$$; \
	  $(GO) build -o .zsh-sh-go.tmp.$$$$ ./cmd/zsh-sh-go \
	    && mv -f .zsh-sh-go.tmp.$$$$ zsh-sh-go \
	    || { rm -f .zsh-sh-go.tmp.$$$$; exit 1; }; \
	fi

# PLAN v12: zsh arrays are 1-based but the A1 contract is canonical
# 0-based — the frontend normalizes subscripts at emit, which the core's
# bash parse cannot match byte-for-byte. Files with array subscripts skip
# byte-equality (executed-stdout is the semantic anchor; byte-equality is
# only a conformance net where the core parses faithfully). `${#x}` /
# `${#a[@]}` (length) have no subscript and stay byte-compared.
# Self-heal the byte-equality oracle too: a missing core otranspilerl-cli binary
# (fresh checkout, interrupted core build) wedges the gate as a confusing
# "FAIL <file> (core)". Build it on demand; the oracle is read-only here.
$(CLI):
	cd $(abspath ../../otranspilerl) && cargo build --bin otranspilerl-cli

test: build $(GATE_CLI)
	# GATE_TMP is a per-invocation mktemp dir; without a trap the worker's
	# 5-min gate loop leaks one ~20MB otranspilerl-cli snapshot per run (~5.8GB/day
	# when /tmp fills, mktemp fails and the gate wedges with a spurious
	# FAIL). Clean it on exit (success or failure) from ONE shell covering
	# both phases.
	@trap 'rm -rf $(GATE_TMP)' EXIT; \
	for f in testdata/*.zsh; do \
	  bn=$$(basename $$f); \
	  ./zsh-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend)"; exit 1; }; \
	  $(GATE_CLI) --shir $$f --raw > /tmp/$$bn.core 2>/dev/null || { echo "FAIL $$bn (core)"; exit 1; }; \
	  if grep -qEf "$(abspath ../../harness/zsh-subscript.ere)" "$$f"; then \
	    echo "OK $$bn (array-subscript file — byte-equality waived; stdout oracle only)"; \
	  elif diff -q /tmp/$$bn.mine /tmp/$$bn.core > /dev/null; then \
	    echo "OK $$bn (byte-equal)"; \
	  else \
	    echo "DIFF $$bn (frontend != core — the gap the worker fixes)"; \
	    diff /tmp/$$bn.mine /tmp/$$bn.core | head -5; \
	    exit 1; \
	  fi; \
	done; \
	echo "--- executed-stdout comparison (native vs JS/ESTree) ---"; \
	bash "$(abspath ../../harness/frontend-stdout.sh)" zsh ./zsh-sh-go testdata "$(GATE_CLI)"

worker: test
	@echo "zsh-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f zsh-sh-go .zsh-sh-go.tmp.* /tmp/*.mine /tmp/*.core
	rm -rf $(GATE_TMP)
