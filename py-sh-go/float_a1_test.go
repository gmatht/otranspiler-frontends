// float_a1_test.go — Float(64) verdicts in the A1 shIR contract.
//
// Python floats are IEEE doubles. The frontend records that fact as
// {"kind":"Float","width":64} (IrType::Float(64)) in var_types —
// additive: the statement lowering is byte-identical with or without
// the verdict (see TestFloatStmtsUnchanged), and backends that ignore
// the annotation render exactly as before.
package pylib

import (
	"encoding/json"
	"testing"
)

func shirVarTypes(t *testing.T, src string) map[string]any {
	t.Helper()
	out, err := Shir(src)
	if err != nil {
		t.Fatalf("Shir(%q): %v", src, err)
	}
	var prog map[string]any
	if err := json.Unmarshal(out, &prog); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := map[string]any{}
	for _, vt := range prog["var_types"].([]any) {
		e := vt.(map[string]any)
		m[e["name"].(string)] = e["type"]
	}
	return m
}

func isFloat64(v any) bool {
	o, ok := v.(map[string]any)
	if !ok {
		return false
	}
	w, ok := o["width"].(float64)
	return o["kind"] == "Float" && ok && w == 64
}

func TestFloatLiteralIsFloat64(t *testing.T) {
	vt := shirVarTypes(t, "x = 1.5\nprint(x)\n")
	if !isFloat64(vt["x"]) {
		t.Errorf("x = 1.5 must be Float(64), got %v", vt["x"])
	}
}

func TestFloatPropagatesThroughVarsAndTrueDiv(t *testing.T) {
	vt := shirVarTypes(t, "x = 1.5\nq = 7 / 2\ny = x + q\nprint(y)\n")
	for _, n := range []string{"x", "q", "y"} {
		if !isFloat64(vt[n]) {
			t.Errorf("%s must be Float(64), got %v", n, vt[n])
		}
	}
}

func TestIntIdiomStaysInt(t *testing.T) {
	// int(n ** 0.5) is int-valued (the isqrt idiom): the float it is
	// computed through must not leak into the verdict.
	vt := shirVarTypes(t, "n = 12\nr = int(n ** 0.5)\nprint(r)\n")
	if vt["n"] != "Int" {
		t.Errorf("n must stay Int, got %v", vt["n"])
	}
	if vt["r"] != "Int" {
		t.Errorf("int(n ** 0.5) must stay Int, got %v", vt["r"])
	}
	// the t86 loop shape end to end
	vt = shirVarTypes(t, "def all_factors(n):\n    for i in range(1, int(n**0.5) + 1):\n        print(i)\n")
	if vt["i"] != "Int" {
		t.Errorf("range counter must stay Int, got %v", vt["i"])
	}
}

func TestFloatAugAssignStaysFloat(t *testing.T) {
	vt := shirVarTypes(t, "x = 1.5\nx += 1\nprint(x)\n")
	if !isFloat64(vt["x"]) {
		t.Errorf("x += 1 on a float must stay Float(64), got %v", vt["x"])
	}
}

func TestIntStaysInt(t *testing.T) {
	vt := shirVarTypes(t, "i = 0\ns = i + 41\nprint(s)\n")
	if vt["i"] != "Int" || vt["s"] != "Int" {
		t.Errorf("ints must stay Int, got %v", vt)
	}
}

func TestFloatStmtsUnchanged(t *testing.T) {
	// The verdict is additive: statement lowering for float programs
	// is exactly the legacy shape (arith-string for literals, structured
	// Arith otherwise) — only var_types gains the Float object.
	out, err := Shir("x = 1.5\nq = 7 / 2\ny = x + q\nprint(y)\n")
	if err != nil {
		t.Fatal(err)
	}
	var prog map[string]any
	if err := json.Unmarshal(out, &prog); err != nil {
		t.Fatal(err)
	}
	stmts := prog["stmts"].([]any)
	if len(stmts) != 4 {
		t.Fatalf("want 4 stmts, got %d", len(stmts))
	}
	asg := stmts[0].(map[string]any)
	if asg["expr"].(map[string]any)["func"] != "arith" {
		t.Errorf("x = 1.5 must stay an arith-string call, got %v", asg["expr"])
	}
	for _, i := range []int{1, 2} {
		e := stmts[i].(map[string]any)["expr"].(map[string]any)
		if e["type"] != "Arith" {
			t.Errorf("stmt %d must stay structured Arith, got %v", i, e["type"])
		}
	}
}
