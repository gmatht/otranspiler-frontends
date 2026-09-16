// container.go — typed int lists (docs/AUTO_CYTHON.md, "typed containers").
//
// Cython pure-Python mode cannot give a Python `list` a C backing store, so a
// typed container is a TRANSFORM, like the GMP one. This pass recognises an
// append-built list of i64 ints and rewrites it to a growable `long long`
// vector in `.pyx` mode:
//
//	xs = []                      # removed
//	xs.append(i * i)             -> xs = _sh_push_i64(xs, &xs_len, &xs_cap, i * i)
//	len(xs)                      -> xs_len
//	xs[3]                        # unchanged (a `long long*` subscript)
//
// REFUSE > GUESS: a list is transformed only when EVERY use is one of those
// forms and every appended value has a proved i64 interval; anything else
// (iteration, slicing, `sum`, passing it to a call, a bare read, a non-int
// element) leaves it a Python list.
//
// Only `.pyx` mode can express this (a C pointer is not Python).
package pylib

import (
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/gmatht/sh2loop/frontends/py-sh-go/gen"
)

// i64PushHelper is emitted once when any int list is transformed. `&x_len` on
// a module-level `cdef long long` is a C pointer, so the helper can grow the
// vector in place.
const i64PushHelper = `from libc.stdlib cimport realloc

cdef long long *_sh_push_i64(long long *p, long long *n, long long *c, long long v):
    if n[0] == c[0]:
        c[0] = c[0] * 2 if c[0] else 16
        p = <long long*>realloc(p, c[0] * sizeof(long long))
    p[n[0]] = v
    n[0] += 1
    return p
`

type span struct{ start, stop int } // char offsets, [start, stop)

type listUse struct {
	span
	kind string // "append" | "len" | "index"
	arg  string // append argument text
}

type replacement struct {
	start, stop int
	text        string
}

// rewriteContainers returns the rewritten source, the container declarations,
// the transformed names, and whether anything changed. `e` is the module int
// env (to prove append arguments fit i64).
func rewriteContainers(tree antlr.Tree, src string, e env) (string, string, []string, bool) {
	nameOcc := map[string][]span{}
	uses := map[string][]listUse{}
	assigns := map[string]span{}

	walkTree(tree, func(n antlr.Tree) {
		switch c := n.(type) {
		case gen.INameContext:
			nm := c.GetText()
			nameOcc[nm] = append(nameOcc[nm], span{c.GetStart().GetStart(), c.GetStop().GetStop() + 1})
		case gen.IAtom_exprContext:
			text := c.GetText()
			sp := span{c.GetStart().GetStart(), c.GetStop().GetStop() + 1}
			// len(name)
			if strings.HasPrefix(text, "len(") && strings.HasSuffix(text, ")") {
				if inner := text[4 : len(text)-1]; isSimpleName(inner) {
					uses[inner] = append(uses[inner], listUse{sp, "len", ""})
				}
				return
			}
			// name.append(arg) / name[...]
			if dot := strings.Index(text, "."); dot > 0 && strings.HasPrefix(text[dot:], ".append(") && strings.HasSuffix(text, ")") {
				if nm := text[:dot]; isSimpleName(nm) {
					uses[nm] = append(uses[nm], listUse{sp, "append", text[dot+len(".append(") : len(text)-1]})
				}
				return
			}
			if br := strings.Index(text, "["); br > 0 && isSimpleName(text[:br]) {
				uses[text[:br]] = append(uses[text[:br]], listUse{sp, "index", ""})
			}
		case gen.IExpr_stmtContext:
			if c.Annassign() != nil || c.Augassign() != nil {
				return
			}
			ts := c.AllTestlist_star_expr()
			if len(ts) != 2 {
				return
			}
			lhs := strings.TrimSpace(ts[0].GetText())
			if !isSimpleName(lhs) {
				return
			}
			if strings.ReplaceAll(ts[1].GetText(), " ", "") == "[]" {
				assigns[lhs] = span{c.GetStart().GetStart(), c.GetStop().GetStop() + 1}
			}
		}
	})

	var reps []replacement
	var decls strings.Builder
	var names []string
	// A name rebound by with/except/del/import/match/walrus is not the
	// list anymore, even with a `name = []` assignment on record.
	obscure := obscureSet(tree)
	for name, asgn := range assigns {
		if obscure[name] {
			continue
		}
		// every mention of the name must be covered by a supported use or the
		// `name = []` assignment itself.
		ok := true
		for _, o := range nameOcc[name] {
			if o.start >= asgn.start && o.stop <= asgn.stop {
				continue
			}
			covered := false
			for _, u := range uses[name] {
				if o.start >= u.start && o.stop <= u.stop {
					covered = true
					break
				}
			}
			if !covered {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// every appended value must be a proved i64 int
		sawAppend := false
		for _, u := range uses[name] {
			if u.kind == "append" {
				sawAppend = true
				if !evalText(u.arg, e).ok {
					ok = false
					break
				}
			}
		}
		if !ok || !sawAppend {
			continue
		}

		reps = append(reps, replacement{asgn.start, asgn.stop, "pass"})
		for _, u := range uses[name] {
			switch u.kind {
			case "append":
				reps = append(reps, replacement{u.start, u.stop,
					name + " = _sh_push_i64(" + name + ", &" + name + "_len, &" + name + "_cap, " + u.arg + ")"})
			case "len":
				reps = append(reps, replacement{u.start, u.stop, name + "_len"})
			}
		}
		decls.WriteString("cdef long long *" + name + " = NULL\n")
		decls.WriteString("cdef long long " + name + "_len = 0\n")
		decls.WriteString("cdef long long " + name + "_cap = 0\n")
		names = append(names, name)
	}
	if len(reps) == 0 {
		return src, "", nil, false
	}
	sort.Strings(names)
	return applyReplacements(src, reps), decls.String(), names, true
}

// applyReplacements splices the replacements into src, right-to-left so the
// offsets stay valid.
func applyReplacements(src string, reps []replacement) string {
	sort.Slice(reps, func(i, j int) bool { return reps[i].start > reps[j].start })
	for _, r := range reps {
		if r.start < 0 || r.stop > len(src) || r.start > r.stop {
			continue
		}
		src = src[:r.start] + r.text + src[r.stop:]
	}
	return src
}
