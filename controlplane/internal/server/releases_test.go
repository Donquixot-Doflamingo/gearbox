package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

func addMembers(h *harness) {
	h.must(201, "POST", "/members", map[string]any{"email": "asha@example.com", "name": "Asha", "role": "author"})
	h.must(201, "POST", "/members", map[string]any{"email": "meera@example.com", "name": "Meera", "role": "approver"})
}

// cutQuoteV2 changes quick lead_routing so a new version exists.
func cutNewRoutingVersion(t *testing.T, h *harness, user reqOpt) int64 {
	t.Helper()
	cur := h.must(200, "GET", app+"/decisions/lead_routing", nil).Map(t)
	d := cur["draft"].(map[string]any)
	spec := d["spec"].(map[string]any)
	spec["budgetMicros"] = float64(300 + time.Now().Nanosecond()%100)
	h.must(200, "PUT", app+"/decisions/lead_routing/draft", map[string]any{"revision": d["revision"], "spec": spec}, user)
	cur = h.must(200, "GET", app+"/decisions/lead_routing", nil).Map(t)
	r := h.must(201, "POST", app+"/decisions/lead_routing/versions", map[string]any{"revision": cur["draft"].(map[string]any)["revision"], "message": "bump"}, user)
	var v struct{ Version int64 }
	r.JSON(t, &v)
	return v.Version
}

func allLatestItems(t *testing.T, h *harness) []map[string]any {
	var items []map[string]any
	for _, k := range model.AllKinds {
		r := h.must(200, "GET", app+"/"+k, nil)
		var page struct {
			Items []struct {
				Name          string `json:"name"`
				LatestVersion int64  `json:"latestVersion"`
			} `json:"items"`
		}
		r.JSON(t, &page)
		for _, it := range page.Items {
			if it.LatestVersion > 0 {
				items = append(items, map[string]any{"kind": k, "name": it.Name, "version": it.LatestVersion})
			}
		}
	}
	return items
}

func TestChangeRequestsAndReleases(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	addMembers(h)
	author, approver := as("asha@example.com"), as("meera@example.com")

	// Open a prod change request with everything (prod: 1 approval, no self-approval).
	items := allLatestItems(t, h)
	r := h.must(201, "POST", app+"/change-requests", map[string]any{"environment": "prod", "title": "Go live", "description": "first prod release", "items": items}, author)
	var cr struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		RequiredApprovals int    `json:"requiredApprovals"`
		Items             []struct {
			Diff string `json:"diff"`
		} `json:"items"`
		Tests struct{ Passed, Failed int } `json:"tests"`
	}
	r.JSON(t, &cr)
	if cr.Status != "open" || cr.RequiredApprovals != 1 || cr.Tests.Passed != 6 || cr.Items[0].Diff == "" {
		t.Fatalf("cr: %s", r.Body)
	}
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/change-requests", map[string]any{"environment": "prod", "title": "bad", "items": []any{map[string]any{"kind": "decisions", "name": "lead_routing", "version": 99}}}, author)
	// Only the decision, without its live dependencies → the would-be bundle is incomplete.
	r = h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/change-requests", map[string]any{"environment": "stage", "title": "partial", "items": []any{map[string]any{"kind": "decisions", "name": "lead_routing", "version": 1}}}, author)
	if !strings.Contains(string(r.Body), "DEPENDENCY_NOT_LIVE") {
		t.Fatalf("partial cr: %s", r.Body)
	}
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/change-requests", map[string]any{"environment": "qa", "title": "x", "items": items}, author)

	pub := app + "/environments/prod/releases"
	h.mustCode(409, "NOT_APPROVED", "POST", pub, map[string]any{"type": "change_request", "changeRequestId": cr.ID}, author)
	h.mustCode(403, "FORBIDDEN", "POST", app+"/change-requests/"+cr.ID+"/approve", nil, author)
	h.must(201, "POST", "/members", map[string]any{"email": "sam@example.com", "role": "approver"})
	// An approver who authored the request may not approve it in prod.
	r = h.must(201, "POST", app+"/change-requests", map[string]any{"environment": "prod", "title": "self", "items": items}, as("sam@example.com"))
	var selfCR struct{ ID string }
	r.JSON(t, &selfCR)
	h.mustCode(403, "SELF_APPROVAL", "POST", app+"/change-requests/"+selfCR.ID+"/approve", nil, as("sam@example.com"))
	h.mustCode(403, "FORBIDDEN", "POST", app+"/change-requests/"+selfCR.ID+"/withdraw", nil, author)
	h.must(200, "POST", app+"/change-requests/"+selfCR.ID+"/withdraw", nil, as("sam@example.com"))
	h.mustCode(409, "INVALID_STATE", "POST", app+"/change-requests/"+selfCR.ID+"/approve", nil, approver)

	h.must(200, "POST", app+"/change-requests/"+cr.ID+"/comments", map[string]any{"body": "looks good"}, approver)
	r = h.must(200, "POST", app+"/change-requests/"+cr.ID+"/approve", map[string]any{"comment": "ship it"}, approver)
	if !strings.Contains(string(r.Body), `"status":"approved"`) {
		t.Fatalf("approve: %s", r.Body)
	}
	h.must(200, "POST", app+"/change-requests/"+cr.ID+"/approve", nil, approver) // idempotent
	r = h.must(201, "POST", pub, map[string]any{"type": "change_request", "changeRequestId": cr.ID}, author)
	var rel struct {
		Generation   int64  `json:"generation"`
		ManifestHash string `json:"manifestHash"`
		Contents     []struct {
			Kind, Name string
			Changed    bool
		} `json:"contents"`
	}
	r.JSON(t, &rel)
	if rel.Generation != 1 || !strings.HasPrefix(rel.ManifestHash, "sha256:") || len(rel.Contents) != len(items) || !rel.Contents[0].Changed {
		t.Fatalf("release: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/change-requests/"+cr.ID, nil)
	if !strings.Contains(string(r.Body), `"status":"published"`) || !strings.Contains(string(r.Body), `"publishedGeneration":1`) {
		t.Fatalf("cr after publish: %s", r.Body)
	}
	h.mustCode(409, "NOT_APPROVED", "POST", pub, map[string]any{"type": "change_request", "changeRequestId": cr.ID}, author)
	r = h.must(200, "GET", app+"/change-requests?status=published&environment=prod", nil)
	if !strings.Contains(string(r.Body), cr.ID) || strings.Contains(string(r.Body), selfCR.ID) {
		t.Fatalf("cr list filter: %s", r.Body)
	}
	h.must(200, "GET", app+"/change-requests?limit=1", nil)

	// Reject flow.
	r = h.must(201, "POST", app+"/change-requests", map[string]any{"environment": "prod", "title": "again", "items": items[:1]}, author)
	var cr2 struct{ ID string }
	r.JSON(t, &cr2)
	h.must(200, "POST", app+"/change-requests/"+cr2.ID+"/reject", map[string]any{"comment": "no"}, approver)
	h.mustCode(404, "NOT_FOUND", "GET", app+"/change-requests/cr_999", nil)

	// Rollout: new version at 10%, then 50%, then 100%.
	v2 := cutNewRoutingVersion(t, h, author)
	r = h.must(201, "POST", app+"/change-requests", map[string]any{"environment": "prod", "title": "v2 at 10%", "items": []any{map[string]any{"kind": "decisions", "name": "lead_routing", "version": v2, "rollout": map[string]any{"percent": 10}}}}, author)
	var cr3 struct{ ID string }
	r.JSON(t, &cr3)
	h.must(200, "POST", app+"/change-requests/"+cr3.ID+"/approve", nil, approver)
	r = h.must(201, "POST", pub, map[string]any{"type": "change_request", "changeRequestId": cr3.ID}, author)
	if !strings.Contains(string(r.Body), `"rollout":{"version":2,"rollout":{"fromVersion":1,"percent":10}}`) {
		t.Fatalf("rollout release: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/decisions/lead_routing", nil)
	if !strings.Contains(string(r.Body), `"prod":{"version":2,"rollout":{"fromVersion":1,"percent":10}}`) {
		t.Fatalf("live rollout: %s", r.Body)
	}
	h.mustCode(403, "FORBIDDEN", "POST", pub, map[string]any{"type": "rollout", "decision": "lead_routing", "percent": 50}, author)
	h.must(201, "POST", pub, map[string]any{"type": "rollout", "decision": "lead_routing", "percent": 50}, approver)
	h.must(201, "POST", pub, map[string]any{"type": "rollout", "decision": "lead_routing", "percent": 100}, approver)
	h.mustCode(409, "NOT_ROLLING_OUT", "POST", pub, map[string]any{"type": "rollout", "decision": "lead_routing", "percent": 20}, approver)
	r = h.must(200, "GET", app+"/decisions/lead_routing", nil)
	if !strings.Contains(string(r.Body), `"prod":{"version":2}`) {
		t.Fatalf("rollout complete: %s", r.Body)
	}

	// Kill switch on and off.
	h.must(201, "POST", pub, map[string]any{"type": "kill_switch", "gate": "new_inspection_flow", "killed": true, "reason": "service down"}, approver)
	h.mustCode(422, "VALIDATION_FAILED", "POST", pub, map[string]any{"type": "kill_switch", "gate": "nope_gate"}, approver)

	// Data releases: prod forbids them; dev allows them.
	h.must(200, "PUT", app+"/segments/blocked_cities/members", map[string]any{"members": []string{"Leh", "Shimla"}}, author)
	cur := h.must(200, "GET", app+"/segments/blocked_cities", nil).Map(t)
	h.must(201, "POST", app+"/segments/blocked_cities/versions", map[string]any{"revision": cur["draft"].(map[string]any)["revision"]}, author)
	h.mustCode(403, "FORBIDDEN", "POST", pub, map[string]any{"type": "data", "items": []any{map[string]any{"kind": "segments", "name": "blocked_cities", "version": 2}}}, author)
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/environments/dev/releases", map[string]any{"type": "data", "items": []any{map[string]any{"kind": "decisions", "name": "lead_routing", "version": 2}}}, author)
	r = h.must(201, "POST", app+"/environments/dev/releases", map[string]any{"type": "data", "items": []any{map[string]any{"kind": "segments", "name": "blocked_cities", "version": 2}}}, author)
	if !strings.Contains(string(r.Body), `"generation":2`) {
		t.Fatalf("data release: %s", r.Body)
	}

	// Rollback (break glass) to generation 1.
	h.mustCode(400, "INVALID_ARGUMENT", "POST", pub, map[string]any{"type": "rollback", "toGeneration": 99}, approver)
	r = h.must(201, "POST", pub, map[string]any{"type": "rollback", "toGeneration": 1, "reason": "v2 misroutes Pune leads"}, approver)
	if !strings.Contains(string(r.Body), `"breakGlass":true`) {
		t.Fatalf("rollback: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/decisions/lead_routing", nil)
	if !strings.Contains(string(r.Body), `"prod":{"version":1}`) {
		t.Fatalf("after rollback: %s", r.Body)
	}
	r = h.must(200, "GET", pub+"?limit=2", nil)
	var rl struct {
		Items []struct {
			Generation int64  `json:"generation"`
			Type       string `json:"type"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	r.JSON(t, &rl)
	if len(rl.Items) != 2 || rl.Items[0].Generation != 6 || rl.Items[0].Type != "rollback" || rl.NextCursor == "" {
		t.Fatalf("releases: %s", r.Body)
	}
	r = h.must(200, "GET", pub+"?cursor="+rl.NextCursor, nil)
	if !strings.Contains(string(r.Body), `"generation":4`) {
		t.Fatalf("releases page 2: %s", r.Body)
	}
	h.must(200, "GET", pub+"/1", nil)
	h.mustCode(404, "NOT_FOUND", "GET", pub+"/42", nil)

	r = h.must(200, "GET", "/audit?app=lead_service&limit=200", nil)
	if !strings.Contains(string(r.Body), `"breakGlass":true`) || !strings.Contains(string(r.Body), "release.published") {
		t.Fatalf("audit: %.400s", r.Body)
	}
	h.must(200, "GET", "/audit?actor=meera@example.com", nil)

	// Object store layout.
	ctx := context.Background()
	for _, key := range []string{"heads/lead_service/prod.json", "envelopes/lead_service/prod/6.pb"} {
		if ok, _ := h.obj.Exists(ctx, key); !ok {
			t.Errorf("object store is missing %s", key)
		}
	}
	head, _ := h.obj.Get(ctx, "heads/lead_service/prod.json")
	if !strings.Contains(string(head), `"generation":6`) {
		t.Errorf("head pointer %s", head)
	}
}

// failingEngine makes every test case fail.
type failingEngine struct{ engineapi.Lite }

type failingSnap struct{ engineapi.Snapshot }

func (f failingEngine) Compile(set engine.Set) (engineapi.Snapshot, error) {
	s, err := f.Lite.Compile(set)
	if err != nil {
		return nil, err
	}
	return failingSnap{s}, nil
}

func (f failingSnap) RunTests(ctx context.Context, d string, v int64) ([]engine.TestResult, error) {
	rs, err := f.Snapshot.RunTests(ctx, d, v)
	for i := range rs {
		rs[i].OK = false
		rs[i].Mismatches = []engine.Mismatch{{Path: "/route", Expected: json.RawMessage(`"fast_track"`), Actual: json.RawMessage(`"standard"`)}}
		rs[i].Error = ""
	}
	return rs, err
}

func TestTestsFailedAndBudget(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	h.s.eng = failingEngine{}
	cur := h.must(200, "GET", app+"/decisions/lead_routing", nil).Map(t)
	d := cur["draft"].(map[string]any)
	spec := d["spec"].(map[string]any)
	spec["budgetMicros"] = 150.0
	h.must(200, "PUT", app+"/decisions/lead_routing/draft", map[string]any{"revision": d["revision"], "spec": spec})
	r := h.mustCode(422, "TESTS_FAILED", "POST", app+"/decisions/lead_routing/versions", map[string]any{"revision": d["revision"].(float64) + 1})
	if !strings.Contains(string(r.Body), "expected \\\"fast_track\\\"") && !strings.Contains(string(r.Body), `fast_track`) {
		t.Fatalf("tests failed body: %s", r.Body)
	}
	r = h.must(200, "POST", app+"/decisions/lead_routing/tests/run", nil)
	if !strings.Contains(string(r.Body), `"failed":6`) {
		t.Fatalf("tests/run: %s", r.Body)
	}
	h.mustCode(422, "TESTS_FAILED", "POST", app+"/environments/dev/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"})
	h.mustCode(422, "TESTS_FAILED", "POST", app+"/change-requests", map[string]any{"environment": "stage", "title": "x", "items": allLatestItems(t, h)})

	// Budget check: a benchmark slower than budgetMicros blocks publishing.
	h.s.eng = slowEngine{}
	h.s.cfg.PublishBenchmark = true
	r = h.mustCode(422, "BUDGET_EXCEEDED", "POST", app+"/environments/dev/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"})
	if !strings.Contains(string(r.Body), "COST_TOO_HIGH") {
		t.Fatalf("budget: %s", r.Body)
	}
}

type slowEngine struct{ engineapi.Lite }
type slowSnap struct{ engineapi.Snapshot }

func (slowEngine) Compile(set engine.Set) (engineapi.Snapshot, error) {
	s, err := engineapi.Lite{}.Compile(set)
	if err != nil {
		return nil, err
	}
	return slowSnap{s}, nil
}
func (slowSnap) Benchmark(context.Context, int) (map[string]int64, error) {
	return map[string]int64{"lead_routing": 5000}, nil
}

type hugeBundler struct{ engineapi.LiteBundler }

func (b hugeBundler) Build(set engine.Set, m bundle.Meta, s bundle.Signer) (bundle.Built, error) {
	out, err := b.LiteBundler.Build(set, m, s)
	out.TotalBytes = 2 << 20
	return out, err
}

func TestBundleTooLarge(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	h.must(200, "PATCH", app, map[string]any{"bundleLimitMB": 1, "description": "small"})
	h.s.bun = hugeBundler{}
	h.mustCode(422, "BUNDLE_TOO_LARGE", "POST", app+"/environments/dev/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"})
}

// Concurrent publishes must yield strictly increasing, unique generations.
func TestConcurrentPublishes(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	var wg sync.WaitGroup
	gens := make(chan int64, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := h.do("POST", app+"/environments/dev/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow", "killed": i%2 == 0})
			if r.Status != 201 {
				t.Errorf("publish %d: %d %s", i, r.Status, r.Body)
				return
			}
			var rel struct{ Generation int64 }
			_ = json.Unmarshal(r.Body, &rel)
			gens <- rel.Generation
		}(i)
	}
	wg.Wait()
	close(gens)
	seen := map[int64]bool{}
	for g := range gens {
		if seen[g] {
			t.Fatalf("generation %d published twice", g)
		}
		seen[g] = true
	}
	for g := int64(2); g <= 17; g++ {
		if !seen[g] {
			t.Fatalf("generation %d missing: %v", g, seen)
		}
	}
}

func TestWebhooks(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	fail := true
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			fail = false
			w.WriteHeader(503)
			return
		}
		got, bodies = append(got, r), append(bodies, b)
		w.WriteHeader(204)
	}))
	defer recv.Close()
	r := h.must(201, "POST", "/webhooks", map[string]any{"url": recv.URL + "/hook", "events": []string{"release.published", "change_request.opened"}})
	var wh struct{ ID, Secret string }
	r.JSON(t, &wh)
	if wh.Secret == "" {
		t.Fatalf("secret not returned: %s", r.Body)
	}
	h.mustCode(400, "INVALID_ARGUMENT", "POST", "/webhooks", map[string]any{"url": "ftp://x", "events": []string{}})
	h.mustCode(400, "INVALID_ARGUMENT", "POST", "/webhooks", map[string]any{"url": "http://x", "events": []string{"bogus"}})
	seedDemo(t, h) // opens a change request and publishes
	ctx := context.Background()
	h.s.DeliverWebhooks(ctx) // first attempt fails (503) and is rescheduled
	h.s.now = func() time.Time { return time.Now().UTC().Add(time.Minute) }
	for i := 0; i < 3; i++ {
		h.s.DeliverWebhooks(ctx)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("deliveries: %d", len(got))
	}
	for i, req := range got {
		if req.Header.Get("X-Gearbox-Signature") != WebhookSignature(wh.Secret, bodies[i]) {
			t.Fatalf("bad signature on delivery %d", i)
		}
	}
	events := req2events(got)
	if !events["release.published"] || !events["change_request.opened"] {
		t.Fatalf("events %v", events)
	}
	r = h.must(200, "GET", "/webhooks", nil)
	if !strings.Contains(string(r.Body), `"lastDelivery"`) {
		t.Fatalf("lastDelivery: %s", r.Body)
	}
	h.must(204, "DELETE", "/webhooks/"+wh.ID, nil)
	h.mustCode(404, "NOT_FOUND", "DELETE", "/webhooks/"+wh.ID, nil)
}

func req2events(rs []*http.Request) map[string]bool {
	m := map[string]bool{}
	for _, r := range rs {
		m[r.Header.Get("X-Gearbox-Event")] = true
	}
	return m
}

var _ = fmt.Sprint
