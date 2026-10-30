// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr/parser"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

type decision struct {
	name    string
	version int64
	kind    string
	nodes   []*node
	byID    map[string]*node
	entry   *node

	reqSchema   *jsonschema.Schema // decision-level inputSchema (checked before entry)
	reqLabel    string
	outSchema   *jsonschema.Schema
	outLabel    string
	fallback    *dataSlot
	tests       []testCase
	budget      int64
	logSample   *float64
	logFields   []logField
	aiPayloads  bool
	hasAI       bool
	deps        []Ref
	depHash     string
	versionText string
}

type logField struct {
	name   string
	output bool
	parts  []pathPart
}

type testCase struct {
	name        string
	now         time.Time
	unit        string
	input       json.RawMessage
	expect      any
	hasExpect   bool
	expectError string
	expectPath  []string
}

var nodeTypes = map[string]bool{
	"rules": true, "table": true, "transform": true, "switch": true, "lookup": true,
	"gate": true, "experiment": true, "subdecision": true, "ai": true, "output": true,
}

type synthSchemas struct {
	gate, experiment, sw *jsonschema.Schema
}

func newSynthSchemas() synthSchemas {
	str := jsonschema.Typed(jsonschema.TStr)
	boolean := jsonschema.Typed(jsonschema.TBool)
	return synthSchemas{
		gate:       jsonschema.ObjectOf(map[string]*jsonschema.Schema{"value": boolean, "rule": str}, []string{"value", "rule"}, true),
		experiment: jsonschema.ObjectOf(map[string]*jsonschema.Schema{"variant": str, "inExperiment": boolean, "reason": str, "parameters": {}}, []string{"variant", "inExperiment", "reason"}, true),
		sw:         jsonschema.ObjectOf(map[string]*jsonschema.Schema{"case": str}, []string{"case"}, true),
	}
}

// subDecision resolves a decision used by a subdecision node.
func (b *builder) subDecision(name, path string) *decision {
	for _, s := range b.stack {
		if s == name {
			b.errorf("CYCLE", path, "subdecision %q leads back to itself (%s → %s)", name, strings.Join(b.stack, " → "), name)
			return nil
		}
	}
	r, ok := b.liveResource(KindDecision, name)
	if !ok {
		b.errorf("DECISION_NOT_FOUND", path, "decision %q does not exist", name)
		return nil
	}
	b.dep(KindDecision, name, 0)
	k := key(KindDecision, r.Name, r.Version)
	if d, ok := b.decisions[k]; ok {
		return d
	}
	if b.invalid[k] {
		b.errorf("DECISION_NOT_FOUND", path, "decision %q is not valid", name)
		return nil
	}
	var d *decision
	problems, _ := b.isolated(func() { d = b.decisionResource(r) })
	for _, p := range problems {
		if p.Code == "CYCLE" {
			b.errorf("CYCLE", path, "%s", p.Message)
			return nil
		}
	}
	if hasErrors(problems) || d == nil {
		b.invalid[k] = true
		b.errorf("DECISION_NOT_FOUND", path, "decision %q is not valid", name)
		return nil
	}
	return d
}

type edge struct {
	from    int
	onError bool
}

// decisionResource compiles a DecisionSpec.
func (b *builder) decisionResource(r Resource) *decision {
	k := key(KindDecision, r.Name, r.Version)
	b.stack = append(b.stack, r.Name)
	defer func() { b.stack = b.stack[:len(b.stack)-1] }()

	var spec decisionSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	d := &decision{name: r.Name, version: r.Version, byID: map[string]*node{}, budget: defaultBudgetMic}
	d.versionText = versionText(r.Name, r.Version)
	if spec.BudgetMicros != nil {
		if *spec.BudgetMicros < 1 {
			b.errorf("INVALID_SPEC", "/budgetMicros", "budgetMicros must be positive")
		} else {
			d.budget = *spec.BudgetMicros
		}
	}
	if len(spec.Nodes) == 0 {
		b.errorf("INVALID_SPEC", "/nodes", "a decision needs at least one node")
		return nil
	}
	if len(spec.Nodes) > MaxNodes {
		b.errorf("INVALID_SPEC", "/nodes", "a decision has at most %d nodes", MaxNodes)
		return nil
	}
	if len(spec.Tests) > MaxTests {
		b.errorf("INVALID_SPEC", "/tests", "at most %d test cases", MaxTests)
	}

	// Pass 1: decode nodes.
	specs := make([]nodeSpec, len(spec.Nodes))
	okNodes := true
	for i, raw := range spec.Nodes {
		p := fmt.Sprintf("/nodes/%d", i)
		if err := decodeStrict(raw, &specs[i]); err != nil {
			pp, m := decodeError(p, err)
			b.errorf("INVALID_SPEC", pp, "%s", m)
			okNodes = false
			continue
		}
		ns := &specs[i]
		if !nodeIDOK(ns.ID) {
			b.errorf("INVALID_SPEC", p+"/id", "node id %q must match ^[a-z][a-z0-9_]{0,62}$", ns.ID)
			okNodes = false
			continue
		}
		if !nodeTypes[ns.Type] {
			b.errorf("INVALID_SPEC", p+"/type", "unknown node type %q", ns.Type)
			okNodes = false
			continue
		}
		if ns.ID == "end" {
			b.errorf("INVALID_SPEC", p+"/id", `"end" is reserved`)
			okNodes = false
			continue
		}
		if _, dup := d.byID[ns.ID]; dup {
			b.errorf("DUPLICATE_ID", p+"/id", "duplicate node id %q", ns.ID)
			okNodes = false
			continue
		}
		n := &node{id: ns.ID, typ: ns.Type, idx: i}
		d.byID[ns.ID] = n
		d.nodes = append(d.nodes, n)
	}
	if !okNodes {
		return nil
	}
	entry, ok := d.byID[spec.Entry]
	if !ok {
		b.errorf("UNKNOWN_NODE", "/entry", "entry %q is not a node of this decision", spec.Entry)
		return nil
	}
	d.entry = entry

	// Edges.
	n := len(d.nodes)
	succ := make([][]int, n)
	preds := make([][]edge, n)
	link := func(from int, target, path string, onError bool) *node {
		if target == "" || (target == "end" && !onError) {
			return nil
		}
		t, ok := d.byID[target]
		if !ok {
			b.errorf("UNKNOWN_NODE", path, "node %q does not exist", target)
			okNodes = false
			return nil
		}
		succ[from] = append(succ[from], t.idx)
		preds[t.idx] = append(preds[t.idx], edge{from: from, onError: onError})
		return t
	}
	for i, ns := range specs {
		nd := d.nodes[i]
		p := fmt.Sprintf("/nodes/%d", i)
		nd.next = link(i, ns.Next, p+"/next", false)
		nd.end = ns.Next == "end"
		nd.onError = link(i, ns.OnError, p+"/onError", true)
		if ns.Type == "switch" {
			nd.sw = &switchNode{}
			for j, c := range ns.Cases {
				cp := fmt.Sprintf("%s/cases/%d", p, j)
				id := c.ID
				if id == "" {
					id = fmt.Sprintf("case%d", j)
				}
				if c.Next == "" {
					b.errorf("INVALID_SPEC", cp+"/next", "case needs next")
					continue
				}
				nd.sw.cases = append(nd.sw.cases, switchCase{id: id, next: link(i, c.Next, cp+"/next", false)})
			}
			var def string
			if len(ns.Default) > 0 {
				if err := json.Unmarshal(ns.Default, &def); err != nil {
					b.errorf("INVALID_SPEC", p+"/default", "switch default must be a node id")
				}
			}
			if def == "" {
				b.errorf("INVALID_SPEC", p+"/default", "switch needs a default node")
			} else {
				nd.sw.def = link(i, def, p+"/default", false)
			}
			if ns.Next != "" {
				b.warnf("INVALID_SPEC", p+"/next", "switch nodes continue with their cases; next is ignored")
				nd.next = nil
			}
		}
		if ns.Type == "ai" && ns.OnError == "" {
			b.errorf("INVALID_SPEC", p+"/onError", "ai nodes need onError")
		}
	}
	if !okNodes {
		return nil
	}

	// Cycles and reachability.
	state := make([]int, n) // 0 new, 1 on stack, 2 done
	var order []int         // reverse postorder
	hasCycle := false
	var visit func(i int)
	visit = func(i int) {
		state[i] = 1
		for _, j := range succ[i] {
			switch state[j] {
			case 0:
				visit(j)
			case 1:
				if !hasCycle {
					b.errorf("CYCLE", fmt.Sprintf("/nodes/%d", i), "node %q leads back to %q; decisions must not loop", d.nodes[i].id, d.nodes[j].id)
				}
				hasCycle = true
			}
		}
		state[i] = 2
		order = append(order, i)
	}
	visit(entry.idx)
	for i := range d.nodes {
		if state[i] == 0 {
			b.errorf("UNREACHABLE_NODE", fmt.Sprintf("/nodes/%d", i), "node %q is never reached from entry %q", d.nodes[i].id, entry.id)
		}
	}
	if hasCycle {
		return nil
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}

	// Request view.
	reqView := sview{}
	if spec.InputSchema != nil {
		if e := b.schemaRef(spec.InputSchema, "/inputSchema"); e != nil {
			d.reqSchema, d.reqLabel = e.schema, spec.InputSchema.String()
			reqView = sview{schema: e.schema, label: d.reqLabel}
		}
	}
	entrySpec := specs[entry.idx]
	if (entrySpec.Type == "rules" || entrySpec.Type == "table") && entrySpec.InputSchema != nil && entrySpec.From == "" {
		if e := b.schemaRef(entrySpec.InputSchema, fmt.Sprintf("/nodes/%d/inputSchema", entry.idx)); e != nil {
			if d.reqSchema != nil && d.reqSchema != e.schema {
				b.warnf("INVALID_SPEC", "/inputSchema", "the entry node checks the request against %s; the decision inputSchema is also checked", entrySpec.InputSchema)
			}
			if d.reqSchema == nil {
				reqView = sview{schema: e.schema, label: entrySpec.InputSchema.String()}
			}
		}
	}
	reqView.taint = taintFromSchema(reqView.schema)
	if spec.Output != nil && spec.Output.Schema != nil {
		if e := b.schemaRef(spec.Output.Schema, "/output/schema"); e != nil {
			d.outSchema, d.outLabel = e.schema, spec.Output.Schema.String()
		}
	}

	// Flow analysis in topological order.
	dom := make([]map[string]bool, n)
	anc := make([]map[string]bool, n)
	inViews := make([]sview, n)
	flowOut := make([]sview, n)
	nodesOut := make([]sview, n)
	compiled := true
	for _, i := range order {
		nd := d.nodes[i]
		ns := &specs[i]
		path := fmt.Sprintf("/nodes/%d", i)
		// dominators / ancestors
		if i == entry.idx {
			dom[i], anc[i] = map[string]bool{}, map[string]bool{}
		} else {
			var dset map[string]bool
			aset := map[string]bool{}
			for _, e := range preds[i] {
				contrib := map[string]bool{}
				for k := range dom[e.from] {
					contrib[k] = true
				}
				if !e.onError {
					contrib[d.nodes[e.from].id] = true
				}
				if dset == nil {
					dset = contrib
				} else {
					for k := range dset {
						if !contrib[k] {
							delete(dset, k)
						}
					}
				}
				for k := range anc[e.from] {
					aset[k] = true
				}
				aset[d.nodes[e.from].id] = true
			}
			if dset == nil {
				dset = map[string]bool{}
			}
			dom[i], anc[i] = dset, aset
		}
		// pre-input view
		var pre sview
		if i == entry.idx {
			pre = reqView
		} else {
			first := true
			same := true
			for _, e := range preds[i] {
				c := flowOut[e.from]
				if e.onError {
					c = inViews[e.from]
				}
				if first {
					pre = sview{schema: c.schema, label: c.label}
					first = false
				} else if c.schema != pre.schema {
					same = false
				}
				pre.taint = (&taint{}).union(pre.taint).union(c.taint)
				if pre.taint.empty() {
					pre.taint = nil
				}
			}
			if !same {
				pre.schema, pre.label = nil, ""
			}
		}
		nodesScope := map[string]*nodeView{}
		for id := range anc[i] {
			j := d.byID[id].idx
			nodesScope[id] = &nodeView{sview: nodesOut[j], mayNotRun: !dom[i][id]}
		}
		base := &scope{vars: varsNode, input: pre, request: reqView, nodes: nodesScope}
		in := pre
		if ns.From != "" {
			prog, info, ok := b.expr(ns.From, path+"/from", base)
			if !ok {
				compiled = false
			} else {
				nd.from = prog
				in = b.chainView(ns.From, base)
				if info.tainted && in.taint.empty() {
					in.taint = &taint{all: true}
				}
			}
		}
		// rules/table: the node checks its input schema at runtime.
		if ns.Type == "rules" || ns.Type == "table" {
			if ns.InputSchema == nil {
				b.errorf("SCHEMA_REQUIRED", path+"/inputSchema", "inputSchema is required for a %s node", ns.Type)
			}
			if ns.OutputSchema == nil {
				b.errorf("SCHEMA_REQUIRED", path+"/outputSchema", "outputSchema is required for a %s node", ns.Type)
			}
			if e := b.schemaRef(ns.InputSchema, path+"/inputSchema"); e != nil {
				if in.schema != nil && in.schema != e.schema {
					if ok, where, why := jsonschema.Accepts(e.schema, in.schema); !ok {
						b.errorf("SCHEMA_EDGE_MISMATCH", path+"/inputSchema", "%s does not accept %s at %s: %s", ns.InputSchema, in.label, orRoot(where), why)
					}
				}
				nd.inSchema, nd.inLabel = e.schema, ns.InputSchema.String()
				in = sview{schema: e.schema, label: nd.inLabel, taint: (&taint{}).union(in.taint).union(taintFromSchema(e.schema))}
				if in.taint.empty() {
					in.taint = nil
				}
			}
		}
		inViews[i] = in
		sc := &scope{vars: varsNode, input: in, request: reqView, nodes: nodesScope}
		before := b.errorCount()
		fo, no := b.compileNode(d, nd, ns, path, sc)
		if b.errorCount() > before || nd.exec == nil {
			compiled = false
		}
		flowOut[i], nodesOut[i] = fo, no
	}
	if !compiled {
		return nil
	}

	// Kind.
	d.kind = "graph"
	if len(d.nodes) == 1 {
		switch entry.typ {
		case "rules", "table", "transform":
			d.kind = entry.typ
		}
	}

	// Output taint (terminal nodes) for logging checks.
	var outTaint *taint
	for i, nd := range d.nodes {
		terminal := nd.next == nil && nd.typ != "switch"
		if terminal {
			outTaint = (&taint{}).union(outTaint).union(flowOut[i].taint)
		}
		if nd.typ == "ai" {
			d.hasAI = true
		}
	}

	// Fallback.
	if len(spec.Fallback) > 0 && !isNull(spec.Fallback) {
		d.fallback = b.data(spec.Fallback, "/fallback", &scope{vars: varsNode, input: reqView, request: reqView, nodes: map[string]*nodeView{}})
		if d.fallback != nil && d.outSchema != nil {
			b.checkConstAgainst(d.fallback, d.outSchema, d.outLabel, "/fallback")
		}
	}

	// Logging.
	if spec.Logging != nil {
		lg := spec.Logging
		if lg.Sample != nil && (*lg.Sample < 0 || *lg.Sample > 1) {
			b.errorf("INVALID_SPEC", "/logging/sample", "sample must be between 0 and 1")
		}
		d.logSample = lg.Sample
		d.aiPayloads = lg.AIPayloads
		for i, f := range lg.Fields {
			p := fmt.Sprintf("/logging/fields/%d", i)
			lf := logField{name: f}
			raw := f
			switch {
			case strings.HasPrefix(f, "input."):
				raw = strings.TrimPrefix(f, "input.")
			case strings.HasPrefix(f, "output."):
				raw, lf.output = strings.TrimPrefix(f, "output."), true
			}
			parts, err := parseFieldPath(raw)
			if err != nil {
				b.errorf("PARSE_ERROR", p, "%s", err)
				continue
			}
			lf.parts = parts
			chain := make([]string, len(parts))
			for j, pp := range parts {
				if pp.index >= 0 {
					chain[j] = "[]"
				} else {
					chain[j] = pp.key
				}
			}
			if !lf.output && reqView.schema != nil {
				if _, _, msg := fieldSchema(reqView, parts); msg != "" {
					if d.outSchema != nil {
						if _, _, msg2 := fieldSchema(sview{schema: d.outSchema, label: d.outLabel}, parts); msg2 == "" {
							lf.output = true
						}
					}
					if !lf.output {
						b.warnf("UNKNOWN_FIELD", p, "%s", msg)
					}
				}
			}
			if (!lf.output && reqView.taint.overlaps(chain)) || (lf.output && outTaint.overlaps(chain)) {
				b.errorf("PII_IN_PROMPT", p, "logging field %q is (or derives from) a field marked x-gearbox-pii; PII is never logged", f)
				continue
			}
			d.logFields = append(d.logFields, lf)
		}
	}

	// Tests.
	for i, tc := range spec.Tests {
		p := fmt.Sprintf("/tests/%d", i)
		t := testCase{name: tc.Name, input: tc.Input, expectError: tc.ExpectError, expectPath: tc.ExpectPath}
		if tc.Name == "" {
			b.errorf("INVALID_SPEC", p+"/name", "test name is required")
		}
		if len(tc.Input) == 0 {
			b.errorf("INVALID_SPEC", p+"/input", "test input is required")
		}
		if tc.Ctx != nil {
			t.unit = tc.Ctx.Unit
			if tc.Ctx.Now != "" {
				ts, err := time.Parse(time.RFC3339Nano, tc.Ctx.Now)
				if err != nil {
					b.errorf("INVALID_SPEC", p+"/ctx/now", "now must be an RFC 3339 time")
				}
				t.now = ts
			}
		}
		if !isNull(tc.Expect) {
			var e any
			if err := json.Unmarshal(tc.Expect, &e); err != nil {
				b.errorf("INVALID_SPEC", p+"/expect", "expect is not valid JSON")
			}
			t.expect, t.hasExpect = unescapeData(e), true
		}
		d.tests = append(d.tests, t)
	}
	if b.errorCount() > 0 {
		return nil
	}
	d.deps = sortedRefs(b.deps)
	b.decisions[k] = d
	return d
}

func orRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func versionText(name string, v int64) string {
	if v == 0 {
		return name + "@draft"
	}
	return fmt.Sprintf("%s@%d", name, v)
}

// chainView resolves a `from` expression that is a plain member chain
// (input, request, nodes.x.y) to a view; anything else is unknown.
func (b *builder) chainView(src string, sc *scope) sview {
	tree, err := parser.Parse(src)
	if err != nil {
		return sview{}
	}
	root, segs, ok := chainOf(tree.Node)
	if !ok {
		return sview{}
	}
	w := &walker{b: b, sc: sc, info: &exprInfo{}}
	s, known, t, tpath, label := w.lookupChain(root, segs)
	view := sview{label: label}
	if known {
		view.schema = s
	}
	if root == "nodes" && len(tpath) > 0 {
		tpath = tpath[1:]
	}
	view.taint = t.sub(tpath)
	if len(segs) > 0 {
		view.label = label + " " + chainText(root, segs)
	}
	return view
}

// compileNode compiles one node; it returns the view of its output for the
// next node's input (flow) and for nodes.<id>.
func (b *builder) compileNode(d *decision, nd *node, ns *nodeSpec, path string, sc *scope) (flow, nodes sview) {
	unused := func(field string, set bool) {
		if set {
			b.errorf("INVALID_SPEC", path+"/"+field, "%s is not used by %s nodes", field, ns.Type)
		}
	}
	if ns.Type != "rules" && ns.Type != "table" {
		unused("inputSchema", ns.InputSchema != nil)
	}
	switch ns.Type {
	case "rules", "table":
		e := b.schemaRef(ns.OutputSchema, path+"/outputSchema")
		var out *jsonschema.Schema
		label := ""
		if e != nil {
			out, label = e.schema, ns.OutputSchema.String()
		}
		var t *taint
		if ns.Type == "rules" {
			t = b.rulesNode(d, nd, ns, path, sc, out, label)
		} else {
			t = b.tableNode(nd, ns, path, sc, out, label)
		}
		view := sview{schema: out, label: label, taint: t}
		if out != nil && ((ns.Type == "rules" && ns.Mode == "all_matches") || (ns.Type == "table" && ns.HitPolicy == "collect")) {
			max := len(ns.Rules) + 1
			if ns.Type == "table" {
				max = len(ns.Rows) + 1
			}
			view.schema = jsonschema.ObjectOf(map[string]*jsonschema.Schema{"matches": jsonschema.ArrayOf(out, max)}, []string{"matches"}, true)
			view.label = "matches of " + label
		}
		return view, view
	case "transform", "output":
		if ns.Type == "output" {
			unused("outputSchema", ns.OutputSchema != nil)
		}
		t, synth := b.transformNode(nd, ns, path, sc, ns.Type == "output")
		view := sview{schema: synth, label: "output of " + nd.id, taint: t}
		if ns.Type == "transform" && ns.OutputSchema != nil {
			if e := b.schemaRef(ns.OutputSchema, path+"/outputSchema"); e != nil {
				nd.outCheck, nd.outLabel = e.schema, ns.OutputSchema.String()
				view.schema, view.label = e.schema, nd.outLabel
			}
		}
		if ns.Type == "output" && d.outSchema != nil {
			view.schema, view.label = d.outSchema, d.outLabel
		}
		return view, view
	case "switch":
		for j, c := range ns.Cases {
			p, _, ok := b.expr(c.When, fmt.Sprintf("%s/cases/%d/when", path, j), sc)
			if ok && j < len(nd.sw.cases) {
				nd.sw.cases[j].when = p
			}
		}
		for _, c := range nd.sw.cases {
			if c.when == nil {
				return sview{}, sview{}
			}
		}
		nd.exec = execSwitch
		return sc.input, sview{schema: b.synth.sw, label: "switch output"}
	case "lookup":
		l := b.lookup(ns.Lookup, path+"/lookup")
		if l == nil {
			return sview{}, sview{}
		}
		p, _, ok := b.expr(ns.Key, path+"/key", sc)
		if !ok {
			return sview{}, sview{}
		}
		nd.lk = &lookupNode{name: ns.Lookup, key: p}
		if len(ns.Default) > 0 {
			nd.lk.def = b.data(ns.Default, path+"/default", sc)
			if nd.lk.def == nil {
				return sview{}, sview{}
			}
			b.checkConstAgainst(nd.lk.def, l.valueSchema, "the lookup valueSchema", path+"/default")
		}
		nd.exec = execLookup
		view := sview{schema: l.valueSchema, label: "lookup " + ns.Lookup}
		if nd.lk.def == nil || !nd.lk.def.present {
			view.schema = nil // may be null
		}
		return view, view
	case "gate":
		if b.gate(ns.Gate, path+"/gate") == nil {
			return sview{}, sview{}
		}
		nd.gate = ns.Gate
		nd.exec = execGate
		v := sview{schema: b.synth.gate, label: "gate output"}
		return v, v
	case "experiment":
		if b.experiment(ns.Experiment, path+"/experiment") == nil {
			return sview{}, sview{}
		}
		nd.exp = ns.Experiment
		nd.exec = execExperiment
		v := sview{schema: b.synth.experiment, label: "experiment output"}
		return v, v
	case "subdecision":
		sd := b.subDecision(ns.Decision, path+"/decision")
		if sd == nil {
			return sview{}, sview{}
		}
		nd.sub = &subNode{decision: ns.Decision}
		var inTaint *taint
		if ns.Input != "" {
			p, info, ok := b.expr(ns.Input, path+"/input", sc)
			if !ok {
				return sview{}, sview{}
			}
			nd.sub.input = p
			if info.tainted {
				inTaint = &taint{all: true}
			}
		} else if !sc.input.taint.empty() {
			inTaint = &taint{all: true}
		}
		nd.exec = execSub
		v := sview{schema: sd.outSchema, label: sd.outLabel, taint: inTaint}
		return v, v
	case "ai":
		return b.aiNodeSpec(d, nd, ns, path, sc)
	}
	return sview{}, sview{}
}

func (b *builder) aiNodeSpec(d *decision, nd *node, ns *nodeSpec, path string, sc *scope) (sview, sview) {
	a := &aiNode{mode: ns.Mode, model: ns.Model, maxTokens: ns.MaxOutputTokens, decision: d.name, decVersion: d.version}
	if a.maxTokens == 0 {
		a.maxTokens = 300
	}
	switch ns.Mode {
	case "shadow", "inline":
	case "precompute":
		b.errorf("NOT_IMPLEMENTED", path+"/mode", "precompute mode is not implemented yet; use shadow or inline")
	default:
		b.errorf("INVALID_SPEC", path+"/mode", "mode must be shadow, inline or precompute")
	}
	if ns.Model == "" {
		b.errorf("INVALID_SPEC", path+"/model", "model is required")
	}
	if ns.TimeoutMs < 50 || ns.TimeoutMs > 10000 {
		b.errorf("INVALID_SPEC", path+"/timeoutMs", "timeoutMs must be between 50 and 10000")
	}
	a.timeout = time.Duration(ns.TimeoutMs) * time.Millisecond
	if ns.Prompt == nil || ns.Prompt.User == "" {
		b.errorf("INVALID_SPEC", path+"/prompt/user", "prompt.user is required")
		return sview{}, sview{}
	}
	a.system, a.user = ns.Prompt.System, ns.Prompt.User
	names := make([]string, 0, len(ns.Vars))
	for name := range ns.Vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		vp := path + "/vars/" + ptrEscape(name)
		p, info, ok := b.expr(ns.Vars[name], vp, sc)
		if !ok {
			continue
		}
		if info.tainted {
			b.errorf("PII_IN_PROMPT", vp, "var %q reads (or derives from) a field marked x-gearbox-pii; PII never reaches a prompt", name)
			continue
		}
		a.vars = append(a.vars, aiVar{name: name, p: p})
	}
	for _, tmpl := range []struct{ text, field string }{{a.system, "system"}, {a.user, "user"}} {
		for _, m := range placeholderRE.FindAllStringSubmatch(tmpl.text, -1) {
			if _, ok := ns.Vars[m[1]]; !ok {
				b.errorf("UNKNOWN_FIELD", path+"/prompt/"+tmpl.field, "placeholder {{ %s }} is not a var; only vars reach the prompt", m[1])
			}
		}
	}
	if isNull(ns.ResponseSchema) {
		b.errorf("INVALID_SPEC", path+"/responseSchema", "responseSchema is required")
		return sview{}, sview{}
	}
	s, issues := jsonschema.Compile(ns.ResponseSchema)
	for _, is := range issues {
		b.errorf(is.Code, path+"/responseSchema"+is.Path, "%s", is.Message)
	}
	if s == nil {
		return sview{}, sview{}
	}
	a.resp, a.respRaw = s, ns.ResponseSchema
	nd.ai = a
	nd.exec = execAI
	v := sview{schema: s, label: "responseSchema of " + nd.id}
	return v, v
}
