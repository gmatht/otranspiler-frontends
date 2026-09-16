// globalveto_test.go — nested `global` writes are module writes.
//
// The module proof cannot see into function bodies (interval env and
// width computation stop at scope boundaries), so a module declaration
// must be vetoed when a nested `global` write escapes it: declaring
// narrow while the nested write is wide truncates (globig prints 0) or
// mistypes (fortarget prints 2.0). Narrow nested writes keep working.
package pylib

import (
	"testing"
)

func TestGlobalWriteVeto(t *testing.T) {
	full := Options{Level: OptFull}
	typed := func(out *CythonOutput, n string) bool {
		for _, x := range out.Typed {
			if x == n {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name string
		src  string
		keep string // must stay declared (precision pins)
		drop string // must be refused (soundness pins)
	}{
		{"bigint escapes", "x = 0\ndef f():\n    global x\n    x = 2**100\nf()\nprint(x)\n", "", "x"},
		{"small escapes kept", "x = 0\ndef f():\n    global x\n    x = 5\nf()\nprint(x)\n", "x", ""},
		{"for ints over float", "x = 0.5\ndef f():\n    global x\n    for x in [1, 2]:\n        pass\nf()\nprint(x)\n", "", "x"},
		{"for bigint over int", "x = 0\ndef f():\n    global x\n    for x in [2**100]:\n        pass\nf()\nprint(x)\n", "", "x"},
		{"for range kept", "x = 0\ndef f():\n    global x\n    for x in range(10):\n        pass\nf()\nprint(x)\n", "x", ""},
		{"float over int escapes", "x = 0\ndef f():\n    global x\n    x = 1.5\nf()\nprint(x)\n", "", "x"},
		{"module for over float", "x = 0.5\nfor x in [1, 2]:\n    pass\nprint(x)\n", "", "x"},
		{"module for range kept", "x = 0\nfor x in range(10):\n    pass\nprint(x)\n", "x", ""},
		{"local shadows kept", "x = 0\ndef f():\n    x = 2**100\n    return x\nprint(x)\n", "x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := AnnotateCython(tc.src, full)
			if err != nil {
				t.Fatalf("annotate: %v", err)
			}
			if tc.drop != "" && typed(out, tc.drop) {
				t.Errorf("%s still declared (miscompile)", tc.drop)
			}
			if tc.keep != "" && !typed(out, tc.keep) {
				t.Errorf("%s refused (over-conservative)", tc.keep)
			}
		})
	}
}
