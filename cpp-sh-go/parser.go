// parser.go — tree-sitter-cpp is THE cpp frontend's parser (CPP_PLAN
// §1–§2). It parses the WHOLE file (GLR error tolerance), and this
// walker refuses any NAMED node outside the expressible whitelist — a
// refusal reads as a decision (node kind + line), never a lexer choke
// on an unknown keyword. The tokenizer in main.go is demoted to C-text
// reconstruction for clib (string/char/comment/preprocessor-safe);
// the desugar (bool/new/delete) still runs there. The whitelist is the
// contract: empirical base = the named kinds in the 6 positive
// testdata files, plus the explicitly-expressible additions below.
// Refuse > guess: anything not listed refuses at the node level.

package cppshgo

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"
)

// allowedKinds — the expressible C++ surface (what clib lowers after
// the desugar). Keep in sync with testdata_cpp/ + FRONTEND.md.
var allowedKinds = map[string]bool{
	// structure
	"translation_unit": true, "comment": true,
	// preprocessor (dropped for clib, like the tokenizer's behavior)
	"preproc_include": true, "preproc_call": true, "preproc_def": true,
	"preproc_if": true, "preproc_ifdef": true, "preproc_defined": true,
	"preproc_elif": true, "preproc_else": true, "preproc_arg": true, "preproc_function_def": true,
	"preproc_params": true, "preproc_directive": true,
	"system_lib_string": true, "string_content": true, "escape_sequence": true,
	// declarations
	"declaration": true, "init_declarator": true, "function_definition": true,
	"declaration_list": true, "field_declaration": true,
	"field_declaration_list": true, "field_identifier": true,
	// types
	"primitive_type": true, "sized_type_specifier": true, "type_identifier": true,
	"struct_specifier": true, "pointer_declarator": true, "array_declarator": true,
	"function_declarator": true, "parameter_list": true, "parameter_declaration": true,
	"parenthesized_declarator": true, "abstract_pointer_declarator": true,
	"type_qualifier": true, "storage_class_specifier": true,
	// statements
	"compound_statement": true, "expression_statement": true, "if_statement": true,
	"else_clause": true, "while_statement": true, "for_statement": true,
	"do_statement": true, "return_statement": true, "break_statement": true,
	"continue_statement": true, "switch_statement": true, "case_statement": true,
	"default": true, "labeled_statement": true, "goto_statement": true,
	"statement_identifier": true, "condition_clause": true, "init_statement": true,
	// expressions
	"identifier": true, "call_expression": true, "argument_list": true,
	"binary_expression": true, "assignment_expression": true,
	"update_expression": true, "unary_expression": true,
	"parenthesized_expression": true, "subscript_expression": true,
	"subscript_argument_list": true, "field_expression": true,
	"pointer_expression": true, "cast_expression": true,
	"sizeof_expression": true, "conditional_expression": true,
	"char_literal": true, "number_literal": true, "string_literal": true,
	"concatenated_string": true, "true": true, "false": true, "null": true, // `null` is the tree-sitter-c node for nullptr (and NULL); the desugar maps nullptr → 0 (FRONTEND.md)
	// the C++-surface expressible set (desugar targets)
	"new_expression": true, "delete_expression": true, "type_descriptor": true,
	"new_declarator": true, "initializer_list": true, "field_initializer": true,
	"field_initializer_list": true,
}

// treeCheck parses src with tree-sitter-cpp and refuses the first NAMED
// node outside the whitelist (or a syntax-error node). The whole file
// is parsed first — tree-sitter's error tolerance is what makes
// node-level refusal honest instead of lexer-level choking.
func treeCheck(src string) error {
	parser := sitter.NewParser()
	parser.SetLanguage(cpp.GetLanguage())
	tree := parser.Parse(nil, []byte(src))
	return checkNode(tree.RootNode(), []byte(src))
}

func checkNode(n *sitter.Node, src []byte) error {
	if n.IsError() {
		return fmt.Errorf("unsupported C++: syntax error at line %d", n.StartPoint().Row+1)
	}
	if n.IsNamed() && !allowedKinds[n.Type()] {
		// member access via pointer: `->` refuses, `.` on structs is
		// expressible — the operator is an anonymous child of
		// field_expression, so check it there.
		if n.Type() == "field_expression" {
			for i := 0; i < int(n.ChildCount()); i++ {
				if n.Child(i).Type() == "->" {
					return fmt.Errorf("unsupported C++: member access via pointer (->) at line %d (use . on structs)", n.StartPoint().Row+1)
				}
			}
		}
		return fmt.Errorf("unsupported C++: %s at line %d", n.Type(), n.StartPoint().Row+1)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if err := checkNode(n.NamedChild(i), src); err != nil {
			return err
		}
	}
	return nil
}
