// SPDX-License-Identifier: MIT

package gearbox_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/ORG/gearbox/clients/go"
	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/sidecar/sidecartest"
)

func leadSet(t *testing.T) engine.Set {
	b, err := os.ReadFile("../../engine/testdata/lead_routing/set.json")
	if err != nil {
		t.Fatal(err)
	}
	var s engine.Set
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	s.Resources = append(s.Resources,
		engine.Resource{Kind: engine.KindGate, Name: "new_flow", Version: 2, Spec: json.RawMessage(`{"rules":[{"id":"vip","when":{"field":"tier","op":"EQ","value":"vip"},"value":true}],"default":false}`)},
		engine.Resource{Kind: engine.KindExperiment, Name: "copy_test", Version: 3, Spec: json.RawMessage(`{"status":"running","variants":[{"name":"control","weight":1,"parameters":{"t":"A"}},{"name":"treatment","weight":1,"parameters":{"t":"B"}}]}`)},
	)
	return s
}

var lead = map[string]any{
	"lead":        map[string]any{"id": "L-1", "city": "Gurgaon", "source": "app"},
	"car":         map[string]any{"year": 2019, "km": 48200, "price": 525000},
	"inspections": []any{map[string]any{"part": "engine", "score": 8}},
}

type obsRec struct {
	mu   sync.Mutex
	outs []string
}

func (o *obsRec) fn(decision string, d time.Duration, outcome string) {
	o.mu.Lock()
	o.outs = append(o.outs, decision+":"+outcome)
	o.mu.Unlock()
}

func TestClient(t *testing.T) {
	sc := sidecartest.Start(t, leadSet(t))
	ob := &obsRec{}
	ctx := context.Background()
	c, err := gearbox.Dial(ctx, sc.Target, gearbox.WithObserver(ob.fn), gearbox.WithWaitReady(), gearbox.WithDefaultTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	d, err := c.Decide(ctx, "lead_routing", lead, gearbox.WithUnit("L-1029386"), gearbox.WithTimeout(time.Second), gearbox.WithTrace(), gearbox.WithRequestID("rq-1"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Output["route"] != "fast_track" || d.Version != "lead_routing@17" || d.Generation != 42 || d.FallbackUsed || d.FromClientFallback || d.RequestID != "rq-1" || len(d.Path) != 3 || d.DependencyHash == "" {
		t.Fatalf("decision = %+v", d)
	}
	// Raw JSON input works too.
	if d, err := c.Decide(ctx, "lead_routing", json.RawMessage(`{"lead":{"id":"x","city":"Srinagar"},"car":{"year":2020,"km":1,"price":1}}`)); err != nil || d.Output["reason"] != "city_not_served" {
		t.Fatalf("raw = %+v %v", d, err)
	}
	// Errors carry reasons.
	_, err = c.Decide(ctx, "nope", lead)
	var ge *gearbox.Error
	if !gearbox.IsReason(err, "DECISION_NOT_FOUND") {
		t.Fatalf("not found = %v", err)
	}
	_, err = c.Decide(ctx, "lead_routing", map[string]any{"lead": map[string]any{}})
	if e, ok := err.(*gearbox.Error); !ok || e.Code != codes.InvalidArgument || e.Reason != "INPUT_SCHEMA" || len(e.Fields) == 0 {
		t.Fatalf("schema = %#v", err)
	}
	_ = ge
	// Client fallback on any error.
	fb, err := c.Decide(ctx, "nope", lead, gearbox.WithFallback(map[string]any{"route": "manual_review"}))
	if err != nil || !fb.FromClientFallback || fb.Output["route"] != "manual_review" || fb.Err == nil || string(fb.Raw) != `{"route":"manual_review"}` {
		t.Fatalf("fallback = %+v %v", fb, err)
	}
	// A deadline that cannot be met falls back too.
	fb, err = c.Decide(ctx, "lead_routing", lead, gearbox.WithTimeout(time.Nanosecond), gearbox.WithFallback(map[string]any{"route": "manual_review"}))
	if err != nil || !fb.FromClientFallback {
		t.Fatalf("deadline fallback = %+v %v", fb, err)
	}
	// Batch.
	res, err := c.DecideBatch(ctx, []gearbox.BatchItem{{Decision: "lead_routing", Input: lead, Unit: "L-1029386"}, {Decision: "missing", Input: map[string]any{}}})
	if err != nil || len(res) != 2 || res[0].Decision.Output["route"] != "fast_track" || !gearbox.IsReason(res[1].Err, "DECISION_NOT_FOUND") {
		t.Fatalf("batch = %+v %v", res, err)
	}
	// Gate and variant.
	g, err := c.CheckGate(ctx, "new_flow", "u1", map[string]any{"tier": "vip"})
	if err != nil || !g.Value || g.RuleID != "vip" || g.Version != "new_flow@2" {
		t.Fatalf("gate = %+v %v", g, err)
	}
	if g, err := c.CheckGate(ctx, "nope", "u1", nil, gearbox.WithFallback(true)); err != nil || !g.Value || g.RuleID != "client_fallback" {
		t.Fatalf("gate fallback = %+v %v", g, err)
	}
	v, err := c.GetVariant(ctx, "copy_test", "user-7", nil)
	if err != nil || !v.InExperiment || v.Reason != "assigned" || v.Version != "copy_test@3" || (v.Name == "control") != (v.Parameters["t"] == "A") {
		t.Fatalf("variant = %+v %v", v, err)
	}
	if v, _ := c.GetVariant(ctx, "copy_test", "", nil); v.InExperiment || v.Reason != "no_unit" {
		t.Fatalf("no unit = %+v", v)
	}
	// Status.
	st, err := c.Status(ctx)
	if err != nil || !st.Ready || st.Generation != 42 || st.EngineVersion != engine.Version || len(st.Decisions) != 1 {
		t.Fatalf("status = %+v %v", st, err)
	}
	// traceparent propagation does not break calls.
	tctx := gearbox.WithTraceparent(ctx, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if _, err := c.Decide(tctx, "lead_routing", lead); err != nil {
		t.Fatal(err)
	}
	ictx := metadata.NewIncomingContext(ctx, metadata.Pairs("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"))
	if _, err := c.Decide(ictx, "lead_routing", lead); err != nil {
		t.Fatal(err)
	}
	ob.mu.Lock()
	defer ob.mu.Unlock()
	if len(ob.outs) < 8 || ob.outs[0] != "lead_routing:ok" {
		t.Fatalf("observer = %v", ob.outs)
	}
	found := false
	for _, o := range ob.outs {
		if o == "nope:client_fallback" {
			found = true
		}
	}
	if !found {
		t.Fatalf("observer = %v", ob.outs)
	}
}

func TestTCPWithToken(t *testing.T) {
	sc := sidecartest.Start(t, leadSet(t), sidecartest.Options{TCPAddr: "127.0.0.1:17643", TCPToken: "tok-123"})
	ctx := context.Background()
	c, err := gearbox.Dial(ctx, sc.TCPAddr, gearbox.WithToken("tok-123"), gearbox.WithDefaultTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if d, err := c.Decide(ctx, "lead_routing", lead); err != nil || d.Output["route"] == nil {
		t.Fatalf("tcp = %+v %v", d, err)
	}
	bad, _ := gearbox.Dial(ctx, sc.TCPAddr, gearbox.WithToken("wrong"), gearbox.WithDefaultTimeout(2*time.Second))
	defer bad.Close()
	if _, err := bad.Decide(ctx, "lead_routing", lead); err == nil {
		t.Fatal("wrong token accepted")
	} else if e, ok := err.(*gearbox.Error); !ok || e.Code != codes.Unauthenticated {
		t.Fatalf("err = %v", err)
	}
}

func BenchmarkDecideOverUnixSocket(b *testing.B) {
	t := &testing.T{}
	_ = t
	sc := sidecartest.Start(b, func() engine.Set {
		bt, _ := os.ReadFile("../../engine/testdata/lead_routing/set.json")
		var s engine.Set
		_ = json.Unmarshal(bt, &s)
		return s
	}())
	c, err := gearbox.Dial(context.Background(), sc.Target, gearbox.WithDefaultTimeout(time.Second))
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	raw, _ := json.Marshal(lead)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Decide(context.Background(), "lead_routing", json.RawMessage(raw), gearbox.WithUnit("L-1")); err != nil {
			b.Fatal(err)
		}
	}
}
