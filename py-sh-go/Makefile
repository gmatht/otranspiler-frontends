# py-sh-go: Python source -> shIR JSON (A1 contract), ANTLR4+Go.
#
# The initial frontend handles a v1 shell-flavored Python subset; the
# per-frontend worker (run_worker.sh) invokes `pi` (opencode-go +
# deepseek-v4-flash, automatic key rotation) on build/test failure to
# expand the listener to handle more of Python.

ANTLR4 ?= antlr4
GRAMMARS := grammars/Python3Lexer.g4 grammars/Python3Parser.g4
GENERATED := generated

.PHONY: all gen build test worker clean

all: gen build

# Generate the Go parser from the official Python3 grammar.
gen:
	mkdir -p $(GENERATED)
	$(ANTLR4) -Dlanguage=Go -package python3parser -visitor -o $(GENERATED) $(GRAMMARS)

# Build the CLI.
build: gen
	go build -o py-sh-go .

# Run the test suite (the worker extends this; the initial version
# has only a minimal smoke test).
test: build
	go test ./...

# The scoped worker: build, run a quick smoke, and exit 0.
# (The full per-iteration loop — git status, commit within scope,
# `pi` on failure — lives in run_worker.sh; this Makefile target is
# what the worker calls.)
worker: build
	./py-sh-go --shir grammars/Python3Lexer.g4 --raw > /dev/null

clean:
	rm -rf $(GENERATED) py-sh-go
