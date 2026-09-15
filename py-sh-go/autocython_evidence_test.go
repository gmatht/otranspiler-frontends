// autocython_evidence_test.go — guardrails for the evidence table.
//
// The evidence table is the audit surface: per typed name it states the
// proved VALUE range and the required C STORAGE width. Two failure modes it
// must never have:
//
//  1. an UNSOUND range — a value the program actually takes lies outside
//     [Lo,Hi]. E.g. `i = 10; while i > 0: i = i - 1` reported Int[2,10]
//     while `i` is 0 at the print. A too-narrow lower bound can also pick a
//     too-narrow width (a miscompile), not merely a dishonest table.
//  2. a value-type that is not an interval at all (the profiler's `1..8`
//     magnitude *bucket* mistaken for a value range), or a width that cannot
//     hold the range it is paired with.
//
// The oracle for (1) is CPython: the trueLo/trueHi columns below are the
// actual min/max the name takes under a traced exec (including the
// post-loop value), not whatever the analysis happens to print. Asserting
// soundness — not equality with the tool's own output — is the whole point:
// a table generated from the proof cannot be checked against itself.
package pylib

import (
	"regexp"
	"strings"
	"testing"
)

type rangeCase struct {
	tag            string
	src, name      string
	trueLo, trueHi int64 // actual min/max under CPython
	tight          bool  // unit-step counter: the analysis should be exact
}

// Ranges verified with `python3` (traced exec) — see the header.
var evidenceRangeCases = []rangeCase{
	{"lt5", "i = 0\nwhile i < 5:\n    i = i + 1\nprint(i)\n", "i", 0, 5, true},
	{"lt100", "i = 0\nwhile i < 100:\n    i = i + 1\nprint(i)\n", "i", 0, 100, true},
	{"gt0", "i = 10\nwhile i > 0:\n    i = i - 1\nprint(i)\n", "i", 0, 10, true},
	{"le5", "i = 0\nwhile i <= 5:\n    i = i + 1\nprint(i)\n", "i", 0, 6, true},
	{"ge0", "i = 10\nwhile i >= 0:\n    i = i - 1\nprint(i)\n", "i", -1, 10, true},
	{"st2", "i = 0\nwhile i < 5:\n    i += 2\nprint(i)\n", "i", 0, 6, true},
	{"dec2", "i = 10\nwhile i > 0:\n    i -= 2\nprint(i)\n", "i", 0, 10, false},
	{"range5", "for i in range(5):\n    pass\nprint(i)\n", "i", 0, 4, false},
	{"range29", "for i in range(2, 9):\n    pass\nprint(i)\n", "i", 2, 8, false},
}

func evidenceFor(t *testing.T, tc rangeCase) (IntEvidence, *CythonOutput) {
	t.Helper()
	out := annotate(t, tc.src)
	ev, ok := out.EvidenceFor(tc.name)
	if !ok {
		t.Errorf("%s: %s not typed (no evidence)", tc.tag, tc.name)
	}
	return ev, out
}

// Soundness: the evidence range contains every value the program takes.
func TestEvidenceCounterRangesSound(t *testing.T) {
	for _, tc := range evidenceRangeCases {
		ev, _ := evidenceFor(t, tc)
		if ev.Lo > tc.trueLo || ev.Hi < tc.trueHi {
			t.Errorf("%s: UNSOUND %s excludes an actual value: reported %s, actual [%d,%d]",
				tc.tag, ev.ValueType(), ev.ValueType(), tc.trueLo, tc.trueHi)
		}
	}
}

// Tightness: a unit-step counter is exactly representable, so the analysis
// should be exact (not merely sound).
func TestEvidenceCounterRangesTight(t *testing.T) {
	for _, tc := range evidenceRangeCases {
		if !tc.tight {
			continue
		}
		ev, _ := evidenceFor(t, tc)
		if ev.Lo != tc.trueLo || ev.Hi != tc.trueHi {
			t.Errorf("%s: want Int[%d,%d], got %s", tc.tag, tc.trueLo, tc.trueHi, ev.ValueType())
		}
	}
}

var reValueType = regexp.MustCompile(`^Int(-?[0-9]+|\[-?[0-9]+,-?[0-9]+\])$`)

// The value-type column is always a real interval — never a bucket/byte span
// (`Int[1,8]` from the profiler's `1..8` magnitude class) — and the width can
// hold the range it is paired with.
func TestEvidenceValueTypeShape(t *testing.T) {
	for _, tc := range evidenceRangeCases {
		_, out := evidenceFor(t, tc)
		for _, ev := range out.Evidence {
			if !reValueType.MatchString(ev.ValueType()) {
				t.Errorf("%s: %s is not an interval", tc.tag, ev.ValueType())
			}
			if ev.Lo > ev.Hi {
				t.Errorf("%s: %s inverted range", tc.tag, ev.Name)
			}
			if ev.Width == WidthNone {
				t.Errorf("%s: %s has evidence but no storage width", tc.tag, ev.Name)
			}
			if ev.Width == WidthI32 && !fitsI32(ev.Lo, ev.Hi) {
				t.Errorf("%s: %s: int cannot hold %s", tc.tag, ev.Name, ev.ValueType())
			}
		}
	}
}

// Single source of truth: the emitted declaration and the evidence must come
// from the same proof, in both output modes. This is what stops the type and
// the "why" column from drifting apart.
func TestEvidenceMatchesDeclarations(t *testing.T) {
	for _, tc := range evidenceRangeCases {
		for _, mode := range []Mode{ModePy, ModePyx} {
			out, err := AnnotateCython(tc.src, Options{Level: OptFull, Mode: mode})
			if err != nil {
				t.Fatalf("%s/%v: %v", tc.tag, mode, err)
			}
			decl := declaredWidths(t, out.Source, mode)
			typed := map[string]bool{}
			for _, n := range out.Typed {
				typed[n] = true
				ev, ok := out.EvidenceFor(n)
				if !ok {
					t.Errorf("%s/%v: typed %q has no evidence", tc.tag, mode, n)
					continue
				}
				w, ok := decl[n]
				if !ok {
					t.Errorf("%s/%v: typed %q has no declaration", tc.tag, mode, n)
					continue
				}
				if w != ev.Width {
					t.Errorf("%s/%v: %q declared %v but evidence says %v", tc.tag, mode, n, w, ev.Width)
				}
			}
			for n := range decl {
				if !typed[n] {
					t.Errorf("%s/%v: %q declared but not typed", tc.tag, mode, n)
				}
			}
		}
	}
}

// declaredWidths parses the C storage width each name is declared with, for
// both `cdef int a` (.pyx) and `cython.declare(a=cython.int, ...)` (.py).
func declaredWidths(t *testing.T, src string, mode Mode) map[string]Width {
	t.Helper()
	out := map[string]Width{}
	add := func(names string, w Width) {
		for _, n := range strings.Split(names, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out[n] = w
			}
		}
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if mode == ModePyx {
			if rest, ok := strings.CutPrefix(line, "cdef int "); ok {
				add(rest, WidthI32)
			} else if rest, ok := strings.CutPrefix(line, "cdef long long "); ok {
				add(rest, WidthI64)
			}
			continue
		}
		rest, ok := strings.CutPrefix(line, "cython.declare(")
		if !ok {
			continue
		}
		for _, part := range strings.Split(strings.TrimSuffix(rest, ")"), ",") {
			name, typ, found := strings.Cut(strings.TrimSpace(part), "=")
			if !found {
				continue
			}
			switch typ {
			case "cython.int":
				add(name, WidthI32)
			case "cython.longlong":
				add(name, WidthI64)
			}
		}
	}
	return out
}
