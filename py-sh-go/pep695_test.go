// pep695_test.go — PEP 695 lowering tests.
//
// The table pins the rewrite; the decline cases pin every guard. Each
// accepted case is also verified to parse with the grammar (the
// AnnotateCython hook relies on that), and the runtime cases are
// covered by testdata/t103_pep695.py (parity gate) plus the semantics
// oracles in coverage/semantics-parity.sh.
package pylib

import (
	"strings"
	"testing"
)

func TestPEP695Lower(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string // substrings of the lowered output
	}{
		{"plain def", "def f[T](x: T) -> T:\n    return x\n",
			[]string{`T_f = typing.TypeVar("T")`, "def f(x: T_f) -> T_f:"}},
		{"bound", "def f[T: int](x: T):\n    return x\n",
			[]string{`T_f = typing.TypeVar("T", bound=int)`}},
		{"constraints", "def f[T: (int, str)](x: T):\n    return x\n",
			[]string{`T_f = typing.TypeVar("T", int, str)`}},
		{"tuple", "def f[*Ts](x: Ts):\n    return x\n",
			[]string{`Ts_f = typing.TypeVarTuple("Ts")`}},
		{"paramspec", "def f[**P](x: P):\n    return x\n",
			[]string{`P_f = typing.ParamSpec("P")`}},
		{"multi", "def f[T, U: list[T]](x: T, y: U):\n    return x\n",
			[]string{`T_f = typing.TypeVar("T")`, `U_f = typing.TypeVar("U", bound=list[T_f])`}},
		{"class", "class C[T]:\n    pass\n",
			[]string{`T_f = typing.TypeVar("T")`, "class C(typing.Generic[T_f]):"}},
		{"class bases", "class C[T](list[T]):\n    pass\n",
			[]string{"class C(list[T_f], typing.Generic[T_f]):"}},
		{"class meta", "class C[T](metaclass=M):\n    pass\n",
			[]string{"class C(typing.Generic[T_f], metaclass=M):"}},
		{"alias", "type X = int\n",
			[]string{"X = int"}},
		{"alias param", "type P[T] = list[T]\n",
			[]string{`T_f = typing.TypeVar("T")`, "P = list[T_f]"}},
		{"alias str", "type X = \"int\"\n",
			[]string{`X = "int"`}},
		{"async", "async def f[T](x: T):\n    return x\n",
			[]string{"async def f(x: T_f):"}},
		{"nested ann", "def o[T](x: T):\n    def g(y: T) -> T:\n        return y\n    return g(x)\n",
			[]string{"def g(y: T_f) -> T_f:"}},
		{"nested default rewrites", "def o[T](x: T):\n    def f[U](y: T):\n        return y\n    return f(x)\n",
			[]string{"def f(y: T_f):"}},
		{"siblings", "def f[T](x: T):\n    return x\ndef g[T](y: T):\n    return y\n",
			[]string{"def f(x: T_f):", "def g(y: T_f2):"}},
		{"deco stays blind", "@deco(T)\ndef f[T](x: T):\n    return x\n",
			[]string{"@deco(T)", "def f(x: T_f):"}},

		{"post read stays", "def f[T](x: T):\n    return x\nprint(T)\n",
			[]string{"print(T)"}},
		{"pre bind stays", "T = 5\ndef f[T](x: T):\n    return x\n",
			[]string{"T = 5", "def f(x: T_f):"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := lowerPEP695(tc.src)
			if !ok {
				t.Fatalf("declined:\n%s", tc.src)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			if _, errs := ParsePython(out); len(errs) > 0 {
				t.Errorf("lowered output does not parse: %v\n%s", errs, out)
			}
		})
	}
}

func TestPEP695Declines(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"body read repr", "def f[T](x: T):\n    return T\n"},
		{"print cell", "def f[T](x: T):\n    print(T)\n"},
		{"nested default owned", "def o[T](x: T):\n    def f[U](y=T):\n        return y\n    return f()\n"},
		{"call arg", "def f[T](x: T):\n    return g(T)\n"},
		{"own default owned", "def f[T](x=T):\n    return x\n"},
		{"underscore param", "def f[_](x):\n    return x\n"},
		{"typing param", "def f[typing](x):\n    return x\n"},
		{"typing shadow", "import typing as t\ntyping = 1\ndef f[T](x: T):\n    return x\n"},
		{"generic dup", "class C[T](typing.Generic[T]):\n    pass\n"},
		{"star import", "from m import *\ndef f[T](x: T):\n    return x\n"},
		{"tuple bound", "def f[*Ts: tuple](x: Ts):\n    return x\n"},
		{"alias call", "type X = int\nprint(X(1))\n"},
		{"alias bare", "type X = int\nprint(X)\n"},
		{"alias fwd", "type X = Later\nclass Later:\n    pass\n"},
		{"alias lambda", "type X = lambda: 1\n"},
		{"alias base", "type X = int\nclass C(X):\n    pass\n"},
		{"alias default", "type X = int\ndef f(a=X):\n    return a\n"},
		{"isinstance plain", "type X = int\nprint(isinstance(5, X))\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, ok := lowerPEP695(tc.src); ok {
				t.Errorf("should decline, lowered to:\n%s", out)
			}
		})
	}
}

func TestPEP695SubscriptRewrite(t *testing.T) {
	src := "type P[T] = list[T]\ndef f(x: P[int]):\n    return x\n"
	out, ok := lowerPEP695(src)
	if !ok {
		t.Fatalf("declined:\n%s", src)
	}
	if !strings.Contains(out, "def f(x: list[int]):") {
		t.Errorf("subscript not substituted:\n%s", out)
	}
	if _, errs := ParsePython(out); len(errs) > 0 {
		t.Errorf("lowered output does not parse: %v", errs)
	}
}

func TestPEP695Noop(t *testing.T) {
	// Files without type parameters are untouched (hook is dead code).
	for _, src := range []string{
		"def f(x):\n    return x\n",
		"x = type(5)\n",
		"type = 5\n",
		"x: type = int\n",
		"print(f\"def f[T](x)\")\n",
		"# def f[T](x):\npass\n",
	} {
		if out, ok := lowerPEP695(src); ok {
			t.Errorf("should not trigger, got:\n%s", out)
		}
	}
}
