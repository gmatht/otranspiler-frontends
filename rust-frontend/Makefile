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
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)

.PHONY: all build test worker clean

all: build

build:
	cargo build --offline

# Self-heal the ingress oracle: a missing core debashc binary wedges the
# gate as a confusing "FAIL (emit is not valid A1 shIR)". Build it on
# demand; the oracle is read-only here.
$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

test: build $(DEBASHC)
	@echo "--- refusal pins (the emit MUST fail: REFUSE > GUESS) ---"
	@for f in testdata/*_refuse.rs; do \
	  bn=$$(basename "$$f"); \
	  if ./target/debug/rust-frontend --shir "$$f" --raw > /dev/null 2>&1; then \
	    echo "FAIL $$bn (should refuse)"; exit 1; \
	  fi; \
	  echo "OK   $$bn (refused)"; \
	done
	@echo "--- ingress acceptance (the core deserializer accepts the emitted A1) ---"
	@for f in testdata/*.rs; do \
	  bn=$$(basename "$$f"); \
	  case "$$bn" in *_refuse.rs) continue;; esac; \
	  ./target/debug/rust-frontend --shir "$$f" --raw > /tmp/$$bn.a1.json 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.a1.json > /dev/null 2>&1; then \
	    echo "OK   $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native rustc vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" rust ./target/debug/rust-frontend testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "rust-frontend worker gate: refusals + ingress acceptance + executed-stdout passed"

clean:
	cargo clean
