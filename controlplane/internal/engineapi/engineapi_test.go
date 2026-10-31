package engineapi

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

const leadRouting = `{
 "entry": "normalize",
 "nodes": [
  {"id": "normalize", "type": "transform", "outputSchema": {"name": "eligibility_input", "version": 1},
   "fields": [{"target": "city", "expr": "input.lead.city"}], "next": "eligibility"},
  {"id": "eligibility", "type": "rules", "inputSchema": {"name": "eligibility_input", "version": 1},
   "outputSchema": {"name": "eligibility_output", "version": 1},
   "rules": [
    {"id": "r1", "when": {"segment": "blocked_cities", "op": "IN_SEGMENT", "key": "input.city"}, "then": {"eligible": false, "reason": "city_not_served"}},
    {"id": "r4", "when": {"all": [{"field": "price", "op": "GT", "value": 1}, {"segment": "metro_premium", "op": "NOT_IN_SEGMENT", "attributes": {"city": "input.city", "price": "input.price"}}, {"gate": "new_flow", "op": "GATE_PASSES"}]},
     "then": {"eligible": true, "lane": "fast", "reason": "fast_lane"}}
   ],
   "default": {"eligible": true, "lane": "standard", "reason": "default"}, "next": "output"},
  {"id": "output", "type": "output", "fields": [{"target": "route", "expr": "nodes.eligibility.lane"}]}
 ],
 "output": {"schema": {"name": "routing_output", "version": 1}},
 "fallback": {"route": "manual_review"},
 "tests": [{"name": "t", "input": {"segment": "not-a-ref"}, "expect": {"route": "fast_track"}}]
}`

func TestDeps(t *testing.T) {
	deps := Deps("decisions", json.RawMessage(leadRouting))
	got := map[string]bool{}
	for _, d := range deps {
		got[d.Kind+":"+d.Name] = true
	}
	for _, want := range []string{"schemas:eligibility_input", "schemas:eligibility_output", "schemas:routing_output", "segments:blocked_cities", "segments:metro_premium", "gates:new_flow"} {
		if !got[want] {
			t.Errorf("missing dep %s in %+v", want, deps)
		}
	}
	if got["segments:not-a-ref"] {
		t.Error("walked into test data")
	}
	slots := RequiredSchemaSlots(json.RawMessage(leadRouting))
	if len(slots) != 2 || slots[0].Missing || slots[0].Where != "/nodes/1/inputSchema" {
		t.Fatalf("slots %+v", slots)
	}
	slots = RequiredSchemaSlots(json.RawMessage(`{"entry":"a","nodes":[{"id":"a","type":"rules","rules":[]}]}`))
	if len(slots) != 2 || !slots[0].Missing || !slots[1].Missing {
		t.Fatalf("missing slots %+v", slots)
	}
	if DecisionSubtype(json.RawMessage(leadRouting)) != "graph" {
		t.Error("subtype")
	}
	if DecisionSubtype(json.RawMessage(`{"entry":"a","nodes":[{"id":"a","type":"rules"}]}`)) != "rules" {
		t.Error("subtype rules")
	}
}

const leadInput = `{"jsonSchema": {"type": "object", "required": ["lead", "car"], "properties": {
  "lead": {"type": "object", "required": ["id", "city"], "properties": {"id": {"type": "string"}, "first_name": {"type": ["string", "null"]}, "city": {"type": "string"}, "source": {"enum": ["app", "web", "partner"]}}},
  "car": {"type": "object", "required": ["year", "price"], "properties": {"year": {"type": "integer", "minimum": 1980}, "price": {"type": "number", "minimum": 0}}},
  "inspections": {"type": "array", "maxItems": 2, "items": {"type": "object", "required": ["part"], "properties": {"part": {"type": "string"}}}}}}}`

func TestValidateDocument(t *testing.T) {
	ok, errs, err := Lite{}.ValidateDocument(json.RawMessage(leadInput), json.RawMessage(`{"lead":{"id":"L","city":"Pune","first_name":null},"car":{"year":2019,"price":1.5}}`))
	if err != nil || !ok {
		t.Fatalf("valid doc rejected: %v %+v", err, errs)
	}
	ok, errs, _ = Lite{}.ValidateDocument(json.RawMessage(leadInput), json.RawMessage(`{"lead":{"id":1,"city":"Pune","source":"tv"},"car":{"year":1970.5},"inspections":[{},{},{}]}`))
	if ok {
		t.Fatal("invalid doc accepted")
	}
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	for _, p := range []string{"/lead/id", "/lead/source", "/car/year", "/car/price", "/inspections", "/inspections/0/part"} {
		if !paths[p] {
			t.Errorf("no error at %s: %+v", p, errs)
		}
	}
}

func TestCompatibility(t *testing.T) {
	prev := json.RawMessage(`{"jsonSchema":{"type":"object","required":["a"],"properties":{"a":{"type":["string","null"]},"b":{"type":"integer"},"c":{"enum":["x","y"]}}}}`)
	ok, ch, err := Lite{}.CheckSchemaCompatibility(prev, json.RawMessage(`{"jsonSchema":{"type":"object","required":["a"],"properties":{"a":{"type":["string","null"]},"b":{"type":"number"},"c":{"enum":["x","y","z"]},"d":{"type":"string"}}}}`))
	if err != nil || !ok {
		t.Fatalf("compatible change flagged: %+v", ch)
	}
	ok, ch, _ = Lite{}.CheckSchemaCompatibility(prev, json.RawMessage(`{"jsonSchema":{"type":"object","required":["a","b"],"properties":{"a":{"type":"string"},"b":{"type":"integer"},"c":{"enum":["x"]}}}}`))
	if ok {
		t.Fatal("breaking change accepted")
	}
	kinds := map[string]bool{}
	for _, c := range ch {
		kinds[c.Kind] = c.Breaking
	}
	for _, k := range []string{"null_removed", "became_required", "enum_narrowed"} {
		if !kinds[k] {
			t.Errorf("missing breaking %s in %+v", k, ch)
		}
	}
	_, ch, _ = Lite{}.CheckSchemaCompatibility(prev, json.RawMessage(`{"jsonSchema":{"type":"object","properties":{"b":{"type":"integer"},"c":{"enum":["x","y"]}}}}`))
	found := false
	for _, c := range ch {
		if c.Kind == "property_removed" && c.Path == "/a" && c.Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("property_removed not reported: %+v", ch)
	}
}

func testSet() engine.Set {
	schema := func(name, js string) engine.Resource {
		return engine.Resource{Kind: engine.KindSchema, Name: name, Version: 1, Spec: json.RawMessage(`{"jsonSchema":` + js + `}`)}
	}
	return engine.Set{App: "a", Environment: "dev", Generation: 3, Resources: []engine.Resource{
		schema("eligibility_input", `{"type":"object","required":["price"],"properties":{"price":{"type":"number"},"city":{"type":"string"}}}`),
		schema("eligibility_output", `{"type":"object","properties":{"eligible":{"type":"boolean"},"lane":{"type":"string"},"reason":{"type":"string"}}}`),
		schema("routing_output", `{"type":"object"}`),
		{Kind: engine.KindSegment, Name: "blocked_cities", Version: 1, Spec: json.RawMessage(`{"type":"static"}`), SegmentMembers: []string{"Srinagar"}},
		{Kind: engine.KindSegment, Name: "metro_premium", Version: 1, Spec: json.RawMessage(`{"type":"dynamic","dynamic":{"attributesSchema":{"name":"eligibility_input","version":1},"when":{"all":[{"field":"city","op":"IN","value":["Delhi"]},{"field":"price","op":"GT","value":800000}]}}}`)},
		{Kind: engine.KindSegment, Name: "beta", Version: 1, Spec: json.RawMessage(`{"type":"rollout","rollout":{"percent":50}}`)},
		{Kind: engine.KindGate, Name: "new_flow", Version: 2, Spec: json.RawMessage(`{"rules":[{"id":"delhi","when":{"field":"city","op":"EQ","value":"Delhi"},"value":true}],"default":false}`)},
		{Kind: engine.KindExperiment, Name: "exp", Version: 1, Spec: json.RawMessage(`{"status":"running","variants":[{"name":"a","weight":1},{"name":"b","weight":1,"parameters":{"x":1}}]}`)},
		{Kind: engine.KindDecision, Name: "lead_routing", Version: 1, Spec: json.RawMessage(leadRouting)},
		{Kind: engine.KindDecision, Name: "lead_routing", Version: 2, Spec: json.RawMessage(leadRouting)},
	}, Rollouts: []engine.Rollout{{Decision: "lead_routing", FromVersion: 1, ToVersion: 2, Percent: 50, Salt: "s"}}}
}

func TestLiteEngine(t *testing.T) {
	ctx := context.Background()
	snap, err := Lite{}.Compile(testSet())
	if err != nil {
		t.Fatal(err)
	}
	if d := snap.Decisions(); len(d) != 1 || d[0].Version != 2 || d[0].Kind != "graph" {
		t.Fatalf("decisions %+v", d)
	}
	res, err := snap.Decide(ctx, engine.Request{Decision: "lead_routing", Input: json.RawMessage(`{"x":1}`), UnitID: "u1"})
	if err != nil || !res.FallbackUsed || string(res.Output) != `{"route": "manual_review"}` && !strings.Contains(string(res.Output), "manual_review") {
		t.Fatalf("decide: %v %+v", err, res)
	}
	_, err = snap.Decide(ctx, engine.Request{Decision: "nope"})
	var ee *engine.Error
	if !errors.As(err, &ee) || ee.Reason != "DECISION_NOT_FOUND" {
		t.Fatalf("not found: %v", err)
	}
	g, _ := snap.CheckGate("new_flow", "u", json.RawMessage(`{"city":"Delhi"}`))
	if !g.Value || g.RuleID != "delhi" || g.Version != 2 {
		t.Fatalf("gate %+v", g)
	}
	g, _ = snap.CheckGate("new_flow", "u", json.RawMessage(`{"city":"Pune"}`))
	if g.Value || g.RuleID != "default" {
		t.Fatalf("gate default %+v", g)
	}
	m, reason, _ := snap.CheckSegment("metro_premium", "", "", json.RawMessage(`{"city":"Delhi","price":900000}`))
	if !m || reason != "conditions_matched" {
		t.Fatalf("dynamic %v %s", m, reason)
	}
	m, reason, _ = snap.CheckSegment("blocked_cities", "Srinagar", "", nil)
	if !m || reason != "listed" {
		t.Fatalf("static %v %s", m, reason)
	}
	_, reason, _ = snap.CheckSegment("beta", "", "unit-1", nil)
	if !strings.HasPrefix(reason, "bucket ") {
		t.Fatalf("rollout reason %s", reason)
	}
	v, _ := snap.GetVariant("exp", "unit-1", nil)
	if !v.InExperiment || v.Reason != "assigned" || (v.Variant != "a" && v.Variant != "b") {
		t.Fatalf("variant %+v", v)
	}
	v, _ = snap.GetVariant("exp", "", nil)
	if v.Reason != "no_unit" {
		t.Fatalf("no unit %+v", v)
	}
	tr, err := snap.RunTests(ctx, "lead_routing", 2)
	if err != nil || len(tr) != 1 || !tr[0].OK {
		t.Fatalf("tests %v %+v", err, tr)
	}
}

func TestLiteValidate(t *testing.T) {
	s := testSet()
	res := setResolver{s}
	bad := engine.Resource{Kind: engine.KindDecision, Name: "d", Spec: json.RawMessage(`{"entry":"a","nodes":[
	 {"id":"a","type":"rules","inputSchema":{"name":"eligibility_input","version":1},"outputSchema":{"name":"eligibility_output","version":1},
	  "rules":[{"id":"r","when":{"segment":"ghost","op":"IN_SEGMENT","key":"x"},"then":{"eligible":"yes"}}],"next":"b"},
	 {"id":"b","type":"switch","cases":[{"when":"true","next":"a"}],"default":"zz"}]}`)}
	v := Lite{}.ValidateResource(bad, res)
	if v.Valid {
		t.Fatal("bad decision valid")
	}
	codes := map[string]bool{}
	for _, p := range v.Problems {
		codes[p.Code] = true
	}
	for _, c := range []string{"SEGMENT_NOT_FOUND", "OUTPUT_SCHEMA_MISMATCH", "UNKNOWN_NODE", "CYCLE"} {
		if !codes[c] {
			t.Errorf("missing %s in %+v", c, v.Problems)
		}
	}
	good := Lite{}.ValidateResource(s.Resources[len(s.Resources)-1], res)
	if !good.Valid || len(good.Dependencies) == 0 {
		t.Fatalf("good decision: %+v", good)
	}
}

func TestLiteBundleRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	s := testSet()
	s.KilledGates = []string{"new_flow"}
	b, err := LiteBundler{}.Build(s, bundle.Meta{ReleaseType: "change_request", ReleaseID: "cr_1", PublishedBy: "x"}, bundle.Signer{KeyID: "k1", PrivateKey: priv})
	if err != nil {
		t.Fatal(err)
	}
	gen, needed, err := LiteBundler{}.VerifyEnvelope(b.Envelope, bundle.TrustedKeys{"k1": pub})
	if err != nil || gen != 3 || len(needed) != len(b.Blobs) {
		t.Fatalf("verify: %v gen=%d needed=%d blobs=%d", err, gen, len(needed), len(b.Blobs))
	}
	if _, _, err := (LiteBundler{}).VerifyEnvelope(b.Envelope, bundle.TrustedKeys{"other": pub}); err == nil {
		t.Fatal("untrusted key accepted")
	}
	tampered := append([]byte(nil), b.Envelope...)
	tampered[10] ^= 1
	if _, _, err := (LiteBundler{}).VerifyEnvelope(tampered, bundle.TrustedKeys{"k1": pub}); err == nil {
		t.Fatal("tampered envelope accepted")
	}
	file, _ := LiteBundler{}.WriteFile(b)
	env, blobs, err := ReadLiteFile(file)
	if err != nil || string(env) != string(b.Envelope) || len(blobs) != len(b.Blobs) {
		t.Fatalf("file round trip: %v", err)
	}
}

func TestMurmur3(t *testing.T) {
	for in, want := range map[string]uint32{"": 0, "hello": 0x248bfa47, "The quick brown fox jumps over the lazy dog": 0x2e4ff723} {
		if got := Murmur3([]byte(in)); got != want {
			t.Errorf("murmur3(%q) = %x, want %x", in, got, want)
		}
	}
}

func TestSelect(t *testing.T) {
	for _, m := range []string{"", "auto", "real", "lite"} {
		if _, _, err := Select(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Select("bogus"); err == nil {
		t.Fatal("bogus mode accepted")
	}
	// In auto mode the hybrid engine must always be usable, stubbed or not.
	snap, err := Hybrid{}.Compile(testSet())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snap.RunTests(context.Background(), "", 0); err != nil {
		t.Fatal(err)
	}
}
