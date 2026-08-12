# cpp-sh-go: C++14 source -> A1 shIR JSON — the C++-only surface over the
# shared clib lowering (CPP_PLAN.md: include but never modify).
#
# `test` runs the CPP gate (refusal pins + ingress acceptance +
# executed-stdout) AND the C-INVARIANT (the c-sh-go corpus must stay
# green — the cpp worker's hard line, CPP_PLAN §3/§5).
DEBASHC ?= $(abspath ../../sh2perl/target/debug/debashc)
GO ?= go
# per-gate scratch dir (unique via mktemp -d): concurrent gates never
# share filenames (the shared-scratch race that caused transient FAILs).
# Self-healing: a stale root-owned .gate-tmp (left by a pre-single-owner
# gate run) made mktemp fail and silently redirected scratch writes to
# '/' (confusing "cannot create /t01_hello.cc.mine" FAILs). A
# non-writable .gate-tmp is removed (we own the parent) and recreated;
# last resort is a unique dir under /tmp (single-owner /tmp artifacts
# per run-gate.sh). test-cpp fails loudly if SCRATCH is still empty.
SCRATCH := $(shell { [ -w $(abspath .gate-tmp) ] || rm -rf $(abspath .gate-tmp); mkdir -p $(abspath .gate-tmp) && mktemp -d $(abspath .gate-tmp)/gate.XXXXXX; } 2>/dev/null || mktemp -d /tmp/cpp-sh-go-gate.XXXXXX)

.PHONY: all build test test-cpp test-c-invariant worker clean

all: build

build:
	# Atomic + conditional: rebuild ONLY when the sources changed, via a
	# temp file + rename (concurrent gates racing `rm -f; go build -o`
	# produced transient "frontend emit" FAILs — a rebuild mid-exec made
	# the other gate's binary vanish/partial). An up-to-date binary is
	# never touched; concurrent builds are atomic (last one wins).
	@if [ ! -x cpp-sh-go ] || find cmd main.go go.mod -newer cpp-sh-go 2>/dev/null | grep -q .; then \
	  echo "  building cpp-sh-go…"; \
	  $(GO) build -o cpp-sh-go.new.$$ ./cmd/cpp-sh-go \
	    && mv -f cpp-sh-go.new.$$ cpp-sh-go \
	    || { rm -f cpp-sh-go.new.$$; exit 1; }; \
	fi

# Self-heal the ingress oracle too (a missing core debashc wedges the
# gate as a confusing "emit is not valid A1 shIR").
$(DEBASHC):
	cd $(abspath ../../sh2perl) && cargo build --bin debashc

# The CPP gate: refusal pins (REFUSE > GUESS), ingress acceptance (the
# core deserializer accepts every expressible emit), executed-stdout
# (native g++ run vs A1->ESTree->JS).
test-cpp: build $(DEBASHC)
	@test -n "$(SCRATCH)" || { echo "FAIL: cannot create gate scratch dir (is $(abspath .gate-tmp) writable?)"; exit 1; }
	@echo "--- refusals: *_refuse.cc must FAIL loudly ---"
	@for f in testdata_cpp/*_refuse.cc; do \
	  bn=$$(basename $$f); \
	  if ./cpp-sh-go --shir $$f --raw >/dev/null 2>$(SCRATCH)/cpp-refuse.err; then \
	    echo "FAIL $$bn (expected loud refusal, got a parse)"; exit 1; \
	  else \
	    echo "OK $$bn (refused loudly: $$(head -1 $(SCRATCH)/cpp-refuse.err))"; \
	  fi; \
	done
	@echo "--- expressible: ingress acceptance (debashc --shir-in-estree) ---"
	@for f in testdata_cpp/*.cc; do \
	  case "$$f" in *_refuse.cc) continue;; esac; \
	  bn=$$(basename $$f); \
	  ./cpp-sh-go --shir $$f --raw > $(SCRATCH)/$$bn.mine 2>/dev/null || { echo "FAIL $$bn (frontend emit)"; exit 1; }; \
	  python3 $(abspath ../../harness/outparam_to_returns.py) < $(SCRATCH)/$$bn.mine > $(SCRATCH)/$$bn.out.json 2>/dev/null && mv $(SCRATCH)/$$bn.out.json $(SCRATCH)/$$bn.mine; \
	  if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	    echo "OK $$bn (valid A1 shIR — the core deserializer accepts it)"; \
	  else \
	    st=$$?; \
	    # One retry: a concurrent core rebuild (the js backend gate \
	    # relinks the SHARED target/debug/debashc) may replace the \
	    # binary mid-gate (exec failure / crash -> exit 126/127/>=128), \
	    # which is NOT an emit defect (run-gate.sh documents the same \
	    # race; the 2026-08-12 12:15 incident rejected valid emits). A \
	    # genuine rejection (exit 1) fails the retry too. \
	    sleep 3; \
	    if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	      echo "OK $$bn (valid A1 shIR — first probe exit $$st, transient oracle hiccup, retry accepted)"; \
	    else \
	      st2=$$?; \
	      # Two consecutive rejections. Rule out a STALE/BROKEN oracle: \
	      # the estree worker's cargo build may leave debashc relinked \
	      # from a transient tree. Remove the binary so cargo RELINKS \
	      # from the checked-out core even when its fingerprint thinks \
	      # the output is fresh (a no-op recompile otherwise — a genuine \
	      # rejection still fails the re-probe) and probe once more. \
	      rm -f $(DEBASHC); \
	      (cd $(abspath ../../sh2perl) && cargo build --bin debashc > /dev/null 2>&1); \
	      if $(DEBASHC) --shir-in-estree $(SCRATCH)/$$bn.mine > /dev/null 2>&1; then \
	        echo "OK $$bn (valid A1 shIR — oracle relinked, re-probe accepted; first probes $$st/$$st2)"; \
	      else \
	        st3=$$?; \
	        echo "FAIL $$bn (emit is not valid A1 shIR — the gap the worker fixes; probes $$st/$$st2, post-rebuild $$st3)"; \
	        exit 1; \
	      fi; \
	    fi; \
	  fi; \
	done
	@echo "--- executed-stdout comparison (native g++ vs JS/ESTree) ---"
	@bash "$(abspath ../../harness/frontend-stdout.sh)" cpp ./cpp-sh-go testdata_cpp "$(DEBASHC)" || exit 1
	@rm -rf $(SCRATCH)

# The C-INVARIANT (CPP_PLAN §3): the C corpus stays green. This is the
# cpp gate's hard line — the cpp worker can never commit while the
# shared lowering regresses C.
test-c-invariant: $(DEBASHC)
	@echo "--- C-invariant: c-sh-go corpus stays green ---"
	@tmp=$$(mktemp -d); trap 'rm -rf $$tmp' EXIT; \
	if $(MAKE) -C ../c-sh-go test > $$tmp/c-inv.log 2>&1; then \
	  echo "OK C-invariant (c-sh-go make test green)"; \
	else \
	  st=$$?; \
	  # One retry: the nested C gate has no oracle-retry of its own, \
	  # and the js backend gate relinks the SHARED debashc — a mid- \
	  # relink truncation (run-gate.sh's documented race; hit on \
	  # 2026-08-12 12:15 with valid emits rejected) fails exactly this \
	  # nested run. A real C regression fails the retry too. \
	  sleep 3; \
	  if $(MAKE) -C ../c-sh-go test > $$tmp/c-inv.log 2>&1; then \
	    echo "OK C-invariant (c-sh-go make test green — first run exit $$st, transient oracle hiccup, retry accepted)"; \
	  else \
	    st2=$$?; \
	    # Still red: rule out a STALE/BROKEN oracle (relinked from a \
	    # transient tree) before declaring the shared lowering broken. \
	    # Remove the binary so cargo RELINKS from the checked-out core \
	    # (a fingerprint-fresh-but-corrupt output would otherwise be \
	    # treated as up to date) and run the C gate once more. \
	    rm -f $(DEBASHC); \
	    (cd $(abspath ../../sh2perl) && cargo build --bin debashc > /dev/null 2>&1); \
	    if $(MAKE) -C ../c-sh-go test > $$tmp/c-inv.log 2>&1; then \
	      echo "OK C-invariant (c-sh-go make test green — oracle relinked, re-run accepted; first runs $$st/$$st2)"; \
	    else \
	      st3=$$?; \
	      echo "FAIL: C-invariant broken (c-sh-go make test; runs $$st/$$st2, post-rebuild $$st3)"; \
	      echo "--- last c-sh-go gate output (tail) ---"; \
	      tail -25 $$tmp/c-inv.log | sed 's/^/  /'; \
	      exit 1; \
	    fi; \
	  fi; \
	fi

test: test-cpp test-c-invariant

worker: test
	@echo "cpp-sh-go worker gate: refusals + ingress + stdout + C-invariant passed"

clean:
	rm -f cpp-sh-go
