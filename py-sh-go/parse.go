// parse.go — the full-Python ANTLR4 front end.
//
// py-sh-go's lowering path (main.go) is still a hand-rolled recursive
// descent parser for the v1 subset. This file exposes the complete
// Python 3 parse produced by the generated ANTLR grammar
// (gen/ — upstream grammars-v4 plus in-repo additions: CPython's
// namedexpr_test, positional-only `/`, numeric underscores, and
// `case` as an ordinary identifier), so coverage can grow by lowering
// from the real parse tree instead
// of extending a hand lexer. ParsePython returns the tree plus the syntax
// errors; a caller that wants the old behaviour keeps using ShirExact.
package pylib

import (
	"fmt"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// errCollector records every syntax error instead of printing it.
type errCollector struct {
	*antlr.DefaultErrorListener
	msgs []string
}

func (e *errCollector) SyntaxError(_ antlr.Recognizer, _ interface{}, line, column int, msg string, _ antlr.RecognitionException) {
	e.msgs = append(e.msgs, fmt.Sprintf("%d:%d %s", line, column, msg))
}

// ParsePython parses src with the full ANTLR Python3 grammar and returns
// the file_input tree plus any syntax-error messages (empty = accepted).
//
// The grammar carries CPython's `namedexpr_test`, so the walrus operator
// (`(n := f())`, `if n := f():`, `print(x := 1)`, `[y := f(x) for x in it]`)
// parses too — see corpus/walrus.py.
func ParsePython(src string) (antlr.ParseTree, []string) {
	ec := &errCollector{DefaultErrorListener: antlr.NewDefaultErrorListener()}

	lex := gen.NewPython3Lexer(antlr.NewInputStream(src))
	lex.RemoveErrorListeners()
	lex.AddErrorListener(ec)

	toks := antlr.NewCommonTokenStream(lex, antlr.TokenDefaultChannel)
	p := gen.NewPython3Parser(toks)
	p.RemoveErrorListeners()
	p.AddErrorListener(ec)

	tree := p.File_input()
	return tree, ec.msgs
}
