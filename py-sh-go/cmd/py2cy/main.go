// py2cy CLI: Python source -> typed pure-Python-mode Cython (stdout).
//
// Thin wrapper over pylib.AnnotateCython (autocython.go); the annotation
// pass runs over the full ANTLR parse tree. See docs/AUTO_CYTHON.md.
//
// Levels (all behaviour-preserving):
//
//	--simple-opts-only   only i64 literals and literal-bounded counters
//	--no-opts            passthrough (no declarations; Stage 0)
//	(default)            the full interval analysis
package main

import (
	"fmt"
	"os"
	"strings"

	pylib "github.com/gmatht/sh2loop/frontends/py-sh-go"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: py2cy [--simple-opts-only|--no-opts] <file.py>

  emits pure-Python-mode Cython with cython.declare(...) for the integer
  scalars the analysis proves fit i64; the source is otherwise unchanged.

  --no-opts           passthrough: no declarations (Stage 0)
  --simple-opts-only  only i64 literals and literal-bounded counters
  --gmp               (reserved) rewrite bigints to GMP; not implemented yet`)
}

func main() {
	opts := pylib.DefaultOptions()
	var file string
	for _, a := range os.Args[1:] {
		switch a {
		case "-h", "--help":
			usage()
			return
		case "--no-opts", "--annotate=none":
			opts.Level = pylib.OptNone
		case "--simple-opts-only", "--annotate=simple":
			opts.Level = pylib.OptSimple
		case "--annotate=full":
			opts.Level = pylib.OptFull
		case "--gmp":
			opts.GMP = true
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintln(os.Stderr, "py2cy: unknown flag "+a)
				os.Exit(2)
			}
			file = a
		}
	}
	if opts.GMP {
		fmt.Fprintln(os.Stderr, "py2cy: --gmp (bigint transform) is not implemented yet")
		os.Exit(2)
	}
	if file == "" {
		usage()
		os.Exit(2)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
		os.Exit(2)
	}
	out, err := pylib.AnnotateCython(string(src), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
		os.Exit(2)
	}
	os.Stdout.WriteString(out.Source)
}
