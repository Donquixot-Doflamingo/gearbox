// SPDX-License-Identifier: MIT

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"
)

const leadInput = `{
  "type": "object",
  "required": ["lead", "car"],
  "properties": {
    "lead": {
      "type": "object",
      "required": ["id", "city"],
      "properties": {
        "id": {"type": "string"},
        "first_name": {"type": ["string", "null"]},
        "phone": {"type": "string", "x-gearbox-pii": true},
        "city": {"type": "string"},
        "source": {"enum": ["app", "web", "partner"]}
      }
    },
    "car": {
      "type": "object",
      "required": ["year", "km", "price"],
      "properties": {
        "year": {"type": "integer", "minimum": 1980},
        "km": {"type": "integer", "minimum": 0},
        "price": {"type": "number", "minimum": 0}
      }
    },
    "inspections": {
      "type": "array", "maxItems": 3,
      "items": {"type": "object", "required": ["part", "score"], "properties": {"part": {"type": "string"}, "score": {"type": "number"}}}
    }
  }
}`

func mustCompile(t *testing.T, src string) *Schema {
	t.Helper()
	s, issues := Compile(json.RawMessage(src))
	if len(issues) > 0 {
		t.Fatalf("compile: %v", issues)
	}
	return s
}

func decode(t *testing.T, src string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestValidate(t *testing.T) {
	s := mustCompile(t, leadInput)
	cases := []struct {
		name string
		doc  string
		errs []string // "path: keyword"
	}{
		{"valid", `{"lead":{"id":"L1","city":"Pune","source":"web","first_name":null},"car":{"year":2019,"km":10,"price":5.5}}`, nil},
		{"missing nested required", `{"lead":{"id":"L-1","city":"Pune"},"car":{"make":"Maruti","year":2019,"km":1}}`, []string{"/car/price: required"}},
		{"wrong type", `{"lead":{"id":1,"city":"Pune"},"car":{"year":2019,"km":1,"price":1}}`, []string{"/lead/id: type"}},
		{"integer required", `{"lead":{"id":"a","city":"Pune"},"car":{"year":2019.5,"km":1,"price":1}}`, []string{"/car/year: type"}},
		{"enum", `{"lead":{"id":"a","city":"Pune","source":"tv"},"car":{"year":2019,"km":1,"price":1}}`, []string{"/lead/source: enum"}},
		{"minimum", `{"lead":{"id":"a","city":"Pune"},"car":{"year":1970,"km":-1,"price":1}}`, []string{"/car/km: minimum", "/car/year: minimum"}},
		{"null not allowed", `{"lead":{"id":"a","city":null},"car":{"year":2000,"km":1,"price":1}}`, []string{"/lead/city: type"}},
		{"maxItems", `{"lead":{"id":"a","city":"x"},"car":{"year":2000,"km":1,"price":1},"inspections":[{"part":"a","score":1},{"part":"a","score":1},{"part":"a","score":1},{"part":"a","score":1}]}`, []string{"/inspections: maxItems"}},
		{"array item", `{"lead":{"id":"a","city":"x"},"car":{"year":2000,"km":1,"price":1},"inspections":[{"part":"a"}]}`, []string{"/inspections/0/score: required"}},
		{"root type", `[1]`, []string{": type"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := decode(t, c.doc)
			errs := s.Validate(doc)
			if s.Valid(doc) != (len(c.errs) == 0) {
				t.Fatalf("Valid mismatch: %v", errs)
			}
			var got []string
			for _, e := range errs {
				got = append(got, e.Path+": "+e.Keyword)
			}
			if strings.Join(got, ",") != strings.Join(c.errs, ",") {
				t.Fatalf("errors = %v, want %v (%v)", got, c.errs, errs)
			}
		})
	}
}

func TestKeywords(t *testing.T) {
	cases := []struct {
		schema string
		doc    string
		valid  bool
	}{
		{`{"type":"string","pattern":"^[A-Z]{2}$"}`, `"AB"`, true},
		{`{"type":"string","pattern":"^[A-Z]{2}$"}`, `"ABC"`, false},
		{`{"type":"string","minLength":2,"maxLength":3}`, `"é"`, false},
		{`{"type":"string","minLength":2,"maxLength":3}`, `"éé"`, true},
		{`{"type":"string","format":"date-time"}`, `"2026-10-30T10:00:00Z"`, true},
		{`{"type":"string","format":"date-time"}`, `"2026-10-30"`, false},
		{`{"type":"string","format":"date"}`, `"2026-10-30"`, true},
		{`{"type":"string","format":"email"}`, `"a@example.com"`, true},
		{`{"type":"string","format":"email"}`, `"a example.com"`, false},
		{`{"type":"string","format":"uuid"}`, `"123e4567-e89b-12d3-a456-426614174000"`, true},
		{`{"type":"string","format":"uuid"}`, `"123e4567e89b12d3a456426614174000"`, false},
		{`{"const":"x"}`, `"x"`, true},
		{`{"const":"x"}`, `"y"`, false},
		{`{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":1}`, `0.5`, true},
		{`{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":1}`, `1`, false},
		{`{"type":"object","additionalProperties":false,"properties":{"a":{}}}`, `{"a":1,"b":2}`, false},
		{`{"type":"object","additionalProperties":{"type":"integer"}}`, `{"a":1,"b":2}`, true},
		{`{"type":"object","additionalProperties":{"type":"integer"}}`, `{"a":1,"b":"x"}`, false},
		{`{"type":"array","prefixItems":[{"type":"string"},{"type":"number"}],"items":{"type":"boolean"}}`, `["a",1,true,false]`, true},
		{`{"type":"array","prefixItems":[{"type":"string"},{"type":"number"}],"items":{"type":"boolean"}}`, `["a",1,2]`, false},
		{`{"type":"array","minItems":1}`, `[]`, false},
		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"},"v":{"type":"integer"}}}},"$ref":"#/$defs/node"}`, `{"v":1,"next":{"v":2,"next":{"v":3}}}`, true},
		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"},"v":{"type":"integer"}}}},"$ref":"#/$defs/node"}`, `{"v":1,"next":{"v":"x"}}`, false},
		{`{"type":["integer","null"]}`, `null`, true},
		{`{"enum":[1,"a",null]}`, `1.0`, true},
		{`true`, `{"anything":1}`, true},
	}
	for _, c := range cases {
		s := mustCompile(t, c.schema)
		if got := s.Valid(decode(t, c.doc)); got != c.valid {
			t.Errorf("%s against %s = %v, want %v (%v)", c.doc, c.schema, got, c.valid, s.Validate(decode(t, c.doc)))
		}
	}
}

func TestCompileIssues(t *testing.T) {
	cases := []struct{ schema, code string }{
		{`{"type":"strng"}`, "INVALID_SCHEMA"},
		{`{"oneOf":[{}]}`, "UNKNOWN_KEYWORD"},
		{`{"pattern":"(?<=a)"}`, "INVALID_REGEX"},
		{`{"$ref":"#/$defs/missing"}`, "REF_NOT_FOUND"},
		{`{"$ref":"http://example.com/s.json"}`, "INVALID_SCHEMA"},
		{`{"format":"ipv4"}`, "INVALID_SCHEMA"},
		{`{"maxItems":-1}`, "INVALID_SCHEMA"},
		{`[]`, "INVALID_SCHEMA"},
	}
	for _, c := range cases {
		_, issues := Compile(json.RawMessage(c.schema))
		if len(issues) == 0 || issues[0].Code != c.code {
			t.Errorf("%s: issues = %v, want %s", c.schema, issues, c.code)
		}
	}
	deep := strings.Repeat(`{"type":"object","properties":{"a":`, 40) + `{}` + strings.Repeat(`}}`, 40)
	if _, issues := Compile(json.RawMessage(deep)); len(issues) == 0 || issues[0].Code != "TOO_DEEP" {
		t.Errorf("deep schema: %v", issues)
	}
	if _, issues := Compile(json.RawMessage(`{"x-other":1,"title":"t","examples":[1]}`)); len(issues) != 0 {
		t.Errorf("annotations rejected: %v", issues)
	}
}

func TestCompare(t *testing.T) {
	base := `{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":["integer","null"]},"c":{"enum":["x","y"]}}}`
	cases := []struct {
		next     string
		kinds    []string
		breaking bool
	}{
		{base, nil, false},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":["integer","null"]},"c":{"enum":["x","y"]},"d":{"type":"string"}}}`, []string{"/d:property_added"}, false},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"c":{"enum":["x","y"]}}}`, []string{"/b:property_removed"}, true},
		{`{"type":"object","required":["a","b"],"properties":{"a":{"type":"string"},"b":{"type":["integer","null"]},"c":{"enum":["x","y"]}}}`, []string{"/b:became_required"}, true},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"integer"},"c":{"enum":["x","y"]}}}`, []string{"/b:null_removed"}, true},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":["number","null"]},"c":{"enum":["x","y"]}}}`, []string{"/b:type_widened"}, false},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":["integer","null"]},"c":{"enum":["x"]}}}`, []string{"/c:enum_narrowed"}, true},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":["integer","null"]},"c":{"enum":["x","y","z"]}}}`, []string{"/c:enum_widened"}, false},
		{`{"type":"object","required":["a"],"properties":{"a":{"type":"integer"},"b":{"type":["integer","null"]},"c":{"enum":["x","y"]}}}`, []string{"/a:type_narrowed"}, true},
	}
	prev := mustCompile(t, base)
	for i, c := range cases {
		changes := Compare(prev, mustCompile(t, c.next))
		var kinds []string
		breaking := false
		for _, ch := range changes {
			kinds = append(kinds, ch.Path+":"+ch.Kind)
			breaking = breaking || ch.Breaking
		}
		if strings.Join(kinds, ",") != strings.Join(c.kinds, ",") || breaking != c.breaking {
			t.Errorf("case %d: changes = %v breaking=%v, want %v %v", i, kinds, breaking, c.kinds, c.breaking)
		}
	}
}

func TestAccepts(t *testing.T) {
	consumer := mustCompile(t, `{"type":"object","required":["city","price"],"properties":{"city":{"type":"string"},"price":{"type":"number"}}}`)
	good := mustCompile(t, `{"type":"object","required":["city","price","x"],"properties":{"city":{"type":"string"},"price":{"type":"integer"},"x":{}}}`)
	if ok, w, why := Accepts(consumer, good); !ok {
		t.Fatalf("should accept: %s %s", w, why)
	}
	optional := mustCompile(t, `{"type":"object","required":["city"],"properties":{"city":{"type":"string"},"price":{"type":"number"}}}`)
	if ok, w, _ := Accepts(consumer, optional); ok || w != "/price" {
		t.Fatalf("should reject optional price, got %v %s", ok, w)
	}
	wrongType := mustCompile(t, `{"type":"object","required":["city","price"],"properties":{"city":{"type":"string"},"price":{"type":"string"}}}`)
	if ok, _, _ := Accepts(consumer, wrongType); ok {
		t.Fatal("should reject string price")
	}
}

func TestStaticHelpers(t *testing.T) {
	s := mustCompile(t, leadInput)
	lead, ok := s.Property("lead")
	if !ok || lead == nil {
		t.Fatal("lead")
	}
	if _, ok := lead.Property("nope"); ok {
		t.Fatal("closed object should not know nope")
	}
	insp, _ := s.Property("inspections")
	if n, ok := insp.MaxItemsOf(); !ok || n != 3 {
		t.Fatal("maxItems")
	}
	paths := s.PIIPaths()
	if len(paths) != 1 || strings.Join(paths[0], ".") != "lead.phone" {
		t.Fatalf("pii paths = %v", paths)
	}
	if !TAll.Has(TInt) || !(TNum | TInt).Has(TInt) || TInt.Has(TNum) {
		t.Fatal("typeset")
	}
}

func BenchmarkValid(b *testing.B) {
	s, _ := Compile(json.RawMessage(leadInput))
	var doc any
	_ = json.Unmarshal([]byte(`{"lead":{"id":"L1","city":"Pune","source":"web","first_name":null,"phone":"+91"},"car":{"year":2019,"km":10,"price":5.5},"inspections":[{"part":"a","score":1}]}`), &doc)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !s.Valid(doc) {
			b.Fatal("invalid")
		}
	}
}
