// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// FuzzSpecDecode feeds arbitrary decision specs to the validator: it must
// never panic and always return problems for invalid input.
func FuzzSpecDecode(f *testing.F) {
	set := loadSet(f, "testdata/lead_routing/set.json")
	deps := resolverOf(set.Resources...)
	for _, r := range set.Resources {
		f.Add(string(r.Kind), string(r.Spec))
	}
	f.Add("decisions", `{"entry":"a","nodes":[{"id":"a","type":"switch","cases":[{"when":"x","next":"a"}],"default":"a"}]}`)
	f.Add("segments", `{"type":"rollout","rollout":{"percent":101}}`)
	f.Add("schemas", `{"jsonSchema":{"$ref":"#"}}`)
	f.Fuzz(func(t *testing.T, kind, spec string) {
		v := ValidateResource(Resource{Kind: Kind(kind), Name: "fuzzed", Version: 1, Spec: json.RawMessage(spec)}, deps)
		if !v.Valid && len(v.Problems) == 0 {
			t.Fatal("invalid without problems")
		}
		_, _ = Dependencies(Resource{Kind: Kind(kind), Name: "fuzzed", Spec: json.RawMessage(spec)})
	})
}

// FuzzExprCompile compiles arbitrary expressions with static analysis.
func FuzzExprCompile(f *testing.F) {
	for _, s := range []string{`input.a.b > 3`, `map(input.xs, .s)`, `let x = 1; x + input.y`, `gate("g")`, `lookup("l", input.k, 1)`, `now()`, `{"a": [1, 2]}`, `input?.a ?? "x"`, `any(1..1000000, # > 0)`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		b := newBuilder(emptyResolver{}, "app", "env", nil)
		_, _, ok := b.expr(src, "/x", &scope{vars: varsTransform})
		if !ok && b.errorCount() == 0 {
			t.Fatalf("expression %q failed without a problem", src)
		}
	})
}

// FuzzDecide runs the example decision on arbitrary inputs: it must return
// an output or a typed *Error, never panic.
func FuzzDecide(f *testing.F) {
	snap := mustCompile(f, loadSet(f, "testdata/lead_routing/set.json"))
	f.Add(`{"lead":{"id":"L-1","city":"Gurgaon","source":"app"},"car":{"year":2019,"km":48200,"price":525000}}`, "u1")
	f.Add(`{"lead":{"id":1},"car":null}`, "")
	f.Add(`[]`, "x")
	f.Add(`{"lead":{"id":"a","city":"b"},"car":{"year":1e308,"km":-1,"price":"NaN"},"inspections":[{"part":"x","score":"y"}]}`, "")
	f.Fuzz(func(t *testing.T, input, unit string) {
		r, err := snap.Decide(context.Background(), Request{Decision: "lead_routing", Input: json.RawMessage(input), UnitID: unit})
		if err != nil {
			if _, ok := err.(*Error); !ok {
				t.Fatalf("untyped error %T: %v", err, err)
			}
			return
		}
		if !json.Valid(r.Output) {
			t.Fatalf("invalid output JSON %q", r.Output)
		}
	})
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
