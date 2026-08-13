// powershell-sh-go CLI: PowerShell (.ps1) source -> A1 shIR JSON (thin
// wrapper around the ps1lib library). `--cst` dumps the tree-sitter CST
// (a developer aid for pinning new constructs — never part of the gate).
package main

import (
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"

	ps1lib "github.com/gmatht/sh2loop/frontends/powershell-sh-go"
)

func dumpCST(root *sitter.Node, src []byte, depth int) {
	pad := ""
	for i := 0; i < depth; i++ {
		pad += "  "
	}
	txt := ""
	if root.NamedChildCount() == 0 {
		txt = fmt.Sprintf(" %q", root.Content(src))
	}
	fmt.Printf("%s%s%s\n", pad, root.Type(), txt)
	for i := 0; i < int(root.NamedChildCount()); i++ {
		dumpCST(root.NamedChild(i), src, depth+1)
	}
}

func main() {
	args := os.Args[1:]
	var file string
	dump := false
	for _, a := range args {
		switch a {
		case "--shir", "--raw":
		case "--cst":
			dump = true
		default:
			file = a
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: powershell-sh-go --shir <file.ps1> [--raw] [--cst]")
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if dump {
		ps1lib.DumpCST(string(src))
		return
	}
	out, err := ps1lib.Shir(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// No trailing newline — byte-identical to the core's `--shir --raw`
	// output (cli_commands::export_shir uses print!, not println!).
	fmt.Print(string(out))
}
