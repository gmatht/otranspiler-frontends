// cpp-sh-go CLI: C++14 source -> A1 shIR JSON (thin wrapper around the
// cppshgo library — the C++-only surface over the shared clib lowering).
package main

import (
	"fmt"
	"os"

	cppshgo "github.com/gmatht/sh2loop/frontends/cpp-sh-go"
)

func main() {
	args := os.Args[1:]
	var file string
	for _, a := range args {
		if a == "--shir" || a == "--raw" || a == "--lang" {
			continue
		}
		if a == "c" || a == "cpp" || a == "c++" { // the --lang value
			continue
		}
		file = a
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: cpp-sh-go --shir <file.cc> [--raw]")
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	out, err := cppshgo.Shir(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Stdout.Write(out)
	os.Stdout.Write([]byte{'\n'})
}
