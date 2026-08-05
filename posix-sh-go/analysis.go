// posix-sh-go A2 var-type analysis + A1 JSON emitter.
// Mirrors shir.rs analyze_var_types (numeric_lift_vars / string_lift_vars)
// and shir_json.rs program_json/expr_json/stmt_json.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const contractVersion = 1

// ─────────────────────────────────────────────────────────────────────
// A2: type verdicts (mirror analyze_var_types)
// ─────────────────────────────────────────────────────────────────────

func isPlainIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isPlainIdentChar(s[i]) {
			return false
		}
	}
	return true
}

var reservedVars = map[string]bool{
	"IFS": true, "PATH": true, "HOME": true, "PWD": true, "OLDPWD": true,
	"SHELL": true, "USER": true, "TERM": true, "LANG": true, "LC_ALL": true,
	"LC_CTYPE": true, "PS1": true, "PS2": true, "PS3": true, "PS4": true,
	"ENV": true, "BASH": true, "BASH_VERSION": true, "RANDOM": true,
	"SECONDS": true, "LINENO": true, "PPID": true, "SHLVL": true,
	"HOSTNAME": true, "TMPDIR": true, "CDPATH": true, "COLUMNS": true,
	"LINES": true, "UID": true, "EUID": true, "GROUPS": true, "OPTIND": true,
	"OPTARG": true, "REPLY": true, "PIPESTATUS": true, "FUNCNAME": true,
	"BASH_SOURCE": true, "BASH_LINENO": true, "BASH_ARGV": true, "BASH_ARGC": true,
}

var jsKeywords = map[string]bool{
	"var": true, "let": true, "const": true, "function": true, "return": true,
	"if": true, "else": true, "for": true, "while": true, "do": true,
	"switch": true, "case": true, "break": true, "continue": true, "new": true,
	"delete": true, "typeof": true, "instanceof": true, "in": true, "of": true,
	"class": true, "extends": true, "super": true, "this": true, "null": true,
	"true": true, "false": true, "undefined": true, "NaN": true, "Infinity": true,
	"async": true, "await": true, "yield": true, "static": true, "import": true,
	"export": true, "default": true, "try": true, "catch": true, "finally": true,
	"throw": true, "void": true, "with": true, "debugger": true, "enum": true,
}

var writeBuiltins = map[string]bool{
	"read": true, "declare": true, "typeset": true, "local": true,
	"export": true, "readonly": true, "unset": true, "mapfile": true,
	"readarray": true, "let": true, "eval": true, "source": true, ".": true,
}

// markStoreRefs — mirror mark_store_refs (string-parsed store reads).
func markStoreRefs(s string, out map[string]bool) {
	b := []byte(s)
	n := len(b)
	i := 0
	for i < n {
		if b[i] != '$' {
			i++
			continue
		}
		if i+2 < n && b[i+1] == '(' && b[i+2] == '(' {
			// $(( ... )) arith region
			j := i + 3
			depth := 2
			for j < n && depth > 0 {
				if b[j] == '(' {
					depth++
				} else if b[j] == ')' {
					depth--
				}
				j++
			}
			if depth != 0 {
				break
			}
			markArithRegion(string(b[i+3:j-2]), out)
			i = j
			continue
		}
		if i+1 < n && b[i+1] == '(' {
			// $( cmd ) — subprocess; skip the region (quote aware)
			j := i + 2
			depth := 1
			inSq, inDq, inBt := false, false, false
			for j < n && depth > 0 {
				cc := b[j]
				if inSq {
					if cc == '\'' {
						inSq = false
					}
					j++
					continue
				}
				if inDq {
					if cc == '\\' {
						j += 2
						continue
					}
					if cc == '"' {
						inDq = false
					}
					j++
					continue
				}
				if inBt {
					if cc == '`' {
						inBt = false
					}
					j++
					continue
				}
				switch cc {
				case '\'':
					inSq = true
				case '"':
					inDq = true
				case '`':
					inBt = true
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			if depth != 0 {
				break
			}
			i = j
			continue
		}
		if i+1 < n && b[i+1] == '\'' {
			// $'...' ANSI-C — not a store read
			j := i + 2
			for j < n && b[j] != '\'' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
			continue
		}
		if i+1 < n && b[i+1] == '{' {
			rest := string(b[i+2:])
			nameLen := 0
			for nameLen < len(rest) && isPlainIdentChar(rest[nameLen]) {
				nameLen++
			}
			if nameLen > 0 {
				name := rest[:nameLen]
				if isPlainIdent(name) {
					out[name] = true
				}
			}
			i += 2 + nameLen
			continue
		}
		rest := string(b[i+1:])
		nameLen := 0
		for nameLen < len(rest) && isPlainIdentChar(rest[nameLen]) {
			nameLen++
		}
		if nameLen > 0 {
			name := rest[:nameLen]
			if isPlainIdent(name) {
				out[name] = true
			}
			i += 1 + nameLen
			continue
		}
		i++
	}
}

func markArithRegion(region string, out map[string]bool) {
	b := []byte(region)
	n := len(b)
	i := 0
	for i < n {
		c := b[i]
		if c == '$' {
			skip := 1
			if i+1 < n && b[i+1] == '{' {
				skip = 2
			}
			rest := string(b[i+skip:])
			nameLen := 0
			for nameLen < len(rest) && isPlainIdentChar(rest[nameLen]) {
				nameLen++
			}
			if nameLen > 0 {
				name := rest[:nameLen]
				if isPlainIdent(name) {
					out[name] = true
				}
			}
			i += skip + nameLen
			continue
		}
		prevAlnum := i > 0 && (isPlainIdentChar(b[i-1]))
		if isIdentStart(c) && !prevAlnum {
			start := i
			for i < n && isPlainIdentChar(b[i]) {
				i++
			}
			w := string(b[start:i])
			if isPlainIdent(w) {
				out[w] = true
			}
		} else {
			i++
		}
	}
}

func markAllIdents(s string, out map[string]bool) {
	b := []byte(s)
	n := len(b)
	i := 0
	for i < n {
		c := b[i]
		if (isIdentStart(c)) && (i == 0 || !isPlainIdentChar(b[i-1])) {
			start := i
			for i < n && isPlainIdentChar(b[i]) {
				i++
			}
			w := string(b[start:i])
			if isPlainIdent(w) {
				out[w] = true
			}
		} else {
			i++
		}
	}
}

func markAllIdentsArgs(e Expr, out map[string]bool) {
	switch t := e.(type) {
	case *StrE:
		markAllIdents(t.Value, out)
	case *ArrayE:
		for _, el := range t.Elems {
			markAllIdentsArgs(el, out)
		}
	case *ObjectE:
		for _, p := range t.Props {
			markAllIdentsArgs(p.Val, out)
		}
	}
}

func markWriteBuiltinVars(e Expr, excluded map[string]bool) {
	switch t := e.(type) {
	case *ArrayE:
		for _, el := range t.Elems {
			markWriteBuiltinVars(el, excluded)
		}
	case *StrE:
		v := strings.SplitN(t.Value, "=", 2)[0]
		if isPlainIdent(v) {
			excluded[v] = true
		}
	}
}

func arithLetArgsNative(args []Expr) bool {
	if len(args) != 2 {
		return false
	}
	cn, ok := args[0].(*StrE)
	if !ok || cn.Value != "let" {
		return false
	}
	cargs, ok := args[1].(*ArrayE)
	if !ok || len(cargs.Elems) == 0 {
		return false
	}
	for _, a := range cargs.Elems {
		sv, ok := a.(*StrE)
		if !ok {
			return false
		}
		if _, ok := parseArithNative(sv.Value); !ok {
			return false
		}
	}
	return true
}

func intDeclareNames(args []Expr) ([]string, bool) {
	if len(args) != 2 {
		return nil, false
	}
	cn, ok := args[0].(*StrE)
	if !ok {
		return nil, false
	}
	if cn.Value != "typeset" && cn.Value != "declare" && cn.Value != "readonly" {
		return nil, false
	}
	cargs, ok := args[1].(*ArrayE)
	if !ok {
		return nil, false
	}
	var names []string
	sawI := false
	for _, a := range cargs.Elems {
		sv, ok := a.(*StrE)
		if !ok {
			return nil, false
		}
		s := sv.Value
		if strings.HasPrefix(s, "-") {
			if strings.ContainsAny(s, "pfF") {
				return nil, false
			}
			if strings.Contains(s, "i") {
				sawI = true
			}
		} else if strings.HasPrefix(s, "+") {
			return nil, false
		} else if strings.Contains(s, "=") {
			return nil, false
		} else if isPlainIdent(s) {
			names = append(names, s)
		} else {
			return nil, false
		}
	}
	if !sawI || len(names) == 0 {
		return nil, false
	}
	return names, true
}

func pureValueDeclare(args []Expr) ([][2]string, bool) {
	if len(args) != 2 {
		return nil, false
	}
	cn, ok := args[0].(*StrE)
	if !ok {
		return nil, false
	}
	if cn.Value != "local" && cn.Value != "declare" && cn.Value != "typeset" && cn.Value != "readonly" {
		return nil, false
	}
	cargs, ok := args[1].(*ArrayE)
	if !ok {
		return nil, false
	}
	var out [][2]string
	for _, a := range cargs.Elems {
		sv, ok := a.(*StrE)
		if !ok {
			return nil, false
		}
		s := sv.Value
		if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
			return nil, false
		}
		eq := strings.Index(s, "=")
		if eq < 0 {
			return nil, false
		}
		name, value := s[:eq], s[eq+1:]
		if !isPlainIdent(name) {
			return nil, false
		}
		for i := 0; i < len(value); i++ {
			c := value[i]
			if !(isPlainIdentChar(c) || c == '.' || c == '/' || c == ':' || c == ',' || c == '+' || c == '-') {
				return nil, false
			}
		}
		out = append(out, [2]string{name, value})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func collectNativeArithSources(args []Expr, assigns map[string][]Expr) {
	if len(args) != 2 {
		return
	}
	cn, ok := args[0].(*StrE)
	if !ok {
		return
	}
	switch cn.Value {
	case "typeset", "declare", "readonly":
		if names, ok := intDeclareNames(args); ok {
			for _, n := range names {
				assigns[n] = append(assigns[n], &IntE{Value: 0})
			}
		} else if pairs, ok := pureValueDeclare(args); ok {
			for _, p := range pairs {
				assigns[p[0]] = append(assigns[p[0]], &StrE{Value: p[1], Style: "SingleQuoted"})
			}
		}
	case "local":
		if pairs, ok := pureValueDeclare(args); ok {
			for _, p := range pairs {
				assigns[p[0]] = append(assigns[p[0]], &StrE{Value: p[1], Style: "SingleQuoted"})
			}
		}
	case "let":
		if !arithLetArgsNative(args) {
			return
		}
		cargs := args[1].(*ArrayE)
		for _, a := range cargs.Elems {
			sv := a.(*StrE)
			if ast, ok := parseArithNative(sv.Value); ok {
				for _, w := range arithWrittenVars(ast) {
					assigns[w] = append(assigns[w], &IntE{Value: 0})
				}
			}
		}
	}
}

func arithWrittenVars(ast ArithAst) []string {
	var out []string
	var walk func(a ArithAst)
	walk = func(a ArithAst) {
		switch t := a.(type) {
		case *ArithAssign:
			out = append(out, t.Var)
			walk(t.Rhs)
		case *ArithIncDec:
			out = append(out, t.Var)
		case *ArithBin:
			walk(t.Lhs)
			walk(t.Rhs)
		case *ArithUn:
			walk(t.Arg)
		case *ArithCond:
			walk(t.Test)
			walk(t.Then)
			walk(t.Else)
		case *ArithIndex:
			walk(t.Key)
		}
	}
	walk(ast)
	return out
}

// iterNumeric — mirror iter_numeric.
func iterNumeric(e Expr) (bool, bool) { // (numeric, known)
	switch t := e.(type) {
	case *ArrayE:
		numeric := true
		known := true
		for _, el := range t.Elems {
			switch elt := el.(type) {
			case *StrE:
				if _, err := strconv.ParseInt(strings.TrimSpace(elt.Value), 10, 64); err != nil {
					numeric = false
				}
			case *CallE:
				if elt.Func == "brace" {
					if v, ok := braceNumeric(elt.Args); ok {
						if !v {
							numeric = false
						}
					} else {
						known = false
					}
				} else {
					known = false
				}
			default:
				known = false
			}
		}
		return numeric, known
	case *CallE:
		if t.Func == "brace" {
			return braceNumeric(t.Args)
		}
	}
	return false, false
}

func braceNumeric(args []Expr) (bool, bool) {
	for _, a := range args {
		if jv, ok := a.(*JsonE); ok {
			numeric := true
			jsonItemsNumeric(jv.Value, &numeric)
			return numeric, true
		}
	}
	return false, false
}

func jsonItemsNumeric(v interface{}, found *bool) {
	switch t := v.(type) {
	case []interface{}:
		for _, x := range t {
			jsonItemsNumeric(x, found)
		}
	case map[string]interface{}:
		if _, isRange := t["range"]; !isRange {
			*found = false
		}
	case string:
		if _, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err != nil {
			*found = false
		}
	default:
		*found = false
	}
}

// ── lift walkers (mirror numeric_lift_vars / string_lift_vars) ──────

type liftCtx struct {
	excluded   map[string]bool
	stringCtx  map[string]bool
	inCopy     bool
}

func walkExprN(e Expr, ctx *liftCtx) {
	switch t := e.(type) {
	case *CallE:
		letArgsNative := t.Func == "exec" && arithLetArgsNative(t.Args)
		if t.Func != "getVar" && t.Func != "test" && t.Func != "setArray" && t.Func != "setArrayAppend" && !letArgsNative {
			for _, a := range t.Args {
				markStrArgs(a, ctx.stringCtx)
			}
		}
		if ctx.inCopy && letArgsNative {
			if len(t.Args) == 2 {
				if _, ok := t.Args[0].(*StrE); ok {
					if cargs, ok := t.Args[1].(*ArrayE); ok {
						for _, a := range cargs.Elems {
							if sv, ok := a.(*StrE); ok {
								if ast, ok := parseArithNative(sv.Value); ok {
									for _, w := range arithWrittenVars(ast) {
										ctx.excluded[w] = true
									}
								}
							}
						}
					}
				}
			}
		}
		if t.Func == "exec" || t.Func == "builtin" {
			if len(t.Args) > 0 {
				if cn, ok := t.Args[0].(*StrE); ok && writeBuiltins[cn.Value] {
					nativeLet := cn.Value == "let" && letArgsNative
					intdecl := []string{}
					if cn.Value != "let" {
						if names, ok := intDeclareNames(t.Args); ok {
							intdecl = names
						}
					}
					pureDecl := !ctx.inCopy
					if pureDecl {
						if _, ok := pureValueDeclare(t.Args); !ok {
							pureDecl = false
						}
					}
					if !(nativeLet || len(intdecl) > 0 || pureDecl) {
						for _, a := range t.Args[1:] {
							markWriteBuiltinVars(a, ctx.excluded)
							markAllIdentsArgs(a, ctx.stringCtx)
						}
					}
				}
			}
		}
		switch t.Func {
		case "arrayIndex", "arrayLen", "arrayItems", "arraySlice", "setArray", "setArrayAppend":
			if len(t.Args) > 0 {
				if name, ok := t.Args[0].(*StrE); ok {
					ctx.excluded[name.Value] = true
				}
			}
		}
		for _, a := range t.Args {
			walkExprN(a, ctx)
		}
	case *ArrowE:
		for _, st := range t.Body {
			walkStmtN(st, ctx)
		}
	case *InterpE:
		for _, p := range t.Parts {
			if !p.IsLit {
				walkExprN(p.Expr, ctx)
			}
		}
	case *ArrayE:
		for _, el := range t.Elems {
			walkExprN(el, ctx)
		}
	case *ObjectE:
		for _, p := range t.Props {
			walkExprN(p.Val, ctx)
		}
	}
}

func walkStmtN(st Stmt, ctx *liftCtx) {
	switch t := st.(type) {
	case *AssignS:
		if ctx.inCopy {
			ctx.excluded[t.Var] = true
		}
		walkExprN(t.Expr, ctx)
	case *ForS:
		walkExprN(t.Iter, ctx)
		for _, b := range t.Body {
			walkStmtN(b, ctx)
		}
	case *WhileS:
		walkExprN(t.Cond, ctx)
		for _, b := range t.Body {
			walkStmtN(b, ctx)
		}
	case *IfS:
		walkExprN(t.Cond, ctx)
		for _, b := range t.Then {
			walkStmtN(b, ctx)
		}
		for _, b := range t.Else {
			walkStmtN(b, ctx)
		}
	case *ExprS:
		walkExprN(t.Expr, ctx)
	case *BlockS:
		for _, b := range t.Body {
			walkStmtN(b, ctx)
		}
	case *BackgroundS:
		for _, b := range t.Body {
			walkStmtN(b, ctx)
		}
	case *SubshellS:
		sub := &liftCtx{excluded: ctx.excluded, stringCtx: ctx.stringCtx, inCopy: true}
		for _, b := range t.Body {
			walkStmtN(b, sub)
		}
	case *RedirectS:
		for _, b := range t.Inner {
			walkStmtN(b, ctx)
		}
		for _, r := range t.Redirects {
			walkExprN(r.Target, ctx)
		}
	case *FunctionS:
		for _, b := range t.Body {
			walkStmtN(b, ctx)
		}
	case *ReturnS:
		if t.Value != nil {
			walkExprN(t.Value, ctx)
		}
	case *CaseS:
		walkExprN(t.Disc, ctx)
		for _, cl := range t.Clauses {
			for _, pat := range cl.Patterns {
				markStoreRefs(pat, ctx.stringCtx)
			}
			for _, b := range cl.Body {
				walkStmtN(b, ctx)
			}
		}
	}
}

func markStrArgs(e Expr, out map[string]bool) {
	switch t := e.(type) {
	case *StrE:
		markStoreRefs(t.Value, out)
	case *ArrayE:
		for _, el := range t.Elems {
			markStrArgs(el, out)
		}
	case *ObjectE:
		for _, p := range t.Props {
			markStrArgs(p.Val, out)
		}
	}
}

// collectAssigns — assignment sources (mirror collect_assigns).
func collectAssigns(st Stmt, assigns map[string][]Expr) {
	switch t := st.(type) {
	case *AssignS:
		assigns[t.Var] = append(assigns[t.Var], t.Expr)
	case *WhileS, *BlockS, *FunctionS, *SubshellS, *BackgroundS:
		var body []Stmt
		switch bt := st.(type) {
		case *WhileS:
			body = bt.Body
		case *BlockS:
			body = bt.Body
		case *FunctionS:
			body = bt.Body
		case *SubshellS:
			body = bt.Body
		case *BackgroundS:
			body = bt.Body
		}
		for _, b := range body {
			collectAssigns(b, assigns)
		}
	case *IfS:
		for _, b := range t.Then {
			collectAssigns(b, assigns)
		}
		for _, b := range t.Else {
			collectAssigns(b, assigns)
		}
	case *ForS:
		if _, ok := assigns[t.Var]; !ok {
			assigns[t.Var] = nil
		}
		for _, b := range t.Body {
			collectAssigns(b, assigns)
		}
	case *ExprS:
		collectExprAssigns(t.Expr, assigns)
	case *CaseS:
		for _, cl := range t.Clauses {
			for _, b := range cl.Body {
				collectAssigns(b, assigns)
			}
		}
	case *RedirectS:
		for _, b := range t.Inner {
			collectAssigns(b, assigns)
		}
	case *PipelineS:
		for _, stage := range t.Stages {
			for _, b := range stage {
				collectAssigns(b, assigns)
			}
		}
	}
}

// PipelineS — needed by collectAssigns (mirror IrStmt::Pipeline).
type PipelineS struct{ Stages [][]Stmt }

func collectExprAssigns(e Expr, assigns map[string][]Expr) {
	switch t := e.(type) {
	case *ArrowE:
		for _, st := range t.Body {
			collectAssigns(st, assigns)
		}
	case *CallE:
		if t.Func == "exec" {
			collectNativeArithSources(t.Args, assigns)
		}
		for _, a := range t.Args {
			collectExprAssigns(a, assigns)
		}
	case *ArrayE:
		for _, el := range t.Elems {
			collectExprAssigns(el, assigns)
		}
	}
}

// collectForIters — mirror collect_for_iters.
func collectForIters(st Stmt, out map[string]Expr) {
	switch t := st.(type) {
	case *ForS:
		out[t.Var] = t.Iter
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *WhileS:
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *BlockS:
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *FunctionS:
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *SubshellS:
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *BackgroundS:
		for _, b := range t.Body {
			collectForIters(b, out)
		}
	case *IfS:
		for _, b := range t.Then {
			collectForIters(b, out)
		}
		for _, b := range t.Else {
			collectForIters(b, out)
		}
	case *RedirectS:
		for _, b := range t.Inner {
			collectForIters(b, out)
		}
	case *CaseS:
		for _, cl := range t.Clauses {
			for _, b := range cl.Body {
				collectForIters(b, out)
			}
		}
	case *PipelineS:
		for _, stage := range t.Stages {
			for _, b := range stage {
				collectForIters(b, out)
			}
		}
	}
}

// numericLiftVars — mirror numeric_lift_vars.
func numericLiftVars(stmts []Stmt) map[string]bool {
	ctx := &liftCtx{excluded: map[string]bool{}, stringCtx: map[string]bool{}}
	for _, st := range stmts {
		walkStmtN(st, ctx)
	}
	assigns := map[string][]Expr{}
	for _, st := range stmts {
		collectAssigns(st, assigns)
	}
	forIters := map[string]Expr{}
	for _, st := range stmts {
		collectForIters(st, forIters)
	}
	lifted := map[string]bool{}
	for {
		changed := false
		for name, exprs := range assigns {
			if lifted[name] || ctx.excluded[name] || ctx.stringCtx[name] ||
				reservedVars[name] || jsKeywords[name] ||
				strings.Contains(name, "[") || strings.Contains(name, "]") {
				continue
			}
			allNumeric := true
			for _, e := range exprs {
				ok := false
				switch t := e.(type) {
				case *ArithE:
					ok = !arithHasDivMod(t.Ast)
				case *IntE:
					ok = true
				case *StrE:
					_, err := strconv.ParseInt(strings.TrimSpace(t.Value), 10, 64)
					ok = err == nil
				case *VarE:
					ok = lifted[t.Name]
				case *CallE:
					if t.Func == "getVar" && len(t.Args) == 1 {
						if n, ok2 := t.Args[0].(*StrE); ok2 {
							ok = lifted[n.Value]
						}
					}
				}
				if !ok {
					allNumeric = false
					break
				}
			}
			if allNumeric {
				if it, ok := forIters[name]; ok {
					num, known := iterNumeric(it)
					if !known || !num {
						allNumeric = false
					}
				}
			}
			if allNumeric {
				lifted[name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return lifted
}

// stringLiftVars — mirror string_lift_vars.
func stringLiftVars(stmts []Stmt, numeric map[string]bool) map[string]bool {
	ctx := &liftCtx{excluded: map[string]bool{}, stringCtx: map[string]bool{}}
	for _, st := range stmts {
		walkStmtN(st, ctx)
	}
	assigns := map[string][]Expr{}
	for _, st := range stmts {
		collectAssigns(st, assigns)
	}
	lifted := map[string]bool{}
	for {
		changed := false
		for name, exprs := range assigns {
			if lifted[name] || numeric[name] || ctx.excluded[name] || ctx.stringCtx[name] ||
				reservedVars[name] || jsKeywords[name] ||
				strings.Contains(name, "[") || strings.Contains(name, "]") {
				continue
			}
			allString := true
			for _, e := range exprs {
				ok := false
				switch t := e.(type) {
				case *StrE:
					ok = true
				case *InterpE:
					ok = true
				case *VarE:
					ok = lifted[t.Name]
				case *CallE:
					if t.Func == "getVar" && len(t.Args) == 1 {
						if n, ok2 := t.Args[0].(*StrE); ok2 {
							ok = lifted[n.Value]
						}
					}
					if t.Func == "capture" && len(t.Args) == 1 {
						if _, ok2 := t.Args[0].(*ArrowE); ok2 {
							ok = true
						}
					}
				}
				if !ok {
					allString = false
					break
				}
			}
			if allString {
				lifted[name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return lifted
}

func analyzeVarTypes(stmts []Stmt) []VarTypeOut {
	numeric := numericLiftVars(stmts)
	strs := stringLiftVars(stmts, numeric)
	names := map[string]bool{}
	for n := range numeric {
		names[n] = true
	}
	for n := range strs {
		names[n] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []VarTypeOut
	for _, n := range sorted {
		t := "Str"
		if numeric[n] {
			t = "Int"
		}
		out = append(out, VarTypeOut{Name: n, Type: t})
	}
	return out
}

type VarTypeOut struct{ Name, Type string }

// ─────────────────────────────────────────────────────────────────────
// optimize (mirror optimize_stmts: arith-const fold; self-assign removal
// is a no-op for our node shapes — IrExpr::Var is never produced)
// ─────────────────────────────────────────────────────────────────────

func foldExpr(e Expr) Expr {
	switch t := e.(type) {
	case *CallE:
		args := make([]Expr, len(t.Args))
		for i, a := range t.Args {
			args[i] = foldExpr(a)
		}
		if t.Func == "arith" && len(args) == 1 {
			if sv, ok := args[0].(*StrE); ok {
				if v, ok := foldArithConst(sv.Value); ok {
					return &IntE{Value: v}
				}
			}
		}
		return &CallE{Func: t.Func, Args: args}
	case *InterpE:
		out := &InterpE{}
		for _, p := range t.Parts {
			if p.IsLit {
				out.Parts = append(out.Parts, p)
			} else {
				out.Parts = append(out.Parts, InterpPartE{IsLit: false, Expr: foldExpr(p.Expr)})
			}
		}
		return out
	case *ArrayE:
		out := &ArrayE{}
		for _, el := range t.Elems {
			out.Elems = append(out.Elems, foldExpr(el))
		}
		return out
	case *ObjectE:
		out := &ObjectE{}
		for _, p := range t.Props {
			out.Props = append(out.Props, PropE{Key: p.Key, Val: foldExpr(p.Val)})
		}
		return out
	case *ArrowE:
		return &ArrowE{Body: foldStmts(t.Body)}
	}
	return e
}

func foldStmt(s Stmt) Stmt {
	switch t := s.(type) {
	case *ExprS:
		return &ExprS{Expr: foldExpr(t.Expr)}
	case *AssignS:
		return &AssignS{Var: t.Var, Expr: foldExpr(t.Expr)}
	}
	return s
}

func foldStmts(stmts []Stmt) []Stmt {
	out := make([]Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = foldStmt(s)
	}
	return out
}

func optimizeStmts(stmts []Stmt) []Stmt {
	return foldStmts(stmts)
}

// ─────────────────────────────────────────────────────────────────────
// A1 JSON emit (mirror shir_json.rs)
// ─────────────────────────────────────────────────────────────────────

func exprJSON(e Expr) map[string]interface{} {
	switch t := e.(type) {
	case *IntE:
		return map[string]interface{}{"type": "Int", "value": t.Value}
	case *StrE:
		return map[string]interface{}{"type": "Str", "value": t.Value, "style": t.Style}
	case *VarE:
		return map[string]interface{}{"type": "Var", "name": t.Name, "sigil": nil}
	case *CallE:
		return map[string]interface{}{
			"type":   "Call",
			"func":   t.Func,
			"args":   exprsJSON(t.Args),
			"purity": callPurity(t.Func, t.Args),
		}
	case *InterpE:
		parts := []interface{}{}
		for _, p := range t.Parts {
			if p.IsLit {
				parts = append(parts, map[string]interface{}{"kind": "lit", "text": p.Lit})
			} else {
				parts = append(parts, map[string]interface{}{"kind": "expr", "expr": exprJSON(p.Expr)})
			}
		}
		return map[string]interface{}{"type": "Interpolate", "parts": parts}
	case *ArrayE:
		return map[string]interface{}{"type": "Array", "elements": exprsJSON(t.Elems)}
	case *ObjectE:
		var props []interface{}
		for _, p := range t.Props {
			props = append(props, map[string]interface{}{"key": p.Key, "value": exprJSON(p.Val)})
		}
		return map[string]interface{}{"type": "Object", "properties": props}
	case *ArrowE:
		return map[string]interface{}{"type": "Arrow", "body": stmtsJSON(t.Body)}
	case *ArithE:
		return map[string]interface{}{"type": "Arith", "ast": arithJSON(t.Ast)}
	case *BoolE:
		return map[string]interface{}{"type": "Bool", "value": t.Value}
	case *JsonE:
		return map[string]interface{}{"type": "Json", "value": t.Value}
	case *BinOpE:
		return map[string]interface{}{
			"type": "BinOp", "op": t.Op,
			"lhs": exprJSON(t.Lhs), "rhs": exprJSON(t.Rhs),
		}
	}
	return map[string]interface{}{"type": "Unsupported"}
}

func exprsJSON(es []Expr) []interface{} {
	out := make([]interface{}, len(es))
	for i, e := range es {
		out[i] = exprJSON(e)
	}
	return out
}

func arithJSON(a ArithAst) map[string]interface{} {
	switch t := a.(type) {
	case *ArithNum:
		return map[string]interface{}{"type": "Num", "value": t.Val}
	case *ArithVar:
		return map[string]interface{}{"type": "Var", "name": t.Name}
	case *ArithIndex:
		return map[string]interface{}{"type": "Index", "var": t.Var, "key": arithJSON(t.Key)}
	case *ArithBin:
		return map[string]interface{}{"type": "Bin", "op": t.Op, "lhs": arithJSON(t.Lhs), "rhs": arithJSON(t.Rhs)}
	case *ArithUn:
		return map[string]interface{}{"type": "Un", "op": t.Op, "arg": arithJSON(t.Arg)}
	case *ArithCond:
		return map[string]interface{}{
			"type": "Cond", "test": arithJSON(t.Test),
			"then": arithJSON(t.Then), "else": arithJSON(t.Else),
		}
	case *ArithAssign:
		return map[string]interface{}{"type": "Assign", "var": t.Var, "op": t.Op, "rhs": arithJSON(t.Rhs)}
	case *ArithIncDec:
		return map[string]interface{}{"type": "IncDec", "var": t.Var, "delta": t.Delta, "prefix": t.Prefix}
	}
	return map[string]interface{}{"type": "Num", "value": 0}
}

func stmtJSON(s Stmt) map[string]interface{} {
	switch t := s.(type) {
	case *AssignS:
		return map[string]interface{}{
			"type": "Assign",
			"targets": []interface{}{map[string]interface{}{
				"var": t.Var, "sigil": nil, "indices": []interface{}{},
			}},
			"expr": exprJSON(t.Expr),
		}
	case *ExprS:
		return map[string]interface{}{"type": "Expr", "expr": exprJSON(t.Expr)}
	case *IfS:
		return map[string]interface{}{
			"type": "If",
			"cond":  exprJSON(t.Cond),
			"then":  stmtsJSON(t.Then),
			"elsifs": []interface{}{},
			"else":  stmtsJSON(t.Else),
		}
	case *WhileS:
		return map[string]interface{}{
			"type": "While",
			"cond": exprJSON(t.Cond),
			"body": stmtsJSON(t.Body),
		}
	case *ForS:
		return map[string]interface{}{
			"type": "For",
			"var":  t.Var,
			"iter": exprJSON(t.Iter),
			"body": stmtsJSON(t.Body),
		}
	case *RedirectS:
		return map[string]interface{}{
			"type":      "Redirect",
			"inner":     stmtsJSON(t.Inner),
			"redirects": redirectsJSON(t.Redirects),
		}
	case *BlockS:
		return map[string]interface{}{"type": "Block", "body": stmtsJSON(t.Body)}
	case *BackgroundS:
		return map[string]interface{}{"type": "Background", "body": stmtsJSON(t.Body)}
	case *SubshellS:
		return map[string]interface{}{"type": "Subshell", "body": stmtsJSON(t.Body)}
	case *FunctionS:
		return map[string]interface{}{"type": "Function", "name": t.Name, "body": stmtsJSON(t.Body)}
	case *ReturnS:
		var v interface{}
		if t.Value != nil {
			v = exprJSON(t.Value)
		}
		return map[string]interface{}{"type": "Return", "value": v}
	case *CaseS:
		var clauses []interface{}
		for _, cl := range t.Clauses {
			clauses = append(clauses, map[string]interface{}{
				"patterns": cl.Patterns,
				"body":     stmtsJSON(cl.Body),
			})
		}
		return map[string]interface{}{"type": "Case", "discriminant": exprJSON(t.Disc), "clauses": clauses}
	case *PipelineS:
		var stages []interface{}
		for _, stage := range t.Stages {
			stages = append(stages, stmtsJSON(stage))
		}
		return map[string]interface{}{
			"type": "Pipeline", "stages": stages,
			"last_output": false, "capture": nil, "cmd_str": "",
			"purity": "Spawn",
		}
	}
	return map[string]interface{}{"type": "noop"}
}

func stmtsJSON(stmts []Stmt) []interface{} {
	out := make([]interface{}, len(stmts))
	for i, s := range stmts {
		out[i] = stmtJSON(s)
	}
	return out
}

func redirectsJSON(rs []RedirectIR) []interface{} {
	out := make([]interface{}, len(rs))
	for i, r := range rs {
		out[i] = map[string]interface{}{
			"fd": r.FD, "mode": r.Mode,
			"target": exprJSON(r.Target), "interpolate": r.Interpolate,
		}
	}
	return out
}

// programJSON — the A1 program with stmt_lines.
func programJSON(stmts []Stmt, varTypes []VarTypeOut) []byte {
	prog := map[string]interface{}{
		"type":             "Program",
		"contract_version": contractVersion,
		"imports":          []string{},
		"requires":         []string{},
		"var_types":        varTypesJSON(varTypes),
		"stmt_lines":       []interface{}{},
		"subs":             []interface{}{},
		"stmts":            stmtsJSON(stmts),
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(prog); err != nil {
		return []byte(fmt.Sprintf("emit error: %v", err))
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	return []byte(out)
}

func varTypesJSON(vts []VarTypeOut) []interface{} {
	out := make([]interface{}, len(vts))
	for i, v := range vts {
		out[i] = map[string]interface{}{"name": v.Name, "type": v.Type}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────
// driver: file → A1 JSON
// ─────────────────────────────────────────────────────────────────────

func shirForSource(src string) ([]byte, error) {
	p := &Parser{src: src}
	cmds, err := p.parseProgram()
	if err != nil {
		return nil, err
	}
	var stmts []Stmt
	for _, c := range cmds {
		if s := stmtForCommand(c); s != nil {
			stmts = append(stmts, s)
		}
	}
	stmts = optimizeStmts(stmts)
	vt := analyzeVarTypes(stmts)
	return programJSON(stmts, vt), nil
}

func main() {
	args := os.Args[1:]
	raw := false
	var filtered []string
	for _, a := range args {
		if a == "--raw" {
			raw = true
		} else {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) != 2 || filtered[0] != "--shir" {
		fmt.Fprintln(os.Stderr, "usage: posix-sh-go --shir <file.sh> [--raw]")
		os.Exit(2)
	}
	inp := filtered[1]
	src := inp
	if strings.Contains(inp, ".sh") || !strings.ContainsAny(inp, " \t\n") {
		if b, err := os.ReadFile(inp); err == nil {
			src = string(b)
		}
	}
	out, err := shirForSource(src)
	if err != nil {
		// the core's export_shir prints the error and returns normally:
		// empty stdout, exit 0
		fmt.Fprintln(os.Stderr, "Parse error: "+err.Error())
		return
	}
	os.Stdout.Write(out)
	if !raw {
		os.Stdout.Write([]byte{'\n'})
	}
}
