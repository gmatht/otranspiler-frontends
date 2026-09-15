package gen

import (
	"regexp"

	"github.com/antlr4-go/antlr/v4"
)

type Python3LexerBase struct {
	*antlr.BaseLexer
	tokens     []antlr.Token
	indents    []int
	Opened     int
	lastToken  antlr.Token
	eofStarted bool
}

// Emit queues the automatically emitted token (the Go runtime returns one
// token per NextToken call; the lexer must be able to hand back several —
// NEWLINE + INDENT/DEDENT — so everything emitted is queued in order).
func (l *Python3LexerBase) Emit() antlr.Token {
	t := l.BaseLexer.Emit()
	l.tokens = append(l.tokens, t)
	return t
}

func (l *Python3LexerBase) EmitToken(t antlr.Token) {
	l.BaseLexer.EmitToken(t)
	l.tokens = append(l.tokens, t)
}

func (l *Python3LexerBase) MakeCommonToken(ttype int, text string) antlr.Token {
	stop := l.TokenStartCharIndex - 1
	start := stop
	if len(text) != 0 {
		start = stop - len(text) + 1
	}
	ctf := l.GetTokenFactory()
	t := ctf.Create(
		l.GetTokenSourceCharStreamPair(),
		ttype,
		text,
		antlr.TokenDefaultChannel,
		start,
		l.TokenStartCharIndex-1,
		l.TokenStartLine,
		l.TokenStartColumn)
	return t
}

func (l *Python3LexerBase) CreateDedent() antlr.Token {
	dedent := l.MakeCommonToken(Python3ParserDEDENT, "")
	//dedent.Line = LastToken.GetLine()
	return dedent
}

func (l *Python3LexerBase) NextToken() antlr.Token {
	for len(l.tokens) == 0 {
		t := l.BaseLexer.NextToken()
		if t.GetTokenType() == antlr.TokenEOF {
			// CPython's tokenizer synthesises a NEWLINE at end of input when
			// the last statement was not newline-terminated (a file may end
			// without `\n`, or with trailing whitespace), then one DEDENT per
			// open indent, then EOF. Without this, `x = 1` (no trailing
			// newline) fails with "expecting NEWLINE".
			l.emitEOF()
			continue
		}
		// Emit()/EmitToken() already queue the token; only the runtime's own
		// EOF path bypasses them, so queue t when it did not land.
		if len(l.tokens) == 0 || l.tokens[len(l.tokens)-1] != t {
			l.tokens = append(l.tokens, t)
		}
	}
	x := l.tokens[0]
	l.tokens = l.tokens[1:]
	if x.GetChannel() == antlr.TokenDefaultChannel {
		l.lastToken = x
	}
	return x
}

// emitEOF queues the end-of-input sequence (NEWLINE? DEDENT* EOF).
func (l *Python3LexerBase) emitEOF() {
	if !l.eofStarted {
		if l.lastToken != nil && !isStmtTerminator(l.lastToken.GetTokenType()) {
			l.EmitToken(l.MakeCommonToken(Python3ParserNEWLINE, "\n"))
		}
		for len(l.indents) != 0 {
			l.EmitToken(l.CreateDedent())
			l.indents = l.indents[:len(l.indents)-1]
		}
		l.eofStarted = true
	}
	l.EmitToken(l.MakeCommonToken(antlr.TokenEOF, "<EOF>"))
}

func isStmtTerminator(tt int) bool {
	switch tt {
	case Python3ParserNEWLINE, Python3ParserINDENT, Python3ParserDEDENT:
		return true
	}
	return false
}

func (l *Python3LexerBase) GetIndentationCount(spaces string) int {
	count := 0
	for _, ch := range spaces {
		if ch == '\t' {
			count += 8 - (count % 8)
		} else {
			count += 1
		}
	}
	return count
}

func (l *Python3LexerBase) atStartOfInput() bool {
	return l.TokenStartColumn == 0 && l.TokenStartLine == 1
}

func (l *Python3LexerBase) openBrace() {
	l.Opened++
}

func (l *Python3LexerBase) closeBrace() {
	l.Opened--
}

func (l *Python3LexerBase) onNewLine() {
	newLineRegex := regexp.MustCompile(`[^\r\n\f]+`)
	newLine := newLineRegex.ReplaceAllString(l.GetText(), "")
	spacesRegex := regexp.MustCompile(`[\r\n\f]+`)
	spaces := spacesRegex.ReplaceAllString(l.GetText(), "")
	next := l.GetInputStream().LA(1)
	nextnext := l.GetInputStream().LA(2)
	if l.Opened > 0 || (nextnext != -1 && (next == '\r' || next == '\n' || next == '\f' || next == '#')) {
		l.Skip()
	} else {
		l.EmitToken(l.MakeCommonToken(Python3LexerNEWLINE, newLine))
		indent := l.GetIndentationCount(spaces)
		previous := 0
		if len(l.indents) == 0 {
			previous = 0
		} else {
			previous = l.indents[len(l.indents)-1]
		}
		if indent == previous {
			l.Skip()
		} else if indent > previous {
			l.indents = append(l.indents, indent)
			l.EmitToken(l.MakeCommonToken(Python3ParserINDENT, spaces))
		} else {
			for len(l.indents) != 0 && l.indents[len(l.indents)-1] > indent {
				l.EmitToken(l.CreateDedent())
				l.indents = l.indents[:len(l.indents)-1]
			}
		}
	}
}

func (l *Python3LexerBase) Reset() {
	l.tokens = make([]antlr.Token, 0)
	l.indents = make([]int, 0)
	l.Opened = 0
	l.eofStarted = false
	//l.lastToken = null
	l.BaseLexer.Reset()
}
