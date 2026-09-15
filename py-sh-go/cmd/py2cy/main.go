// py2cy CLI: Python source -> typed pure-Python-mode Cython (stdout).
//
// Thin wrapper over pylib.AnnotateCython (autocython.go); the annotation
// pass runs over the full ANTLR parse tree. See docs/AUTO_CYTHON.md.
package main

import (
	"fmt"
	"os"

	pylib "github.com/gmatht/sh2loop/frontends/py-sh-go"
)

func main() {
	args := os.Args[1:]
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: py2cy <file.py>    # typed Cython to stdout")
		if len(args) == 1 {
			os.Exit(0)
		}
		os.Exit(2)
	}
	src, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
		os.Exit(2)
	}
	out, err := pylib.AnnotateCython(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
		os.Exit(2)
	}
	os.Stdout.WriteString(out.Source)
}
