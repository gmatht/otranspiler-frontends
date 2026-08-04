# perl-sh-go: Perl source -> shIR JSON (A1 contract), initial
# ANTLR4+Go frontend.

.PHONY: all gen build test worker clean

all: build

# TODO: generate Go parser from grammars/Perl.g4.
gen:
	@echo "TODO: antlr4 -Dlanguage=Go -package perl_sh_go -visitor -o gen grammars/Perl.g4" >&2
	@echo "      (the worker generates the proper .g4 + runs antlr4)" >&2
	@exit 1

build: gen
	go build -o perl-sh-go .

test: build
	./perl-sh-go --shir grammars/Perl.g4 --raw > /dev/null

worker: build
	./perl-sh-go --shir grammars/Perl.g4 --raw > /dev/null

clean:
	rm -f perl-sh-go gen/*.go
