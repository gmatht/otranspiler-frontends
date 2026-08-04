# go-sh: Go source -> shIR JSON (A1 contract), ANTLR4+Go.

.PHONY: all gen build test worker clean

all: build

# TODO: generate Go parser from grammars/GoLexer.g4 + GoParser.g4.
# The worker (run_worker.sh -> pi/deepseek-v4-flash) runs:
#   antlr4 -Dlanguage=Go -package go_sh -visitor -o gen grammars/GoLexer.g4 grammars/GoParser.g4
# and replaces the stub parser in go-sh.go with the generated listener.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package go_sh -visitor -o gen grammars/GoLexer.g4 grammars/GoParser.g4" >&2
	@echo "      (the worker generates the proper .g4 + runs antlr4)" >&2
	@exit 1

build: gen
	go build -o go-sh .

test: build
	./go-sh --shir grammars/GoLexer.g4 --raw > /dev/null

worker: build
	./go-sh --shir grammars/GoLexer.g4 --raw > /dev/null

clean:
	rm -f go-sh gen/*.go
