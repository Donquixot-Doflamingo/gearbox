// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// MaxLoopSteps is the worst-case element steps one expression may take.
const MaxLoopSteps = 1_000_000

// taint is the set of PII paths inside a value ("[]" = any array element,
// "*" = any property). all marks the whole value as PII-derived.
type taint struct {
	all   bool
	paths [][]string
}

func (t *taint) empty() bool { return t == nil || (!t.all && len(t.paths) == 0) }

// overlaps reports whether reading chain touches PII (chain is a prefix of a
// PII path or the other way round).
func (t *taint) overlaps(chain []string) bool {
	if t == nil {
		return false
	}
	if t.all {
		return true
	}
	for _, p := range t.paths {
		n := len(p)
		if len(chain) < n {
			n = len(chain)
		}
		match := true
		for i := 0; i < n; i++ {
			if p[i] != chain[i] && p[i] != "*" && !(p[i] == "[]" && chain[i] == "[]") {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// sub returns the taint below a path prefix.
func (t *taint) sub(prefix []string) *taint {
	if t == nil {
		return nil
	}
	if t.all {
		return t
	}
	out := &taint{}
	for _, p := range t.paths {
		if len(p) <= len(prefix) {
			match := true
			for i := range p {
				if p[i] != prefix[i] && p[i] != "*" {
					match = false
					break
				}
			}
			if match {
				return &taint{all: true}
			}
			continue
		}
		match := true
		for i := range prefix {
			if p[i] != prefix[i] && p[i] != "*" {
				match = false
				break
			}
		}
		if match {
			out.paths = append(out.paths, p[len(prefix):])
		}
	}
	if len(out.paths) == 0 {
		return nil
	}
	return out
}

func (t *taint) add(path []string) *taint {
	if t == nil {
		t = &taint{}
	}
	if len(path) == 0 {
		t.all = true
		return t
	}
	t.paths = append(t.paths, append([]string(nil), path...))
	return t
}

func (t *taint) union(o *taint) *taint {
	if o.empty() {
		return t
	}
	if t == nil {
		t = &taint{}
	}
	if o.all {
		t.all = true
	}
	t.paths = append(t.paths, o.paths...)
	return t
}

func taintFromSchema(s *jsonschema.Schema) *taint {
	if s == nil {
		return nil
	}
	paths := s.PIIPaths()
	if len(paths) == 0 {
		return nil
	}
	t := &taint{}
	for _, p := range paths {
		t.add(p)
	}
	return t
}

// sview is a value whose schema may be known.
type sview struct {
	schema *jsonschema.Schema // nil = unknown/any
	label  string             // "lead_input@1"
	taint  *taint
}

type nodeView struct {
	sview
	mayNotRun bool
}

// scope describes what an expression can see.
type scope struct {
	vars    map[string]bool // available roots
	input   sview
	request sview
	out     *taint
	nodes   map[string]*nodeView
}

var (
	varsNode      = map[string]bool{"input": true, "request": true, "nodes": true, "ctx": true}
	varsTransform = map[string]bool{"input": true, "request": true, "nodes": true, "ctx": true, "out": true}
	varsCell      = map[string]bool{"input": true, "request": true, "nodes": true, "ctx": true, "value": true}
	varsAttrs     = map[string]bool{"input": true, "ctx": true}
)

var ctxFields = map[string]bool{"now": true, "unit": true, "requestId": true, "env": true}

// exprInfo is what static analysis learned about one expression.
type exprInfo struct {
	tainted     bool
	segments    []string
	lookups     []string
	gates       []string
	experiments []string
	nodesRead   []string
	errors      bool
}

type seg struct {
	name  string
	index bool
	dyn   ast.Node
}

type elemCtx struct {
	schema *jsonschema.Schema
	known  bool
	taint  *taint
	path   []string
}

type walker struct {
	b     *builder
	sc    *scope
	path  string
	info  *exprInfo
	lets  []string
	elems []elemCtx
	mult  float64
}

// expr compiles a code slot with static checks. ok=false means it did not
// compile (a problem has been recorded).
func (b *builder) expr(src, path string, sc *scope) (*program, exprInfo, bool) {
	var info exprInfo
	if strings.TrimSpace(src) == "" {
		b.errorf("PARSE_ERROR", path, "expression is empty")
		return nil, info, false
	}
	if len(src) > MaxExprBytes {
		b.errorf("PARSE_ERROR", path, "expression is longer than %d bytes", MaxExprBytes)
		return nil, info, false
	}
	tree, err := parser.Parse(src)
	if err != nil {
		b.errorf("PARSE_ERROR", path, "%s", firstLine(err.Error()))
		return nil, info, false
	}
	w := &walker{b: b, sc: sc, path: path, info: &info, mult: 1}
	before := b.errorCount()
	w.walk(tree.Node)
	info.errors = b.errorCount() > before
	p, err := compileProgram(src)
	if err != nil {
		if !info.errors {
			msg := firstLine(err.Error())
			code := "TYPE_MISMATCH"
			switch {
			case strings.Contains(msg, "unknown name"):
				code = "UNKNOWN_FIELD"
			case strings.Contains(msg, "unexpected token") || strings.Contains(msg, "syntax"):
				code = "PARSE_ERROR"
			}
			b.errorf(code, path, "%s", msg)
		}
		return nil, info, false
	}
	if info.errors {
		return nil, info, false
	}
	return p, info, true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func (w *walker) isLet(name string) bool {
	for _, l := range w.lets {
		if l == name {
			return true
		}
	}
	return false
}

func chainOf(n ast.Node) (root string, segs []seg, ok bool) {
	for {
		switch t := n.(type) {
		case *ast.ChainNode:
			n = t.Node
		case *ast.MemberNode:
			switch p := t.Property.(type) {
			case *ast.StringNode:
				segs = append([]seg{{name: p.Value}}, segs...)
			case *ast.IntegerNode:
				segs = append([]seg{{index: true}}, segs...)
			default:
				segs = append([]seg{{index: true, dyn: p}}, segs...)
			}
			n = t.Node
		case *ast.IdentifierNode:
			return t.Value, segs, true
		case *ast.PointerNode:
			if t.Name == "" {
				return "#", segs, true
			}
			return "#" + t.Name, segs, true
		default:
			return "", nil, false
		}
	}
}

func (w *walker) walk(n ast.Node) {
	switch t := n.(type) {
	case nil:
	case *ast.ChainNode:
		w.walk(t.Node)
	case *ast.MemberNode:
		if root, segs, ok := chainOf(t); ok {
			w.resolve(root, segs, true)
			for _, s := range segs {
				if s.dyn != nil {
					w.walk(s.dyn)
				}
			}
			return
		}
		w.walk(t.Node)
		w.walk(t.Property)
	case *ast.IdentifierNode:
		w.resolve(t.Value, nil, true)
	case *ast.PointerNode:
		if t.Name == "" && len(w.elems) > 0 {
			e := w.elems[len(w.elems)-1]
			if e.taint.overlaps(e.path) {
				w.info.tainted = true
			}
		}
	case *ast.CallNode:
		w.call(t)
	case *ast.BuiltinNode:
		w.builtin(t)
	case *ast.PredicateNode:
		w.walk(t.Node)
	case *ast.VariableDeclaratorNode:
		w.walk(t.Value)
		w.lets = append(w.lets, t.Name)
		w.walk(t.Expr)
		w.lets = w.lets[:len(w.lets)-1]
	case *ast.UnaryNode:
		w.walk(t.Node)
	case *ast.BinaryNode:
		w.walk(t.Left)
		w.walk(t.Right)
	case *ast.ConditionalNode:
		w.walk(t.Cond)
		w.walk(t.Exp1)
		w.walk(t.Exp2)
	case *ast.SliceNode:
		w.walk(t.Node)
		w.walk(t.From)
		w.walk(t.To)
	case *ast.SequenceNode:
		for _, e := range t.Nodes {
			w.walk(e)
		}
	case *ast.ArrayNode:
		for _, e := range t.Nodes {
			w.walk(e)
		}
	case *ast.MapNode:
		for _, p := range t.Pairs {
			w.walk(p)
		}
	case *ast.PairNode:
		w.walk(t.Key)
		w.walk(t.Value)
	}
}

func (w *walker) call(t *ast.CallNode) {
	id, isIdent := t.Callee.(*ast.IdentifierNode)
	if !isIdent {
		w.walk(t.Callee)
		for _, a := range t.Arguments {
			w.walk(a)
		}
		return
	}
	name := id.Value
	if !knownFunction(name) && !w.isLet(name) {
		if name == "now" {
			w.b.errorf("UNKNOWN_FUNCTION", w.path, "now() is disabled so decisions can be replayed; use ctx.now")
		} else {
			w.b.errorf("UNKNOWN_FUNCTION", w.path, "unknown function %s()", name)
		}
	}
	dep := map[string]*[]string{"lookup": &w.info.lookups, "inSegment": &w.info.segments, "gate": &w.info.gates, "variant": &w.info.experiments}
	if list, ok := dep[name]; ok {
		if len(t.Arguments) == 0 {
			w.b.errorf("PARSE_ERROR", w.path, "%s() needs a name argument", name)
		} else if s, ok := t.Arguments[0].(*ast.StringNode); ok {
			*list = append(*list, s.Value)
			w.b.checkRef(name, s.Value, w.path)
		} else {
			w.b.errorf("NAME_NOT_LITERAL", w.path, "the first argument of %s() must be a literal name so the bundle knows what the decision reads", name)
		}
	}
	if name == "gate" || name == "variant" || name == "bucket" {
		// read ctx.unit; nothing to check
	}
	for _, a := range t.Arguments {
		w.walk(a)
	}
}

var predicateBuiltins = map[string]bool{
	"all": true, "none": true, "any": true, "one": true, "filter": true, "map": true, "count": true, "sum": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "groupBy": true, "sortBy": true, "reduce": true,
}

func (w *walker) builtin(t *ast.BuiltinNode) {
	if t.Name == "now" {
		w.b.errorf("UNKNOWN_FUNCTION", w.path, "now() is disabled so decisions can be replayed; use ctx.now")
	}
	hasPred := false
	for _, a := range t.Arguments {
		if _, ok := a.(*ast.PredicateNode); ok {
			hasPred = true
		}
	}
	if !predicateBuiltins[t.Name] || !hasPred || len(t.Arguments) == 0 {
		for _, a := range t.Arguments {
			w.walk(a)
		}
		return
	}
	coll := t.Arguments[0]
	w.walk(coll)
	elem := elemCtx{}
	steps := -1.0
	if root, segs, ok := chainOf(coll); ok {
		s, known, tt, tpath, label := w.lookupChain(root, segs)
		elem.taint, elem.path = tt, append(tpath, "[]")
		if known && s != nil {
			types := s.EffectiveTypes()
			if types == 0 || types&jsonschema.TArr != 0 {
				if max, ok := s.MaxItemsOf(); ok {
					steps = float64(max)
				} else if types&jsonschema.TArr != 0 {
					w.b.errorf("COST_TOO_HIGH", w.path, "%s() loops over %s, which has no maxItems in %s; every array a rule loops over must declare maxItems", t.Name, chainText(root, segs), label)
				}
				elem.schema, elem.known = s.Element(), true
				if elem.schema == nil {
					elem.known = false
				}
			}
		}
	} else if bin, ok := coll.(*ast.BinaryNode); ok && bin.Operator == ".." {
		if hi, ok := bin.Right.(*ast.IntegerNode); ok {
			lo := 0
			if l, ok := bin.Left.(*ast.IntegerNode); ok {
				lo = l.Value
			}
			steps = float64(hi.Value - lo + 1)
		}
	}
	saved := w.mult
	if steps >= 0 {
		w.mult *= steps
		if w.mult > MaxLoopSteps {
			w.b.errorf("COST_TOO_HIGH", w.path, "worst case of nested loops is %.0f element steps (limit %d)", w.mult, MaxLoopSteps)
		}
	}
	w.elems = append(w.elems, elem)
	for _, a := range t.Arguments[1:] {
		w.walk(a)
	}
	w.elems = w.elems[:len(w.elems)-1]
	w.mult = saved
}

func chainText(root string, segs []seg) string {
	var b strings.Builder
	b.WriteString(root)
	for _, s := range segs {
		if s.index {
			b.WriteString("[]")
		} else {
			b.WriteByte('.')
			b.WriteString(s.name)
		}
	}
	return b.String()
}

// lookupChain resolves a chain to its schema (if known) and taint position
// without reporting problems.
func (w *walker) lookupChain(root string, segs []seg) (s *jsonschema.Schema, known bool, t *taint, tpath []string, label string) {
	var base sview
	switch {
	case root == "input":
		base = w.sc.input
	case root == "request":
		base = w.sc.request
	case root == "nodes":
		if len(segs) == 0 || w.sc.nodes == nil {
			return nil, false, nil, nil, ""
		}
		nv := w.sc.nodes[segs[0].name]
		if nv == nil {
			return nil, false, nil, nil, ""
		}
		base = nv.sview
		segs = segs[1:]
	case root == "#":
		if len(w.elems) == 0 {
			return nil, false, nil, nil, ""
		}
		e := w.elems[len(w.elems)-1]
		base = sview{schema: e.schema, taint: e.taint}
		tpath = append([]string(nil), e.path...)
		if !e.known {
			base.schema = nil
		}
	case root == "out":
		base = sview{taint: w.sc.out}
	default:
		return nil, false, nil, nil, ""
	}
	s, known = base.schema, base.schema != nil
	t = base.taint
	for _, sg := range segs {
		if sg.index {
			tpath = append(tpath, "[]")
		} else {
			tpath = append(tpath, sg.name)
		}
		if !known || s == nil {
			known = false
			continue
		}
		if sg.index {
			s = s.Element()
		} else {
			sub, ok := s.Property(sg.name)
			if !ok {
				return nil, false, t, tpath, base.label
			}
			s = sub
		}
		if s == nil {
			known = false
		}
	}
	return s, known, t, tpath, base.label
}

// resolve checks a member chain against the scope and records taint.
func (w *walker) resolve(root string, segs []seg, report bool) {
	if w.isLet(root) || root == "$env" || strings.HasPrefix(root, "#") && root != "#" {
		return
	}
	if root == "#" {
		if len(w.elems) == 0 {
			return
		}
		e := w.elems[len(w.elems)-1]
		w.checkSegs(sview{schema: e.schema}, e.known && e.schema != nil, "element", segs)
		tp := append([]string(nil), e.path...)
		for _, s := range segs {
			if s.index {
				tp = append(tp, "[]")
			} else {
				tp = append(tp, s.name)
			}
		}
		if e.taint.overlaps(tp) {
			w.info.tainted = true
		}
		return
	}
	if _, isVar := envSample()[root]; !isVar {
		w.b.errorf("UNKNOWN_FIELD", w.path, "unknown variable %q (available: %s)", root, varList(w.sc.vars))
		return
	}
	if !w.sc.vars[root] {
		w.b.errorf("UNKNOWN_FIELD", w.path, "variable %q is not available here (available: %s)", root, varList(w.sc.vars))
		return
	}
	tp := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.index {
			tp = append(tp, "[]")
		} else {
			tp = append(tp, s.name)
		}
	}
	switch root {
	case "ctx":
		if len(segs) > 0 && !segs[0].index && !ctxFields[segs[0].name] {
			w.b.errorf("UNKNOWN_FIELD", w.path, "ctx has no field %q (now, unit, requestId, env)", segs[0].name)
		}
	case "input":
		w.checkSegs(w.sc.input, w.sc.input.schema != nil, "input", segs)
		if w.sc.input.taint.overlaps(tp) {
			w.info.tainted = true
		}
	case "request":
		w.checkSegs(w.sc.request, w.sc.request.schema != nil, "request", segs)
		if w.sc.request.taint.overlaps(tp) {
			w.info.tainted = true
		}
	case "out":
		if w.sc.out.overlaps(tp) {
			w.info.tainted = true
		}
	case "nodes":
		if len(segs) == 0 {
			for _, nv := range w.sc.nodes {
				if !nv.taint.empty() {
					w.info.tainted = true
				}
			}
			return
		}
		if segs[0].index {
			return
		}
		id := segs[0].name
		nv, ok := w.sc.nodes[id]
		if !ok {
			w.b.errorf("UNKNOWN_FIELD", w.path, "nodes.%s: there is no node %q in this decision before this point", id, id)
			return
		}
		w.info.nodesRead = append(w.info.nodesRead, id)
		if nv.mayNotRun {
			w.b.warnf("NODE_MAY_NOT_HAVE_RUN", w.path, "nodes.%s may not have run on every path to this node; it reads as nil when it did not (use ?? for a default)", id)
		}
		w.checkSegs(nv.sview, nv.schema != nil, "nodes."+id, segs[1:])
		if nv.taint.overlaps(tp[1:]) {
			w.info.tainted = true
		}
	}
}

func varList(m map[string]bool) string {
	var out []string
	for _, k := range []string{"input", "request", "nodes", "out", "value", "ctx"} {
		if m[k] {
			out = append(out, k)
		}
	}
	return strings.Join(out, ", ")
}

func (w *walker) checkSegs(base sview, known bool, rootText string, segs []seg) {
	if !known {
		return
	}
	s := base.schema
	text := rootText
	for _, sg := range segs {
		if s == nil {
			return
		}
		types := s.EffectiveTypes()
		if sg.index {
			if types != 0 && types&(jsonschema.TArr|jsonschema.TObj) == 0 {
				w.b.errorf("TYPE_MISMATCH", w.path, "%s is %s in %s; it cannot be indexed", text, types, base.label)
				return
			}
			text += "[]"
			s = s.Element()
			continue
		}
		if types != 0 && types&jsonschema.TObj == 0 {
			w.b.errorf("TYPE_MISMATCH", w.path, "%s is %s in %s; it has no field %q", text, types, base.label, sg.name)
			return
		}
		sub, ok := s.Property(sg.name)
		if !ok {
			w.b.errorf("UNKNOWN_FIELD", w.path, "%s.%s is not in %s", text, sg.name, labelOr(base.label))
			return
		}
		text += "." + sg.name
		s = sub
	}
}

func labelOr(l string) string {
	if l == "" {
		return "the schema"
	}
	return l
}

// fieldSchema resolves a dotted field path (structured conditions).
func fieldSchema(base sview, parts []pathPart) (s *jsonschema.Schema, known bool, msg string) {
	s = base.schema
	if s == nil {
		return nil, false, ""
	}
	text := ""
	for _, p := range parts {
		if s == nil {
			return nil, false, ""
		}
		types := s.EffectiveTypes()
		if p.index >= 0 {
			if types != 0 && types&jsonschema.TArr == 0 {
				return nil, false, fmt.Sprintf("%s is %s in %s; it cannot be indexed", text, types, base.label)
			}
			s = s.Element()
			text += fmt.Sprintf("[%d]", p.index)
			continue
		}
		if types != 0 && types&jsonschema.TObj == 0 {
			return nil, false, fmt.Sprintf("%s is %s in %s; it has no field %q", text, types, base.label, p.key)
		}
		sub, ok := s.Property(p.key)
		if !ok {
			if text == "" {
				return nil, false, fmt.Sprintf("%s is not in %s", p.key, labelOr(base.label))
			}
			return nil, false, fmt.Sprintf("%s.%s is not in %s", text, p.key, labelOr(base.label))
		}
		if text != "" {
			text += "."
		}
		text += p.key
		s = sub
	}
	return s, s != nil, ""
}
