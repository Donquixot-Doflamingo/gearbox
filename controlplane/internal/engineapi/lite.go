package engineapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/engine"
)

// LiteMessage explains what lite mode cannot do.
const LiteMessage = "lite engine: expressions are not evaluated (the data-plane engine is not available); the decision's fallback is returned"

// Lite is the built-in stand-in engine. See the package comment.
type Lite struct{}

func (Lite) Name() string    { return "lite" }
func (Lite) Version() string { return engine.Version }

func (Lite) CheckSchemaCompatibility(prev, next json.RawMessage) (bool, []engine.SchemaChange, error) {
	return checkCompat(prev, next)
}

func (Lite) ValidateDocument(schemaSpec, doc json.RawMessage) (bool, []engine.FieldError, error) {
	return validateDoc(schemaSpec, doc)
}

func (Lite) Catalogue() json.RawMessage { return liteCatalogue }

func problem(code, path, msg string) engine.Problem {
	return engine.Problem{Code: code, Severity: "error", Path: path, Message: msg}
}

func (Lite) ValidateResource(r engine.Resource, deps engine.Resolver) engine.Validation {
	var probs []engine.Problem
	var m map[string]any
	if err := json.Unmarshal(r.Spec, &m); err != nil || m == nil {
		return engine.Validation{Valid: false, Problems: []engine.Problem{problem("PARSE_ERROR", "", "spec must be a JSON object")}}
	}
	resolve := func(kind engine.Kind, name string, version int64) bool {
		if deps == nil {
			return true
		}
		_, ok := deps.Get(kind, name, version)
		return ok
	}
	switch r.Kind {
	case engine.KindSchema:
		js, ok := m["jsonSchema"].(map[string]any)
		if !ok {
			probs = append(probs, problem("PARSE_ERROR", "/jsonSchema", "jsonSchema must be an object"))
			break
		}
		lintSchema(js, "/jsonSchema", 0, &probs)
		if ex, ok := m["examples"].([]any); ok {
			for i, e := range ex {
				doc, _ := json.Marshal(e)
				if valid, errs, _ := validateDoc(r.Spec, doc); !valid {
					for _, fe := range errs {
						probs = append(probs, problem("EXAMPLE_INVALID", fmt.Sprintf("/examples/%d%s", i, strings.TrimSuffix(fe.Path, "/")), fe.Message))
					}
				}
			}
		}
	case engine.KindDecision:
		probs = append(probs, validateDecision(m, r.Spec, resolve, deps)...)
	case engine.KindLayer:
		var sum float64
		if allocs, ok := m["allocations"].([]any); ok {
			for _, a := range allocs {
				if am, ok := a.(map[string]any); ok {
					if p, ok := am["percent"].(float64); ok {
						sum += p
					}
				}
			}
		}
		if sum > 100 {
			probs = append(probs, problem("LAYER_OVER_ALLOCATED", "/allocations", fmt.Sprintf("allocations sum to %v%%; at most 100%% allowed", sum)))
		}
	case engine.KindExperiment:
		var total float64
		names := map[string]bool{}
		if vs, ok := m["variants"].([]any); ok {
			for i, v := range vs {
				vm, _ := v.(map[string]any)
				n, _ := vm["name"].(string)
				if names[n] {
					probs = append(probs, problem("DUPLICATE_VARIANT", fmt.Sprintf("/variants/%d/name", i), "variant names must be unique"))
				}
				names[n] = true
				w, _ := vm["weight"].(float64)
				total += w
			}
		}
		if total <= 0 {
			probs = append(probs, problem("NO_WEIGHT", "/variants", "variant weights must sum to more than 0"))
		}
		if sv, ok := m["stoppedVariant"].(string); ok && !names[sv] {
			probs = append(probs, problem("UNKNOWN_VARIANT", "/stoppedVariant", "stoppedVariant is not a variant"))
		}
	}
	// References to other resources must resolve.
	for _, d := range Deps(string(r.Kind), r.Spec) {
		if d.Kind == "decisions" && d.Name == r.Name {
			probs = append(probs, problem("CYCLE", d.Where, "a decision cannot call itself"))
			continue
		}
		if !resolve(engine.Kind(d.Kind), d.Name, d.Version) {
			code := strings.ToUpper(strings.TrimSuffix(d.Kind, "s")) + "_NOT_FOUND"
			msg := fmt.Sprintf("%s %q is not registered", strings.TrimSuffix(d.Kind, "s"), d.Name)
			if d.Kind == "schemas" {
				msg = fmt.Sprintf("schema %s@%d is not registered", d.Name, d.Version)
			}
			probs = append(probs, problem(code, d.Where, msg))
		}
	}
	v := engine.Validation{Valid: true, Problems: probs}
	for _, p := range probs {
		if p.Severity == "error" {
			v.Valid = false
		}
	}
	if v.Problems == nil {
		v.Problems = []engine.Problem{}
	}
	if v.Valid {
		seen := map[string]bool{}
		for _, d := range Deps(string(r.Kind), r.Spec) {
			k := d.Kind + "/" + d.Name + "@" + strconv.FormatInt(d.Version, 10)
			if !seen[k] {
				seen[k] = true
				v.Dependencies = append(v.Dependencies, engine.Ref{Kind: engine.Kind(d.Kind), Name: d.Name, Version: d.Version})
			}
		}
	}
	return v
}

func validateDecision(m map[string]any, spec json.RawMessage, resolve func(engine.Kind, string, int64) bool, deps engine.Resolver) []engine.Problem {
	var probs []engine.Problem
	nodes, _ := m["nodes"].([]any)
	if len(nodes) == 0 {
		return append(probs, problem("PARSE_ERROR", "/nodes", "a decision needs at least one node"))
	}
	ids := map[string]int{}
	for i, n := range nodes {
		nm, _ := n.(map[string]any)
		id, _ := nm["id"].(string)
		if _, dup := ids[id]; dup {
			probs = append(probs, problem("DUPLICATE_NODE", fmt.Sprintf("/nodes/%d/id", i), fmt.Sprintf("node id %q is used twice", id)))
		}
		ids[id] = i
	}
	entry, _ := m["entry"].(string)
	if _, ok := ids[entry]; !ok {
		probs = append(probs, problem("UNKNOWN_NODE", "/entry", fmt.Sprintf("entry node %q does not exist", entry)))
	}
	edges := map[string][]string{}
	for i, n := range nodes {
		nm, _ := n.(map[string]any)
		id, _ := nm["id"].(string)
		typ, _ := nm["type"].(string)
		base := fmt.Sprintf("/nodes/%d", i)
		target := func(field, t string) {
			if t == "" || t == "end" {
				return
			}
			if _, ok := ids[t]; !ok {
				probs = append(probs, problem("UNKNOWN_NODE", base+"/"+field, fmt.Sprintf("node %q does not exist", t)))
				return
			}
			edges[id] = append(edges[id], t)
		}
		if s, ok := nm["next"].(string); ok {
			target("next", s)
		}
		if s, ok := nm["onError"].(string); ok {
			target("onError", s)
		}
		if typ == "switch" {
			if s, ok := nm["default"].(string); ok {
				target("default", s)
			}
			if cases, ok := nm["cases"].([]any); ok {
				for ci, c := range cases {
					cm, _ := c.(map[string]any)
					if s, ok := cm["next"].(string); ok {
						target(fmt.Sprintf("cases/%d/next", ci), s)
					}
				}
			}
		}
		if typ == "ai" {
			if _, ok := nm["onError"].(string); !ok {
				probs = append(probs, problem("ON_ERROR_REQUIRED", base+"/onError", "ai nodes need onError"))
			}
		}
		if typ == "rules" || typ == "table" {
			// Literal `then`/`default` data must match outputSchema.
			out, _ := nm["outputSchema"].(map[string]any)
			name, _ := out["name"].(string)
			ver, _ := out["version"].(float64)
			if name != "" && deps != nil && typ == "rules" {
				if sres, ok := deps.Get(engine.KindSchema, name, int64(ver)); ok {
					check := func(path string, val any) {
						if s, ok := val.(string); ok && strings.HasPrefix(s, "=") && !strings.HasPrefix(s, "==") {
							return
						}
						if containsExprString(val) {
							return
						}
						doc, _ := json.Marshal(val)
						if valid, errs, _ := validateDoc(sres.Spec, doc); !valid {
							for _, fe := range errs {
								probs = append(probs, problem("OUTPUT_SCHEMA_MISMATCH", path+strings.TrimSuffix(fe.Path, "/"), fe.Message+" (outputSchema "+name+"@"+strconv.Itoa(int(ver))+")"))
							}
						}
					}
					mode, _ := nm["mode"].(string)
					if mode != "all_matches" {
						if rules, ok := nm["rules"].([]any); ok {
							for ri, rr := range rules {
								rm, _ := rr.(map[string]any)
								if th, ok := rm["then"]; ok {
									check(fmt.Sprintf("%s/rules/%d/then", base, ri), th)
								}
							}
						}
						if d, ok := nm["default"]; ok {
							check(base+"/default", d)
						}
					}
				}
			}
		}
	}
	// Cycle detection (DFS).
	state := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		state[n] = 1
		for _, t := range edges[n] {
			if state[t] == 1 || (state[t] == 0 && visit(t)) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	for id := range ids {
		if state[id] == 0 && visit(id) {
			probs = append(probs, problem("CYCLE", "/nodes", "the node graph has a cycle"))
			break
		}
	}
	// Unreachable nodes are warnings.
	reach := map[string]bool{}
	var walkR func(string)
	walkR = func(n string) {
		if reach[n] {
			return
		}
		reach[n] = true
		for _, t := range edges[n] {
			walkR(t)
		}
	}
	walkR(entry)
	for id, i := range ids {
		if !reach[id] {
			probs = append(probs, engine.Problem{Code: "UNREACHABLE_NODE", Severity: "warning", Path: fmt.Sprintf("/nodes/%d", i), Message: fmt.Sprintf("node %q is never reached from the entry node", id)})
		}
	}
	sort.SliceStable(probs, func(i, j int) bool { return probs[i].Path < probs[j].Path })
	return probs
}

// containsExprString reports whether data contains "=…" expression strings
// (checked at runtime, not at save).
func containsExprString(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.HasPrefix(t, "=") && !strings.HasPrefix(t, "==")
	case []any:
		for _, x := range t {
			if containsExprString(x) {
				return true
			}
		}
	case map[string]any:
		for _, x := range t {
			if containsExprString(x) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- snapshot

type setResolver struct{ set engine.Set }

func (s setResolver) Get(kind engine.Kind, name string, version int64) (engine.Resource, bool) {
	var best *engine.Resource
	for i := range s.set.Resources {
		r := &s.set.Resources[i]
		if r.Kind != kind || r.Name != name {
			continue
		}
		if version > 0 {
			if r.Version == version {
				return *r, true
			}
			continue
		}
		if best == nil || r.Version > best.Version {
			best = r
		}
	}
	if best == nil {
		return engine.Resource{}, false
	}
	return *best, true
}

func (Lite) Compile(set engine.Set) (Snapshot, error) {
	res := setResolver{set}
	var msgs []string
	for _, r := range set.Resources {
		v := Lite{}.ValidateResource(r, res)
		for _, p := range v.Problems {
			if p.Severity == "error" {
				msgs = append(msgs, fmt.Sprintf("%s/%s@%d%s: %s", r.Kind, r.Name, r.Version, p.Path, p.Message))
			}
		}
	}
	if len(msgs) > 0 {
		return nil, &CompileError{Problems: msgs}
	}
	return &liteSnapshot{set: set, res: res}, nil
}

// CompileError lists the problems that stopped a lite compile.
type CompileError struct{ Problems []string }

func (e *CompileError) Error() string { return "compile failed: " + strings.Join(e.Problems, "; ") }

type liteSnapshot struct {
	set engine.Set
	res setResolver
}

func (s *liteSnapshot) Generation() int64 { return s.set.Generation }

// liveVersion returns the version a unit gets for a decision.
func (s *liteSnapshot) liveVersion(name, unit string, pin int64) (engine.Resource, bool) {
	if pin > 0 {
		return s.res.Get(engine.KindDecision, name, pin)
	}
	for _, ro := range s.set.Rollouts {
		if ro.Decision == name {
			v := ro.FromVersion
			if unit != "" && Bucket(ro.Salt, unit) < ro.Percent*100 {
				v = ro.ToVersion
			}
			return s.res.Get(engine.KindDecision, name, v)
		}
	}
	return s.res.Get(engine.KindDecision, name, 0)
}

func (s *liteSnapshot) Decisions() []engine.DecisionInfo {
	seen := map[string]bool{}
	var out []engine.DecisionInfo
	for _, r := range s.set.Resources {
		if r.Kind != engine.KindDecision || seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		lv, _ := s.liveVersion(r.Name, "", 0)
		for _, ro := range s.set.Rollouts {
			if ro.Decision == r.Name {
				lv, _ = s.res.Get(engine.KindDecision, r.Name, ro.ToVersion)
			}
		}
		out = append(out, engine.DecisionInfo{Name: r.Name, Version: lv.Version, Kind: DecisionSubtype(lv.Spec)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type liteDecision struct {
	Entry       string `json:"entry"`
	InputSchema *struct {
		Name    string `json:"name"`
		Version int64  `json:"version"`
	} `json:"inputSchema"`
	Nodes []struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		InputSchema *struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
		} `json:"inputSchema"`
	} `json:"nodes"`
	Fallback json.RawMessage   `json:"fallback"`
	Tests    []json.RawMessage `json:"tests"`
}

func versionLabel(r engine.Resource) string {
	if r.Version == 0 {
		return r.Name + "@draft"
	}
	return r.Name + "@" + strconv.FormatInt(r.Version, 10)
}

func (s *liteSnapshot) Decide(ctx context.Context, req engine.Request) (engine.Result, error) {
	start := time.Now()
	r, ok := s.liveVersion(req.Decision, req.UnitID, req.PinVersion)
	if !ok {
		return engine.Result{}, &engine.Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("decision %q is not in this snapshot", req.Decision)}
	}
	if len(req.Input) > 1<<20 {
		return engine.Result{}, &engine.Error{Reason: "INPUT_TOO_LARGE", Message: "input exceeds 1 MiB"}
	}
	var d liteDecision
	if err := json.Unmarshal(r.Spec, &d); err != nil {
		return engine.Result{}, &engine.Error{Reason: "NODE_ERROR", Message: err.Error()}
	}
	entryType := ""
	ref := d.InputSchema
	for _, n := range d.Nodes {
		if n.ID == d.Entry {
			entryType = n.Type
			if n.InputSchema != nil && (n.Type == "rules" || n.Type == "table") {
				ref = n.InputSchema
			}
		}
	}
	if ref != nil {
		if sr, ok := s.res.Get(engine.KindSchema, ref.Name, ref.Version); ok {
			input := req.Input
			if len(input) == 0 {
				input = json.RawMessage("null")
			}
			if valid, errs, _ := validateDoc(sr.Spec, input); !valid {
				return engine.Result{}, &engine.Error{Reason: "INPUT_SCHEMA", Message: fmt.Sprintf("input does not match %s@%d", ref.Name, ref.Version), Fields: errs}
			}
		}
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	res := engine.Result{
		DecisionVersion: versionLabel(r),
		DependencyHash:  "sha256:lite",
		Generation:      s.set.Generation,
		EvaluatedAt:     now.UTC(),
		Path:            []engine.Step{{NodeID: d.Entry, NodeType: entryType, Outcome: "fallback", Error: LiteMessage}},
	}
	if len(d.Fallback) == 0 || string(d.Fallback) == "null" {
		return engine.Result{}, &engine.Error{Reason: "NODE_ERROR", Message: LiteMessage + " (the decision has no fallback)", Node: d.Entry}
	}
	res.Output = d.Fallback
	res.FallbackUsed = true
	res.EvalMicros = time.Since(start).Microseconds()
	return res, nil
}

func (s *liteSnapshot) CheckGate(gate, unit string, attrs json.RawMessage) (engine.GateResult, error) {
	r, ok := s.res.Get(engine.KindGate, gate, 0)
	if !ok {
		return engine.GateResult{}, &engine.Error{Reason: "GATE_NOT_FOUND", Message: fmt.Sprintf("gate %q is not live", gate)}
	}
	out := engine.GateResult{Version: r.Version}
	for _, k := range s.set.KilledGates {
		if k == gate {
			out.RuleID = "killed"
			return out, nil
		}
	}
	var spec struct {
		Enabled *bool `json:"enabled"`
		Rules   []struct {
			ID      string `json:"id"`
			When    any    `json:"when"`
			Value   bool   `json:"value"`
			Rollout *struct {
				Percent float64 `json:"percent"`
				Salt    string  `json:"salt"`
			} `json:"rollout"`
		} `json:"rules"`
		Default bool `json:"default"`
	}
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		return out, err
	}
	if spec.Enabled != nil && !*spec.Enabled {
		out.RuleID = "disabled"
		return out, nil
	}
	a, err := decodeAttrs(attrs)
	if err != nil {
		return out, &engine.Error{Reason: "INVALID_ARGUMENT", Message: err.Error()}
	}
	for _, rule := range spec.Rules {
		var unsup bool
		if !evalCond(rule.When, a, &unsup) {
			continue
		}
		if rule.Rollout != nil {
			salt := rule.Rollout.Salt
			if salt == "" {
				salt = gate + "." + rule.ID
			}
			if unit == "" || float64(Bucket(salt, unit)) >= rule.Rollout.Percent*100 {
				continue
			}
		}
		out.Value, out.RuleID = rule.Value, rule.ID
		return out, nil
	}
	out.Value, out.RuleID = spec.Default, "default"
	return out, nil
}

func (s *liteSnapshot) GetVariant(experiment, unit string, attrs json.RawMessage) (engine.VariantResult, error) {
	r, ok := s.res.Get(engine.KindExperiment, experiment, 0)
	if !ok {
		return engine.VariantResult{}, &engine.Error{Reason: "EXPERIMENT_NOT_FOUND", Message: fmt.Sprintf("experiment %q is not live", experiment)}
	}
	var spec struct {
		Status         string  `json:"status"`
		Salt           string  `json:"salt"`
		Layer          string  `json:"layer"`
		HoldoutPercent float64 `json:"holdoutPercent"`
		Targeting      any     `json:"targeting"`
		Variants       []struct {
			Name       string          `json:"name"`
			Weight     float64         `json:"weight"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"variants"`
		StoppedVariant string `json:"stoppedVariant"`
	}
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		return engine.VariantResult{}, err
	}
	out := engine.VariantResult{Version: r.Version}
	variant := func(name string) {
		for _, v := range spec.Variants {
			if v.Name == name {
				out.Variant, out.Parameters = v.Name, v.Parameters
			}
		}
	}
	switch spec.Status {
	case "stopped":
		sv := spec.StoppedVariant
		if sv == "" && len(spec.Variants) > 0 {
			sv = spec.Variants[0].Name
		}
		variant(sv)
		out.Reason = "stopped"
		return out, nil
	case "draft":
		out.Reason = "not_targeted"
		return out, nil
	}
	if unit == "" {
		out.Reason = "no_unit"
		return out, nil
	}
	if spec.Targeting != nil {
		a, err := decodeAttrs(attrs)
		if err != nil {
			return out, &engine.Error{Reason: "INVALID_ARGUMENT", Message: err.Error()}
		}
		var unsup bool
		if !evalCond(spec.Targeting, a, &unsup) {
			out.Reason = "not_targeted"
			return out, nil
		}
	}
	if spec.Layer != "" {
		if lr, ok := s.res.Get(engine.KindLayer, spec.Layer, 0); ok {
			var layer struct {
				Salt        string `json:"salt"`
				Allocations []struct {
					Experiment string  `json:"experiment"`
					Percent    float64 `json:"percent"`
				} `json:"allocations"`
			}
			_ = json.Unmarshal(lr.Spec, &layer)
			salt := layer.Salt
			if salt == "" {
				salt = spec.Layer
			}
			b := float64(Bucket(salt, unit))
			lo, in := 0.0, false
			for _, a := range layer.Allocations {
				hi := lo + a.Percent*100
				if a.Experiment == experiment && b >= lo && b < hi {
					in = true
				}
				lo = hi
			}
			if !in {
				out.Reason = "layer_conflict"
				return out, nil
			}
		}
	}
	salt := spec.Salt
	if salt == "" {
		salt = experiment
	}
	b := float64(Bucket(salt, unit))
	if b < spec.HoldoutPercent*100 {
		out.Reason = "holdout"
		return out, nil
	}
	var total float64
	for _, v := range spec.Variants {
		total += v.Weight
	}
	pos := (b - spec.HoldoutPercent*100) / (10000 - spec.HoldoutPercent*100) * total
	acc := 0.0
	for _, v := range spec.Variants {
		acc += v.Weight
		if pos < acc {
			variant(v.Name)
			break
		}
	}
	if out.Variant == "" && len(spec.Variants) > 0 {
		variant(spec.Variants[len(spec.Variants)-1].Name)
	}
	out.InExperiment, out.Reason = true, "assigned"
	return out, nil
}

func (s *liteSnapshot) CheckSegment(segment, key, unit string, attrs json.RawMessage) (bool, string, error) {
	r, ok := s.res.Get(engine.KindSegment, segment, 0)
	if !ok {
		return false, "", &engine.Error{Reason: "SEGMENT_NOT_FOUND", Message: fmt.Sprintf("segment %q is not available", segment)}
	}
	var spec struct {
		Type    string `json:"type"`
		Dynamic *struct {
			When any `json:"when"`
		} `json:"dynamic"`
		Rollout *struct {
			Percent float64 `json:"percent"`
			Salt    string  `json:"salt"`
			Include any     `json:"include"`
		} `json:"rollout"`
	}
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		return false, "", err
	}
	switch spec.Type {
	case "static":
		for _, m := range r.SegmentMembers {
			if m == key {
				return true, "listed", nil
			}
		}
		return false, "not_listed", nil
	case "dynamic":
		if spec.Dynamic == nil {
			return false, "conditions_failed", nil
		}
		a, err := decodeAttrs(attrs)
		if err != nil {
			return false, "", err
		}
		var unsup bool
		if evalCond(spec.Dynamic.When, a, &unsup) {
			return true, "conditions_matched", nil
		}
		return false, "conditions_failed", nil
	case "rollout":
		if spec.Rollout == nil {
			return false, "", errors.New("rollout segment without rollout settings")
		}
		k := key
		if k == "" {
			k = unit
		}
		if k == "" {
			return false, "no_unit", nil
		}
		if spec.Rollout.Include != nil {
			a, err := decodeAttrs(attrs)
			if err != nil {
				return false, "", err
			}
			var unsup bool
			if !evalCond(spec.Rollout.Include, a, &unsup) {
				return false, "not_included", nil
			}
		}
		salt := spec.Rollout.Salt
		if salt == "" {
			salt = segment
		}
		b := Bucket(salt, k)
		limit := int(spec.Rollout.Percent * 100)
		if b < limit {
			return true, fmt.Sprintf("bucket %d < %d", b, limit), nil
		}
		return false, fmt.Sprintf("bucket %d >= %d", b, limit), nil
	}
	return false, "", fmt.Errorf("unknown segment type %q", spec.Type)
}

func (s *liteSnapshot) RunTests(ctx context.Context, decision string, version int64) ([]engine.TestResult, error) {
	var names []string
	if decision != "" {
		names = []string{decision}
	} else {
		for _, d := range s.Decisions() {
			names = append(names, d.Name)
		}
	}
	var out []engine.TestResult
	for _, name := range names {
		r, ok := s.res.Get(engine.KindDecision, name, version)
		if !ok {
			return nil, &engine.Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("decision %q is not in this snapshot", name)}
		}
		var d struct {
			Tests []struct {
				Name string `json:"name"`
				Ctx  struct {
					Now  string `json:"now"`
					Unit string `json:"unit"`
				} `json:"ctx"`
				Input       json.RawMessage `json:"input"`
				Expect      json.RawMessage `json:"expect"`
				ExpectError string          `json:"expectError"`
			} `json:"tests"`
		}
		_ = json.Unmarshal(r.Spec, &d)
		for _, tc := range d.Tests {
			start := time.Now()
			now, _ := time.Parse(time.RFC3339, tc.Ctx.Now)
			_, err := s.Decide(ctx, engine.Request{Decision: name, Input: tc.Input, UnitID: tc.Ctx.Unit, PinVersion: r.Version, Now: now})
			tr := engine.TestResult{Name: tc.Name, OK: true, Expected: tc.Expect}
			var ee *engine.Error
			switch {
			case tc.ExpectError != "":
				switch {
				case errors.As(err, &ee):
					tr.OK = ee.Reason == tc.ExpectError
					if !tr.OK {
						tr.Error = fmt.Sprintf("expected error %s, got %s", tc.ExpectError, ee.Reason)
					}
				case tc.ExpectError == "INPUT_SCHEMA":
					tr.OK = false
					tr.Error = "expected error INPUT_SCHEMA, but the input matches the schema"
				default:
					// Lite cannot produce node errors; it cannot judge this case.
					tr.Error = "not evaluated: " + LiteMessage
				}
			case errors.As(err, &ee) && ee.Reason == "INPUT_SCHEMA":
				tr.OK = false
				tr.Error = ee.Error()
			default:
				tr.Error = "not evaluated: " + LiteMessage
			}
			tr.Micros = time.Since(start).Microseconds()
			out = append(out, tr)
		}
	}
	if out == nil {
		out = []engine.TestResult{}
	}
	return out, nil
}

func (s *liteSnapshot) Benchmark(ctx context.Context, n int) (map[string]int64, error) {
	out := map[string]int64{}
	for _, d := range s.Decisions() {
		out[d.Name] = 0
	}
	return out, nil
}

var liteCatalogue = json.RawMessage(`{
 "variables": [
  {"name": "input", "type": "any", "description": "The node's input, already validated against its input schema."},
  {"name": "request", "type": "any", "description": "The original request input."},
  {"name": "nodes", "type": "object", "description": "Outputs of nodes that already ran: nodes.<id>."},
  {"name": "out", "type": "object", "description": "Transform only: fields assigned so far."},
  {"name": "ctx", "type": "object", "description": "ctx.now (time), ctx.unit, ctx.requestId, ctx.env."}
 ],
 "functions": [
  {"name": "len", "signature": "len(v) int", "description": "Length of a string, array or map."},
  {"name": "map", "signature": "map(array, predicate) array", "description": "Apply an expression to every element (use . for the element)."},
  {"name": "filter", "signature": "filter(array, predicate) array", "description": "Keep elements for which the predicate is true."},
  {"name": "mean", "signature": "mean(array) float", "description": "Average of numbers."},
  {"name": "sum", "signature": "sum(array) number", "description": "Sum of numbers."},
  {"name": "lower", "signature": "lower(string) string", "description": "Lower-case a string."},
  {"name": "upper", "signature": "upper(string) string", "description": "Upper-case a string."},
  {"name": "now", "signature": "now() time", "description": "Current time (prefer ctx.now)."}
 ],
 "operators": [
  {"symbol": "==", "description": "Equal"}, {"symbol": "!=", "description": "Not equal"},
  {"symbol": "<", "description": "Less than"}, {"symbol": ">", "description": "Greater than"},
  {"symbol": "&&", "description": "And"}, {"symbol": "||", "description": "Or"}, {"symbol": "!", "description": "Not"},
  {"symbol": "??", "description": "Nil coalescing"}, {"symbol": "?:", "description": "Ternary"},
  {"symbol": "in", "description": "Membership in array or map"}
 ],
 "conditionOperators": [
  {"op": "EQ", "description": "Equals", "valueKind": "scalar"},
  {"op": "NEQ", "description": "Not equal", "valueKind": "scalar"},
  {"op": "GT", "description": "Greater than", "valueKind": "scalar"},
  {"op": "GTE", "description": "Greater than or equal", "valueKind": "scalar"},
  {"op": "LT", "description": "Less than", "valueKind": "scalar"},
  {"op": "LTE", "description": "Less than or equal", "valueKind": "scalar"},
  {"op": "BETWEEN", "description": "Between value and valueTo (inclusive)", "valueKind": "range"},
  {"op": "IN", "description": "One of a list", "valueKind": "list"},
  {"op": "NOT_IN", "description": "None of a list", "valueKind": "list"},
  {"op": "CONTAINS", "description": "String or array contains", "valueKind": "scalar"},
  {"op": "STARTS_WITH", "description": "String prefix", "valueKind": "scalar"},
  {"op": "ENDS_WITH", "description": "String suffix", "valueKind": "scalar"},
  {"op": "MATCHES", "description": "RE2 regular expression", "valueKind": "regex"},
  {"op": "EXISTS", "description": "Field is present and not null", "valueKind": "none"},
  {"op": "NOT_EXISTS", "description": "Field is missing or null", "valueKind": "none"},
  {"op": "IS_TRUE", "description": "Boolean true", "valueKind": "none"},
  {"op": "IS_FALSE", "description": "Boolean false", "valueKind": "none"},
  {"op": "IN_SEGMENT", "description": "Unit is in a segment", "valueKind": "segment"},
  {"op": "NOT_IN_SEGMENT", "description": "Unit is not in a segment", "valueKind": "segment"},
  {"op": "GATE_PASSES", "description": "Gate is on for the unit", "valueKind": "gate"},
  {"op": "GATE_FAILS", "description": "Gate is off for the unit", "valueKind": "gate"}
 ]
}`)
