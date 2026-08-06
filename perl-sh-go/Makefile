# perl-sh-go: Perl source -> shIR JSON (A1 contract), ANTLR4+Go.
# The hand-rolled recursive-descent parser in main.go lowers the v1 Perl
# subset (testdata/) to the A1 shIR vocabulary; `build` compiles it (the
# antlr4 generation from grammars/Perl.g4 is the WORKER's TODO — not a
# build blocker). `test` runs the BYTE-EQUALITY ORACLE vs the core's
# ESTree backend: the emitted A1 must survive the strict ingress
# (`debashc --shir-in-estree`) AND, executed through the reference
# runner, match native perl stdout byte-for-byte. A passing test = real
# progress (correct output); a mismatch = the gap to fix.

ANTLR4 ?= antlr4
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= /snap/go/current/bin/go

.PHONY: all gen build test worker clean

all: build

# The ingress oracle (--shir-in-estree) is the core's debashc binary,
# normally built by the estree worker. Build it when missing so this
# gate is self-sufficient (cargo's file lock serializes concurrent
# builds; a no-op when already present).
$(DEBASHC):
	cd ../../sh2perl && cargo build --bin debashc

# TODO: generate the Go parser from grammars/Perl.g4 (the worker wires
# this via pi/deepseek-v4-flash). The stub in main.go is the initial impl.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package perl_sh_go -visitor -o gen grammars/Perl.g4" >&2
	@exit 1

build:
	$(GO) build -o perl-sh-go .

test: build $(DEBASHC)
	@for f in testdata/*.pl; do \
	  bn=$$(basename $$f); \
	  ./perl-sh-go --shir $$f --raw > /tmp/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  # Ingress acceptance: the core consumes SHELL, not pl source, so \
	  # byte-equality vs debashc --shir is impossible. The progress signal \
	  # is: the emitted A1 shIR JSON is VALID (the core's deserializer \
	  # accepts it via --shir-in-estree). \
	  if $(DEBASHC) --shir-in-estree /tmp/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes)"; \
	    exit 1; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" pl ./perl-sh-go testdata "$(DEBASHC)" || exit 1

worker: test
	@echo "perl-sh-go worker gate: ingress acceptance + executed-stdout passed"

clean:
	rm -f perl-sh-go /tmp/*.mine /tmp/*.core
