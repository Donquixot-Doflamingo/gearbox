// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// CompileOptions tunes Compile. The zero value is valid.
type CompileOptions struct {
	// PIIKey is the HMAC key for members of pii static segments. Empty
	// means DefaultPIIKey(set.App).
	PIIKey []byte
}

// CompileError lists the problems that made Compile fail.
type CompileError struct {
	Problems map[string][]Problem // "kind/name@version" → problems
}

func (e *CompileError) Error() string {
	keys := make([]string, 0, len(e.Problems))
	for k := range e.Problems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, p := range e.Problems[k] {
			if p.Severity != "error" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s %s: %s", k, p.Path, p.Code, p.Message))
			if len(parts) >= 10 {
				break
			}
		}
	}
	return "engine: compile failed: " + strings.Join(parts, "; ")
}

// setResolver serves a Set: version 0 means the live version.
type setResolver struct {
	byKey map[string]Resource
	live  map[string]int64 // "kind/name" → live version
}

func newSetResolver(set Set) *setResolver {
	sr := &setResolver{byKey: map[string]Resource{}, live: map[string]int64{}}
	for _, r := range set.Resources {
		sr.byKey[key(r.Kind, r.Name, r.Version)] = r
		lk := string(r.Kind) + "/" + r.Name
		if r.Version > sr.live[lk] {
			sr.live[lk] = r.Version
		}
	}
	for _, ro := range set.Rollouts {
		sr.live[string(KindDecision)+"/"+ro.Decision] = ro.ToVersion
	}
	return sr
}

func (sr *setResolver) Get(kind Kind, name string, version int64) (Resource, bool) {
	if version == 0 {
		v, ok := sr.live[string(kind)+"/"+name]
		if !ok {
			return Resource{}, false
		}
		version = v
	}
	r, ok := sr.byKey[key(kind, name, version)]
	return r, ok
}

// CompileWithOptions is Compile with options.
func CompileWithOptions(set Set, opts CompileOptions) (*Snapshot, error) {
	c, err := compileSet(set, opts)
	if err != nil {
		return nil, err
	}
	return &Snapshot{set: set, c: c}, nil
}

func compileSet(set Set, opts CompileOptions) (*compiled, error) {
	piiKey := opts.PIIKey
	if len(piiKey) == 0 {
		piiKey = DefaultPIIKey(set.App)
	}
	sr := newSetResolver(set)
	b := newBuilder(sr, set.App, set.Environment, piiKey)
	failed := map[string][]Problem{}
	seen := map[string]bool{}
	// Schemas first, then the rest in a stable order.
	res := append([]Resource(nil), set.Resources...)
	order := map[Kind]int{KindSchema: 0, KindLayer: 1, KindSegment: 2, KindLookup: 3, KindGate: 4, KindExperiment: 5, KindDecision: 6}
	sort.SliceStable(res, func(i, j int) bool {
		if order[res[i].Kind] != order[res[j].Kind] {
			return order[res[i].Kind] < order[res[j].Kind]
		}
		if res[i].Name != res[j].Name {
			return res[i].Name < res[j].Name
		}
		return res[i].Version < res[j].Version
	})
	c := &compiled{
		app: set.App, env: set.Environment, generation: set.Generation,
		schemas: map[string]*schemaEntry{}, segments: map[string]*segment{}, lookups: map[string]*lookup{},
		gates: map[string]*gate{}, experiments: map[string]*experiment{}, layers: map[string]*layer{},
		decisions: map[string]*decisionEntry{}, killed: map[string]bool{},
	}
	for _, r := range res {
		k := key(r.Kind, r.Name, r.Version)
		if seen[k] {
			failed[k] = append(failed[k], Problem{Code: "INVALID_SPEC", Severity: "error", Message: "resource appears twice in the set"})
			continue
		}
		seen[k] = true
		problems, _, out := b.validateResource(r)
		if hasErrors(problems) || out == nil {
			failed[k] = problems
			continue
		}
		live := sr.live[string(r.Kind)+"/"+r.Name] == r.Version
		switch v := out.(type) {
		case *schemaEntry:
			c.schemas[k] = v
		case *segment:
			if live {
				c.segments[r.Name] = v
			}
		case *lookup:
			if live {
				c.lookups[r.Name] = v
			}
		case *gate:
			if live {
				c.gates[r.Name] = v
			}
		case *experiment:
			if live {
				c.experiments[r.Name] = v
			}
		case *layer:
			if live {
				c.layers[r.Name] = v
			}
		case *decision:
			e := c.decisions[r.Name]
			if e == nil {
				e = &decisionEntry{versions: map[int64]*decision{}}
				c.decisions[r.Name] = e
			}
			e.versions[r.Version] = v
			if live {
				e.live = v
			}
		}
	}
	for i, ro := range set.Rollouts {
		k := fmt.Sprintf("rollouts/%d", i)
		e := c.decisions[ro.Decision]
		if e == nil || e.versions[ro.FromVersion] == nil || e.versions[ro.ToVersion] == nil {
			failed[k] = []Problem{{Code: "DECISION_NOT_FOUND", Severity: "error", Message: fmt.Sprintf("rollout of %s needs versions %d and %d in the set", ro.Decision, ro.FromVersion, ro.ToVersion)}}
			continue
		}
		if ro.Percent < 0 || ro.Percent > 100 {
			failed[k] = []Problem{{Code: "INVALID_SPEC", Severity: "error", Message: "rollout percent must be between 0 and 100"}}
			continue
		}
		salt := ro.Salt
		if salt == "" {
			salt = ro.Decision + ".rollout"
		}
		e.rollout = &rolloutState{from: e.versions[ro.FromVersion], to: e.versions[ro.ToVersion], percent: ro.Percent, salt: salt}
	}
	for _, g := range set.KilledGates {
		c.killed[g] = true
	}
	if len(failed) > 0 {
		return nil, &CompileError{Problems: failed}
	}
	for _, e := range c.decisions {
		for _, d := range e.versions {
			d.depHash = c.dependencyHash(d)
		}
		if e.live == nil {
			// Only non-live versions present: newest is live.
			var best *decision
			for _, d := range e.versions {
				if best == nil || d.version > best.version {
					best = d
				}
			}
			e.live = best
		}
	}
	return c, nil
}

// validate implements ValidateResource.
func validate(r Resource, deps Resolver) Validation {
	if deps == nil {
		deps = emptyResolver{}
	}
	b := newBuilder(selfResolver{r: r, next: deps}, "", "", nil)
	problems, refs, _ := b.validateResource(r)
	if problems == nil {
		problems = []Problem{}
	}
	v := Validation{Valid: !hasErrors(problems), Problems: problems}
	if v.Valid {
		v.Dependencies = refs
	}
	return v
}

type emptyResolver struct{}

func (emptyResolver) Get(Kind, string, int64) (Resource, bool) { return Resource{}, false }

// selfResolver lets a draft refer to its own name consistently.
type selfResolver struct {
	r    Resource
	next Resolver
}

func (s selfResolver) Get(kind Kind, name string, version int64) (Resource, bool) {
	if kind == s.r.Kind && name == s.r.Name && (version == 0 || version == s.r.Version) {
		return s.r, true
	}
	return s.next.Get(kind, name, version)
}

// dependencies implements Dependencies without needing other resources.
func dependencies(r Resource) ([]Ref, error) {
	var refs []Ref
	seen := map[Ref]bool{}
	add := func(kind Kind, name string, version int64) {
		ref := Ref{Kind: kind, Name: name, Version: version}
		if name != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	var spec any
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}
	switch r.Kind {
	case KindSchema:
		return []Ref{}, nil
	case KindLayer:
		var ls layerSpec
		if err := json.Unmarshal(r.Spec, &ls); err != nil {
			return nil, err
		}
		for _, a := range ls.Allocations {
			add(KindExperiment, a.Experiment, 0)
		}
		return refs, nil
	}
	var walk func(v any, parentKey string)
	walk = func(v any, parentKey string) {
		switch t := v.(type) {
		case map[string]any:
			// Schema references: {name, version}
			if name, ok := t["name"].(string); ok {
				if ver, ok := t["version"].(float64); ok && len(t) == 2 {
					switch parentKey {
					case "inputSchema", "outputSchema", "attributesSchema", "valueSchema", "schema":
						add(KindSchema, name, int64(ver))
					}
				}
			}
			if s, ok := t["segment"].(string); ok {
				add(KindSegment, s, 0)
			}
			if s, ok := t["gate"].(string); ok {
				add(KindGate, s, 0)
			}
			if s, ok := t["experiment"].(string); ok {
				add(KindExperiment, s, 0)
			}
			if typ, _ := t["type"].(string); typ == "lookup" {
				if s, ok := t["lookup"].(string); ok {
					add(KindLookup, s, 0)
				}
			}
			if typ, _ := t["type"].(string); typ == "subdecision" {
				if s, ok := t["decision"].(string); ok {
					add(KindDecision, s, 0)
				}
			}
			if s, ok := t["layer"].(string); ok && r.Kind == KindExperiment && parentKey == "" {
				add(KindLayer, s, 0)
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k == "responseSchema" || k == "tests" || k == "jsonSchema" {
					continue
				}
				walk(t[k], k)
			}
		case []any:
			for _, e := range t {
				walk(e, parentKey)
			}
		case string:
			for _, fn := range []struct {
				name string
				kind Kind
			}{{"lookup", KindLookup}, {"inSegment", KindSegment}, {"gate", KindGate}, {"variant", KindExperiment}} {
				for _, m := range funcRefRE(fn.name).FindAllStringSubmatch(t, -1) {
					add(fn.kind, m[1], 0)
				}
			}
		}
	}
	walk(spec, "")
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Version < refs[j].Version
	})
	if refs == nil {
		refs = []Ref{}
	}
	return refs, nil
}

// checkSchemaCompatibility implements CheckSchemaCompatibility.
func checkSchemaCompatibility(prev, next json.RawMessage) (bool, []SchemaChange, error) {
	ps, err := schemaFromSpec(prev)
	if err != nil {
		return false, nil, fmt.Errorf("previous schema: %w", err)
	}
	ns, err := schemaFromSpec(next)
	if err != nil {
		return false, nil, fmt.Errorf("next schema: %w", err)
	}
	changes := jsonschema.Compare(ps, ns)
	out := make([]SchemaChange, 0, len(changes))
	ok := true
	for _, c := range changes {
		out = append(out, SchemaChange{Path: c.Path, Kind: c.Kind, Breaking: c.Breaking})
		if c.Breaking {
			ok = false
		}
	}
	return ok, out, nil
}

// schemaFromSpec accepts a SchemaSpec ({jsonSchema: ...}) or a bare JSON Schema.
func schemaFromSpec(raw json.RawMessage) (*jsonschema.Schema, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err == nil {
		if js, ok := probe["jsonSchema"]; ok {
			raw = js
		}
	}
	s, issues := jsonschema.Compile(raw)
	if len(issues) > 0 {
		return nil, issues[0]
	}
	return s, nil
}

func validateDocument(schemaSpec, doc json.RawMessage) (bool, []FieldError, error) {
	s, err := schemaFromSpec(schemaSpec)
	if err != nil {
		return false, nil, err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return false, []FieldError{{Path: "", Message: "document is not valid JSON: " + err.Error()}}, nil
	}
	errs := s.Validate(v)
	if len(errs) == 0 {
		return true, []FieldError{}, nil
	}
	return false, toFieldErrors(errs), nil
}

// ---- snapshot queries

func (c *compiled) decisionInfos() []DecisionInfo {
	names := make([]string, 0, len(c.decisions))
	for n := range c.decisions {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]DecisionInfo, 0, len(names))
	for _, n := range names {
		d := c.decisions[n].live
		out = append(out, DecisionInfo{Name: n, Version: d.version, Kind: d.kind})
	}
	return out
}

func decodeAttrs(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || isNull(raw) {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("attributes are not valid JSON: %w", err)
	}
	return v, nil
}

func (c *compiled) apiGate(name, unit string, attributes json.RawMessage) (GateResult, error) {
	attrs, err := decodeAttrs(attributes)
	if err != nil {
		return GateResult{}, err
	}
	r := c.getRun(bgCtx, time.Now(), unit, "")
	defer c.putRun(r)
	return c.checkGate(r, name, unit, attrs)
}

func (c *compiled) apiVariant(name, unit string, attributes json.RawMessage) (VariantResult, error) {
	attrs, err := decodeAttrs(attributes)
	if err != nil {
		return VariantResult{}, err
	}
	e := c.experiments[name]
	if e == nil {
		return VariantResult{}, fmt.Errorf("experiment %q is not in the bundle", name)
	}
	r := c.getRun(bgCtx, time.Now(), unit, "")
	defer c.putRun(r)
	return e.assign(r, unit, attrs)
}

func (c *compiled) apiSegment(name, key, unit string, attributes json.RawMessage) (bool, string, error) {
	s := c.segments[name]
	if s == nil {
		return false, "", fmt.Errorf("segment %q is not in the bundle", name)
	}
	attrs, err := decodeAttrs(attributes)
	if err != nil {
		return false, "", err
	}
	r := c.getRun(bgCtx, time.Now(), unit, "")
	defer c.putRun(r)
	var k any = key
	if s.typ == "rollout" && key == "" {
		k = unit
	}
	return s.member(r, k, attrs)
}

var bgCtx = context.Background()

var funcRefCache = map[string]*regexp.Regexp{
	"lookup":    regexp.MustCompile(`\blookup\(\s*["']([a-z][a-z0-9_]*)["']`),
	"inSegment": regexp.MustCompile(`\binSegment\(\s*["']([a-z][a-z0-9_]*)["']`),
	"gate":      regexp.MustCompile(`\bgate\(\s*["']([a-z][a-z0-9_]*)["']`),
	"variant":   regexp.MustCompile(`\bvariant\(\s*["']([a-z][a-z0-9_]*)["']`),
}

func funcRefRE(name string) *regexp.Regexp { return funcRefCache[name] }
