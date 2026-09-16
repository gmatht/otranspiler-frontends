// parse_test.go — the ANTLR front-end coverage gate.
//
// The hand-rolled v1 parser covered the t01-t101 subset; the generated
// ANTLR grammar must accept that whole corpus AND real Python the subset
// refused (see corpus/). A parse failure here is a real coverage
// regression, not a cosmetic one.
package pylib

import (
	"os"
	"path/filepath"
	"testing"
)

func parseDir(t *testing.T, glob string) int {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatalf("glob %s: %v", glob, err)
	}
	if len(files) == 0 {
		t.Fatalf("no files matched %s", glob)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if needsLowering[f] {
			// Post-12 syntax the grammar cannot parse: the lowering
			// (pep695.go) must accept it and its output must parse.
			lowered, ok := lowerPEP695(string(src))
			if !ok {
				t.Errorf("%s: lowering declined", f)
				continue
			}
			if _, errs := ParsePython(lowered); len(errs) > 0 {
				t.Errorf("%s: lowered output has %d syntax error(s): %v", f, len(errs), errs[:1])
			}
			continue
		}
		if _, errs := ParsePython(string(src)); len(errs) > 0 {
			n := len(errs)
			if n > 3 {
				n = 3
			}
			t.Errorf("%s: %d syntax error(s), first %d: %v", f, len(errs), n, errs[:n])
		}
	}
	return len(files)
}

// needsLowering lists fixtures using post-12 syntax the ANTLR grammar
// cannot parse (PEP 695): they must lower instead of parsing raw.
var needsLowering = map[string]bool{
	"testdata/t103_pep695.py": true,
}

func TestParseSubsetCorpus(t *testing.T) {
	// Every t01-t101 example must parse (they are the lowering corpus).
	if n := parseDir(t, "testdata/*.py"); n < 90 {
		t.Errorf("only %d testdata files found", n)
	}
}

func TestParseRealPython(t *testing.T) {
	// Constructs the hand-rolled parser refused: class/decorator/lambda/
	// generator/async/star-args/import/match/comprehension/...
	if n := parseDir(t, "corpus/*.py"); n < 1 {
		t.Errorf("no corpus files found")
	}
}

func TestParseRefusesGarbage(t *testing.T) {
	// A non-program must still be rejected, so the gate is not vacuous.
	if _, errs := ParsePython("def f(:\n    pass\n"); len(errs) == 0 {
		t.Errorf("garbage accepted by the ANTLR parser")
	}
}
