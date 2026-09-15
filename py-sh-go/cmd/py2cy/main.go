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
  --py                pure-Python mode (default; .py valid under CPython)
  --pyx               Cython .pyx with cdef declarations
  --gmp               rewrite bigints to GMP (implies --pyx)
  --i128              rewrite 65..128-bit ints to __int128 (implies --pyx)`)
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
		case "--py", "--pure-python", "--mode=py", "--output=py":
			opts.Mode = pylib.ModePy
		case "--pyx", "--mode=pyx", "--output=pyx":
			opts.Mode = pylib.ModePyx
		case "--gmp":
			opts.GMP = true
			opts.Mode = pylib.ModePyx // GMP is .pyx-only
		case "--i128":
			opts.I128 = true
			opts.Mode = pylib.ModePyx // i128 is .pyx-only
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintln(os.Stderr, "py2cy: unknown flag "+a)
				os.Exit(2)
			}
			file = a
		}
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
	if opts.I128 {
		// __int128 middle tier: .pyx with cdef int128/uint128 + helpers.
		// Decline (out of exact subset) falls back to the exact
		// pure-Python output.
		if i128, ok, err := pylib.AnnotateI128(string(src)); err != nil {
			fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
			os.Exit(2)
		} else if ok {
			os.Stdout.WriteString(i128.Source)
			return
		}
		fmt.Fprintln(os.Stderr, "py2cy: --i128: no rewritable 65..128-bit int; falling back to pure-Python annotation")
	}
	if opts.GMP {
		// bigint transform: .pyx with cdef mpz_t + GMP calls. Decline (no
		// rewritable bigint) falls back to the exact pure-Python output.
		if gmp, ok, err := pylib.AnnotateGMP(string(src)); err != nil {
			fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
			os.Exit(2)
		} else if ok {
			os.Stdout.WriteString(gmp.Source)
			return
		}
		fmt.Fprintln(os.Stderr, "py2cy: --gmp: no rewritable bigint; falling back to pure-Python annotation")
	}
	out, err := pylib.AnnotateCython(string(src), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "py2cy: "+err.Error())
		os.Exit(2)
	}
	if opts.Mode == pylib.ModePyx && out.Declined {
		fmt.Fprintln(os.Stderr, "py2cy: --pyx declined (source uses identifiers reserved in Cython .pyx files); emitting pure-Python mode instead")
	}
	os.Stdout.WriteString(out.Source)
}
