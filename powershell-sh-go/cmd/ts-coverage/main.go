// ts-coverage: report tree-sitter-powershell NAMED node types that no
// testdata example exercises. This is the powershell-sh-go mode of
// rules-gap.sh — the vendored grammar (the official parser) is the
// source of truth for "do the examples cover the parser's features",
// the same principle as grammars-v4/PPI for the other frontends.
//
// Usage: ts-coverage <testdata-dir> <node-types.json>
//
// Parses every testdata/*.ps1 (skipping *_refuse* / *_gap* pins) with
// the vendored tree-sitter-powershell grammar, collects the node types
// that appear in the parse trees, and prints the grammar's NAMED node
// types (node-types.json, named=true — tokens and lexer noise are
// excluded) that no example exercises, one per line as
// "ts node <type>". The caller (rules-gap.sh) applies the refused/bugs
// ledger exclusions; the worker's pi judges expressibility (the gate is
// the final arbiter).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	psgrammar "github.com/gmatht/sh2loop/frontends/powershell-sh-go/grammars/tree-sitter-powershell/bindings/go"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ts-coverage <testdata-dir> <node-types.json>")
		os.Exit(2)
	}
	td, ntPath := os.Args[1], os.Args[2]

	lang := sitter.NewLanguage(psgrammar.Language())
	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	seen := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(td, "*.ps1"))
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
		walk(tree.RootNode(), seen)
		tree.Close()
	}

	raw, err := os.ReadFile(ntPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "node-types.json:", err)
		os.Exit(1)
	}
	var types []struct {
		Type  string `json:"type"`
		Named bool   `json:"named"`
	}
	if err := json.Unmarshal(raw, &types); err != nil {
		fmt.Fprintln(os.Stderr, "node-types.json parse:", err)
		os.Exit(1)
	}
	var gaps []string
	for _, t := range types {
		if !t.Named || seen[t.Type] {
			continue
		}
		gaps = append(gaps, "ts node "+t.Type)
	}
	sort.Strings(gaps)
	for _, g := range gaps {
		fmt.Println(g)
	}
}

func walk(n *sitter.Node, seen map[string]bool) {
	if n == nil {
		return
	}
	seen[n.Type()] = true
	for i := uint32(0); i < n.ChildCount(); i++ {
		walk(n.Child(int(i)), seen)
	}
}
