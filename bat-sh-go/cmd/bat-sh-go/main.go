// bat-sh-go CLI: Windows batch (.bat) source -> A1 shIR JSON.
// Thin wrapper around the batshgo library (the otranspiler dispatch and
// the frontend worker both call this).
package main

import (
	"fmt"
	"os"

	batshgo "github.com/gmatht/sh2loop/frontends/bat-sh-go"
	shiremit "github.com/gmatht/sh2loop/frontends/shir-emit-go"
)

func main() {
	args := os.Args[1:]
	raw := false
	filtered := []string{}
	for _, a := range args {
		if a == "--raw" {
			raw = true
		} else {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) != 2 || filtered[0] != "--shir" {
		fmt.Fprintln(os.Stderr, "usage: bat-sh-go --shir <file.bat> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src, err := os.ReadFile(inp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", inp, err)
		os.Exit(1)
	}
	prog, err := batshgo.Parse(string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", inp, err)
		os.Exit(1)
	}
	b, err := shiremit.Emit(prog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "emit: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
	if !raw {
		fmt.Println()
	}
}
