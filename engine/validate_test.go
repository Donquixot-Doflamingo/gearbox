// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

var (
	inSchema  = schemaRes("in_s", 1, `{"type":"object","required":["km","city"],"properties":{"km":{"type":"integer"},"city":{"type":"string"},"phone":{"type":"string","x-gearbox-pii":true},"tags":{"type":"array","items":{"type":"string"}},"items":{"type":"array","maxItems":2000,"items":{"type":"object","properties":{"subs":{"type":"array","maxItems":1000,"items":{"type":"number"}}}}}}}`)
	outSchema = schemaRes("out_s", 1, `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"},"note":{"type":"string"}}}`)
	otherIn   = schemaRes("other_s", 1, `{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}}`)
	staticSeg = res(KindSegment, "vip", 1, `{"type":"static","static":{"keyType":"string"}}`)
)

func rulesDecision(when string, extra string) Resource {
	return res(KindDecision, "dec", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"in_s","version":1},"outputSchema":{"name":"out_s","version":1},"rules":[{"id":"a","when":`+when+`,"then":{"ok":true}}],"default":{"ok":false}`+extra+`}]}`)
}

func TestValidateProblems(t *testing.T) {
	deps := resolverOf(inSchema, outSchema, otherIn, staticSeg)
	cases := []struct {
		name string
		r    Resource
		want string // first error code
	}{
		{"valid", rulesDecision(`{"field":"km","op":"GT","value":5}`, ""), ""},
		{"schema required", res(KindDecision, "dec", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","outputSchema":{"name":"out_s","version":1},"rules":[]}]}`), "SCHEMA_REQUIRED"},
		{"schema not found", res(KindDecision, "dec", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"in_s","version":9},"outputSchema":{"name":"out_s","version":1},"rules":[]}]}`), "SCHEMA_NOT_FOUND"},
		{"type mismatch value", rulesDecision(`{"field":"km","op":"GT","value":"60000"}`, ""), "TYPE_MISMATCH"},
		{"type mismatch op", rulesDecision(`{"field":"km","op":"STARTS_WITH","value":"6"}`, ""), "TYPE_MISMATCH"},
		{"unknown field", rulesDecision(`{"field":"kms","op":"GT","value":5}`, ""), "UNKNOWN_FIELD"},
		{"unknown field in expr", rulesDecision(`{"expr":"input.cty == \"x\""}`, ""), "UNKNOWN_FIELD"},
		{"unknown function", rulesDecision(`{"expr":"frobnicate(input.km)"}`, ""), "UNKNOWN_FUNCTION"},
		{"now disabled", rulesDecision(`{"expr":"now().Year() > 2000"}`, ""), "UNKNOWN_FUNCTION"},
		{"parse error", rulesDecision(`{"expr":"input.km >"}`, ""), "PARSE_ERROR"},
		{"bad regex", rulesDecision(`{"field":"city","op":"MATCHES","value":"(?<=x)"}`, ""), "PARSE_ERROR"},
		{"segment not found", rulesDecision(`{"segment":"nope","op":"IN_SEGMENT","key":"input.city"}`, ""), "SEGMENT_NOT_FOUND"},
		{"static segment needs key", rulesDecision(`{"segment":"vip","op":"IN_SEGMENT"}`, ""), "MISSING_VALUE"},
		{"then mismatch", res(KindDecision, "dec", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"in_s","version":1},"outputSchema":{"name":"out_s","version":1},"rules":[{"id":"a","when":{"field":"km","op":"EXISTS"},"then":{"ok":"yes"}}]}]}`), "TYPE_MISMATCH"},
		{"cost no maxItems", rulesDecision(`{"expr":"any(input.tags, # == \"x\")"}`, ""), "COST_TOO_HIGH"},
		{"cost nested product", rulesDecision(`{"expr":"any(input.items, any(.subs, # > 1))"}`, ""), "COST_TOO_HIGH"},
		{"cycle", res(KindDecision, "dec", 1, `{"entry":"a","nodes":[{"id":"a","type":"transform","fields":[{"target":"x","expr":"1"}],"next":"b"},{"id":"b","type":"transform","fields":[{"target":"x","expr":"1"}],"next":"a"}]}`), "CYCLE"},
		{"unreachable", res(KindDecision, "dec", 1, `{"entry":"a","nodes":[{"id":"a","type":"transform","fields":[{"target":"x","expr":"1"}]},{"id":"b","type":"transform","fields":[{"target":"x","expr":"1"}]}]}`), "UNREACHABLE_NODE"},
		{"unknown node", res(KindDecision, "dec", 1, `{"entry":"a","nodes":[{"id":"a","type":"transform","fields":[{"target":"x","expr":"1"}],"next":"zz"}]}`), "UNKNOWN_NODE"},
		{"edge mismatch", res(KindDecision, "dec", 1, `{"entry":"t","nodes":[{"id":"t","type":"transform","outputSchema":{"name":"other_s","version":1},"fields":[{"target":"x","expr":"\"a\""}],"next":"r"},{"id":"r","type":"rules","inputSchema":{"name":"in_s","version":1},"outputSchema":{"name":"out_s","version":1},"rules":[],"default":{"ok":true}}]}`), "SCHEMA_EDGE_MISMATCH"},
		{"pii in prompt", res(KindDecision, "dec", 1, `{"entry":"r","inputSchema":{"name":"in_s","version":1},"nodes":[{"id":"r","type":"ai","model":"m","mode":"inline","timeoutMs":100,"vars":{"p":"input.phone"},"prompt":{"user":"{{ p }}"},"responseSchema":{"type":"object"},"onError":"f"},{"id":"f","type":"transform","fields":[{"target":"x","expr":"1"}]}]}`), "PII_IN_PROMPT"},
		{"pii laundered through transform", res(KindDecision, "dec", 1, `{"entry":"t","inputSchema":{"name":"in_s","version":1},"nodes":[{"id":"t","type":"transform","fields":[{"target":"contact","expr":"\"tel:\" + input.phone"},{"target":"city","expr":"input.city"}],"next":"a"},{"id":"a","type":"ai","model":"m","mode":"shadow","timeoutMs":100,"vars":{"c":"input.contact"},"prompt":{"user":"{{ c }}"},"responseSchema":{"type":"object"},"onError":"f"},{"id":"f","type":"transform","fields":[{"target":"x","expr":"1"}]}]}`), "PII_IN_PROMPT"},
		{"pii logging field", res(KindDecision, "dec", 1, `{"entry":"t","inputSchema":{"name":"in_s","version":1},"logging":{"fields":["phone"]},"nodes":[{"id":"t","type":"transform","fields":[{"target":"x","expr":"1"}]}]}`), "PII_IN_PROMPT"},
		{"precompute", res(KindDecision, "dec", 1, `{"entry":"a","nodes":[{"id":"a","type":"ai","model":"m","mode":"precompute","timeoutMs":100,"prompt":{"user":"hi"},"responseSchema":{"type":"object"},"onError":"f"},{"id":"f","type":"transform","fields":[{"target":"x","expr":"1"}]}]}`), "NOT_IMPLEMENTED"},
		{"unknown node property", res(KindDecision, "dec", 1, `{"entry":"a","nodes":[{"id":"a","type":"transform","fieldz":[]}]}`), "INVALID_SPEC"},
		{"name not literal", rulesDecision(`{"expr":"inSegment(input.city, input.city)"}`, ""), "NAME_NOT_LITERAL"},
		{"bad schema", schemaRes("sch", 1, `{"type":"object","oneOf":[]}`), "UNKNOWN_KEYWORD"},
		{"segment dynamic needs schema", res(KindSegment, "seg", 1, `{"type":"dynamic","dynamic":{"when":{"field":"a","op":"EXISTS"}}}`), "SCHEMA_REQUIRED"},
		{"lookup needs schema", res(KindLookup, "lk", 1, `{"keyType":"string"}`), "SCHEMA_REQUIRED"},
		{"experiment one variant", res(KindExperiment, "ex", 1, `{"status":"running","variants":[{"name":"a","weight":1}]}`), "INVALID_SPEC"},
		{"layer over 100", res(KindLayer, "ly", 1, `{"allocations":[{"experiment":"aa","percent":60},{"experiment":"bb","percent":50}]}`), "INVALID_SPEC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := ValidateResource(c.r, deps)
			codes := problemCodes(v)
			first := strings.Split(codes, ",")[0]
			if first != c.want {
				b, _ := json.MarshalIndent(v.Problems, "", " ")
				t.Fatalf("first error = %q, want %q; problems: %s", first, c.want, b)
			}
			if c.want == "" && !v.Valid {
				t.Fatal("expected valid")
			}
			for _, p := range v.Problems {
				if p.Path != "" && !strings.HasPrefix(p.Path, "/") {
					t.Fatalf("problem path %q is not a JSON pointer", p.Path)
				}
			}
		})
	}
}

func TestValidateReportsPaths(t *testing.T) {
	v := ValidateResource(rulesDecision(`{"all":[{"field":"km","op":"GT","value":1},{"field":"km","op":"GT","value":"60000"}]}`, ""), resolverOf(inSchema, outSchema))
	if v.Valid || v.Problems[0].Path != "/nodes/0/rules/0/when/all/1" {
		t.Fatalf("problems = %+v", v.Problems)
	}
	if !strings.Contains(v.Problems[0].Message, "km is integer in in_s@1") {
		t.Fatalf("message = %q", v.Problems[0].Message)
	}
}

func TestValidateDependencies(t *testing.T) {
	r := rulesDecision(`{"segment":"vip","op":"IN_SEGMENT","key":"input.city"}`, "")
	v := ValidateResource(r, resolverOf(inSchema, outSchema, staticSeg))
	if !v.Valid {
		t.Fatalf("problems: %+v", v.Problems)
	}
	var got []string
	for _, d := range v.Dependencies {
		got = append(got, string(d.Kind)+"/"+d.Name)
	}
	if strings.Join(got, ",") != "schemas/in_s,schemas/out_s,segments/vip" {
		t.Fatalf("deps = %v", got)
	}
	deps, err := Dependencies(r)
	if err != nil || len(deps) != 3 {
		t.Fatalf("Dependencies = %v %v", deps, err)
	}
}

func TestMayNotHaveRunWarning(t *testing.T) {
	r := res(KindDecision, "dec", 1, `{"entry":"s","nodes":[
		{"id":"s","type":"switch","cases":[{"id":"yes","when":"input.x == 1","next":"a"}],"default":"b"},
		{"id":"a","type":"transform","fields":[{"target":"v","expr":"1"}],"next":"o"},
		{"id":"b","type":"transform","fields":[{"target":"v","expr":"2"}],"next":"o"},
		{"id":"o","type":"output","fields":[{"target":"v","expr":"nodes.a?.v ?? nodes.b.v"}]}]}`)
	v := ValidateResource(r, resolverOf())
	if !v.Valid {
		t.Fatalf("problems: %+v", v.Problems)
	}
	warned := false
	for _, p := range v.Problems {
		if p.Code == "NODE_MAY_NOT_HAVE_RUN" && p.Severity == "warning" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected warning, got %+v", v.Problems)
	}
}

func TestSchemaCompatibilityAPI(t *testing.T) {
	ok, changes, err := CheckSchemaCompatibility(json.RawMessage(`{"jsonSchema":{"type":"object","properties":{"a":{"type":"string"}}}}`), json.RawMessage(`{"jsonSchema":{"type":"object","properties":{}}}`))
	if err != nil || ok || len(changes) != 1 || changes[0].Kind != "property_removed" || !changes[0].Breaking {
		t.Fatalf("got %v %+v %v", ok, changes, err)
	}
	valid, errs, err := ValidateDocument(json.RawMessage(`{"jsonSchema":{"type":"object","required":["car"],"properties":{"car":{"type":"object","required":["price"]}}}}`), json.RawMessage(`{"car":{}}`))
	if err != nil || valid || len(errs) != 1 || errs[0].Path != "/car/price" || errs[0].Message != "required property missing" {
		t.Fatalf("got %v %+v %v", valid, errs, err)
	}
}

func TestCatalogue(t *testing.T) {
	var cat struct {
		Functions          []map[string]any `json:"functions"`
		ConditionOperators []map[string]any `json:"conditionOperators"`
		Variables          []map[string]any `json:"variables"`
		Operators          []map[string]any `json:"operators"`
	}
	if err := json.Unmarshal(Catalogue(), &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.ConditionOperators) != 39 || len(cat.Functions) < 40 || len(cat.Variables) == 0 || len(cat.Operators) == 0 {
		t.Fatalf("catalogue sizes: %d %d %d", len(cat.ConditionOperators), len(cat.Functions), len(cat.Variables))
	}
	for _, f := range cat.Functions {
		if f["name"] == "now" {
			t.Fatal("now must not be listed")
		}
	}
}
