// powershell-sh-go: PowerShell (.ps1) source -> A1 shIR JSON.
//
// The A1 contract (sh2perl/src/shir_json.rs) is the source of truth:
// the emitted JSON must be byte-identical to the core frontend's output
// for the equivalent program (sorted keys, same node shapes, same
// purity verdicts). The parser is wharflab/tree-sitter-powershell
// (vendored under grammars/, loaded via smacker/go-tree-sitter cgo) —
// PLAN_POWERSHELL_F.md §2. REFUSE > GUESS: anything outside the v1
// subset errors loudly (see refuse.go) instead of miscompiling; the
// worker lands one pinned testdata construct at a time.
package ps1lib

import (
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	psgrammar "github.com/gmatht/sh2loop/frontends/powershell-sh-go/grammars/tree-sitter-powershell/bindings/go"
)

var (
	langOnce sync.Once
	tsLang   *sitter.Language
)

// tsLanguage lazily loads the vendored PowerShell grammar (cgo; the
// binding compiles src/parser.c + src/scanner.c into the binary).
func tsLanguage() *sitter.Language {
	langOnce.Do(func() {
		tsLang = sitter.NewLanguage(psgrammar.Language())
	})
	return tsLang
}

// stripComments — remove `#` line comments so a comment-only source is
// recognized as empty. (A `#` inside a string is untouched — the real
// lexing happens in tree-sitter; this fast path only recognizes the
// comment-only case, mirroring the original stub.)
func stripComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			out.WriteString(line[:i])
		} else {
			out.WriteString(line)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// Shir — the frontend entry: parse the source, lower the v1 subset to
// A1 shIR, emit byte-identical JSON. Anything unlisted REFUSES loudly.
func Shir(src string) (out []byte, err error) {
	// Comment/whitespace-only sources are a valid EMPTY A1 program.
	if strings.TrimSpace(stripComments(src)) == "" {
		return emitProgram(nil)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(tsLanguage())
	tree := parser.Parse(nil, []byte(src))
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, fmt.Errorf("REFUSE: parse error%s", firstErrorDetail(root, []byte(src)))
	}
	stmts, err := lowerProgram(root, []byte(src))
	if err != nil {
		return nil, err
	}
	return emitProgram(stmts)
}

// DumpCST — print the tree-sitter CST (developer aid; wired to the
// CLI's --cst flag for pinning new constructs).
func DumpCST(src string) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(tsLanguage())
	tree := parser.Parse(nil, []byte(src))
	defer tree.Close()
	dumpNode(tree.RootNode(), []byte(src), 0)
}

func dumpNode(n *sitter.Node, src []byte, depth int) {
	pad := ""
	for i := 0; i < depth; i++ {
		pad += "  "
	}
	fmt.Printf("%s%s %q\n", pad, n.Type(), n.Content(src))
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		field := n.FieldNameForChild(i)
		if field != "" {
			fmt.Printf("%s  [field %s]\n", pad, field)
		}
		dumpNode(c, src, depth+1)
	}
}

// firstErrorDetail — a human hint pointing at the first ERROR node so a
// refused source names what the grammar could not parse.
func firstErrorDetail(root *sitter.Node, src []byte) string {
	var n *sitter.Node
	var find func(*sitter.Node) bool
	find = func(x *sitter.Node) bool {
		if x.IsError() {
			n = x
			return true
		}
		for i := 0; i < int(x.NamedChildCount()); i++ {
			if find(x.NamedChild(i)) {
				return true
			}
		}
		return false
	}
	find(root)
	if n == nil {
		return ""
	}
	return fmt.Sprintf(" near %q", n.Content(src))
}
