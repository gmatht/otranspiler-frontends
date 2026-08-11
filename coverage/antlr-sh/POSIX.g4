// POSIX.g4 — ANTLR4 grammar for a POSIX-shell subset, written for the
// parser-coverage test (frontends/coverage/). This is the grammar the
// ANTLR plan (plan-antlr-go.md §"POSIX sh / bash": "None in
// grammars-v4... A custom .g4 informed by the POSIX shell grammar is
// the right call") called for but never got: the repo's
// frontends/posix-sh-go/grammars/POSIX.g4 is a 14-line placeholder
// stub. This file is the MEASUREMENT TOOL for "how far would an
// ANTLR4 POSIX parser get on the corpus", not a production parser.
//
// Subset + approximations (documented — this is a coverage instrument):
//   - lexical: comments (only outside words — `#` is not a word char,
//     so `echo foo#bar` degenerates to `echo foo`), single/double
//     quotes (single-line), $(...) command substitution + $((...))
//     arithmetic as one balanced token, ${...} parameter expansion as
//     one balanced token, backticks (single-line), no heredoc lexer
//     modes (`<< WORD` parses; the body lines then parse as commands —
//     an over-approximation)
//   - keywords (if/then/elif/else/fi/while/until/do/done/for/in/case/
//     esac/function) are always keywords, so keyword-as-argument
//     (`echo if`) is a parse error — a context-free-lexer limitation
//   - grammar: full POSIX structure — pipelines (incl. `!`), and/or
//     lists, simple commands with prefix/suffix assignments +
//     redirections, if/elif/else, while/until, for-in, case, functions
//     (`name()` and `function name`), subshells, brace groups
//   - bashisms not modeled: `[[ ]]` (parses as a plain command — word
//     chars include brackets), C-style `for ((;;))`, `select`, arrays
//     `a=(...)`, `&>`/`>>&`, `;;&`, process substitution `<(...)`,
//     `$'...'` ANSI-C quotes (degenerates to `$` + quote)
//
// bash -n is the oracle for "what the corpus actually is": the corpus
// is bash, this grammar is POSIX-subset, so the coverage delta is the
// real, measured reason the fleet uses hand-rolled parsers instead.

grammar POSIX;

// ── keywords — lexed before WORD so they win at equal length ──
IF: 'if';
THEN: 'then';
ELIF: 'elif';
ELSE: 'else';
FI: 'fi';
WHILE: 'while';
UNTIL: 'until';
DO: 'do';
DONE: 'done';
FOR: 'for';
IN: 'in';
CASE: 'case';
ESAC: 'esac';
FUNCTION: 'function';

// ── operators ──
AND_IF: '&&';
OR_IF: '||';
PIPE: '|';
SEMI: ';';
AMP: '&';
LPAREN: '(';
RPAREN: ')';
LBRACE: '{';
RBRACE: '}';
GREAT: '>';
DGREAT: '>>';
LESS: '<';
DLESS: '<<';
LESSAND: '<&';
GREATAND: '>&';
DLESSDASH: '<<-';
ASSIGN: '=';
BANG: '!';

WS: [ \t]+ -> skip;
NEWLINE: '\r'? '\n' [ \t]*;
COMMENT: '#' ~[\r\n]* -> skip;

SQUOTE: '\'' ~['\r\n]* '\'';
DQUOTE: '"' ( ~["`\\\r\n] | '\\' . )* '"';
CMDSUB: '$(' CMDSUB_INNER ')';
fragment CMDSUB_INNER: ( ~[()] | '(' CMDSUB_INNER ')' )*;
PARAM: '${' PARAM_INNER '}';
fragment PARAM_INNER: ( ~[{}] | '{' PARAM_INNER '}' )*;
BTICK: '`' ~[`\r\n]* '`';

WORD: ~[ \t\r\n&;|(){}<>="'#]+;

// ── parser (POSIX shell grammar shape) ──
start: (complete_command | NEWLINE)* EOF;

complete_command: and_or separator*;

separator: NEWLINE | SEMI | AMP;

and_or: pipeline ( (AND_IF | OR_IF) pipeline )*;

pipeline: BANG? command (PIPE command)*;

command:
    simple_command
  | if_command
  | while_command
  | until_command
  | for_command
  | case_command
  | function_definition
  | subshell
  | brace_group
  ;

simple_command: (assignment | WORD | SQUOTE | DQUOTE | CMDSUB | BTICK | redirect)+;

assignment: WORD ASSIGN (WORD | SQUOTE | DQUOTE | CMDSUB | BTICK | PARAM)*;

redirect: (GREAT | DGREAT | LESS | DLESS | LESSAND | GREATAND | DLESSDASH) WORD;

if_command: IF compound_list THEN compound_list (ELIF compound_list THEN compound_list)* (ELSE compound_list)? FI;
while_command: WHILE compound_list DO compound_list DONE;
until_command: UNTIL compound_list DO compound_list DONE;
for_command: FOR WORD (IN word_list)? separator* DO compound_list DONE;
case_command: CASE WORD IN case_item* ESAC;
case_item: LPAREN? pattern RPAREN compound_list? (SEMI SEMI | SEMI AMP | SEMI SEMI AMP)?;
pattern: (WORD | SQUOTE | DQUOTE) (PIPE (WORD | SQUOTE | DQUOTE))*;

function_definition:
    WORD LPAREN RPAREN separator* brace_group
  | FUNCTION WORD separator* brace_group
  ;

subshell: LPAREN compound_list RPAREN;
brace_group: LBRACE compound_list RBRACE;

compound_list: separator* and_or separator*;
word_list: (WORD | SQUOTE | DQUOTE | CMDSUB | BTICK | PARAM)+;
