// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr/vm"

	"github.com/ORG/gearbox/engine/internal/hashing"
	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// compiled is the immutable runtime form of a Set.
type compiled struct {
	app, env    string
	generation  int64
	schemas     map[string]*schemaEntry
	segments    map[string]*segment
	lookups     map[string]*lookup
	gates       map[string]*gate
	experiments map[string]*experiment
	layers      map[string]*layer
	decisions   map[string]*decisionEntry
	killed      map[string]bool
	runs        sync.Pool
}

type decisionEntry struct {
	live     *decision
	versions map[int64]*decision
	rollout  *rolloutState
}

type rolloutState struct {
	from, to *decision
	percent  int
	salt     string
}

func (c *compiled) bucket(salt, unit string) int {
	return hashing.RolloutBucket(c.app, salt, unit)
}

// pick selects the decision version for a call.
func (c *compiled) pick(name string, pin int64, unit string) (*decision, error) {
	e := c.decisions[name]
	if e == nil {
		return nil, &Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("decision %q is not in the active bundle", name)}
	}
	if pin > 0 {
		d := e.versions[pin]
		if d == nil {
			return nil, &Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("version %d of decision %q is not in the active bundle", pin, name)}
		}
		return d, nil
	}
	if ro := e.rollout; ro != nil {
		if unit == "" {
			return ro.from, nil
		}
		if c.bucket(ro.salt, unit) < ro.percent*100 {
			return ro.to, nil
		}
		return ro.from, nil
	}
	return e.live, nil
}

// run is the per-call state. Runs are pooled; nothing in them is shared
// between concurrent calls.
type run struct {
	c         *compiled
	ctx       context.Context
	cc        exprCtx
	env       map[string]any
	nodes     map[string]any
	vms       []*vm.VM
	depth     int
	path      []Step
	exposures []Exposure
	trace     bool
	debug     bool
	ai        AICaller
	fallback  bool
	sub       int
	gates     map[string]GateResult
	variants  map[string]VariantResult
	lastRaw   json.RawMessage // pre-encoded output of the last node when constant
}

func (c *compiled) getRun(ctx context.Context, now time.Time, unit, requestID string) *run {
	r, _ := c.runs.Get().(*run)
	if r == nil {
		r = &run{c: c, env: make(map[string]any, 8), nodes: make(map[string]any, 8)}
	}
	r.ctx = ctx
	r.cc = exprCtx{Now: now, Unit: unit, RequestID: requestID, Env: c.env, r: r}
	r.env["ctx"] = &r.cc
	r.env["nodes"] = r.nodes
	return r
}

func (c *compiled) putRun(r *run) {
	for k := range r.nodes {
		delete(r.nodes, k)
	}
	r.env["input"], r.env["request"], r.env["out"], r.env["value"] = nil, nil, nil, nil
	r.ctx = nil
	r.cc.r = nil
	r.path = r.path[:0]
	r.exposures = nil
	r.ai = nil
	r.trace, r.debug, r.fallback = false, false, false
	r.sub, r.depth = 0, 0
	r.gates, r.variants = nil, nil
	r.lastRaw = nil
	r.cc.r = r
	c.runs.Put(r)
}

func (r *run) vm() *vm.VM {
	if r.depth >= len(r.vms) {
		r.vms = append(r.vms, &vm.VM{})
	}
	v := r.vms[r.depth]
	r.depth++
	return v
}

func (r *run) eval(p *program, in any) (any, error) {
	v := r.vm()
	prev := r.env["input"]
	r.env["input"] = in
	out, err := v.Run(p.p, r.env)
	r.env["input"] = prev
	r.depth--
	return out, err
}

func (r *run) evalValue(p *program, in, value any) (any, error) {
	prev := r.env["value"]
	r.env["value"] = value
	out, err := r.eval(p, in)
	r.env["value"] = prev
	return out, err
}

// gate evaluates a gate for the call's unit, memoised per call.
func (r *run) gate(name string, attrs any) (GateResult, error) {
	if res, ok := r.gates[name]; ok {
		return res, nil
	}
	res, err := r.c.checkGate(r, name, r.cc.Unit, attrs)
	if err != nil {
		return res, err
	}
	if r.gates == nil {
		r.gates = map[string]GateResult{}
	}
	r.gates[name] = res
	return res, nil
}

func (c *compiled) checkGate(r *run, name, unit string, attrs any) (GateResult, error) {
	g := c.gates[name]
	if g == nil {
		return GateResult{}, fmt.Errorf("gate %q is not in the bundle", name)
	}
	if c.killed[name] {
		return GateResult{Value: false, Version: g.version, RuleID: "killed"}, nil
	}
	return g.eval(r, unit, attrs)
}

// variant assigns the call's unit, memoised per call, and records exposure.
func (r *run) variant(name string, attrs any) (VariantResult, error) {
	if res, ok := r.variants[name]; ok {
		return res, nil
	}
	e := r.c.experiments[name]
	if e == nil {
		return VariantResult{}, fmt.Errorf("experiment %q is not in the bundle", name)
	}
	res, err := e.assign(r, r.cc.Unit, attrs)
	if err != nil {
		return res, err
	}
	if r.variants == nil {
		r.variants = map[string]VariantResult{}
	}
	r.variants[name] = res
	if r.cc.Unit != "" {
		r.exposures = append(r.exposures, Exposure{Experiment: name, ExperimentVersion: e.version, Variant: res.Variant, Reason: res.Reason})
	}
	return res, nil
}

// subdecide runs another decision with the same call context.
func (r *run) subdecide(name string, in any) (any, string, error) {
	if r.sub+1 > MaxSubdecisions {
		return nil, "", fmt.Errorf("subdecisions nest deeper than %d", MaxSubdecisions)
	}
	d, err := r.c.pick(name, 0, r.cc.Unit)
	if err != nil {
		return nil, "", err
	}
	child := r.c.getRun(r.ctx, r.cc.Now, r.cc.Unit, r.cc.RequestID)
	child.ai, child.sub = r.ai, r.sub+1
	child.gates, child.variants = r.gates, r.variants
	out, err := child.runDecision(d, in)
	r.exposures = append(r.exposures, child.exposures...)
	if child.gates != nil {
		r.gates = child.gates
	}
	if child.variants != nil {
		r.variants = child.variants
	}
	if child.fallback {
		r.fallback = true
	}
	r.c.putRun(child)
	if err != nil {
		var ee *Error
		if errors.As(err, &ee) {
			return nil, "", fmt.Errorf("subdecision %s: %s", d.versionText, ee.Message)
		}
		return nil, "", fmt.Errorf("subdecision %s: %w", d.versionText, err)
	}
	return out, d.versionText, nil
}

func toFieldErrors(errs []jsonschema.FieldError) []FieldError {
	out := make([]FieldError, len(errs))
	for i, e := range errs {
		out[i] = FieldError{Path: e.Path, Message: e.Message}
	}
	return out
}

// runDecision executes the graph and returns the output.
func (r *run) runDecision(d *decision, request any) (any, error) {
	if d.reqSchema != nil && !d.reqSchema.Valid(request) {
		errs := d.reqSchema.Validate(request)
		reason := "INPUT_SCHEMA"
		for _, e := range errs {
			if e.Keyword == "maxItems" {
				reason = "INPUT_TOO_LARGE"
			}
		}
		return nil, &Error{Reason: reason, Message: "input does not match " + d.reqLabel, Fields: toFieldErrors(errs)}
	}
	r.env["request"] = request
	cur := request
	n := d.entry
	for steps := 0; n != nil; steps++ {
		if steps > 2*len(d.nodes)+2 {
			return nil, &Error{Reason: "NODE_ERROR", Message: "decision did not terminate", Node: n.id}
		}
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		in := cur
		r.lastRaw = nil
		var t0 time.Time
		if r.trace {
			t0 = time.Now()
		}
		var out any
		var outcome string
		var next *node
		var err error
		if n.from != nil {
			var v any
			v, err = r.eval(n.from, cur)
			in = normalize(v)
		}
		if err == nil {
			out, outcome, next, err = n.exec(r, n, in)
		}
		step := Step{NodeID: n.id, NodeType: n.typ, Outcome: outcome}
		if r.trace {
			step.Micros = time.Since(t0).Microseconds()
		}
		if r.debug {
			step.Input = encodeJSON(in)
			if err == nil {
				step.Output = encodeJSON(out)
			}
		}
		if err != nil {
			step.Error = err.Error()
			var ie *inputError
			var tl *tooLargeError
			if r.sub == 0 && n == d.entry && n.from == nil && (errors.As(err, &tl) || errors.As(err, &ie)) {
				r.path = append(r.path, Step{NodeID: n.id, NodeType: n.typ, Outcome: "error", Error: step.Error})
				reason, fields := "INPUT_SCHEMA", []FieldError(nil)
				if tl != nil {
					reason, fields = "INPUT_TOO_LARGE", tl.fields
				} else {
					fields = ie.fields
				}
				return nil, &Error{Reason: reason, Message: err.Error(), Fields: fields, Node: n.id}
			}
			if errors.Is(err, errShadow) {
				step.Outcome, step.Error = "shadow", ""
				r.path = append(r.path, step)
				n = n.onError
				continue
			}
			if n.onError != nil {
				step.Outcome = "fallback"
				r.fallback = true
				r.path = append(r.path, step)
				n = n.onError
				continue
			}
			step.Outcome = "error"
			r.path = append(r.path, step)
			return r.decisionFallback(d, request, n.id, err)
		}
		r.path = append(r.path, step)
		if n.typ != "switch" {
			r.nodes[n.id] = out
		}
		if next == nil {
			next = n.next
		}
		if n.end || next == nil {
			if d.outSchema != nil && !d.outSchema.Valid(out) {
				errs := d.outSchema.Validate(out)
				oerr := fmt.Errorf("output does not match %s: %s %s", d.outLabel, errs[0].Path, errs[0].Message)
				r.path[len(r.path)-1].Outcome = "error"
				r.path[len(r.path)-1].Error = oerr.Error()
				return r.decisionFallback(d, request, n.id, oerr)
			}
			return out, nil
		}
		cur = out
		n = next
	}
	return nil, &Error{Reason: "NODE_ERROR", Message: "decision ended without output"}
}

func (r *run) decisionFallback(d *decision, request any, nodeID string, cause error) (any, error) {
	r.lastRaw = nil
	if d.fallback == nil || !d.fallback.present {
		return nil, &Error{Reason: "NODE_ERROR", Message: fmt.Sprintf("node %s: %v", nodeID, cause), Node: nodeID}
	}
	out, err := d.fallback.eval(r, request)
	if err != nil {
		return nil, &Error{Reason: "NODE_ERROR", Message: fmt.Sprintf("node %s: %v; fallback: %v", nodeID, cause, err), Node: nodeID}
	}
	r.fallback = true
	if d.fallback.constant {
		r.lastRaw = d.fallback.raw
	}
	return out, nil
}

// decide is Snapshot.Decide.
func (c *compiled) decide(ctx context.Context, req Request, input any, decoded bool) (Result, error) {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	d, err := c.pick(req.Decision, req.PinVersion, req.UnitID)
	if err != nil {
		return Result{}, err
	}
	if !decoded {
		if len(req.Input) == 0 {
			input = nil
		} else if err := json.Unmarshal(req.Input, &input); err != nil {
			return Result{}, &Error{Reason: "INPUT_SCHEMA", Message: "input is not valid JSON: " + err.Error()}
		}
	}
	now := req.Now
	if now.IsZero() {
		now = start.UTC()
	}
	r := c.getRun(ctx, now, req.UnitID, req.RequestID)
	r.trace, r.debug, r.ai = req.Trace || req.Debug, req.Debug, req.AI
	out, err := r.runDecision(d, input)
	raw := r.lastRaw
	res := Result{
		DecisionVersion: d.versionText,
		DependencyHash:  d.depHash,
		Generation:      c.generation,
		EvaluatedAt:     now,
		FallbackUsed:    r.fallback,
	}
	res.Path = make([]Step, len(r.path))
	copy(res.Path, r.path)
	if len(r.exposures) > 0 {
		res.Exposures = append([]Exposure(nil), r.exposures...)
	}
	c.putRun(r)
	if err != nil {
		var ee *Error
		if !errors.As(err, &ee) {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			ee = &Error{Reason: "NODE_ERROR", Message: err.Error()}
		}
		res.EvalMicros = time.Since(start).Microseconds()
		return res, ee
	}
	if raw != nil {
		res.Output = raw
	} else {
		res.Output = encodeJSON(out)
	}
	res.EvalMicros = time.Since(start).Microseconds()
	return res, nil
}

// dependencyHash computes "sha256:<hex>" over the decision version and every
// resource version it reads (transitively).
func (c *compiled) dependencyHash(d *decision) string {
	seen := map[string]bool{}
	var lines []string
	add := func(s string) bool {
		if seen[s] {
			return false
		}
		seen[s] = true
		lines = append(lines, s)
		return true
	}
	var walk func(refs []Ref)
	walk = func(refs []Ref) {
		for _, ref := range refs {
			switch ref.Kind {
			case KindSchema:
				add(key(KindSchema, ref.Name, ref.Version))
			case KindSegment:
				if s := c.segments[ref.Name]; s != nil && add(key(KindSegment, s.name, s.version)) {
					walk(s.deps)
				}
			case KindLookup:
				if l := c.lookups[ref.Name]; l != nil && add(key(KindLookup, l.name, l.version)) {
					walk(l.deps)
				}
			case KindGate:
				if g := c.gates[ref.Name]; g != nil && add(key(KindGate, g.name, g.version)) {
					if c.killed[g.name] {
						add("killed/" + g.name)
					}
					walk(g.deps)
				}
			case KindExperiment:
				if e := c.experiments[ref.Name]; e != nil && add(key(KindExperiment, e.name, e.version)) {
					walk(e.deps)
				}
			case KindLayer:
				if l := c.layers[ref.Name]; l != nil && add(key(KindLayer, l.name, l.version)) {
					walk(l.deps)
				}
			case KindDecision:
				if e := c.decisions[ref.Name]; e != nil && e.live != nil && add(key(KindDecision, ref.Name, e.live.version)) {
					walk(e.live.deps)
				}
			}
		}
	}
	add(key(KindDecision, d.name, d.version))
	walk(d.deps)
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
