// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var benchInput = schemaRes("bench_in", 1, `{"type":"object","required":["price","age","km","city","source","score","tags"],"properties":{
	"price":{"type":"number"},"age":{"type":"integer"},"km":{"type":"integer"},"city":{"type":"string"},
	"source":{"type":"string"},"score":{"type":"number"},"tags":{"type":"array","maxItems":10,"items":{"type":"string"}}}}`)
var benchOut = schemaRes("bench_out", 1, `{"type":"object","properties":{"eligible":{"type":"boolean"},"lane":{"type":"string"},"rule":{"type":"string"}}}`)

const benchDoc = `{"price":525000,"age":7,"km":48200,"city":"Gurgaon","source":"app","score":8,"tags":["certified","single_owner"]}`

func benchSnapshot(b *testing.B, d Resource) (*Snapshot, any) {
	b.Helper()
	s := mustCompile(b, Set{App: "bench", Environment: "prod", Resources: []Resource{benchInput, benchOut, d}})
	var in any
	if err := json.Unmarshal([]byte(benchDoc), &in); err != nil {
		b.Fatal(err)
	}
	return s, in
}

const sevenConditions = `{"all":[
	{"field":"price","op":"BETWEEN","value":300000,"valueTo":2500000},
	{"field":"age","op":"LTE","value":9},
	{"field":"km","op":"LT","value":60000},
	{"field":"city","op":"IN","value":["Gurgaon","Delhi","Noida"]},
	{"field":"source","op":"IN","value":["app","web"]},
	{"field":"score","op":"GTE","value":7},
	{"field":"tags","op":"CONTAINS","value":"certified"}]}`

func runBench(b *testing.B, s *Snapshot, name string, in any) {
	ctx := context.Background()
	req := Request{Decision: name, UnitID: "L-1029384", Now: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)}
	if _, err := s.DecideParsed(ctx, req, in); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.DecideParsed(ctx, req, in); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRule7Conditions: one rule with seven structured conditions
// (target ≤ 2 µs, excluding JSON decode).
func BenchmarkRule7Conditions(b *testing.B) {
	d := res(KindDecision, "seven", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"bench_in","version":1},"outputSchema":{"name":"bench_out","version":1},
		"rules":[{"id":"fast","when":`+sevenConditions+`,"then":{"eligible":true,"lane":"fast"}}],"default":{"eligible":false}}]}`)
	s, in := benchSnapshot(b, d)
	runBench(b, s, "seven", in)
}

// BenchmarkTransform12Fields: a 12-field transform (target ≤ 25 µs).
func BenchmarkTransform12Fields(b *testing.B) {
	fields := []string{
		`{"target":"leadId","expr":"input.city + \"-\" + string(input.age)"}`,
		`{"target":"customer.city","expr":"upper(input.city)"}`,
		`{"target":"customer.source","expr":"input.source"}`,
		`{"target":"vehicle.ageYears","expr":"ctx.now.Year() - 2019"}`,
		`{"target":"vehicle.priceLakh","expr":"input.price / 100000"}`,
		`{"target":"vehicle.km","expr":"input.km"}`,
		`{"target":"vehicle.highKm","expr":"input.km > 50000"}`,
		`{"target":"inspection.score","expr":"input.score"}`,
		`{"target":"inspection.good","expr":"input.score >= 7"}`,
		`{"target":"tags.count","expr":"len(input.tags)"}`,
		`{"target":"tags.certified","expr":"\"certified\" in input.tags"}`,
		`{"target":"summary","expr":"out.customer.city + \" \" + string(out.vehicle.priceLakh)"}`,
	}
	d := res(KindDecision, "tr12", 1, `{"entry":"t","nodes":[{"id":"t","type":"transform","fields":[`+strings.Join(fields, ",")+`]}]}`)
	s, in := benchSnapshot(b, d)
	runBench(b, s, "tr12", in)
}

// BenchmarkRules50FirstMatch: 50 rules, only the last matches (target ≤ 30 µs).
func BenchmarkRules50FirstMatch(b *testing.B) {
	var rules []string
	for i := 0; i < 49; i++ {
		rules = append(rules, fmt.Sprintf(`{"id":"r%d","when":{"all":[{"field":"price","op":"GT","value":%d},{"field":"city","op":"EQ","value":"City%d"}]},"then":{"rule":"r%d"}}`, i, 1000*i, i, i))
	}
	rules = append(rules, `{"id":"last","when":`+sevenConditions+`,"then":{"rule":"last"}}`)
	d := res(KindDecision, "fifty", 1, `{"entry":"r","nodes":[{"id":"r","type":"rules","inputSchema":{"name":"bench_in","version":1},"outputSchema":{"name":"bench_out","version":1},"rules":[`+strings.Join(rules, ",")+`],"default":{"rule":"none"}}]}`)
	s, in := benchSnapshot(b, d)
	runBench(b, s, "fifty", in)
}

// BenchmarkLeadRoutingGraph: the 3-node example graph (transform, rules with
// segments and rollout, output).
func BenchmarkLeadRoutingGraph(b *testing.B) {
	s := mustCompile(b, leadSet(b))
	var in any
	_ = json.Unmarshal([]byte(`{"lead":{"id":"L-1","city":"Gurgaon","source":"app"},"car":{"year":2019,"km":48200,"price":525000},"inspections":[{"part":"engine","score":8},{"part":"body","score":5}]}`), &in)
	runBench(b, s, "lead_routing", in)
}

// BenchmarkLeadRoutingWithDecode includes the JSON decode of the input.
func BenchmarkLeadRoutingWithDecode(b *testing.B) {
	s := mustCompile(b, leadSet(b))
	req := Request{Decision: "lead_routing", UnitID: "L-1029384", Input: json.RawMessage(`{"lead":{"id":"L-1","city":"Gurgaon","source":"app"},"car":{"year":2019,"km":48200,"price":525000},"inspections":[{"part":"engine","score":8},{"part":"body","score":5}]}`)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Decide(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParallelLeadRouting(b *testing.B) {
	s := mustCompile(b, leadSet(b))
	var in any
	_ = json.Unmarshal([]byte(`{"lead":{"id":"L-1","city":"Gurgaon","source":"app"},"car":{"year":2019,"km":48200,"price":525000}}`), &in)
	req := Request{Decision: "lead_routing", UnitID: "L-1029384"}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := s.DecideParsed(context.Background(), req, in); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCompileLeadSet(b *testing.B) {
	set := leadSet(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(set); err != nil {
			b.Fatal(err)
		}
	}
}
