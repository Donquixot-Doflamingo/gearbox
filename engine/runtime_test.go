// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var anySchema = schemaRes("any_obj", 1, `{"type":"object"}`)
var okSchema = schemaRes("ok_out", 1, `{"type":"object","properties":{"ok":{"type":"boolean"}}}`)

// condSet compiles a one-rule decision over an untyped object input.
func condSet(t *testing.T, when string, extra ...Resource) *Snapshot {
	t.Helper()
	d := res(KindDecision, "cond", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"any_obj","version":1},"outputSchema":{"name":"ok_out","version":1},"rules":[{"id":"hit","when":`+when+`,"then":{"ok":true}}],"default":{"ok":false}}]}`)
	return mustCompile(t, Set{App: "app", Environment: "test", Generation: 1, Resources: append([]Resource{anySchema, okSchema, d}, extra...)})
}

func TestFieldOperators(t *testing.T) {
	cases := []struct {
		op, value, input string
		want             bool
	}{
		{"EQ", `"a"`, `{"f":"a"}`, true},
		{"EQ", `5`, `{"f":5.0}`, true},
		{"EQ", `5`, `{"f":"5"}`, true},
		{"EQ", `"a"`, `{"f":"b"}`, false},
		{"NEQ", `"a"`, `{"f":"b"}`, true},
		{"NEQ", `"a"`, `{}`, false},
		{"NEQ", `"a"`, `{"f":null}`, false},
		{"EQ_CASE_INSENSITIVE", `"ABC"`, `{"f":"abc"}`, true},
		{"NEQ_CASE_INSENSITIVE", `"ABC"`, `{"f":"abc"}`, false},
		{"GT", `10`, `{"f":11}`, true},
		{"GT", `10`, `{"f":"11"}`, true},
		{"GT", `10`, `{"f":"x"}`, false},
		{"GTE", `10`, `{"f":10}`, true},
		{"LT", `10`, `{"f":9.5}`, true},
		{"LTE", `10`, `{"f":11}`, false},
		{"IN", `["a","b"]`, `{"f":"b"}`, true},
		{"IN", `[1,2]`, `{"f":2}`, true},
		{"NOT_IN", `["a","b"]`, `{"f":"c"}`, true},
		{"NOT_IN", `["a","b"]`, `{}`, false},
		{"IN_CASE_INSENSITIVE", `["Delhi"]`, `{"f":"DELHI"}`, true},
		{"NOT_IN_CASE_INSENSITIVE", `["Delhi"]`, `{"f":"delhi"}`, false},
		{"IN_NUMERIC", `[1,2,3]`, `{"f":"2"}`, true},
		{"NOT_IN_NUMERIC", `[1,2,3]`, `{"f":4}`, true},
		{"CONTAINS", `"ell"`, `{"f":"hello"}`, true},
		{"CONTAINS", `"x"`, `{"f":["x","y"]}`, true},
		{"CONTAINS", `"z"`, `{"f":["x","y"]}`, false},
		{"NOT_CONTAINS", `"z"`, `{"f":["x","y"]}`, true},
		{"CONTAINS_CASE_INSENSITIVE", `"ELL"`, `{"f":"hello"}`, true},
		{"CONTAINS_CASE_INSENSITIVE", `"X"`, `{"f":["x"]}`, true},
		{"NOT_CONTAINS_CASE_INSENSITIVE", `"ELL"`, `{"f":"hello"}`, false},
		{"CONTAINS_NUMERIC", `2`, `{"f":[1,2]}`, true},
		{"NOT_CONTAINS_NUMERIC", `3`, `{"f":[1,2]}`, true},
		{"CONTAINS_ANY", `["a","q"]`, `{"f":["a","b"]}`, true},
		{"CONTAINS_ANY", `["q"]`, `{"f":["a","b"]}`, false},
		{"CONTAINS_NONE", `["q"]`, `{"f":["a","b"]}`, true},
		{"CONTAINS_NONE", `["a"]`, `{"f":["a","b"]}`, false},
		{"STARTS_WITH", `"he"`, `{"f":"hello"}`, true},
		{"ENDS_WITH", `"lo"`, `{"f":"hello"}`, true},
		{"STARTS_WITH_CASE_INSENSITIVE", `"HE"`, `{"f":"hello"}`, true},
		{"ENDS_WITH_CASE_INSENSITIVE", `"LO"`, `{"f":"hello"}`, true},
		{"MATCHES", `"^h.*o$"`, `{"f":"hello"}`, true},
		{"NOT_MATCHES", `"^x"`, `{"f":"hello"}`, true},
		{"MATCHES_CASE_INSENSITIVE", `"^HEL"`, `{"f":"hello"}`, true},
		{"NOT_MATCHES_CASE_INSENSITIVE", `"^HEL"`, `{"f":"hello"}`, false},
		{"NOT_MATCHES", `"^x"`, `{}`, false},
		{"EXISTS", `null`, `{"f":0}`, true},
		{"EXISTS", `null`, `{"f":null}`, false},
		{"NOT_EXISTS", `null`, `{}`, true},
		{"NOT_EXISTS", `null`, `{"f":null}`, true},
		{"NOT_EXISTS", `null`, `{"f":1}`, false},
		{"IS_TRUE", `null`, `{"f":true}`, true},
		{"IS_TRUE", `null`, `{"f":"TRUE"}`, true},
		{"IS_FALSE", `null`, `{"f":"false"}`, true},
		{"IS_FALSE", `null`, `{"f":true}`, false},
		{"GT", `1`, `{}`, false},
	}
	for _, c := range cases {
		t.Run(c.op+" "+c.input, func(t *testing.T) {
			cond := fmt.Sprintf(`{"field":"f","op":%q,"value":%s}`, c.op, c.value)
			if c.value == "null" {
				cond = fmt.Sprintf(`{"field":"f","op":%q}`, c.op)
			}
			s := condSet(t, cond)
			r := decide(t, s, "cond", c.input, "")
			if got := string(r.Output) == `{"ok":true}`; got != c.want {
				t.Fatalf("%s %s on %s = %s", c.op, c.value, c.input, r.Output)
			}
		})
	}
	s := condSet(t, `{"field":"f","op":"BETWEEN","value":10,"valueTo":20}`)
	for in, want := range map[string]bool{`{"f":10}`: true, `{"f":20}`: true, `{"f":"15"}`: true, `{"f":21}`: false, `{}`: false} {
		if got := string(decide(t, s, "cond", in, "").Output) == `{"ok":true}`; got != want {
			t.Errorf("BETWEEN %s = %v", in, got)
		}
	}
	if len(fieldOps) != 35 {
		t.Fatalf("operators = %d, want 35", len(fieldOps))
	}
}

func TestGroupsAndNestedPaths(t *testing.T) {
	s := condSet(t, `{"any":[{"all":[{"field":"a.b","op":"EQ","value":1},{"not":{"field":"c","op":"EXISTS"}}]},{"field":"list[1].x","op":"EQ","value":"y"}]}`)
	cases := map[string]bool{
		`{"a":{"b":1}}`:                  true,
		`{"a":{"b":1},"c":1}`:            false,
		`{"list":[{"x":"n"},{"x":"y"}]}`: true,
		`{"a":"notmap"}`:                 false,
	}
	for in, want := range cases {
		if got := string(decide(t, s, "cond", in, "").Output) == `{"ok":true}`; got != want {
			t.Errorf("%s = %v", in, got)
		}
	}
}

func evalExpr(t *testing.T, src, input string) (json.RawMessage, error) {
	t.Helper()
	d := res(KindDecision, "ex", 1, `{"entry":"t","nodes":[{"id":"t","type":"transform","omitNil":false,"fields":[{"target":"v","expr":`+jsonString(src)+`}]}]}`)
	s, err := Compile(Set{App: "app", Environment: "test", Resources: []Resource{d}})
	if err != nil {
		return nil, err
	}
	r, err := s.Decide(context.Background(), Request{Decision: "ex", Input: json.RawMessage(input), Now: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)})
	return r.Output, err
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestExpressions(t *testing.T) {
	cases := []struct{ src, input, want string }{
		{`input.a.b.c`, `{}`, `{"v":null}`},
		{`input.a.b.c ?? "d"`, `{}`, `{"v":"d"}`},
		{`input.missing > 3`, `{}`, `{"v":false}`},
		{`input.missing <= 3`, `{}`, `{"v":false}`},
		{`input.x > 3`, `{"x":4}`, `{"v":true}`},
		{`input.missing == nil`, `{}`, `{"v":true}`},
		{`keys(input.m)`, `{"m":{"z":1,"a":2,"k":3}}`, `{"v":["a","k","z"]}`},
		{`values(input.m)`, `{"m":{"z":1,"a":2}}`, `{"v":[2,1]}`},
		{`toPairs(input.m)`, `{"m":{"b":1,"a":2}}`, `{"v":[["a",2],["b",1]]}`},
		{`coalesce(input.a, input.b, "c")`, `{"b":"x"}`, `{"v":"x"}`},
		{`daysBetween("2026-10-01", ctx.now)`, `{}`, `{"v":29}`},
		{`ctx.now.Year() - input.year`, `{"year":2019}`, `{"v":7}`},
		{`bucket("s")`, `{}`, `{"v":null}`},
		{`bucket("s") < 5000`, `{}`, `{"v":false}`},
		{`mean(map(input.xs, .s))`, `{"xs":[{"s":2},{"s":4}]}`, `{"v":3}`},
		{`map(filter(input.xs, .s < 3), .p)`, `{"xs":[{"s":2,"p":"a"},{"s":4,"p":"b"}]}`, `{"v":["a"]}`},
		{`let a = input.x * 2; a + 1`, `{"x":2}`, `{"v":5}`},
		{`toJSON({"b": 1, "a": 2})`, `{}`, `{"v":"{\n  \"a\": 2,\n  \"b\": 1\n}"}`},
		{`date("2026-01-02").Month()`, `{}`, `{"v":1}`},
		{`upper(input.s) + "!"`, `{"s":"hi"}`, `{"v":"HI!"}`},
	}
	for _, c := range cases {
		out, err := evalExpr(t, c.src, c.input)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if string(out) != c.want {
			t.Errorf("%s on %s = %s, want %s", c.src, c.input, out, c.want)
		}
	}
	// Arithmetic with nil is an error (no fallback → NODE_ERROR).
	if _, err := evalExpr(t, `input.missing + 1`, `{}`); err == nil {
		t.Error("nil arithmetic should fail")
	} else {
		var ee *Error
		if !errors.As(err, &ee) || ee.Reason != "NODE_ERROR" || ee.Node != "t" {
			t.Errorf("err = %v", err)
		}
	}
	if _, err := evalExpr(t, `now()`, `{}`); err == nil || !strings.Contains(err.Error(), "UNKNOWN_FUNCTION") {
		t.Errorf("now() err = %v", err)
	}
}

func TestDataSlots(t *testing.T) {
	d := res(KindDecision, "ds", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"any_obj","version":1},"outputSchema":{"name":"any_obj","version":1},
		"rules":[{"id":"a","when":{"expr":"true"},"then":{"lit":"=input.x * 2","esc":"==not code","nested":{"l":["=input.y", 1]}}}]}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{anySchema, d}})
	r := decide(t, s, "ds", `{"x":21,"y":"z"}`, "")
	if string(r.Output) != `{"esc":"=not code","lit":42,"nested":{"l":["z",1]}}` {
		t.Fatalf("output = %s", r.Output)
	}
}

func TestTransform(t *testing.T) {
	d := res(KindDecision, "tr", 1, `{"entry":"n","nodes":[{"id":"n","type":"transform","fields":[
		{"target":"leadId","expr":"input.lead.id"},
		{"target":"customer.name","expr":"input.lead.first + \" \" + input.lead.last"},
		{"target":"customer.greeting","expr":"\"Hi \" + out.customer.name"},
		{"target":"items[0].first","expr":"input.parts[0]"},
		{"target":"tags[]","expr":"\"a\""},
		{"target":"tags[]","expr":"\"b\""},
		{"target":"rows[].x","expr":"1"},
		{"target":"rows[].x","expr":"2"},
		{"target":"gone","expr":"input.nothing"}],
		"defaults":[{"target":"customer.country","value":"IN"},{"target":"leadId","value":"unused"}]}]}`)
	p := res(KindDecision, "patch", 1, `{"entry":"n","nodes":[{"id":"n","type":"transform","mode":"patch","fields":[{"target":"lead.flag","expr":"true"}],"drop":["lead.secret","parts[0]"]}]}`)
	e := res(KindDecision, "exprform", 1, `{"entry":"n","nodes":[{"id":"n","type":"transform","expression":"{\"id\": input.lead.id, \"n\": len(input.parts)}"}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{d, p, e}})
	in := `{"lead":{"id":"L1","first":"Rahul","last":"Sharma","secret":"s"},"parts":["engine","body"]}`
	r := decide(t, s, "tr", in, "")
	want := `{"customer":{"country":"IN","greeting":"Hi Rahul Sharma","name":"Rahul Sharma"},"items":[{"first":"engine"}],"leadId":"L1","rows":[{"x":1},{"x":2}],"tags":["a","b"]}`
	if string(r.Output) != want {
		t.Fatalf("build = %s", r.Output)
	}
	r = decide(t, s, "patch", in, "")
	if string(r.Output) != `{"lead":{"first":"Rahul","flag":true,"id":"L1","last":"Sharma"},"parts":["body"]}` {
		t.Fatalf("patch = %s", r.Output)
	}
	// The patch must not modify the caller's input (runs again identically).
	if r2 := decide(t, s, "patch", in, ""); string(r2.Output) != string(r.Output) {
		t.Fatal("patch not repeatable")
	}
	if r = decide(t, s, "exprform", in, ""); string(r.Output) != `{"id":"L1","n":2}` {
		t.Fatalf("expression = %s", r.Output)
	}
}

func TestDecisionTable(t *testing.T) {
	tblOut := schemaRes("band_out", 1, `{"type":"object","properties":{"band":{"type":"string"},"max_discount":{"type":"number"}}}`)
	mk := func(policy string) Resource {
		return res(KindDecision, "tbl_"+policy, 1, `{"entry":"t","nodes":[{"id":"t","type":"table","hitPolicy":"`+policy+`","inputSchema":{"name":"any_obj","version":1},"outputSchema":{"name":"band_out","version":1},
		"inputs":[{"name":"price","expr":"input.price"},{"name":"age","expr":"input.age"},{"name":"city","expr":"input.city"}],
		"outputs":["band","max_discount"],
		"rows":[
			{"id":"row_1","priority":1,"when":{"price":"< 300000","age":"<= 5","city":"-"},"then":{"band":"C","max_discount":5000}},
			{"id":"row_2","priority":5,"when":{"price":"[300000..800000]","age":"<= 8","city":"[\"Gurgaon\",\"Delhi\"]"},"then":{"band":"A","max_discount":15000}},
			{"id":"row_3","priority":3,"when":{"price":"[300000..800000]","age":"-","city":"value != \"Pune\""},"then":{"band":"B","max_discount":10000}},
			{"id":"row_4","when":{"city":"\"Pune\""},"then":{"band":"P","max_discount":"=input.price / 100"}}
		],"default":{"band":"D","max_discount":0}}]}`)
	}
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{anySchema, tblOut, mk("first"), mk("collect"), mk("priority"), mk("unique")}})
	in := `{"price":500000,"age":4,"city":"Delhi"}`
	r := decide(t, s, "tbl_first", in, "")
	if string(r.Output) != `{"band":"A","max_discount":15000}` || r.Path[0].Outcome != "row_2" {
		t.Fatalf("first = %s %v", r.Output, r.Path)
	}
	r = decide(t, s, "tbl_collect", in, "")
	if string(r.Output) != `{"matches":[{"band":"A","max_discount":15000},{"band":"B","max_discount":10000}]}` || r.Path[0].Outcome != "row_2,row_3" {
		t.Fatalf("collect = %s %v", r.Output, r.Path)
	}
	r = decide(t, s, "tbl_priority", in, "")
	if string(r.Output) != `{"band":"A","max_discount":15000}` {
		t.Fatalf("priority = %s", r.Output)
	}
	if r = decide(t, s, "tbl_first", `{"price":900000,"age":1,"city":"Pune"}`, ""); string(r.Output) != `{"band":"P","max_discount":9000}` {
		t.Fatalf("expr then = %s", r.Output)
	}
	if r = decide(t, s, "tbl_first", `{"price":900000,"age":1,"city":"Agra"}`, ""); string(r.Output) != `{"band":"D","max_discount":0}` || r.Path[0].Outcome != "default" {
		t.Fatalf("default = %s", r.Output)
	}
	if _, err := s.Decide(context.Background(), Request{Decision: "tbl_unique", Input: json.RawMessage(in)}); err == nil {
		t.Fatal("unique with two matches should fail")
	}
}

func TestRulesModesAndRollout(t *testing.T) {
	d := res(KindDecision, "rm", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","mode":"all_matches","inputSchema":{"name":"any_obj","version":1},"outputSchema":{"name":"any_obj","version":1},"rules":[
		{"id":"low","priority":1,"when":{"field":"x","op":"GT","value":1},"then":{"id":"low"}},
		{"id":"off","enabled":false,"when":{"field":"x","op":"GT","value":1},"then":{"id":"off"}},
		{"id":"high","priority":9,"when":{"field":"x","op":"GT","value":1},"then":{"id":"high"}},
		{"id":"half","rollout":{"percent":50},"when":{"field":"x","op":"GT","value":1},"then":{"id":"half"}}]}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{anySchema, d}})
	r := decide(t, s, "rm", `{"x":5}`, "")
	if string(r.Output) != `{"matches":[{"id":"high"},{"id":"low"}]}` {
		t.Fatalf("no unit = %s", r.Output)
	}
	in, out := 0, 0
	for i := 0; i < 2000; i++ {
		r := decide(t, s, "rm", `{"x":5}`, fmt.Sprintf("u%d", i))
		if strings.Contains(string(r.Output), "half") {
			in++
		} else {
			out++
		}
	}
	if in < 850 || in > 1150 {
		t.Fatalf("50%% rollout matched %d of 2000", in)
	}
	if r := decide(t, s, "rm", `{"x":0}`, ""); string(r.Output) != `{"matches":[]}` || r.Path[0].Outcome != "default" {
		t.Fatalf("none = %s", r.Output)
	}
}

func TestSwitchLookupGateExperimentNodes(t *testing.T) {
	zone := schemaRes("zone", 1, `{"type":"string"}`)
	lk := Resource{Kind: KindLookup, Name: "city_zone", Version: 2, Spec: json.RawMessage(`{"keyType":"string","valueSchema":{"name":"zone","version":1}}`), LookupRows: map[string]json.RawMessage{"Delhi": json.RawMessage(`"north"`), "Pune": json.RawMessage(`"west"`)}}
	g := res(KindGate, "new_flow", 5, `{"rules":[{"id":"delhi","when":{"field":"city","op":"EQ","value":"Delhi"},"value":true}],"default":false}`)
	ex := res(KindExperiment, "copy_test", 3, `{"status":"running","variants":[{"name":"control","weight":50,"parameters":{"text":"A"}},{"name":"treatment","weight":50,"parameters":{"text":"B"}}]}`)
	d := res(KindDecision, "flow", 1, `{"entry":"sw","nodes":[
		{"id":"sw","type":"switch","cases":[{"id":"has_city","when":"input.city != nil","next":"z"}],"default":"none"},
		{"id":"z","type":"lookup","lookup":"city_zone","key":"input.city","default":"unknown","next":"g"},
		{"id":"g","type":"gate","gate":"new_flow","from":"request","next":"e"},
		{"id":"e","type":"experiment","experiment":"copy_test","next":"o"},
		{"id":"o","type":"output","fields":[{"target":"zone","expr":"nodes.z"},{"target":"gate","expr":"nodes.g.value"},{"target":"variant","expr":"nodes.e.variant"},{"target":"text","expr":"nodes.e.parameters.text"},{"target":"lk","expr":"lookup(\"city_zone\", request.city, \"x\")"},{"target":"fn_gate","expr":"gate(\"new_flow\")"},{"target":"case","expr":"nodes.sw.case"}]},
		{"id":"none","type":"output","expression":"{\"zone\": \"none\"}"}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{zone, lk, g, ex, d}})
	r := decide(t, s, "flow", `{"city":"Delhi"}`, "unit-7")
	var out map[string]any
	_ = json.Unmarshal(r.Output, &out)
	if out["zone"] != "north" || out["gate"] != true || out["lk"] != "north" || out["case"] != "has_city" {
		t.Fatalf("output = %s path=%s", r.Output, pathString(r.Path))
	}
	if (out["variant"] == "control") != (out["text"] == "A") {
		t.Fatalf("variant/params mismatch: %s", r.Output)
	}
	if len(r.Exposures) != 1 || r.Exposures[0].Experiment != "copy_test" || r.Exposures[0].ExperimentVersion != 3 || r.Exposures[0].Reason != "assigned" {
		t.Fatalf("exposures = %+v", r.Exposures)
	}
	if got := pathString(r.Path); !strings.HasPrefix(got, "sw:has_city,z:hit,g:delhi,e:") {
		t.Fatalf("path = %s", got)
	}
	r = decide(t, s, "flow", `{}`, "")
	if string(r.Output) != `{"zone":"none"}` || len(r.Exposures) != 0 {
		t.Fatalf("default branch = %s", r.Output)
	}
	r = decide(t, s, "flow", `{"city":"Agra"}`, "")
	if !strings.Contains(string(r.Output), `"zone":"unknown"`) || !strings.Contains(string(r.Output), `"variant":"control"`) {
		t.Fatalf("miss/no unit = %s", r.Output)
	}
}

func TestGatesAPI(t *testing.T) {
	attrs := schemaRes("g_attrs", 1, `{"type":"object","properties":{"tier":{"type":"string"}}}`)
	g := res(KindGate, "beta", 2, `{"attributesSchema":{"name":"g_attrs","version":1},"rules":[{"id":"vip","when":{"field":"tier","op":"EQ","value":"vip"},"value":true},{"id":"pct","when":{"expr":"true"},"value":true,"rollout":{"percent":30}}],"default":false}`)
	off := res(KindGate, "off", 1, `{"enabled":false,"rules":[],"default":true}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{attrs, g, off}, KilledGates: []string{"killed_one"}})
	if r, err := s.CheckGate("beta", "u1", json.RawMessage(`{"tier":"vip"}`)); err != nil || !r.Value || r.RuleID != "vip" || r.Version != 2 {
		t.Fatalf("vip = %+v %v", r, err)
	}
	if r, _ := s.CheckGate("beta", "", nil); r.Value || r.RuleID != "no_unit" {
		t.Fatalf("no unit = %+v", r)
	}
	if r, _ := s.CheckGate("off", "u", nil); r.Value || r.RuleID != "disabled" {
		t.Fatalf("disabled = %+v", r)
	}
	on := 0
	for i := 0; i < 2000; i++ {
		r, _ := s.CheckGate("beta", fmt.Sprintf("u%d", i), nil)
		if r.Value {
			on++
		}
	}
	if on < 480 || on > 720 {
		t.Fatalf("30%% rollout on for %d of 2000", on)
	}
	if _, err := s.CheckGate("nope", "u", nil); err == nil {
		t.Fatal("unknown gate should fail")
	}
	k := mustCompile(t, Set{App: "a1", Resources: []Resource{attrs, g}, KilledGates: []string{"beta"}})
	if r, _ := k.CheckGate("beta", "u1", json.RawMessage(`{"tier":"vip"}`)); r.Value || r.RuleID != "killed" {
		t.Fatalf("killed = %+v", r)
	}
}

func TestExperimentsAndLayers(t *testing.T) {
	ly := res(KindLayer, "checkout", 1, `{"allocations":[{"experiment":"exp_a","percent":50},{"experiment":"exp_b","percent":50}]}`)
	a := res(KindExperiment, "exp_a", 1, `{"status":"running","layer":"checkout","variants":[{"name":"control","weight":1},{"name":"t","weight":1}]}`)
	b := res(KindExperiment, "exp_b", 1, `{"status":"running","layer":"checkout","holdoutPercent":20,"targeting":{"field":"country","op":"EQ","value":"IN"},"variants":[{"name":"control","weight":1},{"name":"t","weight":3}]}`)
	st := res(KindExperiment, "exp_stopped", 1, `{"status":"stopped","stoppedVariant":"t","variants":[{"name":"control","weight":1},{"name":"t","weight":1,"parameters":{"p":1}}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{ly, a, b, st}})
	counts := map[string]int{}
	for i := 0; i < 4000; i++ {
		unit := fmt.Sprintf("user-%d", i)
		ra, _ := s.GetVariant("exp_a", unit, nil)
		rb, _ := s.GetVariant("exp_b", unit, json.RawMessage(`{"country":"IN"}`))
		if ra.InExperiment && rb.InExperiment {
			t.Fatalf("unit %s in both layer experiments", unit)
		}
		counts["a:"+ra.Reason]++
		counts["b:"+rb.Reason]++
		if rb.InExperiment {
			counts["b:"+rb.Variant]++
		}
		// Stable.
		ra2, _ := s.GetVariant("exp_a", unit, nil)
		if ra2.Variant != ra.Variant || ra2.Reason != ra.Reason {
			t.Fatal("assignment not stable")
		}
	}
	if counts["a:assigned"] < 1800 || counts["a:assigned"] > 2200 || counts["a:layer_conflict"] < 1800 {
		t.Fatalf("layer split = %v", counts)
	}
	if counts["b:holdout"] < 600 || counts["b:holdout"] > 1000 {
		t.Fatalf("holdout = %v", counts)
	}
	if counts["b:t"] < 3*counts["b:control"]-400 {
		t.Fatalf("weights = %v", counts)
	}
	r, _ := s.GetVariant("exp_b", "user-1", json.RawMessage(`{"country":"US"}`))
	if r.InExperiment || (r.Reason != "not_targeted" && r.Reason != "holdout" && r.Reason != "layer_conflict") {
		t.Fatalf("targeting = %+v", r)
	}
	if r, _ := s.GetVariant("exp_a", "", nil); r.Reason != "no_unit" || r.Variant != "control" {
		t.Fatalf("no unit = %+v", r)
	}
	if r, _ := s.GetVariant("exp_stopped", "u", nil); r.Reason != "stopped" || r.Variant != "t" || string(r.Parameters) != `{"p":1}` {
		t.Fatalf("stopped = %+v", r)
	}
}

func TestSegmentsAPI(t *testing.T) {
	attrs := schemaRes("seg_attrs", 1, `{"type":"object","required":["city","price"],"properties":{"city":{"type":"string"},"price":{"type":"number"}}}`)
	st := Resource{Kind: KindSegment, Name: "vip", Version: 1, Spec: json.RawMessage(`{"type":"static"}`), SegmentMembers: []string{"d1", "d2"}}
	pii := Resource{Kind: KindSegment, Name: "phones", Version: 1, Spec: json.RawMessage(`{"type":"static","static":{"pii":true}}`), SegmentMembers: []string{SegmentMemberHMAC(DefaultPIIKey("a1"), "+911")}}
	dyn := res(KindSegment, "metro", 1, `{"type":"dynamic","dynamic":{"attributesSchema":{"name":"seg_attrs","version":1},"when":{"all":[{"field":"city","op":"IN","value":["Delhi"]},{"field":"price","op":"GT","value":800000}]}}}`)
	ro := res(KindSegment, "ten_pct", 1, `{"type":"rollout","rollout":{"percent":10}}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{attrs, st, pii, dyn, ro}})
	check := func(seg, key, unit, attrs string, want bool, reason string) {
		t.Helper()
		m, why, err := s.CheckSegment(seg, key, unit, json.RawMessage(attrs))
		if err != nil || m != want || (reason != "" && why != reason) {
			t.Fatalf("%s: %v %q %v", seg, m, why, err)
		}
	}
	check("vip", "d1", "", "", true, "listed")
	check("vip", "d3", "", "", false, "not_listed")
	check("phones", "+911", "", "", true, "listed")
	check("metro", "", "", `{"city":"Delhi","price":950000}`, true, "conditions_matched")
	check("metro", "", "", `{"city":"Delhi","price":10}`, false, "conditions_failed")
	if _, _, err := s.CheckSegment("metro", "", "", json.RawMessage(`{"city":"Delhi"}`)); err == nil {
		t.Fatal("attributes missing price should fail")
	}
	members := 0
	for i := 0; i < 3000; i++ {
		m, why, _ := s.CheckSegment("ten_pct", "", fmt.Sprintf("u%d", i), nil)
		if m {
			members++
			if !strings.HasPrefix(why, "bucket ") {
				t.Fatalf("reason = %q", why)
			}
		}
	}
	if members < 220 || members > 380 {
		t.Fatalf("10%% rollout segment has %d of 3000", members)
	}
}

func TestVersionRollout(t *testing.T) {
	v1 := res(KindDecision, "price", 1, `{"entry":"o","nodes":[{"id":"o","type":"output","expression":"\"v1\""}]}`)
	v2 := res(KindDecision, "price", 2, `{"entry":"o","nodes":[{"id":"o","type":"output","expression":"\"v2\""}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{v1, v2}, Rollouts: []Rollout{{Decision: "price", FromVersion: 1, ToVersion: 2, Percent: 30, Salt: "price-2"}}})
	if r := decide(t, s, "price", `{}`, ""); string(r.Output) != `"v1"` || r.DecisionVersion != "price@1" {
		t.Fatalf("no unit = %s %s", r.Output, r.DecisionVersion)
	}
	n2 := 0
	for i := 0; i < 2000; i++ {
		r := decide(t, s, "price", `{}`, fmt.Sprintf("u%d", i))
		if r.DecisionVersion == "price@2" {
			n2++
		}
	}
	if n2 < 480 || n2 > 720 {
		t.Fatalf("30%% on v2 got %d", n2)
	}
	r, err := s.Decide(context.Background(), Request{Decision: "price", PinVersion: 1, UnitID: "u1"})
	if err != nil || string(r.Output) != `"v1"` {
		t.Fatalf("pin = %s %v", r.Output, err)
	}
	if _, err := s.Decide(context.Background(), Request{Decision: "price", PinVersion: 9}); err == nil {
		t.Fatal("pin to missing version should fail")
	}
	if infos := s.Decisions(); len(infos) != 1 || infos[0].Version != 2 {
		t.Fatalf("decisions = %+v", infos)
	}
	var ee *Error
	if _, err := s.Decide(context.Background(), Request{Decision: "nope"}); !errors.As(err, &ee) || ee.Reason != "DECISION_NOT_FOUND" {
		t.Fatalf("not found = %v", err)
	}
}

func TestErrorsFallbackAndOnError(t *testing.T) {
	strict := schemaRes("strict", 1, `{"type":"object","required":["n"],"properties":{"n":{"type":"number"},"xs":{"type":"array","maxItems":2}}}`)
	d := res(KindDecision, "fb", 1, `{"entry":"r","fallback":{"result":"fallback"},"nodes":[
		{"id":"r","type":"rules","inputSchema":{"name":"strict","version":1},"outputSchema":{"name":"any_obj","version":1},"rules":[{"id":"a","when":{"expr":"input.n > 0"},"then":{"v":"=int(\"x\") + input.n"}}],"default":{"v":0},"next":"t"},
		{"id":"t","type":"transform","fields":[{"target":"x","expr":"input.v + input.missing"}],"onError":"rescue"},
		{"id":"rescue","type":"output","expression":"{\"result\": \"rescued\"}"}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{anySchema, strict, d}})
	var ee *Error
	_, err := s.Decide(context.Background(), Request{Decision: "fb", Input: json.RawMessage(`{"n":"x"}`)})
	if !errors.As(err, &ee) || ee.Reason != "INPUT_SCHEMA" || len(ee.Fields) != 1 || ee.Fields[0].Path != "/n" {
		t.Fatalf("schema err = %#v", err)
	}
	_, err = s.Decide(context.Background(), Request{Decision: "fb", Input: json.RawMessage(`{"n":1,"xs":[1,2,3]}`)})
	if !errors.As(err, &ee) || ee.Reason != "INPUT_TOO_LARGE" {
		t.Fatalf("too large = %v", err)
	}
	r := decide(t, s, "fb", `{"n":0}`, "")
	if string(r.Output) != `{"result":"rescued"}` || !r.FallbackUsed || pathString(r.Path) != "r:default,t:fallback,rescue:ok" {
		t.Fatalf("onError = %s %v %s", r.Output, r.FallbackUsed, pathString(r.Path))
	}
	r = decide(t, s, "fb", `{"n":2}`, "")
	if string(r.Output) != `{"result":"fallback"}` || !r.FallbackUsed || r.Path[0].Outcome != "error" || r.Path[0].Error == "" {
		t.Fatalf("decision fallback = %s %+v", r.Output, r.Path)
	}
	if _, err := s.Decide(context.Background(), Request{Decision: "fb", Input: json.RawMessage(`{bad`)}); !errors.As(err, &ee) || ee.Reason != "INPUT_SCHEMA" {
		t.Fatalf("bad json = %v", err)
	}
}

func TestSubdecisions(t *testing.T) {
	child := res(KindDecision, "child", 4, `{"entry":"o","nodes":[{"id":"o","type":"output","expression":"{\"double\": input.n * 2}"}]}`)
	parent := res(KindDecision, "parent", 1, `{"entry":"s","nodes":[{"id":"s","type":"subdecision","decision":"child","input":"{\"n\": input.v}","next":"o"},{"id":"o","type":"output","fields":[{"target":"r","expr":"nodes.s.double + 1"}]}]}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{child, parent}})
	r := decide(t, s, "parent", `{"v":5}`, "")
	if string(r.Output) != `{"r":11}` || r.Path[0].Outcome != "child@4" {
		t.Fatalf("sub = %s %v", r.Output, r.Path)
	}
	a := res(KindDecision, "loop_a", 1, `{"entry":"s","nodes":[{"id":"s","type":"subdecision","decision":"loop_b"}]}`)
	b := res(KindDecision, "loop_b", 1, `{"entry":"s","nodes":[{"id":"s","type":"subdecision","decision":"loop_a"}]}`)
	v := ValidateResource(a, resolverOf(a, b))
	if v.Valid || !strings.Contains(problemCodes(v), "CYCLE") {
		t.Fatalf("cycle = %+v", v.Problems)
	}
	if _, err := Compile(Set{App: "a1", Resources: []Resource{a, b}}); err == nil {
		t.Fatal("compile should fail on decision cycle")
	}
}

type fakeAI struct {
	mu     sync.Mutex
	answer string
	delay  time.Duration
	err    error
	calls  []AICall
	shadow chan error
}

func (f *fakeAI) Call(ctx context.Context, c AICall) (AIAnswer, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return AIAnswer{}, ctx.Err()
	}
	if f.err != nil {
		return AIAnswer{}, f.err
	}
	return AIAnswer{Output: json.RawMessage(f.answer), Model: "m-2026"}, nil
}

func (f *fakeAI) RecordShadow(c AICall, a AIAnswer, d time.Duration, err error) {
	if f.shadow != nil {
		f.shadow <- err
	}
}

func aiDecision(mode string) Resource {
	return res(KindDecision, "ai_"+mode, 1, `{"entry":"a","inputSchema":{"name":"lead_s","version":1},"nodes":[
		{"id":"a","type":"ai","model":"reasoning-default","mode":"`+mode+`","timeoutMs":60,"vars":{"city":"input.city"},
		 "prompt":{"system":"Rate leads.","user":"City: {{ city }}."},
		 "responseSchema":{"type":"object","required":["route"],"properties":{"route":{"enum":["fast_track","standard"]}}},"onError":"rules","next":"end"},
		{"id":"rules","type":"output","expression":"{\"route\": \"standard\"}"}]}`)
}

func TestAINodes(t *testing.T) {
	lead := schemaRes("lead_s", 1, `{"type":"object","properties":{"city":{"type":"string"},"phone":{"type":"string","x-gearbox-pii":true}}}`)
	s := mustCompile(t, Set{App: "a1", Resources: []Resource{lead, aiDecision("inline"), aiDecision("shadow")}})
	ai := &fakeAI{answer: `{"route":"fast_track"}`, shadow: make(chan error, 4)}
	r, err := s.Decide(context.Background(), Request{Decision: "ai_inline", Input: json.RawMessage(`{"city":"Pune"}`), AI: ai, RequestID: "rq"})
	if err != nil || string(r.Output) != `{"route":"fast_track"}` || r.FallbackUsed {
		t.Fatalf("inline = %s %v %v", r.Output, r.FallbackUsed, err)
	}
	c := ai.calls[0]
	if c.User != "City: Pune." || c.System != "Rate leads." || c.Model != "reasoning-default" || c.CallID == "" || c.Timeout != 60*time.Millisecond || c.Decision != "ai_inline" || c.Node != "a" {
		t.Fatalf("call = %+v", c)
	}
	// Invalid answer → onError, fallbackUsed.
	ai.answer = `{"route":"maybe"}`
	r, _ = s.Decide(context.Background(), Request{Decision: "ai_inline", Input: json.RawMessage(`{"city":"Pune"}`), AI: ai})
	if string(r.Output) != `{"route":"standard"}` || !r.FallbackUsed || r.Path[0].Outcome != "fallback" {
		t.Fatalf("invalid answer = %s %+v", r.Output, r.Path)
	}
	// Timeout → onError.
	ai.answer, ai.delay = `{"route":"fast_track"}`, 200*time.Millisecond
	start := time.Now()
	r, _ = s.Decide(context.Background(), Request{Decision: "ai_inline", Input: json.RawMessage(`{"city":"Pune"}`), AI: ai})
	if string(r.Output) != `{"route":"standard"}` || !r.FallbackUsed || time.Since(start) > 150*time.Millisecond {
		t.Fatalf("timeout = %s after %v", r.Output, time.Since(start))
	}
	// No AI caller → onError.
	r, _ = s.Decide(context.Background(), Request{Decision: "ai_inline", Input: json.RawMessage(`{"city":"Pune"}`)})
	if string(r.Output) != `{"route":"standard"}` {
		t.Fatalf("no caller = %s", r.Output)
	}
	// Shadow: rules decide immediately; AI result recorded in background.
	ai.delay = 5 * time.Millisecond
	start = time.Now()
	r, _ = s.Decide(context.Background(), Request{Decision: "ai_shadow", Input: json.RawMessage(`{"city":"Pune"}`), AI: ai})
	if string(r.Output) != `{"route":"standard"}` || r.FallbackUsed || r.Path[0].Outcome != "shadow" || time.Since(start) > 4*time.Millisecond+2*time.Millisecond {
		t.Logf("shadow took %v", time.Since(start))
	}
	if string(r.Output) != `{"route":"standard"}` || r.FallbackUsed || r.Path[0].Outcome != "shadow" {
		t.Fatalf("shadow = %s %v %+v", r.Output, r.FallbackUsed, r.Path)
	}
	select {
	case err := <-ai.shadow:
		if err != nil {
			t.Fatalf("shadow call err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shadow call not recorded")
	}
}

func TestDeterminism(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	inputs := []string{
		`{"lead":{"id":"L-1","city":"Gurgaon","source":"app"},"car":{"year":2019,"km":48200,"price":525000},"inspections":[{"part":"engine","score":8}]}`,
		`{"lead":{"id":"L-2","city":"Srinagar","source":"web"},"car":{"year":2020,"km":1,"price":400000}}`,
		`{"lead":{"id":"L-3","city":"Pune"},"car":{"year":2005,"km":1,"price":400000}}`,
		`{"lead":{"id":"L-4","city":"Mumbai","source":"app"},"car":{"year":2022,"km":1,"price":950000},"inspections":[{"part":"a","score":9}]}`,
	}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	type key struct{ in, unit int }
	first := map[key]string{}
	order := make([]key, 0, 1000)
	for i := 0; i < 1000; i++ {
		order = append(order, key{i % len(inputs), i % 7})
	}
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fails atomic.Int32
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(order); i += 4 {
				k := order[i]
				r, err := snap.Decide(context.Background(), Request{Decision: "lead_routing", Input: json.RawMessage(inputs[k.in]), UnitID: fmt.Sprintf("L-%d", 1029384+k.unit), Now: now})
				if err != nil {
					fails.Add(1)
					continue
				}
				got := string(r.Output) + "|" + r.DependencyHash + "|" + pathString(r.Path)
				mu.Lock()
				if prev, ok := first[k]; ok && prev != got {
					fails.Add(1)
				}
				first[k] = got
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if fails.Load() != 0 {
		t.Fatalf("%d nondeterministic or failed results", fails.Load())
	}
}

func TestDependencyHashChangesWithDependencies(t *testing.T) {
	set := leadSet(t)
	a := mustCompile(t, set)
	ra := decide(t, a, "lead_routing", `{"lead":{"id":"1","city":"Pune"},"car":{"year":2020,"km":1,"price":1}}`, "")
	for i, r := range set.Resources {
		if r.Kind == KindSegment && r.Name == "blocked_cities" {
			set.Resources[i].Version = 4
		}
	}
	b := mustCompile(t, set)
	rb := decide(t, b, "lead_routing", `{"lead":{"id":"1","city":"Pune"},"car":{"year":2020,"km":1,"price":1}}`, "")
	if ra.DependencyHash == rb.DependencyHash {
		t.Fatal("dependency hash should change when a segment version changes")
	}
	c := mustCompile(t, leadSet(t))
	if rc := decide(t, c, "lead_routing", `{"lead":{"id":"1","city":"Pune"},"car":{"year":2020,"km":1,"price":1}}`, ""); rc.DependencyHash != ra.DependencyHash {
		t.Fatal("dependency hash must be stable across compiles")
	}
}

func TestTraceAndDebug(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	r, err := snap.Decide(context.Background(), Request{Decision: "lead_routing", Debug: true, Input: json.RawMessage(`{"lead":{"id":"1","city":"Pune","source":"web"},"car":{"year":2020,"km":1,"price":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Path) != 3 || r.Path[0].Input == nil || r.Path[0].Output == nil {
		t.Fatalf("debug path = %+v", r.Path)
	}
	r, _ = snap.Decide(context.Background(), Request{Decision: "lead_routing", Input: json.RawMessage(`{"lead":{"id":"1","city":"Pune","source":"web"},"car":{"year":2020,"km":1,"price":1}}`)})
	if r.Path[0].Input != nil {
		t.Fatal("inputs must not appear without Debug")
	}
}

func TestContextCancelled(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := snap.Decide(ctx, Request{Decision: "lead_routing", Input: json.RawMessage(`{"lead":{"id":"1","city":"Pune"},"car":{"year":2020,"km":1,"price":1}}`)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestBenchmarkAPI(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	p99, err := snap.Benchmark(context.Background(), 20)
	if err != nil || p99["lead_routing"] <= 0 {
		t.Fatalf("benchmark = %v %v", p99, err)
	}
}
