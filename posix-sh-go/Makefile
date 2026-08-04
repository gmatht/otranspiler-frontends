# posix-sh-go: POSIX shell source -> shIR JSON (A1 contract),
# initial ANTLR4+Go frontend.

.PHONY: all gen build test worker clean

all: build

# TODO: generate Go parser from grammars/POSIX.g4.
# The worker (run_worker.sh -> pi/deepseek-v4-flash) fleshes this
# out: runs `antlr4 -Dlanguage=Go -package posix_sh_go -visitor -o gen
# grammars/POSIX.g4`, and replaces the hand-rolled stub parser in
# main.go with the generated listener.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package posix_sh_go -visitor -o gen grammars/POSIX.g4" >&2
	@echo "      (the worker generates the proper .g4 + runs antlr4)" >&2
	@exit 1

build: gen
	go build -o posix-sh-go .

test: build
	./posix-sh-go --shir grammars/POSIX.g4 --raw > /dev/null

worker: build
	./posix-sh-go --shir grammars/POSIX.g4 --raw > /dev/null

clean:
	rm -f posix-sh-go gen/*.go
