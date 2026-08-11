// zig-sh-go CLI: Zig source -> A1 shIR JSON (thin wrapper around the
// ziglib library).
package main

import (
	"fmt"
	"os"

	ziglib "github.com/gmatht/sh2loop/frontends/zig-sh-go"
)

func main() {
	args := os.Args[1:]
	var file string
	for _, a := range args {
		if a == "--shir" || a == "--raw" {
			continue
		}
		file = a
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: zig-sh-go --shir <file.zig> [--raw]")
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	out, err := ziglib.Shir(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
