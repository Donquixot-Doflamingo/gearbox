// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// Limits checked when compiling.
const (
	MaxNodes         = 200
	MaxSubdecisions  = 8 // runtime nesting depth
	MaxRules         = 500
	MaxTableRows     = 5000
	MaxTests         = 500
	defaultBudgetMic = 200
)

// builder compiles resources, reporting problems. It is used both by
// ValidateResource (one resource, dependencies from a Resolver) and by
// Compile (every resource of a Set).
type builder struct {
	res    Resolver
	app    string
	env    string
	piiKey []byte

	problems []Problem
	deps     map[Ref]bool

	schemas     map[string]*schemaEntry
	segments    map[string]*segment
	lookups     map[string]*lookup
	gates       map[string]*gate
	experiments map[string]*experiment
	layers      map[string]*layer
	decisions   map[string]*decision

	invalid  map[string]bool // "kind/name@v" that failed to compile
	stack    []string        // decisions being compiled (cycle detection)
	synth    synthSchemas
	building map[string]bool
}

type schemaEntry struct {
	name    string
	version int64
	schema  *jsonschema.Schema
}

func newBuilder(res Resolver, app, env string, piiKey []byte) *builder {
	return &builder{
		res: res, app: app, env: env, piiKey: piiKey,
		deps:        map[Ref]bool{},
		schemas:     map[string]*schemaEntry{},
		segments:    map[string]*segment{},
		lookups:     map[string]*lookup{},
		gates:       map[string]*gate{},
		experiments: map[string]*experiment{},
		layers:      map[string]*layer{},
		decisions:   map[string]*decision{},
		invalid:     map[string]bool{},
		building:    map[string]bool{},
		synth:       newSynthSchemas(),
	}
}

func (b *builder) errorf(code, path, format string, args ...any) {
	b.problems = append(b.problems, Problem{Code: code, Severity: "error", Path: path, Message: fmt.Sprintf(format, args...)})
}

func (b *builder) warnf(code, path, format string, args ...any) {
	b.problems = append(b.problems, Problem{Code: code, Severity: "warning", Path: path, Message: fmt.Sprintf(format, args...)})
}

func (b *builder) errorCount() int {
	n := 0
	for _, p := range b.problems {
		if p.Severity == "error" {
			n++
		}
	}
	return n
}

// isolated runs f with a fresh problem list and dependency set and returns
// what f produced (used when compiling a dependency).
func (b *builder) isolated(f func()) ([]Problem, map[Ref]bool) {
	savedP, savedD := b.problems, b.deps
	b.problems, b.deps = nil, map[Ref]bool{}
	f()
	p, d := b.problems, b.deps
	b.problems, b.deps = savedP, savedD
	return p, d
}

func hasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == "error" {
			return true
		}
	}
	return false
}

func key(kind Kind, name string, version int64) string {
	return string(kind) + "/" + name + "@" + strconv.FormatInt(version, 10)
}

func (b *builder) dep(kind Kind, name string, version int64) {
	b.deps[Ref{Kind: kind, Name: name, Version: version}] = true
}

// ---- schemas

// schemaRef resolves a pinned schema reference. requiredFor is used in the
// SCHEMA_REQUIRED message; empty means the reference is optional.
func (b *builder) schemaRef(ref *schemaRef, path string) *schemaEntry {
	if ref == nil {
		return nil
	}
	if ref.Name == "" || ref.Version < 1 {
		b.errorf("SCHEMA_NOT_FOUND", path, "schema reference needs a name and a version >= 1")
		return nil
	}
	b.dep(KindSchema, ref.Name, ref.Version)
	k := key(KindSchema, ref.Name, ref.Version)
	if e, ok := b.schemas[k]; ok {
		return e
	}
	if b.invalid[k] {
		b.errorf("SCHEMA_NOT_FOUND", path, "schema %s is not valid", ref)
		return nil
	}
	r, ok := b.res.Get(KindSchema, ref.Name, ref.Version)
	if !ok || r.Version != ref.Version {
		b.errorf("SCHEMA_NOT_FOUND", path, "schema %s is not registered; register it under Schemas and cut version %d first", ref, ref.Version)
		return nil
	}
	e := b.compileSchemaResource(r)
	if e == nil {
		b.errorf("SCHEMA_NOT_FOUND", path, "schema %s is not valid", ref)
	}
	return e
}

func (b *builder) compileSchemaResource(r Resource) *schemaEntry {
	k := key(KindSchema, r.Name, r.Version)
	if e, ok := b.schemas[k]; ok {
		return e
	}
	if b.invalid[k] {
		return nil
	}
	var e *schemaEntry
	problems, _ := b.isolated(func() { e = b.schemaSpec(r) })
	if hasErrors(problems) || e == nil {
		b.invalid[k] = true
		return nil
	}
	b.schemas[k] = e
	return e
}

func (b *builder) schemaSpec(r Resource) *schemaEntry {
	var spec schemaSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	if isNull(spec.JSONSchema) {
		b.errorf("INVALID_SPEC", "/jsonSchema", "jsonSchema is required")
		return nil
	}
	s, issues := jsonschema.Compile(spec.JSONSchema)
	for _, is := range issues {
		code := is.Code
		if code == "INVALID_REGEX" {
			code = "PARSE_ERROR"
		}
		b.errorf(code, "/jsonSchema"+is.Path, "%s", is.Message)
	}
	if s == nil {
		return nil
	}
	for i, ex := range spec.Examples {
		var doc any
		if err := json.Unmarshal(ex, &doc); err != nil {
			b.errorf("INVALID_SPEC", fmt.Sprintf("/examples/%d", i), "example is not valid JSON")
			continue
		}
		for _, fe := range s.Validate(doc) {
			b.errorf("EXAMPLE_INVALID", fmt.Sprintf("/examples/%d%s", i, fe.Path), "example does not match the schema: %s", fe.Message)
		}
	}
	return &schemaEntry{name: r.Name, version: r.Version, schema: s}
}

// ---- named (live) resources

func (b *builder) liveResource(kind Kind, name string) (Resource, bool) {
	r, ok := b.res.Get(kind, name, 0)
	if !ok || r.Name != name {
		return Resource{}, false
	}
	return r, true
}

// checkRef verifies a name used by an expression function.
func (b *builder) checkRef(fn, name, path string) {
	switch fn {
	case "lookup":
		if b.lookup(name, path) == nil {
			return
		}
	case "inSegment":
		b.segment(name, path)
	case "gate":
		b.gate(name, path)
	case "variant":
		b.experiment(name, path)
	}
}

func (b *builder) segment(name, path string) *segment {
	if s, ok := b.segments[name]; ok {
		b.dep(KindSegment, name, 0)
		return s
	}
	r, ok := b.liveResource(KindSegment, name)
	if !ok {
		b.errorf("SEGMENT_NOT_FOUND", path, "segment %q does not exist", name)
		return nil
	}
	b.dep(KindSegment, name, 0)
	s := b.compileNamed(KindSegment, r, func() any { return b.segmentSpec(r) })
	if s == nil {
		b.errorf("SEGMENT_NOT_FOUND", path, "segment %q is not valid", name)
		return nil
	}
	return s.(*segment)
}

func (b *builder) lookup(name, path string) *lookup {
	if l, ok := b.lookups[name]; ok {
		b.dep(KindLookup, name, 0)
		return l
	}
	r, ok := b.liveResource(KindLookup, name)
	if !ok {
		b.errorf("LOOKUP_NOT_FOUND", path, "lookup %q does not exist", name)
		return nil
	}
	b.dep(KindLookup, name, 0)
	l := b.compileNamed(KindLookup, r, func() any { return b.lookupSpec(r) })
	if l == nil {
		b.errorf("LOOKUP_NOT_FOUND", path, "lookup %q is not valid", name)
		return nil
	}
	return l.(*lookup)
}

func (b *builder) gate(name, path string) *gate {
	if g, ok := b.gates[name]; ok {
		b.dep(KindGate, name, 0)
		return g
	}
	r, ok := b.liveResource(KindGate, name)
	if !ok {
		b.errorf("GATE_NOT_FOUND", path, "gate %q does not exist", name)
		return nil
	}
	b.dep(KindGate, name, 0)
	g := b.compileNamed(KindGate, r, func() any { return b.gateSpec(r) })
	if g == nil {
		b.errorf("GATE_NOT_FOUND", path, "gate %q is not valid", name)
		return nil
	}
	return g.(*gate)
}

func (b *builder) experiment(name, path string) *experiment {
	if e, ok := b.experiments[name]; ok {
		b.dep(KindExperiment, name, 0)
		return e
	}
	r, ok := b.liveResource(KindExperiment, name)
	if !ok {
		b.errorf("EXPERIMENT_NOT_FOUND", path, "experiment %q does not exist", name)
		return nil
	}
	b.dep(KindExperiment, name, 0)
	e := b.compileNamed(KindExperiment, r, func() any { return b.experimentSpec(r) })
	if e == nil {
		b.errorf("EXPERIMENT_NOT_FOUND", path, "experiment %q is not valid", name)
		return nil
	}
	return e.(*experiment)
}

func (b *builder) layer(name, path string) *layer {
	if l, ok := b.layers[name]; ok {
		b.dep(KindLayer, name, 0)
		return l
	}
	r, ok := b.liveResource(KindLayer, name)
	if !ok {
		b.errorf("LAYER_NOT_FOUND", path, "layer %q does not exist", name)
		return nil
	}
	b.dep(KindLayer, name, 0)
	l := b.compileNamed(KindLayer, r, func() any { return b.layerSpec(r) })
	if l == nil {
		b.errorf("LAYER_NOT_FOUND", path, "layer %q is not valid", name)
		return nil
	}
	return l.(*layer)
}

// compileNamed compiles a dependency in isolation, caching the result.
func (b *builder) compileNamed(kind Kind, r Resource, f func() any) any {
	k := key(kind, r.Name, r.Version)
	if b.invalid[k] {
		return nil
	}
	if b.building[k] {
		// A cycle between resources (for example a gate whose condition
		// reads itself). Report on the outer resource.
		b.errorf("CYCLE", "", "%s %q depends on itself", kind, r.Name)
		return nil
	}
	b.building[k] = true
	var out any
	problems, deps := b.isolated(func() { out = f() })
	delete(b.building, k)
	if hasErrors(problems) || out == nil || isNilResource(out) {
		b.invalid[k] = true
		return nil
	}
	setDeps(out, deps)
	switch kind {
	case KindSegment:
		b.segments[r.Name] = out.(*segment)
	case KindLookup:
		b.lookups[r.Name] = out.(*lookup)
	case KindGate:
		b.gates[r.Name] = out.(*gate)
	case KindExperiment:
		b.experiments[r.Name] = out.(*experiment)
	case KindLayer:
		b.layers[r.Name] = out.(*layer)
	}
	return out
}

func isNilResource(v any) bool {
	switch t := v.(type) {
	case *segment:
		return t == nil
	case *lookup:
		return t == nil
	case *gate:
		return t == nil
	case *experiment:
		return t == nil
	case *layer:
		return t == nil
	case *decision:
		return t == nil
	}
	return false
}

func setDeps(v any, deps map[Ref]bool) {
	refs := sortedRefs(deps)
	switch t := v.(type) {
	case *segment:
		t.deps = refs
	case *lookup:
		t.deps = refs
	case *gate:
		t.deps = refs
	case *experiment:
		t.deps = refs
	case *layer:
		t.deps = refs
	}
}

func sortedRefs(m map[Ref]bool) []Ref {
	out := make([]Ref, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// validateResource compiles one resource and returns its problems and deps.
func (b *builder) validateResource(r Resource) ([]Problem, []Ref, any) {
	var out any
	problems, deps := b.isolated(func() {
		switch r.Kind {
		case KindSchema:
			if e := b.schemaSpec(r); e != nil {
				out = e
			}
		case KindDecision:
			if d := b.decisionResource(r); d != nil {
				out = d
			}
		case KindSegment:
			if s := b.segmentSpec(r); s != nil {
				out = s
			}
		case KindLookup:
			if l := b.lookupSpec(r); l != nil {
				out = l
			}
		case KindGate:
			if g := b.gateSpec(r); g != nil {
				out = g
			}
		case KindExperiment:
			if e := b.experimentSpec(r); e != nil {
				out = e
			}
		case KindLayer:
			if l := b.layerSpec(r); l != nil {
				out = l
			}
		default:
			b.errorf("INVALID_SPEC", "", "unknown resource kind %q", r.Kind)
		}
	})
	if !nameRE(r.Name) {
		problems = append([]Problem{{Code: "INVALID_NAME", Severity: "error", Path: "", Message: fmt.Sprintf("name %q must match ^[a-z][a-z0-9_]{1,62}$", r.Name)}}, problems...)
	}
	if out != nil {
		setDeps(out, deps)
	}
	return problems, sortedRefs(deps), out
}

func nameRE(s string) bool {
	if len(s) < 2 || len(s) > 63 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func nodeIDOK(s string) bool {
	if len(s) < 1 || len(s) > 63 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
