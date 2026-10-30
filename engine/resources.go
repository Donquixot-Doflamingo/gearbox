// SPDX-License-Identifier: MIT

package engine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/ORG/gearbox/engine/internal/hashing"
	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// SegmentMemberHMAC is how members of a pii static segment are stored and
// shipped: lowercase hex HMAC-SHA256(key, member). The control plane must
// hash uploaded members with the same key the sidecar compiles with
// (CompileOptions.PIIKey, default DefaultPIIKey(app)).
func SegmentMemberHMAC(key []byte, member string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(member))
	return hex.EncodeToString(m.Sum(nil))
}

// DefaultPIIKey is the HMAC key used when no installation key is configured.
// It only prevents members from appearing in clear in bundles; configure a
// secret key for real protection (see engine/README.md).
func DefaultPIIKey(app string) []byte {
	return []byte("gearbox-pii-hmac-v1:" + app)
}

// ---- segments

type segment struct {
	name, typ  string
	version    int64
	members    map[string]struct{}
	pii        bool
	piiKey     []byte
	attrs      *jsonschema.Schema
	attrsLabel string
	when       condFn
	percent    float64
	salt       string
	include    condFn
	app        string
	deps       []Ref
}

func (b *builder) segmentSpec(r Resource) *segment {
	var spec segmentSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	s := &segment{name: r.Name, version: r.Version, typ: spec.Type, piiKey: b.piiKey, app: b.app}
	attrScope := &scope{vars: varsAttrs}
	switch spec.Type {
	case "static":
		if spec.Dynamic != nil || spec.Rollout != nil {
			b.errorf("INVALID_SPEC", "", "a static segment has only the static block")
		}
		if spec.Static != nil {
			s.pii = spec.Static.PII
			if kt := spec.Static.KeyType; kt != "" && kt != "string" && kt != "number" {
				b.errorf("INVALID_SPEC", "/static/keyType", "keyType must be string or number")
			}
		}
		s.members = make(map[string]struct{}, len(r.SegmentMembers))
		for _, m := range r.SegmentMembers {
			s.members[m] = struct{}{}
		}
	case "dynamic":
		if spec.Dynamic == nil {
			b.errorf("INVALID_SPEC", "/dynamic", "a dynamic segment needs the dynamic block")
			return nil
		}
		if spec.Dynamic.AttributesSchema == nil {
			b.errorf("SCHEMA_REQUIRED", "/dynamic/attributesSchema", "a dynamic segment needs a registered attributesSchema")
			return nil
		}
		e := b.schemaRef(spec.Dynamic.AttributesSchema, "/dynamic/attributesSchema")
		if e == nil {
			return nil
		}
		s.attrs, s.attrsLabel = e.schema, spec.Dynamic.AttributesSchema.String()
		attrScope.input = sview{schema: e.schema, label: s.attrsLabel}
		if isNull(spec.Dynamic.When) {
			b.errorf("INVALID_SPEC", "/dynamic/when", "a dynamic segment needs a condition")
			return nil
		}
		s.when = b.cond(spec.Dynamic.When, "/dynamic/when", attrScope)
		if s.when == nil {
			return nil
		}
	case "rollout":
		if spec.Rollout == nil || spec.Rollout.Percent == nil {
			b.errorf("INVALID_SPEC", "/rollout/percent", "a rollout segment needs a percent")
			return nil
		}
		s.percent = *spec.Rollout.Percent
		if s.percent < 0 || s.percent > 100 {
			b.errorf("INVALID_SPEC", "/rollout/percent", "percent must be between 0 and 100")
		}
		s.salt = spec.Rollout.Salt
		if s.salt == "" {
			s.salt = r.Name
		}
		if !isNull(spec.Rollout.Include) {
			s.include = b.cond(spec.Rollout.Include, "/rollout/include", attrScope)
			if s.include == nil {
				return nil
			}
		}
	default:
		b.errorf("INVALID_SPEC", "/type", "segment type must be static, dynamic or rollout")
		return nil
	}
	return s
}

// member tests membership. key is used by static and rollout segments,
// attrs by dynamic segments and rollout include conditions.
func (s *segment) member(r *run, key any, attrs any) (bool, string, error) {
	switch s.typ {
	case "static":
		k, ok := scalarString(key)
		if !ok {
			return false, "no_key", nil
		}
		if s.pii {
			k = SegmentMemberHMAC(s.piiKey, k)
		}
		if _, ok := s.members[k]; ok {
			return true, "listed", nil
		}
		return false, "not_listed", nil
	case "dynamic":
		am, ok := attrs.(map[string]any)
		if !ok {
			return false, "", fmt.Errorf("segment %q needs attributes", s.name)
		}
		if !s.attrs.Valid(am) {
			errs := s.attrs.Validate(am)
			return false, "", fmt.Errorf("segment %q: attributes do not match %s: %s %s", s.name, s.attrsLabel, errs[0].Path, errs[0].Message)
		}
		m, err := s.when(r, am)
		if err != nil {
			return false, "", err
		}
		if m {
			return true, "conditions_matched", nil
		}
		return false, "conditions_failed", nil
	case "rollout":
		if s.include != nil {
			ok, err := s.include(r, attrs)
			if err != nil {
				return false, "", err
			}
			if !ok {
				return false, "not_included", nil
			}
		}
		k, ok := scalarString(key)
		if !ok || k == "" {
			return false, "no_unit", nil
		}
		bucket := hashing.RolloutBucket(s.app, s.salt, k)
		limit := int(s.percent * 100)
		if bucket < limit {
			return true, fmt.Sprintf("bucket %d < %d", bucket, limit), nil
		}
		return false, fmt.Sprintf("bucket %d >= %d", bucket, limit), nil
	}
	return false, "", errors.New("unknown segment type")
}

// ---- lookups

type lookup struct {
	name        string
	version     int64
	rows        map[string]any
	valueSchema *jsonschema.Schema
	deps        []Ref
}

func (b *builder) lookupSpec(r Resource) *lookup {
	var spec lookupSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	if spec.KeyType != "string" && spec.KeyType != "number" {
		b.errorf("INVALID_SPEC", "/keyType", "keyType must be string or number")
	}
	if spec.ValueSchema == nil {
		b.errorf("SCHEMA_REQUIRED", "/valueSchema", "a lookup needs a registered valueSchema")
		return nil
	}
	e := b.schemaRef(spec.ValueSchema, "/valueSchema")
	if e == nil {
		return nil
	}
	l := &lookup{name: r.Name, version: r.Version, rows: make(map[string]any, len(r.LookupRows)), valueSchema: e.schema}
	bad := 0
	for k, raw := range r.LookupRows {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			if bad < 10 {
				b.errorf("INVALID_SPEC", "/rows/"+ptrEscape(k), "row value is not valid JSON")
			}
			bad++
			continue
		}
		if spec.KeyType == "number" {
			if f, err := strconv.ParseFloat(k, 64); err != nil {
				if bad < 10 {
					b.errorf("TYPE_MISMATCH", "/rows/"+ptrEscape(k), "key %q is not a number", k)
				}
				bad++
				continue
			} else {
				k = formatNumber(f)
			}
		}
		if errs := e.schema.Validate(v); len(errs) > 0 {
			if bad < 10 {
				b.errorf("TYPE_MISMATCH", "/rows/"+ptrEscape(k)+errs[0].Path, "row does not match %s: %s", spec.ValueSchema, errs[0].Message)
			}
			bad++
			continue
		}
		l.rows[k] = v
	}
	return l
}

// ---- gates

type gateRule struct {
	id      string
	when    condFn
	value   bool
	percent *float64
	salt    string
}

type gate struct {
	name    string
	version int64
	enabled bool
	rules   []gateRule
	def     bool
	deps    []Ref
}

func (b *builder) gateSpec(r Resource) *gate {
	var spec gateSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	g := &gate{name: r.Name, version: r.Version, enabled: spec.Enabled == nil || *spec.Enabled}
	if spec.Default == nil {
		b.errorf("INVALID_SPEC", "/default", "default is required")
	} else {
		g.def = *spec.Default
	}
	sc := &scope{vars: varsAttrs}
	if spec.AttributesSchema != nil {
		e := b.schemaRef(spec.AttributesSchema, "/attributesSchema")
		if e != nil {
			sc.input = sview{schema: e.schema, label: spec.AttributesSchema.String()}
		}
	}
	seen := map[string]bool{}
	for i, rs := range spec.Rules {
		p := fmt.Sprintf("/rules/%d", i)
		if rs.ID == "" {
			b.errorf("INVALID_SPEC", p+"/id", "rule id is required")
		} else if seen[rs.ID] {
			b.errorf("DUPLICATE_ID", p+"/id", "duplicate rule id %q", rs.ID)
		}
		seen[rs.ID] = true
		if rs.Value == nil {
			b.errorf("INVALID_SPEC", p+"/value", "value is required")
			continue
		}
		gr := gateRule{id: rs.ID, value: *rs.Value}
		if isNull(rs.When) {
			gr.when = func(*run, any) (bool, error) { return true, nil }
		} else if gr.when = b.cond(rs.When, p+"/when", sc); gr.when == nil {
			continue
		}
		if rs.Rollout != nil {
			if rs.Rollout.Percent == nil || *rs.Rollout.Percent < 0 || *rs.Rollout.Percent > 100 {
				b.errorf("INVALID_SPEC", p+"/rollout/percent", "rollout percent must be between 0 and 100")
				continue
			}
			gr.percent = rs.Rollout.Percent
			gr.salt = rs.Rollout.Salt
			if gr.salt == "" {
				gr.salt = r.Name + "." + rs.ID
			}
		}
		g.rules = append(g.rules, gr)
	}
	return g
}

// eval evaluates the gate (killed is checked by the caller).
func (g *gate) eval(r *run, unit string, attrs any) (GateResult, error) {
	if !g.enabled {
		return GateResult{Value: false, Version: g.version, RuleID: "disabled"}, nil
	}
	skippedNoUnit := false
	for i := range g.rules {
		gr := &g.rules[i]
		if gr.percent != nil && unit == "" {
			skippedNoUnit = true
			continue
		}
		ok, err := gr.when(r, attrs)
		if err != nil {
			return GateResult{}, fmt.Errorf("gate %q rule %s: %w", g.name, gr.id, err)
		}
		if !ok {
			continue
		}
		if gr.percent != nil && r.c.bucket(gr.salt, unit) >= int(*gr.percent*100) {
			continue
		}
		return GateResult{Value: gr.value, Version: g.version, RuleID: gr.id}, nil
	}
	id := "default"
	if skippedNoUnit {
		id = "no_unit"
	}
	return GateResult{Value: g.def, Version: g.version, RuleID: id}, nil
}

// ---- experiments and layers

type variantDef struct {
	name   string
	weight float64
	params *dataSlot
}

type experiment struct {
	name      string
	version   int64
	status    string
	salt      string
	layer     string
	holdout   float64
	targeting condFn
	variants  []variantDef
	byName    map[string]*variantDef
	ranges    hashing.Ranges
	stopped   string
	deps      []Ref
}

func (b *builder) experimentSpec(r Resource) *experiment {
	var spec experimentSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	e := &experiment{name: r.Name, version: r.Version, status: spec.Status, salt: spec.Salt, layer: spec.Layer, holdout: spec.HoldoutPercent, byName: map[string]*variantDef{}}
	if e.salt == "" {
		e.salt = r.Name
	}
	switch spec.Status {
	case "draft", "running", "stopped":
	default:
		b.errorf("INVALID_SPEC", "/status", "status must be draft, running or stopped")
	}
	if e.holdout < 0 || e.holdout > 50 {
		b.errorf("INVALID_SPEC", "/holdoutPercent", "holdoutPercent must be between 0 and 50")
	}
	sc := &scope{vars: varsAttrs}
	if spec.AttributesSchema != nil {
		if se := b.schemaRef(spec.AttributesSchema, "/attributesSchema"); se != nil {
			sc.input = sview{schema: se.schema, label: spec.AttributesSchema.String()}
		}
	}
	if !isNull(spec.Targeting) {
		e.targeting = b.cond(spec.Targeting, "/targeting", sc)
	}
	if len(spec.Variants) < 2 {
		b.errorf("INVALID_SPEC", "/variants", "an experiment needs at least two variants")
	}
	var names []string
	var weights []float64
	total := 0.0
	for i, v := range spec.Variants {
		p := fmt.Sprintf("/variants/%d", i)
		if v.Name == "" {
			b.errorf("INVALID_SPEC", p+"/name", "variant name is required")
			continue
		}
		if e.byName[v.Name] != nil {
			b.errorf("DUPLICATE_ID", p+"/name", "duplicate variant %q", v.Name)
			continue
		}
		if v.Weight == nil || *v.Weight < 0 {
			b.errorf("INVALID_SPEC", p+"/weight", "weight must be a number >= 0")
			continue
		}
		params := b.data(v.Parameters, p+"/parameters", sc)
		if params == nil {
			continue
		}
		e.variants = append(e.variants, variantDef{name: v.Name, weight: *v.Weight, params: params})
		names = append(names, v.Name)
		weights = append(weights, *v.Weight)
		total += *v.Weight
	}
	for i := range e.variants {
		e.byName[e.variants[i].name] = &e.variants[i]
	}
	if len(e.variants) > 0 && total <= 0 {
		b.errorf("INVALID_SPEC", "/variants", "variant weights must not all be zero")
	} else if total > 0 {
		e.ranges = hashing.Cumulative(names, weights, total)
	}
	e.stopped = spec.StoppedVariant
	if e.stopped == "" && len(e.variants) > 0 {
		e.stopped = e.variants[0].name
	} else if e.stopped != "" && e.byName[e.stopped] == nil {
		b.errorf("UNKNOWN_VARIANT", "/stoppedVariant", "stoppedVariant %q is not a variant", e.stopped)
	}
	if spec.Layer != "" {
		l := b.layer(spec.Layer, "/layer")
		if l != nil && !l.has(r.Name) {
			b.warnf("LAYER_CONFLICT", "/layer", "layer %q has no allocation for this experiment; every unit will get layer_conflict", spec.Layer)
		}
	}
	return e
}

func (e *experiment) defaultVariant() *variantDef {
	if v := e.byName[e.stopped]; v != nil {
		return v
	}
	if len(e.variants) > 0 {
		return &e.variants[0]
	}
	return nil
}

type layer struct {
	name    string
	version int64
	salt    string
	ranges  hashing.Ranges
	members map[string]bool
	deps    []Ref
}

func (l *layer) has(exp string) bool { return l.members[exp] }

func (b *builder) layerSpec(r Resource) *layer {
	var spec layerSpec
	if err := decodeStrict(r.Spec, &spec); err != nil {
		p, m := decodeError("", err)
		b.errorf("INVALID_SPEC", p, "%s", m)
		return nil
	}
	l := &layer{name: r.Name, version: r.Version, salt: spec.Salt, members: map[string]bool{}}
	if l.salt == "" {
		l.salt = r.Name
	}
	var names []string
	var shares []float64
	sum := 0.0
	for i, a := range spec.Allocations {
		p := fmt.Sprintf("/allocations/%d", i)
		if a.Percent < 0 || a.Percent > 100 {
			b.errorf("INVALID_SPEC", p+"/percent", "percent must be between 0 and 100")
			continue
		}
		if l.members[a.Experiment] {
			b.errorf("DUPLICATE_ID", p+"/experiment", "experiment %q is allocated twice", a.Experiment)
			continue
		}
		if !nameRE(a.Experiment) {
			b.errorf("INVALID_SPEC", p+"/experiment", "invalid experiment name %q", a.Experiment)
			continue
		}
		l.members[a.Experiment] = true
		names = append(names, a.Experiment)
		shares = append(shares, a.Percent)
		sum += a.Percent
	}
	if sum > 100+1e-9 {
		b.errorf("INVALID_SPEC", "/allocations", "allocations sum to %.4g%%; they must sum to at most 100%%", sum)
		return nil
	}
	l.ranges = hashing.Cumulative(names, shares, 100)
	return l
}

// assign evaluates an experiment for a unit; attrs are the targeting
// attributes. Order (parity with the previous service): status, unit,
// holdout, layer allocation, targeting, variant bucket.
func (e *experiment) assign(r *run, unit string, attrs any) (VariantResult, error) {
	res := VariantResult{Version: e.version}
	def := e.defaultVariant()
	fill := func(v *variantDef) error {
		if v == nil {
			return nil
		}
		res.Variant = v.name
		p, err := v.params.eval(r, attrs)
		if err != nil {
			return err
		}
		if v.params.present {
			res.Parameters = encodeJSON(p)
		}
		return nil
	}
	notIn := func(reason string) (VariantResult, error) {
		res.Reason = reason
		return res, fill(def)
	}
	if e.status != "running" {
		return notIn("stopped")
	}
	if unit == "" {
		return notIn("no_unit")
	}
	app := r.c.app
	if e.holdout > 0 {
		hb := hashing.Bucket(hashing.Request{SchemaVersion: "1", Tenant: app, Scope: hashing.ScopeHoldout, ResourceID: e.name, AssignmentVersion: "1", Salt: e.salt, Value: unit})
		if hb < int(e.holdout*100) {
			return notIn("holdout")
		}
	}
	if e.layer != "" {
		l := r.c.layers[e.layer]
		if l == nil {
			return notIn("layer_conflict")
		}
		lb := hashing.Bucket(hashing.Request{SchemaVersion: "1", Tenant: app, Scope: hashing.ScopeLayer, ResourceID: l.name, AssignmentVersion: "1", Salt: l.salt, Value: unit})
		owner, ok := l.ranges.Lookup(lb)
		if !ok || owner != e.name {
			return notIn("layer_conflict")
		}
	}
	if e.targeting != nil {
		ok, err := e.targeting(r, attrs)
		if err != nil {
			return VariantResult{}, err
		}
		if !ok {
			return notIn("not_targeted")
		}
	}
	vb := hashing.Bucket(hashing.Request{SchemaVersion: "1", Tenant: app, Scope: hashing.ScopeExperiment, ResourceID: e.name, AssignmentVersion: "1", Salt: e.salt, Value: unit})
	name, ok := e.ranges.Lookup(vb)
	if !ok {
		return notIn("not_targeted")
	}
	res.InExperiment = true
	res.Reason = "assigned"
	return res, fill(e.byName[name])
}
