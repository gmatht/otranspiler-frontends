// powershell-sh-go CLI: PowerShell (.ps1) source -> A1 shIR JSON (thin
// wrapper around the ps1lib library).
package main

import (
	"fmt"
	"os"

	ps1lib "github.com/gmatht/sh2loop/frontends/powershell-sh-go"
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
		fmt.Fprintln(os.Stderr, "usage: powershell-sh-go --shir <file.ps1> [--raw]")
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	out, err := ps1lib.Shir(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
