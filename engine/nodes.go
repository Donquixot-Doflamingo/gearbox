// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// node is one compiled decision node.
type node struct {
	id, typ string
	idx     int
	from    *program
	next    *node
	end     bool // next: "end"
	onError *node
	exec    func(r *run, n *node, in any) (out any, outcome string, next *node, err error)

	inSchema *jsonschema.Schema // validated at runtime (rules, table)
	inLabel  string
	outCheck *jsonschema.Schema // output validated at runtime
	outLabel string

	rules *rulesNode
	table *tableNode
	tr    *transformNode
	sw    *switchNode
	lk    *lookupNode
	gate  string
	exp   string
	sub   *subNode
	ai    *aiNode
}

// inputError is a node input that does not match its inputSchema.
type inputError struct {
	label  string
	fields []FieldError
}

func (e *inputError) Error() string {
	if len(e.fields) == 0 {
		return "input does not match " + e.label
	}
	return fmt.Sprintf("input does not match %s: %s %s", e.label, e.fields[0].Path, e.fields[0].Message)
}

func (n *node) checkInput(in any) error {
	if n.inSchema == nil || n.inSchema.Valid(in) {
		return nil
	}
	errs := n.inSchema.Validate(in)
	out := make([]FieldError, len(errs))
	for i, e := range errs {
		out[i] = FieldError{Path: e.Path, Message: e.Message}
	}
	ie := &inputError{label: n.inLabel, fields: out}
	for _, e := range errs {
		if e.Keyword == "maxItems" {
			return &tooLargeError{inputError: ie}
		}
	}
	return ie
}

type tooLargeError struct{ *inputError }

func (n *node) checkOutput(out any) error {
	if n.outCheck == nil || n.outCheck.Valid(out) {
		return nil
	}
	errs := n.outCheck.Validate(out)
	return fmt.Errorf("output does not match %s: %s %s", n.outLabel, errs[0].Path, errs[0].Message)
}

// ---- rules

type compiledRule struct {
	id      string
	when    condFn
	then    *dataSlot
	percent *float64
	salt    string
}

type rulesNode struct {
	all      bool
	rules    []compiledRule
	def      *dataSlot
	checkOut bool
}

func (b *builder) rulesNode(d *decision, n *node, ns *nodeSpec, path string, sc *scope, out *jsonschema.Schema, outLabel string) *taint {
	rn := &rulesNode{}
	switch ns.Mode {
	case "", "first_match":
	case "all_matches":
		rn.all = true
	default:
		b.errorf("INVALID_SPEC", path+"/mode", "mode must be first_match or all_matches")
	}
	if len(ns.Rules) > MaxRules {
		b.errorf("INVALID_SPEC", path+"/rules", "a rules node has at most %d rules", MaxRules)
	}
	t := &taint{}
	seen := map[string]bool{}
	type ordered struct {
		cr  compiledRule
		pri int
		pos int
	}
	var list []ordered
	idRE := regexp.MustCompile(`^[a-zA-Z0-9_]{1,40}$`)
	for i, rs := range ns.Rules {
		p := fmt.Sprintf("%s/rules/%d", path, i)
		if !idRE.MatchString(rs.ID) {
			b.errorf("INVALID_SPEC", p+"/id", "rule id must match ^[a-zA-Z0-9_]{1,40}$")
		} else if seen[rs.ID] {
			b.errorf("DUPLICATE_ID", p+"/id", "duplicate rule id %q", rs.ID)
		}
		seen[rs.ID] = true
		if isNull(rs.When) {
			b.errorf("INVALID_SPEC", p+"/when", "when is required")
			continue
		}
		cr := compiledRule{id: rs.ID}
		cr.when = b.cond(rs.When, p+"/when", sc)
		cr.then = b.data(rs.Then, p+"/then", sc)
		if cr.then != nil && !cr.then.present {
			b.errorf("INVALID_SPEC", p+"/then", "then is required")
			continue
		}
		if cr.when == nil || cr.then == nil {
			continue
		}
		b.checkConstAgainst(cr.then, out, outLabel, p+"/then")
		if !cr.then.constant {
			rn.checkOut = true
		}
		t.union(cr.then.taint)
		if rs.Rollout != nil {
			if rs.Rollout.Percent == nil || *rs.Rollout.Percent < 0 || *rs.Rollout.Percent > 100 {
				b.errorf("INVALID_SPEC", p+"/rollout/percent", "rollout percent must be between 0 and 100")
				continue
			}
			cr.percent = rs.Rollout.Percent
			cr.salt = rs.Rollout.Salt
			if cr.salt == "" {
				cr.salt = d.name + "." + n.id + "." + rs.ID
			}
		}
		if rs.Enabled != nil && !*rs.Enabled {
			continue
		}
		list = append(list, ordered{cr: cr, pri: rs.Priority, pos: i})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].pri > list[j].pri })
	for _, o := range list {
		rn.rules = append(rn.rules, o.cr)
	}
	if len(ns.Default) > 0 {
		rn.def = b.data(ns.Default, path+"/default", sc)
		if rn.def != nil {
			b.checkConstAgainst(rn.def, out, outLabel, path+"/default")
			if !rn.def.constant {
				rn.checkOut = true
			}
			t.union(rn.def.taint)
		}
	}
	n.rules = rn
	n.exec = execRules
	if rn.checkOut {
		n.outCheck, n.outLabel = out, outLabel
	}
	if t.empty() {
		return nil
	}
	return t
}

func (r *run) ruleRollout(cr *compiledRule) bool {
	if cr.percent == nil {
		return true
	}
	if r.cc.Unit == "" {
		return false
	}
	return r.c.bucket(cr.salt, r.cc.Unit) < int(*cr.percent*100)
}

func execRules(r *run, n *node, in any) (any, string, *node, error) {
	if err := n.checkInput(in); err != nil {
		return nil, "", nil, err
	}
	rn := n.rules
	if !rn.all {
		for i := range rn.rules {
			cr := &rn.rules[i]
			if cr.percent != nil && r.cc.Unit == "" {
				continue
			}
			ok, err := cr.when(r, in)
			if err != nil {
				return nil, "", nil, fmt.Errorf("rule %s: %w", cr.id, err)
			}
			if !ok || !r.ruleRollout(cr) {
				continue
			}
			out, err := cr.then.eval(r, in)
			if err != nil {
				return nil, "", nil, fmt.Errorf("rule %s then: %w", cr.id, err)
			}
			if !cr.then.constant {
				if err := n.checkOutput(out); err != nil {
					return nil, "", nil, fmt.Errorf("rule %s then: %w", cr.id, err)
				}
			}
			if cr.then.constant {
				r.lastRaw = cr.then.raw
			}
			return out, cr.id, nil, nil
		}
		if rn.def == nil || !rn.def.present {
			return nil, "", nil, errors.New("no rule matched and the node has no default")
		}
		out, err := rn.def.eval(r, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("default: %w", err)
		}
		if !rn.def.constant {
			if err := n.checkOutput(out); err != nil {
				return nil, "", nil, fmt.Errorf("default: %w", err)
			}
		}
		if rn.def.constant {
			r.lastRaw = rn.def.raw
		}
		return out, "default", nil, nil
	}
	var matches []any
	var ids []string
	for i := range rn.rules {
		cr := &rn.rules[i]
		if cr.percent != nil && r.cc.Unit == "" {
			continue
		}
		ok, err := cr.when(r, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("rule %s: %w", cr.id, err)
		}
		if !ok || !r.ruleRollout(cr) {
			continue
		}
		out, err := cr.then.eval(r, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("rule %s then: %w", cr.id, err)
		}
		if !cr.then.constant {
			if err := n.checkOutput(out); err != nil {
				return nil, "", nil, fmt.Errorf("rule %s then: %w", cr.id, err)
			}
		}
		matches = append(matches, out)
		ids = append(ids, cr.id)
	}
	if len(matches) == 0 {
		matches = []any{}
		if rn.def != nil && rn.def.present {
			out, err := rn.def.eval(r, in)
			if err != nil {
				return nil, "", nil, fmt.Errorf("default: %w", err)
			}
			matches = append(matches, out)
		}
		return map[string]any{"matches": matches}, "default", nil, nil
	}
	return map[string]any{"matches": matches}, strings.Join(ids, ","), nil, nil
}

// ---- decision tables

type cellFn func(r *run, in, v any) (bool, error)

type tableRow struct {
	id       string
	priority int
	cells    []cellFn
	then     *dataSlot
}

type tableNode struct {
	policy   string
	inputs   []*program
	names    []string
	rows     []tableRow
	def      *dataSlot
	checkOut bool
}

func (b *builder) tableNode(n *node, ns *nodeSpec, path string, sc *scope, out *jsonschema.Schema, outLabel string) *taint {
	tn := &tableNode{policy: ns.HitPolicy}
	switch tn.policy {
	case "":
		tn.policy = "first"
	case "first", "unique", "collect", "priority":
	default:
		b.errorf("INVALID_SPEC", path+"/hitPolicy", "hitPolicy must be first, unique, collect or priority")
	}
	if len(ns.Rows) > MaxTableRows {
		b.errorf("INVALID_SPEC", path+"/rows", "a table has at most %d rows", MaxTableRows)
	}
	idx := map[string]int{}
	for i, in := range ns.Inputs {
		p := fmt.Sprintf("%s/inputs/%d", path, i)
		if in.Name == "" {
			b.errorf("INVALID_SPEC", p+"/name", "input name is required")
			continue
		}
		if _, dup := idx[in.Name]; dup {
			b.errorf("DUPLICATE_ID", p+"/name", "duplicate input %q", in.Name)
			continue
		}
		prog, _, ok := b.expr(in.Expr, p+"/expr", sc)
		if !ok {
			continue
		}
		idx[in.Name] = len(tn.inputs)
		tn.inputs = append(tn.inputs, prog)
		tn.names = append(tn.names, in.Name)
	}
	outputs := map[string]bool{}
	for i, o := range ns.Outputs {
		outputs[o] = true
		if out != nil {
			if _, known := out.Property(o); !known {
				b.errorf("UNKNOWN_FIELD", fmt.Sprintf("%s/outputs/%d", path, i), "output %q is not in %s", o, outLabel)
			}
		}
	}
	t := &taint{}
	cellScope := *sc
	cellScope.vars = varsCell
	seen := map[string]bool{}
	for i, row := range ns.Rows {
		p := fmt.Sprintf("%s/rows/%d", path, i)
		if row.ID == "" {
			b.errorf("INVALID_SPEC", p+"/id", "row id is required")
		} else if seen[row.ID] {
			b.errorf("DUPLICATE_ID", p+"/id", "duplicate row id %q", row.ID)
		}
		seen[row.ID] = true
		tr := tableRow{id: row.ID, priority: row.Priority, cells: make([]cellFn, len(tn.inputs))}
		names := make([]string, 0, len(row.When))
		for k := range row.When {
			names = append(names, k)
		}
		sort.Strings(names)
		ok := true
		for _, name := range names {
			cp := p + "/when/" + ptrEscape(name)
			j, known := idx[name]
			if !known {
				b.errorf("UNKNOWN_FIELD", cp, "%q is not a table input", name)
				ok = false
				continue
			}
			c, good := b.cell(row.When[name], cp, &cellScope)
			if !good {
				ok = false
				continue
			}
			tr.cells[j] = c
		}
		var thenMap map[string]any
		if err := json.Unmarshal(row.Then, &thenMap); err != nil || thenMap == nil {
			b.errorf("INVALID_SPEC", p+"/then", "then must be an object of output values")
			continue
		}
		for k := range thenMap {
			if len(outputs) > 0 && !outputs[k] {
				b.errorf("UNKNOWN_FIELD", p+"/then/"+ptrEscape(k), "%q is not one of the table outputs", k)
				ok = false
			}
		}
		tr.then = b.data(row.Then, p+"/then", sc)
		if tr.then == nil || !ok {
			continue
		}
		b.checkConstAgainst(tr.then, out, outLabel, p+"/then")
		if !tr.then.constant {
			tn.checkOut = true
		}
		t.union(tr.then.taint)
		tn.rows = append(tn.rows, tr)
	}
	if len(ns.Default) > 0 {
		tn.def = b.data(ns.Default, path+"/default", sc)
		if tn.def != nil {
			b.checkConstAgainst(tn.def, out, outLabel, path+"/default")
			if !tn.def.constant {
				tn.checkOut = true
			}
		}
	}
	n.table = tn
	n.exec = execTable
	if tn.checkOut && tn.policy != "collect" {
		n.outCheck, n.outLabel = out, outLabel
	}
	if t.empty() {
		return nil
	}
	return t
}

var rangeCell = regexp.MustCompile(`^([\[\(])\s*(-?[0-9.eE+]+)\s*\.\.\s*(-?[0-9.eE+]+)\s*([\]\)])$`)

// cell compiles one table cell.
func (b *builder) cell(src, path string, sc *scope) (cellFn, bool) {
	s := strings.TrimSpace(src)
	if s == "" || s == "-" {
		return nil, true
	}
	if m := rangeCell.FindStringSubmatch(s); m != nil {
		lo, err1 := strconv.ParseFloat(m[2], 64)
		hi, err2 := strconv.ParseFloat(m[3], 64)
		if err1 != nil || err2 != nil {
			b.errorf("PARSE_ERROR", path, "invalid range %q", s)
			return nil, false
		}
		loIncl, hiIncl := m[1] == "[", m[4] == "]"
		return func(_ *run, _ any, v any) (bool, error) {
			f, ok := toNumber(v)
			if !ok {
				return false, nil
			}
			return (f > lo || loIncl && f == lo) && (f < hi || hiIncl && f == hi), nil
		}, true
	}
	for _, op := range []string{"<=", ">=", "!=", "==", "<", ">", "="} {
		if !strings.HasPrefix(s, op) {
			continue
		}
		rest := strings.TrimSpace(s[len(op):])
		var lit any
		if err := json.Unmarshal([]byte(rest), &lit); err != nil {
			lit = rest
		}
		num, isNum := toFloat(lit)
		return func(_ *run, _ any, v any) (bool, error) {
			if v == nil {
				return false, nil
			}
			switch op {
			case "=", "==":
				return valuesEqual(v, lit), nil
			case "!=":
				return !valuesEqual(v, lit), nil
			}
			var c int
			if isNum {
				f, ok := toNumber(v)
				if !ok {
					return false, nil
				}
				switch {
				case f < num:
					c = -1
				case f > num:
					c = 1
				}
			} else {
				ls, ok1 := lit.(string)
				vs, ok2 := v.(string)
				if !ok1 || !ok2 {
					return false, nil
				}
				c = strings.Compare(vs, ls)
			}
			switch op {
			case "<":
				return c < 0, nil
			case "<=":
				return c <= 0, nil
			case ">":
				return c > 0, nil
			}
			return c >= 0, nil
		}, true
	}
	var lit any
	if err := json.Unmarshal([]byte(s), &lit); err == nil {
		if list, ok := lit.([]any); ok {
			return func(_ *run, _ any, v any) (bool, error) {
				if v == nil {
					return false, nil
				}
				for _, e := range list {
					if valuesEqual(v, e) {
						return true, nil
					}
				}
				return false, nil
			}, true
		}
		if _, isMap := lit.(map[string]any); !isMap {
			return func(_ *run, _ any, v any) (bool, error) { return v != nil && valuesEqual(v, lit), nil }, true
		}
	}
	p, _, ok := b.expr(s, path, sc)
	if !ok {
		return nil, false
	}
	return func(r *run, in, v any) (bool, error) {
		out, err := r.evalValue(p, in, v)
		if err != nil {
			return false, err
		}
		switch t := out.(type) {
		case bool:
			return t, nil
		case nil:
			return false, nil
		}
		return false, fmt.Errorf("cell %q returned %s, not a boolean", src, kindName(out))
	}, true
}

func execTable(r *run, n *node, in any) (any, string, *node, error) {
	if err := n.checkInput(in); err != nil {
		return nil, "", nil, err
	}
	tn := n.table
	var vbuf [8]any
	vals := vbuf[:0]
	for i, p := range tn.inputs {
		v, err := r.eval(p, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("input %s: %w", tn.names[i], err)
		}
		vals = append(vals, v)
	}
	var matched []*tableRow
	for i := range tn.rows {
		row := &tn.rows[i]
		ok := true
		for j, c := range row.cells {
			if c == nil {
				continue
			}
			m, err := c(r, in, vals[j])
			if err != nil {
				return nil, "", nil, fmt.Errorf("row %s %s: %w", row.id, tn.names[j], err)
			}
			if !m {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if tn.policy == "first" {
			out, err := row.then.eval(r, in)
			if err != nil {
				return nil, "", nil, fmt.Errorf("row %s: %w", row.id, err)
			}
			if !row.then.constant {
				if err := n.checkOutput(out); err != nil {
					return nil, "", nil, fmt.Errorf("row %s: %w", row.id, err)
				}
			}
			return out, row.id, nil, nil
		}
		matched = append(matched, row)
	}
	switch {
	case len(matched) == 0:
		if tn.policy == "collect" {
			return map[string]any{"matches": []any{}}, "default", nil, nil
		}
		if tn.def == nil || !tn.def.present {
			return nil, "", nil, errors.New("no row matched and the table has no default")
		}
		out, err := tn.def.eval(r, in)
		if err != nil {
			return nil, "", nil, err
		}
		return out, "default", nil, nil
	case tn.policy == "unique" && len(matched) > 1:
		return nil, "", nil, fmt.Errorf("hit policy unique: rows %s and %s both match", matched[0].id, matched[1].id)
	case tn.policy == "collect":
		list := make([]any, 0, len(matched))
		ids := make([]string, 0, len(matched))
		for _, row := range matched {
			out, err := row.then.eval(r, in)
			if err != nil {
				return nil, "", nil, fmt.Errorf("row %s: %w", row.id, err)
			}
			list = append(list, out)
			ids = append(ids, row.id)
		}
		return map[string]any{"matches": list}, strings.Join(ids, ","), nil, nil
	}
	best := matched[0]
	if tn.policy == "priority" {
		for _, row := range matched[1:] {
			if row.priority > best.priority {
				best = row
			}
		}
	}
	out, err := best.then.eval(r, in)
	if err != nil {
		return nil, "", nil, fmt.Errorf("row %s: %w", best.id, err)
	}
	if !best.then.constant {
		if err := n.checkOutput(out); err != nil {
			return nil, "", nil, fmt.Errorf("row %s: %w", best.id, err)
		}
	}
	return out, best.id, nil, nil
}

// ---- transform and output

type tstep struct {
	key    string
	index  int
	append bool
}

type tfield struct {
	steps []tstep
	p     *program
	text  string
}

type tdefault struct {
	steps []tstep
	d     *dataSlot
}

type transformNode struct {
	patch      bool
	fields     []tfield
	expression *program
	defaults   []tdefault
	drop       [][]tstep
	omitNil    bool
}

func parseTarget(t string) ([]tstep, error) {
	if t == "" {
		return nil, errors.New("target is empty")
	}
	var steps []tstep
	for _, part := range strings.Split(t, ".") {
		if part == "" {
			return nil, fmt.Errorf("empty segment in %q", t)
		}
		name := part
		var suffix []tstep
		for {
			open := strings.IndexByte(name, '[')
			if open < 0 {
				break
			}
			close := strings.IndexByte(name[open:], ']')
			if close < 0 {
				return nil, fmt.Errorf("unclosed [ in %q", t)
			}
			inner := name[open+1 : open+close]
			if inner == "" {
				suffix = append(suffix, tstep{append: true, index: -1})
			} else {
				i, err := strconv.Atoi(inner)
				if err != nil || i < 0 || i > 10000 {
					return nil, fmt.Errorf("invalid index %q in %q", inner, t)
				}
				suffix = append(suffix, tstep{index: i})
			}
			rest := name[open+close+1:]
			if rest != "" && rest[0] != '[' {
				return nil, fmt.Errorf("unexpected %q in %q", rest, t)
			}
			name = name[:open] + rest
		}
		if name != "" {
			steps = append(steps, tstep{key: name, index: -1})
		} else if len(steps) == 0 && len(suffix) > 0 {
			return nil, fmt.Errorf("target %q must start with a field name", t)
		}
		steps = append(steps, suffix...)
	}
	return steps, nil
}

func stepsPath(steps []tstep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		if s.key != "" {
			out[i] = s.key
		} else {
			out[i] = "[]"
		}
	}
	return out
}

func setTarget(cur any, steps []tstep, v any) (any, error) {
	if len(steps) == 0 {
		return v, nil
	}
	st := steps[0]
	switch {
	case st.key != "":
		var m map[string]any
		switch t := cur.(type) {
		case nil:
			m = map[string]any{}
		case map[string]any:
			m = t
		default:
			return nil, fmt.Errorf("cannot set %q on a %s", st.key, kindName(cur))
		}
		child, err := setTarget(m[st.key], steps[1:], v)
		if err != nil {
			return nil, err
		}
		m[st.key] = child
		return m, nil
	case st.append:
		var l []any
		switch t := cur.(type) {
		case nil:
		case []any:
			l = t
		default:
			return nil, fmt.Errorf("cannot append to a %s", kindName(cur))
		}
		child, err := setTarget(nil, steps[1:], v)
		if err != nil {
			return nil, err
		}
		return append(l, child), nil
	default:
		var l []any
		switch t := cur.(type) {
		case nil:
		case []any:
			l = t
		default:
			return nil, fmt.Errorf("cannot index a %s", kindName(cur))
		}
		for len(l) <= st.index {
			l = append(l, nil)
		}
		child, err := setTarget(l[st.index], steps[1:], v)
		if err != nil {
			return nil, err
		}
		l[st.index] = child
		return l, nil
	}
}

func hasTarget(cur any, steps []tstep) bool {
	for _, st := range steps {
		switch {
		case st.key != "":
			m, ok := cur.(map[string]any)
			if !ok {
				return false
			}
			cur, ok = m[st.key]
			if !ok {
				return false
			}
		case st.append:
			return false
		default:
			l, ok := cur.([]any)
			if !ok || st.index >= len(l) {
				return false
			}
			cur = l[st.index]
		}
	}
	return true
}

func dropTarget(cur any, steps []tstep) any {
	if len(steps) == 0 {
		return cur
	}
	st := steps[0]
	last := len(steps) == 1
	switch {
	case st.key != "":
		m, ok := cur.(map[string]any)
		if !ok {
			return cur
		}
		if last {
			delete(m, st.key)
			return m
		}
		if child, ok := m[st.key]; ok {
			m[st.key] = dropTarget(child, steps[1:])
		}
		return m
	case st.append:
		l, ok := cur.([]any)
		if !ok {
			return cur
		}
		for i := range l {
			if last {
				return []any{}
			}
			l[i] = dropTarget(l[i], steps[1:])
		}
		return l
	default:
		l, ok := cur.([]any)
		if !ok || st.index >= len(l) {
			return cur
		}
		if last {
			return append(l[:st.index:st.index], l[st.index+1:]...)
		}
		l[st.index] = dropTarget(l[st.index], steps[1:])
		return l
	}
}

// transformNode compiles transform and output nodes. It returns the output
// taint and a synthesized output schema (nil when unknown).
func (b *builder) transformNode(n *node, ns *nodeSpec, path string, sc *scope, isOutput bool) (*taint, *jsonschema.Schema) {
	tn := &transformNode{omitNil: ns.OmitNil == nil || *ns.OmitNil}
	if !isOutput {
		switch ns.Mode {
		case "", "build":
		case "patch":
			tn.patch = true
		default:
			b.errorf("INVALID_SPEC", path+"/mode", "mode must be build or patch")
		}
	}
	if len(ns.Fields) > 500 {
		b.errorf("INVALID_SPEC", path+"/fields", "at most 500 fields")
	}
	if ns.Expression != "" && len(ns.Fields) > 0 {
		b.errorf("INVALID_SPEC", path+"/expression", "use either fields or expression, not both")
	}
	t := &taint{}
	if tn.patch {
		t.union(sc.input.taint)
	}
	tsc := *sc
	tsc.vars = varsTransform
	tsc.out = &taint{}
	synth := newTargetTree()
	for i, f := range ns.Fields {
		p := fmt.Sprintf("%s/fields/%d", path, i)
		steps, err := parseTarget(f.Target)
		if err != nil {
			b.errorf("PARSE_ERROR", p+"/target", "%s", err)
			continue
		}
		prog, info, ok := b.expr(f.Expr, p+"/expr", &tsc)
		if !ok {
			continue
		}
		if info.tainted {
			t.add(stepsPath(steps))
			tsc.out.add(stepsPath(steps))
		}
		synth.add(steps)
		tn.fields = append(tn.fields, tfield{steps: steps, p: prog, text: f.Target})
	}
	if ns.Expression != "" {
		prog, info, ok := b.expr(ns.Expression, path+"/expression", sc)
		if ok {
			tn.expression = prog
			if info.tainted {
				t.all = true
			}
		}
		synth = nil
	} else if len(ns.Fields) == 0 && !tn.patch {
		b.errorf("INVALID_SPEC", path, "a %s node needs fields or an expression", ns.Type)
	}
	if isOutput && (len(ns.Defaults) > 0 || len(ns.Drop) > 0) {
		b.errorf("INVALID_SPEC", path, "output nodes take fields or expression only")
	}
	for i, d := range ns.Defaults {
		p := fmt.Sprintf("%s/defaults/%d", path, i)
		steps, err := parseTarget(d.Target)
		if err != nil {
			b.errorf("PARSE_ERROR", p+"/target", "%s", err)
			continue
		}
		ds := b.data(d.Value, p+"/value", sc)
		if ds == nil {
			continue
		}
		if !ds.taint.empty() {
			t.add(stepsPath(steps))
		}
		if synth != nil {
			synth.add(steps)
		}
		tn.defaults = append(tn.defaults, tdefault{steps: steps, d: ds})
	}
	for i, d := range ns.Drop {
		steps, err := parseTarget(d)
		if err != nil {
			b.errorf("PARSE_ERROR", fmt.Sprintf("%s/drop/%d", path, i), "%s", err)
			continue
		}
		tn.drop = append(tn.drop, steps)
	}
	n.tr = tn
	n.exec = execTransform
	if t.empty() {
		t = nil
	}
	if tn.patch || synth == nil {
		return t, nil
	}
	return t, synth.schema()
}

func execTransform(r *run, n *node, in any) (any, string, *node, error) {
	tn := n.tr
	var out any
	switch {
	case tn.expression != nil:
		v, err := r.eval(tn.expression, in)
		if err != nil {
			return nil, "", nil, err
		}
		out = normalize(v)
		if len(tn.defaults) > 0 || len(tn.drop) > 0 {
			if _, ok := out.(map[string]any); !ok {
				return nil, "", nil, fmt.Errorf("expression returned %s; defaults and drop need an object", kindName(out))
			}
			out = deepCopy(out)
		}
	case tn.patch:
		m, ok := in.(map[string]any)
		if !ok {
			return nil, "", nil, fmt.Errorf("patch mode needs an object input, got %s", kindName(in))
		}
		out = deepCopy(m)
	default:
		out = make(map[string]any, len(tn.fields))
	}
	if len(tn.fields) > 0 {
		saved := r.env["out"]
		r.env["out"] = out
		for i := range tn.fields {
			f := &tn.fields[i]
			v, err := r.eval(f.p, in)
			if err != nil {
				r.env["out"] = saved
				return nil, "", nil, fmt.Errorf("field %s: %w", f.text, err)
			}
			if v == nil && tn.omitNil {
				continue
			}
			nv, err := setTarget(out, f.steps, normalize(v))
			if err != nil {
				r.env["out"] = saved
				return nil, "", nil, fmt.Errorf("field %s: %w", f.text, err)
			}
			out = nv
			r.env["out"] = out
		}
		r.env["out"] = saved
	}
	for i := range tn.defaults {
		d := &tn.defaults[i]
		if hasTarget(out, d.steps) {
			continue
		}
		v, err := d.d.eval(r, in)
		if err != nil {
			return nil, "", nil, err
		}
		nv, err := setTarget(out, d.steps, deepCopy(v))
		if err != nil {
			return nil, "", nil, err
		}
		out = nv
	}
	for _, d := range tn.drop {
		out = dropTarget(out, d)
	}
	if err := n.checkOutput(out); err != nil {
		return nil, "", nil, err
	}
	return out, "ok", nil, nil
}

// targetTree synthesizes a closed schema from transform targets.
type targetTree struct {
	props map[string]*targetTree
	arr   *targetTree
	leaf  bool
}

func newTargetTree() *targetTree { return &targetTree{} }

func (t *targetTree) add(steps []tstep) {
	cur := t
	for _, s := range steps {
		if s.key != "" {
			if cur.props == nil {
				cur.props = map[string]*targetTree{}
			}
			nx := cur.props[s.key]
			if nx == nil {
				nx = &targetTree{}
				cur.props[s.key] = nx
			}
			cur = nx
		} else {
			if cur.arr == nil {
				cur.arr = &targetTree{}
			}
			cur = cur.arr
		}
	}
	cur.leaf = true
}

func (t *targetTree) schema() *jsonschema.Schema {
	if t.leaf || (t.props == nil && t.arr == nil) {
		return nil
	}
	if t.arr != nil && t.props == nil {
		return jsonschema.ArrayOf(t.arr.schema(), -1)
	}
	props := map[string]*jsonschema.Schema{}
	for k, c := range t.props {
		props[k] = c.schema()
		if props[k] == nil {
			props[k] = &jsonschema.Schema{}
		}
	}
	return jsonschema.ObjectOf(props, nil, true)
}

// ---- switch

type switchCase struct {
	id   string
	when *program
	next *node
}

type switchNode struct {
	cases []switchCase
	def   *node
}

func execSwitch(r *run, n *node, in any) (any, string, *node, error) {
	for i := range n.sw.cases {
		c := &n.sw.cases[i]
		v, err := r.eval(c.when, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("case %s: %w", c.id, err)
		}
		switch t := v.(type) {
		case bool:
			if t {
				r.nodes[n.id] = map[string]any{"case": c.id}
				return in, c.id, c.next, nil
			}
		case nil:
		default:
			return nil, "", nil, fmt.Errorf("case %s returned %s, not a boolean", c.id, kindName(v))
		}
	}
	r.nodes[n.id] = map[string]any{"case": "default"}
	return in, "default", n.sw.def, nil
}

// ---- lookup, gate, experiment

type lookupNode struct {
	name string
	key  *program
	def  *dataSlot
}

func execLookup(r *run, n *node, in any) (any, string, *node, error) {
	k, err := r.eval(n.lk.key, in)
	if err != nil {
		return nil, "", nil, fmt.Errorf("key: %w", err)
	}
	l := r.c.lookups[n.lk.name]
	if l == nil {
		return nil, "", nil, fmt.Errorf("lookup %q is not in the bundle", n.lk.name)
	}
	if ks, ok := scalarString(k); ok {
		if v, found := l.rows[ks]; found {
			return v, "hit", nil, nil
		}
	}
	if n.lk.def != nil && n.lk.def.present {
		v, err := n.lk.def.eval(r, in)
		return v, "miss", nil, err
	}
	return nil, "miss", nil, nil
}

func execGate(r *run, n *node, in any) (any, string, *node, error) {
	res, err := r.gate(n.gate, in)
	if err != nil {
		return nil, "", nil, err
	}
	return map[string]any{"value": res.Value, "rule": res.RuleID}, res.RuleID, nil, nil
}

func execExperiment(r *run, n *node, in any) (any, string, *node, error) {
	res, err := r.variant(n.exp, in)
	if err != nil {
		return nil, "", nil, err
	}
	var params any
	if len(res.Parameters) > 0 {
		_ = json.Unmarshal(res.Parameters, &params)
	}
	out := map[string]any{"variant": res.Variant, "inExperiment": res.InExperiment, "reason": res.Reason, "parameters": params}
	outcome := res.Reason
	if res.InExperiment {
		outcome = res.Variant
	}
	return out, outcome, nil, nil
}

// ---- subdecision

type subNode struct {
	decision string
	input    *program
}

func execSub(r *run, n *node, in any) (any, string, *node, error) {
	subIn := in
	if n.sub.input != nil {
		v, err := r.eval(n.sub.input, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("input: %w", err)
		}
		subIn = normalize(v)
	}
	out, version, err := r.subdecide(n.sub.decision, subIn)
	if err != nil {
		return nil, "", nil, err
	}
	return out, version, nil, nil
}

// ---- AI

type aiNode struct {
	mode       string
	model      string
	timeout    time.Duration
	vars       []aiVar
	system     string
	user       string
	respRaw    json.RawMessage
	resp       *jsonschema.Schema
	maxTokens  int
	decision   string
	decVersion int64
}

type aiVar struct {
	name string
	p    *program
}

var placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// errShadow marks a shadow AI node: the onError path decides.
var errShadow = errors.New("shadow mode: the onError path decides")

func render(tmpl string, vals map[string]any) string {
	return placeholderRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		v := vals[name]
		if s, ok := v.(string); ok {
			return s
		}
		return string(encodeJSON(v))
	})
}

func newCallID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func execAI(r *run, n *node, in any) (any, string, *node, error) {
	a := n.ai
	vals := make(map[string]any, len(a.vars))
	for _, v := range a.vars {
		x, err := r.eval(v.p, in)
		if err != nil {
			return nil, "", nil, fmt.Errorf("var %s: %w", v.name, err)
		}
		vals[v.name] = normalize(x)
	}
	call := AICall{
		Decision: a.decision, DecisionVersion: a.decVersion, Node: n.id, Model: a.model,
		System: render(a.system, vals), User: render(a.user, vals),
		ResponseSchema: a.respRaw, MaxOutputTokens: a.maxTokens, Timeout: a.timeout, CallID: newCallID(),
	}
	if r.ai == nil {
		if a.mode == "shadow" {
			return nil, "", nil, errShadow
		}
		return nil, "", nil, errors.New("AI calls are not available (no AI caller)")
	}
	if a.mode == "shadow" {
		caller := r.ai
		parent := context.WithoutCancel(r.ctx)
		go func() {
			ctx, cancel := context.WithTimeout(parent, a.timeout)
			defer cancel()
			start := time.Now()
			ans, err := caller.Call(ctx, call)
			if err == nil {
				err = validateAnswer(a, ans)
			}
			if rec, ok := caller.(AIShadowRecorder); ok {
				rec.RecordShadow(call, ans, time.Since(start), err)
			}
		}()
		return nil, "", nil, errShadow
	}
	ctx, cancel := context.WithTimeout(r.ctx, a.timeout)
	defer cancel()
	ans, err := r.ai.Call(ctx, call)
	if err != nil {
		return nil, "", nil, fmt.Errorf("AI call: %w", err)
	}
	if err := validateAnswer(a, ans); err != nil {
		return nil, "", nil, err
	}
	var out any
	_ = json.Unmarshal(ans.Output, &out)
	return out, "ok", nil, nil
}

func validateAnswer(a *aiNode, ans AIAnswer) error {
	var out any
	if err := json.Unmarshal(ans.Output, &out); err != nil {
		return fmt.Errorf("AI answer is not JSON: %w", err)
	}
	if errs := a.resp.Validate(out); len(errs) > 0 {
		return fmt.Errorf("AI answer does not match responseSchema: %s %s", errs[0].Path, errs[0].Message)
	}
	return nil
}

// AIShadowRecorder is optionally implemented by an AICaller to receive the
// outcome of shadow-mode calls (they run after the decision returned).
type AIShadowRecorder interface {
	RecordShadow(call AICall, answer AIAnswer, latency time.Duration, err error)
}
