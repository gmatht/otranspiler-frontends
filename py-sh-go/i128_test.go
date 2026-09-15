// i128_test.go — magnitude proof + classifier gate. Every row pins the
// tier decision at a boundary or a soundness edge; the refusal rows are
// as important as the accept rows (fail-closed).
package pylib

import (
	"strings"
	"testing"
)

func classify(t *testing.T, src string) (i128, u128, longs map[string]bool, mag []MagEvidence) {
	t.Helper()
	tree, errs := ParsePython(src)
	if len(errs) > 0 {
		t.Fatalf("parse %q: %v", src, errs[0])
	}
	return classifyI128(tree)
}

func inSet(m map[string]bool, n string) bool { return m[n] }

func TestI128CounterBigBound(t *testing.T) {
	// 101-bit bound: counter is exactly [0, 2^100] → u128 (nonneg).
	src := "i = 0\nwhile i < 1267650600228229401496703205376:\n    i = i + 1\nprint(i)\n"
	i128, u128, _, mag := classify(t, src)
	if !inSet(u128, "i") {
		t.Errorf("want u128 i, got i128=%v u128=%v mag=%v", i128, u128, mag)
	}
	if inSet(i128, "i") {
		t.Errorf("i must not be signed i128: %v", mag)
	}
}

func TestI128CounterSigned(t *testing.T) {
	// Counter from a negative init can go negative → signed i128.
	src := "i = -5\nwhile i < 1267650600228229401496703205376:\n    i = i + 1\nprint(i)\n"
	i128, u128, _, _ := classify(t, src)
	if !inSet(i128, "i") || inSet(u128, "i") {
		t.Errorf("want signed i128 i, got i128=%v u128=%v", i128, u128)
	}
}

func TestI128LiteralAssign(t *testing.T) {
	// 2**100 needs 101 bits, nonneg → u128. (2**128-1 → u128; 2**128 → refuse.)
	for _, tc := range []struct {
		lit      string
		wantU128 bool
	}{
		{"1267650600228229401496703205376", true},   // 2**100
		{"340282366920938463463374607431768211455", true}, // 2**128-1
		{"340282366920938463463374607431768211456", false}, // 2**128: refuse
		{"-5", false}, // fits i64: single owner stays canonical there
	} {
		src := "x = " + tc.lit + "\nprint(x)\n"
		_, u128, _, mag := classify(t, src)
		if inSet(u128, "x") != tc.wantU128 {
			t.Errorf("lit %s: wantU128=%v got u128=%v mag=%v", tc.lit, tc.wantU128, u128, mag)
		}
	}
}

func TestI128LoopCarriedGrowthRefuses(t *testing.T) {
	// x grows without bound in the loop (10^66 iterations): must NOT be
	// i128/u128 (needs GMP). The counter i is likewise unbounded (220-bit
	// bound) — refused by both counter rules.
	src := "x = 2 ** 100\ni = 0\nwhile i < 1000000000000000000000000000000000000000000000000000000000000000000:\n    x = x * 3\n    i = i + 1\nprint(x % 1000000007)\n"
	i128, u128, _, _ := classify(t, src)
	if inSet(i128, "x") || inSet(u128, "x") {
		t.Errorf("unbounded grower x must refuse: i128=%v u128=%v", i128, u128)
	}
	if inSet(i128, "i") || inSet(u128, "i") {
		t.Errorf("220-bit counter i must refuse: i128=%v u128=%v", i128, u128)
	}
}

func TestI128StraightArithmetic(t *testing.T) {
	// Straight-line exact intervals: `y = x + 1` with x u128 stays u128.
	src := "x = 1267650600228229401496703205376\ny = x + 1\nprint(y)\n"
	_, u128, _, _ := classify(t, src)
	if !inSet(u128, "x") || !inSet(u128, "y") {
		t.Errorf("want u128 x,y: u128=%v", u128)
	}
}

func TestI128DivZeroRefuses(t *testing.T) {
	src := "x = 1267650600228229401496703205376\ny = x % 0\nprint(y)\n"
	_, u128, _, _ := classify(t, src)
	if inSet(u128, "y") {
		t.Errorf("div-by-zero must refuse: u128=%v", u128)
	}
}

func TestI128UseBeforeDefHull(t *testing.T) {
	// `print(x); x = <101-bit lit>`: the classifier unions every assigned
	// value (hull), so x IS proven — use-before-def safety lives in the
	// RENDERER (dominance: reads before the proving assign decline; a C
	// var would otherwise read garbage where Python raises NameError).
	// Skipping the proof instead would leave STALE narrow proofs when a
	// later reassign grows the value (unsound tiering) — union is the
	// sound direction here.
	src := "print(x)\nx = 1267650600228229401496703205376\n"
	_, u128, _, mag := classify(t, src)
	if !inSet(u128, "x") {
		t.Errorf("union must prove x: u128=%v mag=%v", u128, mag)
	}
}

func TestI128MagEvidenceConsistent(t *testing.T) {
	// Every classified var has exactly one mag row with real decimal
	// bounds; rows never render as width buckets.
	src := "x = 1267650600228229401496703205376\ni = 0\nwhile i < 1000:\n    i = i + 1\nprint(x)\nprint(i)\n"
	i128, u128, _, mag := classify(t, src)
	seen := map[string]int{}
	for _, ev := range mag {
		seen[ev.Name]++
		if !strings.Contains(ev.ValueType(), "[") || !strings.Contains(ev.ValueType(), "]") {
			t.Errorf("%s: mag row is not a real interval: %s", ev.Name, ev.ValueType())
		}
	}
	for n := range i128 {
		if seen[n] != 1 {
			t.Errorf("i128 %s has %d mag rows", n, seen[n])
		}
	}
	for n := range u128 {
		if seen[n] != 1 {
			t.Errorf("u128 %s has %d mag rows", n, seen[n])
		}
	}
	if !inSet(u128, "x") {
		t.Errorf("want u128 x: u128=%v mag=%v", u128, mag)
	}
}
