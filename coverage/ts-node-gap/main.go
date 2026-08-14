// ts-node-gap — tree-sitter NODE-TYPE coverage for the tree-sitter-backed
// languages (c, cpp, powershell).
//
// The EXTERNAL TRUTH: node types the grammar DEFINES (node-types.json)
// minus node types the testdata parse trees EXERCISE. The frontends' own
// parsers are tree-sitter (cpp, powershell) or hand-rolled (c) — either
// way the GRAMMAR is the source of truth for "do the examples cover the
// language's parser". Prints "ts node <type>" per gap (named types only).
//
// Usage: ts-node-gap <lang> <testdata-dir> <node-types.json> <ext>
//   lang: c-sh-go | cpp-sh-go | powershell-sh-go
// Exit 0 with empty output where no grammar/inventory applies.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
)

/*
#cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/../../powershell-sh-go/grammars/tree-sitter-powershell/src
#include "../../powershell-sh-go/grammars/tree-sitter-powershell/src/parser.c"
#include "../../powershell-sh-go/grammars/tree-sitter-powershell/src/scanner.c"
*/
import "C"

import "unsafe"

func powershellLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_powershell()))
}

type nodeType struct {
	Type     string     `json:"type"`
	Named    bool       `json:"named"`
	Subtypes []nodeType `json:"subtypes"`
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: ts-node-gap <lang> <testdata-dir> <node-types.json> <ext>")
		os.Exit(2)
	}
	lang, td, invPath, ext := os.Args[1], os.Args[2], os.Args[3], os.Args[4]

	var grammar *sitter.Language
	switch lang {
	case "c-sh-go":
		grammar = c.GetLanguage()
	case "cpp-sh-go":
		grammar = cpp.GetLanguage()
	case "powershell-sh-go":
		grammar = powershellLanguage()
	default:
		os.Exit(0) // not a tree-sitter frontend — no ts inventory
	}

	// defined set: named node types from the grammar's inventory
	raw, err := os.ReadFile(invPath)
	if err != nil {
		os.Exit(0) // no inventory -> report no gaps (worker falls back)
	}
	var types []nodeType
	if err := json.Unmarshal(raw, &types); err != nil {
		os.Exit(0)
	}
	defined := map[string]bool{}
	for _, t := range types {
		if t.Named && len(t.Subtypes) == 0 {
			defined[t.Type] = true
		}
	}

	// exercised set: node types in the testdata parse trees
	used := map[string]bool{}
	parser := sitter.NewParser()
	parser.SetLanguage(grammar)
	files, _ := filepath.Glob(filepath.Join(td, "*."+ext))
	for _, f := range files {
		bn := filepath.Base(f)
		if strings.Contains(bn, "_refuse") || strings.Contains(bn, "_gap") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		tree := parser.Parse(nil, src)
		if tree == nil {
			continue
		}
		collect(tree.RootNode(), used)
	}

	var gaps []string
	for t := range defined {
		if !used[t] {
			gaps = append(gaps, t)
		}
	}
	sort.Strings(gaps)
	for _, g := range gaps {
		fmt.Println("ts node " + g)
	}
}

func collect(n *sitter.Node, used map[string]bool) {
	if n == nil {
		return
	}
	if n.IsNamed() {
		used[n.Type()] = true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		collect(n.Child(i), used)
	}
}
