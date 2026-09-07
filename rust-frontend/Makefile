# rust-frontend: Rust source -> A1 shIR JSON (syn-based, offline build).
# v0.1 subset: fn main only; let/mut integer+string bindings; int arith;
# compound assigns; if/else/else-if; while; for i in 0..N / 0..=N (literal
# bounds); print!/println! with {} placeholders (-> printf %s, % escaped
# to %%); comparisons + &&/||/!/parens conditions (-> the test string
# grammar). Everything else REFUSES (exit 1) — see FRONTEND.md.
#
# `test` runs the three-stage gate:
#   1. refusal pins (testdata/*_refuse.rs — the emit MUST fail)
#   2. ingress acceptance (the core deserializer accepts the emitted A1)
#   3. executed-stdout oracle (native rustc vs the A1->ESTree->JS run)
CLI ?= $(abspath ../../otranspilerl/target/debug/otranspilerl-cli)
# Per-gate oracle snapshot (see harness/snapshot-otranspilerl-cli.sh): concurrent
# workers rebuild target/debug/otranspilerl-cli mid-gate (estree worker core
# changes; other gates' self-heal rules), and a torn binary wedges ONE
# test with a misleading FAIL. 2026-08-15: the rust-frontend gate failed
# right as the estree worker relinked otranspilerl-cli (core mediation round
# 177255ec, binary dated 18:43; gate failure ~18:46) — the ingress stage
# was invoking the SHARED binary directly, so a relink tear read as
# "FAIL <file> (emit is not valid A1 shIR — the gap the worker fixes)"
# and escalated to pi as a frontend build failure. The zsh-sh-go gate
# already runs every oracle phase on a verified snapshot (t71_var_name_mods
# incident, 2026-08-12 20:55:24); this gate now does the same: stages 2
# and 3 run the stable verified copy below, never the shared binary.
# GATE_TMP is created LAZILY — only the test/worker goals need the
# oracle snapshot. An eager `$(shell mktemp -d …)` at parse time made
# EVERY make invocation (build/all/clean) leak one ~20MB dir. $(if)
# expands only the taken branch, so a non-test goal never runs mktemp.
GATE_TMP := $(if $(filter test worker,$(MAKECMDGOALS)),$(shell mktemp -d /tmp/rust-frontend-gate.XXXXXX),/nonexistent-gate-tmp)
GATE_CLI := $(GATE_TMP)/otranspilerl-cli

$(GATE_CLI): $(CLI)
	@bash "$(abspath ../../harness/snapshot-otranspilerl-cli.sh)" $(CLI) $@ \
	  || { echo "FAIL: no verified otranspilerl-cli snapshot (concurrent rebuild?)"; exit 1; }

.PHONY: all build test worker clean

all: build

build:
	cargo build --offline

# Self-heal the ingress oracle: a missing core otranspilerl-cli binary wedges the
# gate as a confusing "FAIL (emit is not valid A1 shIR)". Build it on
# demand; the oracle is read-only here (the snapshot rule then verifies
# a stable copy of it).
$(CLI):
	cd $(abspath ../../otranspilerl) && cargo build --bin otranspilerl-cli

test: build $(GATE_CLI)
	# GATE_TMP is a per-invocation mktemp dir; without a trap the worker's
	# 5-min gate loop leaks one ~20MB otranspilerl-cli snapshot per run (~5.8GB/day;
	# when /tmp fills, mktemp fails and the gate wedges with a spurious
	# FAIL). Clean it on exit (success or failure) from ONE shell covering
	# all three phases.
	@trap 'rm -rf $(GATE_TMP)' EXIT; \
	echo "--- refusal pins (the emit MUST fail: REFUSE > GUESS) ---"; \
	for f in testdata/*_refuse.rs; do \
	  bn=$$(basename "$$f"); \
	  if ./target/debug/rust-frontend --shir "$$f" --raw > /dev/null 2>&1; then \
	    echo "FAIL $$bn (should refuse)"; exit 1; \
	  fi; \
	  echo "OK   $$bn (refused)"; \
	done; \
	echo "--- ingress acceptance (the core deserializer accepts the emitted A1) ---"; \
	for f in testdata/*.rs; do \
	  bn=$$(basename "$$f"); \
	  case "$$bn" in *_refuse.rs) continue;; esac; \
	  ./target/debug/rust-frontend --shir "$$f" --raw > /tmp/$$bn.a1.json 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(GATE_CLI) - --target estree < /tmp/$$bn.a1.json > /dev/null 2>&1; then \
	    echo "OK   $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done; \
	echo "--- executed-stdout comparison (native rustc vs JS/ESTree) ---"; \
	bash "$(abspath ../../harness/frontend-stdout.sh)" rust ./target/debug/rust-frontend testdata "$(GATE_CLI)" || exit 1

worker: test
	@echo "rust-frontend worker gate: refusals + ingress acceptance + executed-stdout passed"

clean:
	cargo clean
	rm -rf $(GATE_TMP)
