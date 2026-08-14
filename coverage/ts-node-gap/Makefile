# Precompile the vendored tree-sitter grammars into a static library ONCE —
# cgo would otherwise recompile ~16MB of generated parser.c on every go
# build (minutes on a loaded box). ts-node-gap.sh runs `make libgrammars.a`
# when it's missing; go build then just links.
CFLAGS ?= -std=c11 -fPIC -O0
TS  := ../../
OBJ := obj

all: libgrammars.a ts-node-gap

obj:
	mkdir -p $@

obj/powershell-parser.o: $(TS)powershell-sh-go/grammars/tree-sitter-powershell/src/parser.c | obj
	gcc $(CFLAGS) -I$(TS)powershell-sh-go/grammars/tree-sitter-powershell/src -c $< -o $@
obj/powershell-scanner.o: $(TS)powershell-sh-go/grammars/tree-sitter-powershell/src/scanner.c | obj
	gcc $(CFLAGS) -I$(TS)powershell-sh-go/grammars/tree-sitter-powershell/src -c $< -o $@
obj/fish-parser.o: $(TS)fish-sh-go/grammars/tree-sitter-fish/src/parser.c | obj
	gcc $(CFLAGS) -I$(TS)fish-sh-go/grammars/tree-sitter-fish/src -c $< -o $@
obj/fish-scanner.o: $(TS)fish-sh-go/grammars/tree-sitter-fish/src/scanner.c | obj
	gcc $(CFLAGS) -I$(TS)fish-sh-go/grammars/tree-sitter-fish/src -c $< -o $@
obj/zsh-parser.o: $(TS)zsh-sh-go/grammars/tree-sitter-zsh/src/parser.c | obj
	gcc $(CFLAGS) -I$(TS)zsh-sh-go/grammars/tree-sitter-zsh/src -c $< -o $@
obj/zsh-scanner.o: $(TS)zsh-sh-go/grammars/tree-sitter-zsh/src/scanner.c | obj
	gcc $(CFLAGS) -I$(TS)zsh-sh-go/grammars/tree-sitter-zsh/src -c $< -o $@
obj/zig-parser.o: $(TS)zig-sh-go/grammars/tree-sitter-zig/src/parser.c | obj
	gcc $(CFLAGS) -I$(TS)zig-sh-go/grammars/tree-sitter-zig/src -c $< -o $@

libgrammars.a: obj/powershell-parser.o obj/powershell-scanner.o obj/fish-parser.o obj/fish-scanner.o obj/zsh-parser.o obj/zsh-scanner.o obj/zig-parser.o
	ar rcs $@ $^

ts-node-gap: libgrammars.a main.go go.mod go.sum
	go build -o $@ .

clean:
	rm -rf obj libgrammars.a ts-node-gap
