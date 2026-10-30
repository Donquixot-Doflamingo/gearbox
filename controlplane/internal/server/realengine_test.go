package server

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/engine/bundle"
)

// TestRealEngine runs the main flows on the data-plane engine and bundle
// packages (no lite fallback). Skipped while they are stubs.
func TestRealEngine(t *testing.T) {
	if e, b := engineapi.RealImplemented(); !e || !b {
		t.Skip("data-plane engine/bundle not implemented in this checkout")
	}
	h := newHarness(t, func(_ *Config, d *Deps) { d.Engine, d.Bundler = engineapi.Real{}, engineapi.RealBundler{} })
	res := seedDemo(t, h)

	fast := map[string]any{"lead": map[string]any{"id": "L-1029384", "city": "Gurgaon", "source": "app"}, "car": map[string]any{"year": 2019, "km": 48200, "price": 525000}, "inspections": []any{map[string]any{"part": "engine", "score": 8}}}
	r := h.must(200, "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"source": "draft", "input": fast, "ctx": map[string]any{"now": "2026-10-30T10:00:00Z", "unit": "L-1029386"}})
	body := string(r.Body)
	if !strings.Contains(body, `"route":"fast_track"`) || !strings.Contains(body, `"decisionVersion":"lead_routing@draft"`) || !strings.Contains(body, `"outcome":"r4"`) {
		t.Fatalf("evaluate: %s", body)
	}
	r = h.must(200, "POST", app+"/decisions/lead_routing/tests/run", nil)
	if !strings.Contains(string(r.Body), `"passed":6`) || !strings.Contains(string(r.Body), `"failed":0`) {
		t.Fatalf("tests: %s", r.Body)
	}
	// Entry schema errors are 422 in the console and 400 at runtime.
	bad := map[string]any{"lead": map[string]any{"id": "x", "city": "Pune"}}
	h.must(422, "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"input": bad})
	cred := bearerTok(res.Credential)
	base := app + "/environments/dev"
	h.mustCode(400, "INVALID_ARGUMENT", "POST", base+"/decide", map[string]any{"decision": "lead_routing", "input": bad}, cred)
	r = h.must(200, "POST", base+"/decide", map[string]any{"decision": "lead_routing", "unitId": "L-1029386", "input": fast, "options": map[string]any{"now": "2026-10-30T10:00:00Z"}}, cred)
	if !strings.Contains(string(r.Body), `"route":"fast_track"`) || !strings.Contains(string(r.Body), `"decisionVersion":"lead_routing@1"`) {
		t.Fatalf("decide: %s", r.Body)
	}
	h.must(200, "POST", base+"/gates/new_inspection_flow/check", map[string]any{"unitId": "u1", "attributes": map[string]any{"city": "Pune", "price": 1}}, cred)
	r = h.must(200, "POST", base+"/experiments/callback_timing/assign", map[string]any{"unitId": "u1"}, cred)
	if !strings.Contains(string(r.Body), `"inExperiment":true`) {
		t.Fatalf("assign: %s", r.Body)
	}
	h.must(200, "POST", app+"/segments/metro_premium/check", map[string]any{"attributes": map[string]any{"city": "Delhi", "price": 900000}})

	// The published bundle opens with the data-plane reader.
	r = h.must(200, "GET", "/sidecar/v1/head", nil, cred)
	var head model.BundleHead
	r.JSON(t, &head)
	_, env := get(t, head.Envelope.URL)
	blobs := map[string][]byte{}
	for _, rs := range head.Resources {
		_, b := get(t, rs.URL)
		blobs[rs.SHA256] = b
	}
	op, err := bundle.Open(env, blobs, bundle.TrustedKeys{"test": h.key.Public().(ed25519.PublicKey)}, false, 64<<20)
	if err != nil {
		t.Fatalf("bundle.Open: %v", err)
	}
	if op.Generation != 1 || op.App != "lead_service" || len(op.Set.Resources) != 13 {
		t.Fatalf("opened: gen=%d app=%s resources=%d", op.Generation, op.App, len(op.Set.Resources))
	}

	// Rollout through the real engine (rollouts need both versions in the set).
	h.must(201, "POST", "/members", map[string]any{"email": "meera@example.com", "role": "approver"})
	v2 := cutNewRoutingVersion(t, h, as(h.user))
	r = h.must(201, "POST", app+"/change-requests", map[string]any{"environment": "dev", "title": "v2", "items": []any{map[string]any{"kind": "decisions", "name": "lead_routing", "version": v2, "rollout": map[string]any{"percent": 30}}}})
	var cr struct{ ID string }
	r.JSON(t, &cr)
	h.must(201, "POST", base+"/releases", map[string]any{"type": "change_request", "changeRequestId": cr.ID})
	h.must(201, "POST", base+"/releases", map[string]any{"type": "rollout", "decision": "lead_routing", "percent": 60}, as("meera@example.com"))
	// Publish-time benchmark with a generous factor (race/CI machines are slow).
	h.s.cfg.PublishBenchmark, h.s.cfg.BudgetFactor = true, 1000
	h.must(201, "POST", base+"/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"}, as("meera@example.com"))
	h.s.cfg.BudgetFactor = 0.0000001
	h.mustCode(422, "BUDGET_EXCEEDED", "POST", base+"/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow", "killed": false}, as("meera@example.com"))
	h.s.cfg.PublishBenchmark = false
	r = h.must(200, "POST", base+"/gates/new_inspection_flow/check", map[string]any{"unitId": "u1", "attributes": map[string]any{"city": "Delhi", "price": 1}}, cred)
	if !strings.Contains(string(r.Body), `"value":false`) {
		t.Fatalf("killed gate: %s", r.Body)
	}
}
