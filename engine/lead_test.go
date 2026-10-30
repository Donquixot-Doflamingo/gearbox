// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLeadRoutingTestCases(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	results, err := snap.RunTests(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("got %d results", len(results))
	}
	for _, r := range results {
		if !r.OK {
			b, _ := json.MarshalIndent(r, "", " ")
			t.Errorf("test %s failed: %s", r.Name, b)
		}
	}
}

func TestLeadRoutingDecide(t *testing.T) {
	snap := mustCompile(t, leadSet(t))
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	r, err := snap.Decide(context.Background(), Request{
		Decision: "lead_routing", UnitID: "L-1029386", Now: now, Trace: true,
		Input: json.RawMessage(`{"lead":{"id":"L-1","city":"Gurgaon","source":"app","phone":"+91-1"},"car":{"year":2019,"km":48200,"price":525000},"inspections":[{"part":"engine","score":8},{"part":"body","score":7}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Output) != `{"reason":"fast_lane","route":"fast_track"}` {
		t.Fatalf("output = %s", r.Output)
	}
	if r.DecisionVersion != "lead_routing@17" || r.Generation != 42 || r.FallbackUsed {
		t.Fatalf("result = %+v", r)
	}
	if !strings.HasPrefix(r.DependencyHash, "sha256:") || len(r.DependencyHash) != 71 {
		t.Fatalf("dependency hash = %q", r.DependencyHash)
	}
	if got := pathString(r.Path); got != "normalize:ok,eligibility:r4,output:ok" {
		t.Fatalf("path = %s", got)
	}
	if !r.EvaluatedAt.Equal(now) {
		t.Fatalf("evaluatedAt = %v", r.EvaluatedAt)
	}
	in, out := snap.LogFields(r.DecisionVersion, json.RawMessage(`{"lead":{"city":"Gurgaon","phone":"x"},"car":{"year":2019}}`), r.Output)
	if in["lead.city"] != "Gurgaon" || in["car.year"] != float64(2019) || out["output.route"] != "fast_track" || len(in) != 2 {
		t.Fatalf("log fields = %v %v", in, out)
	}
}
