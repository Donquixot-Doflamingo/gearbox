package server

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/engine/bundle"
)

func get(t *testing.T, u string, opts ...reqOpt) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	for _, o := range opts {
		o(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func TestSidecarProtocol(t *testing.T) {
	h := newHarness(t)
	res := seedDemo(t, h)
	cred := bearerTok(res.Credential)

	h.mustCode(401, "UNAUTHENTICATED", "GET", "/sidecar/v1/head", nil)
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/sidecar/v1/head", nil, bearerTok("gbx_cred_nope"))
	h.mustCode(403, "FORBIDDEN", "GET", "/apps", nil, cred)

	r := h.must(200, "GET", "/sidecar/v1/head", nil, cred)
	var head model.BundleHead
	r.JSON(t, &head)
	etag := r.Header.Get("ETag")
	if head.Generation != 1 || etag == "" || len(head.Resources) == 0 {
		t.Fatalf("head: %s", r.Body)
	}
	h.must(304, "GET", "/sidecar/v1/head", nil, cred, hdr("If-None-Match", etag))

	// Download and verify the envelope and every blob.
	code, env := get(t, head.Envelope.URL)
	if code != 200 || sha(env) != head.Envelope.SHA256 {
		t.Fatalf("envelope download %d", code)
	}
	gen, needed, err := engineapi.LiteBundler{}.VerifyEnvelope(env, bundle.TrustedKeys{"test": h.key.Public().(ed25519.PublicKey)})
	if err != nil || gen != 1 || len(needed) != len(head.Resources) {
		t.Fatalf("verify: %v gen=%d needed=%d", err, gen, len(needed))
	}
	for _, rsrc := range head.Resources {
		code, b := get(t, rsrc.URL)
		if code != 200 || sha(b) != rsrc.SHA256 {
			t.Fatalf("blob %s: %d", rsrc.SHA256, code)
		}
	}
	// "have" leaves known blobs out (and changes the ETag).
	have := head.Resources[0].SHA256 + "," + head.Resources[1].SHA256
	r = h.must(200, "GET", "/sidecar/v1/head?have="+have, nil, cred)
	var head2 model.BundleHead
	r.JSON(t, &head2)
	if len(head2.Resources) != len(head.Resources)-2 || r.Header.Get("ETag") == etag {
		t.Fatalf("have filter: %d vs %d", len(head2.Resources), len(head.Resources))
	}
	// Tampered or expired links are refused.
	u, _ := url.Parse(head.Resources[0].URL)
	q := u.Query()
	q.Set("sig", strings.Repeat("0", 64))
	h.mustCode(403, "FORBIDDEN", "GET", "/sidecar/v1/blobs/"+head.Resources[0].SHA256+"?"+q.Encode(), nil)
	h.mustCode(400, "INVALID_ARGUMENT", "GET", "/sidecar/v1/blobs/"+head.Resources[0].SHA256, nil)
	h.mustCode(400, "INVALID_ARGUMENT", "GET", "/sidecar/v1/blobs/not-a-hash?sig=x&exp=1", nil)

	// Watch: current generation on connect, then the next publish.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.ts.URL+"/api/v1/sidecar/v1/watch", nil)
	req.Header.Set("Authorization", "Bearer "+res.Credential)
	wres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := wres.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("watch content type %q", ct)
	}
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(wres.Body)
		ev := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				ev = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				events <- ev + " " + v
			}
		}
		close(events)
	}()
	expect := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-events:
				if e == want {
					return
				}
			case <-deadline:
				t.Fatalf("no %q event", want)
			}
		}
	}
	expect(`generation {"generation":1}`)
	expect(`ping {}`)
	h.must(201, "POST", app+"/environments/dev/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"})
	expect(`generation {"generation":2}`)
	cancel()
	wres.Body.Close()

	// Heartbeats and logs.
	now := time.Now().UTC()
	h.must(204, "POST", "/sidecar/v1/heartbeat", map[string]any{"id": "pod-a", "node": "n1", "sidecarVersion": "1.0.0", "engineVersion": "1.0.0", "generation": 2, "lastSyncAt": now, "p99Micros": 40, "callsPerMinute": 600, "fallbackRate": 0.01}, cred)
	h.must(204, "POST", "/sidecar/v1/heartbeat", map[string]any{"id": "pod-b", "sidecarVersion": "0.9.0", "engineVersion": "1.0.0", "generation": 1, "lastError": "signature_invalid", "p99Micros": 90}, cred)
	h.mustCode(400, "INVALID_ARGUMENT", "POST", "/sidecar/v1/heartbeat", map[string]any{"id": "x"}, cred)
	ts := now.Format(time.RFC3339Nano)
	logs := map[string]any{"records": []any{
		map[string]any{"type": "decision", "ts": ts, "requestId": "req_1", "decision": "lead_routing", "decisionVersion": "lead_routing@1", "generation": 2, "fallbackUsed": false, "evalMicros": 30, "status": "OK", "unitId": "L-1",
			"output": map[string]any{"route": "fast_track"}, "ai": []any{map[string]any{"node": "ai_check", "model": "m", "latencyMs": 120, "result": "fast_track"}}},
		map[string]any{"type": "decision", "ts": ts, "requestId": "req_2", "decision": "lead_routing", "decisionVersion": "lead_routing@1", "generation": 2, "fallbackUsed": true, "evalMicros": 50, "status": "OK",
			"output": map[string]any{"route": "standard"}, "ai": []any{map[string]any{"node": "ai_check", "latencyMs": 300, "result": "fast_track"}, map[string]any{"node": "ai_check", "latencyMs": 900, "result": "timeout"}}},
		map[string]any{"type": "update", "ts": ts, "sidecarId": "pod-b", "fromGeneration": 1, "toGeneration": 2, "result": "kept_previous", "signature": "invalid", "error": "signature_invalid"},
		map[string]any{"type": "exposure", "ts": ts, "unitId": "L-1", "experiment": "callback_timing", "experimentVersion": "callback_timing@1", "variant": "fast", "reason": "assigned"},
	}}
	h.must(202, "POST", "/sidecar/v1/logs", logs, cred)
	h.mustCode(400, "INVALID_ARGUMENT", "POST", "/sidecar/v1/logs", map[string]any{"records": []any{map[string]any{"type": "bogus"}}}, cred)

	ops := app + "/environments/dev"
	r = h.must(200, "GET", ops+"/decision-logs?decision=lead_routing&fallback=true", nil)
	if !strings.Contains(string(r.Body), "req_2") || strings.Contains(string(r.Body), "req_1") {
		t.Fatalf("decision log filter: %s", r.Body)
	}
	r = h.must(200, "GET", ops+"/decision-logs?limit=1", nil)
	if !strings.Contains(string(r.Body), "nextCursor") {
		t.Fatalf("decision log paging: %s", r.Body)
	}
	h.must(200, "GET", ops+"/decision-logs?version=1&requestId=req_1&unitId=L-1&from="+now.Add(-time.Hour).Format(time.RFC3339)+"&to="+now.Add(time.Hour).Format(time.RFC3339), nil)
	r = h.must(200, "GET", ops+"/update-logs?result=kept_previous", nil)
	if !strings.Contains(string(r.Body), "signature_invalid") {
		t.Fatalf("update logs: %s", r.Body)
	}
	r = h.must(200, "GET", ops+"/exposure-logs?experiment=callback_timing&unitId=L-1", nil)
	if !strings.Contains(string(r.Body), `"variant":"fast"`) {
		t.Fatalf("exposure logs: %s", r.Body)
	}
	r = h.must(200, "GET", ops+"/sidecars", nil)
	var fleet struct {
		Summary model.FleetSummary `json:"summary"`
		Items   []model.Sidecar    `json:"items"`
	}
	r.JSON(t, &fleet)
	if fleet.Summary.Total != 2 || fleet.Summary.OnLatest != 1 || fleet.Items[0].ID != "pod-b" || fleet.Items[0].Status != "error" || fleet.Summary.Versions["0.9.0"] != 1 {
		t.Fatalf("fleet: %s", r.Body)
	}
	r = h.must(200, "GET", ops+"/overview", nil)
	var ov model.Overview
	r.JSON(t, &ov)
	if ov.Generation != 2 || ov.P99Micros != 90 || ov.CallsPerMinute != 600 || len(ov.LatencySeries) != 48 || ov.Decisions != 1 || len(ov.RecentReleases) != 2 {
		t.Fatalf("overview: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/decisions/lead_routing/nodes/ai_check/shadow-report?environment=dev&days=1", nil)
	var rep model.ShadowReport
	r.JSON(t, &rep)
	if rep.Calls != 3 || rep.Agreement != 0.5 || len(rep.Disagreements) != 1 || rep.TimeoutRate < 0.33 || rep.Matrix.Labels[0] != "fast_track" {
		t.Fatalf("shadow report: %s", r.Body)
	}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestRuntimeAPI(t *testing.T) {
	h := newHarness(t)
	res := seedDemo(t, h)
	cred := bearerTok(res.Credential)
	base := app + "/environments/dev"
	in := map[string]any{"lead": map[string]any{"id": "L-1", "city": "Gurgaon", "source": "app"}, "car": map[string]any{"year": 2019, "km": 1, "price": 525000}}
	r := h.must(200, "POST", base+"/decide", map[string]any{"decision": "lead_routing", "unitId": "L-1", "requestId": "req_x", "input": in, "options": map[string]any{"forceLog": true}}, cred)
	if !strings.Contains(string(r.Body), `"generation":1`) || !strings.Contains(string(r.Body), `"requestId":"req_x"`) {
		t.Fatalf("decide: %s", r.Body)
	}
	h.must(200, "POST", base+"/decide", map[string]any{"decision": "lead_routing", "input": in}) // user token works too
	h.mustCode(404, "NOT_FOUND", "POST", base+"/decide", map[string]any{"decision": "nope", "input": in}, cred)
	h.mustCode(400, "INVALID_ARGUMENT", "POST", base+"/decide", map[string]any{"input": in}, cred)
	h.mustCode(403, "FORBIDDEN", "POST", app+"/environments/prod/decide", map[string]any{"decision": "lead_routing", "input": in}, cred)
	h.mustCode(404, "NOT_FOUND", "POST", app+"/environments/prod/decide", map[string]any{"decision": "lead_routing", "input": in})
	r = h.must(200, "GET", base+"/decision-logs?requestId=req_x", nil)
	if !strings.Contains(string(r.Body), `"sidecarId":"control-plane"`) {
		t.Fatalf("remote decide not logged: %s", r.Body)
	}

	r = h.must(200, "POST", base+"/gates/new_inspection_flow/check", map[string]any{"unitId": "L-1", "attributes": map[string]any{"city": "Pune"}}, cred)
	if !strings.Contains(string(r.Body), `"value":false`) || !strings.Contains(string(r.Body), `"gateVersion":"new_inspection_flow@1"`) {
		t.Fatalf("gate: %s", r.Body)
	}
	h.must(201, "POST", base+"/releases", map[string]any{"type": "kill_switch", "gate": "new_inspection_flow"})
	r = h.must(200, "POST", base+"/gates/new_inspection_flow/check", map[string]any{"unitId": "L-1", "attributes": map[string]any{"city": "Delhi"}}, cred)
	if !strings.Contains(string(r.Body), `"ruleId":"killed"`) || !strings.Contains(string(r.Body), `"generation":2`) {
		t.Fatalf("killed gate: %s", r.Body)
	}
	h.mustCode(404, "NOT_FOUND", "POST", base+"/gates/nope_gate/check", map[string]any{}, cred)
	r = h.must(200, "POST", base+"/experiments/callback_timing/assign", map[string]any{"unitId": "L-1"}, cred)
	if !strings.Contains(string(r.Body), `"reason":"assigned"`) || !strings.Contains(string(r.Body), `"delayMinutes"`) {
		t.Fatalf("assign: %s", r.Body)
	}
	r = h.must(200, "GET", base+"/exposure-logs?experiment=callback_timing", nil)
	if !strings.Contains(string(r.Body), `"unitId":"L-1"`) {
		t.Fatalf("exposure not logged: %s", r.Body)
	}
}

func TestAppsMembersCredentials(t *testing.T) {
	h := newHarness(t)
	h.must(200, "GET", "/meta/version", nil)
	h.must(200, "GET", "/meta/expr", nil)
	r := h.must(200, "GET", "/session", nil)
	if !strings.Contains(string(r.Body), `"role":"admin"`) {
		t.Fatalf("session: %s", r.Body)
	}
	h.must(201, "POST", "/apps", map[string]any{"name": "pricing", "description": "x", "environments": []string{"dev", "prod"}})
	h.mustCode(409, "NAME_TAKEN", "POST", "/apps", map[string]any{"name": "pricing"})
	h.mustCode(422, "VALIDATION_FAILED", "POST", "/apps", map[string]any{"name": "Bad"})
	h.mustCode(422, "VALIDATION_FAILED", "POST", "/apps", map[string]any{"name": "dupe_envs", "environments": []string{"dev", "dev"}})
	h.must(200, "GET", "/apps/pricing", nil)
	h.mustCode(404, "NOT_FOUND", "GET", "/apps/nothing", nil)
	r = h.must(200, "PATCH", "/apps/pricing", map[string]any{"owners": []string{"team-x"}, "bundleLimitMB": 32})
	if !strings.Contains(string(r.Body), `"bundleLimitMB":32`) {
		t.Fatalf("update app: %s", r.Body)
	}
	h.must(200, "GET", "/apps", nil)
	r = h.must(200, "GET", "/apps/pricing/environments", nil)
	if !strings.Contains(string(r.Body), `"requiredApprovals":0`) || !strings.Contains(string(r.Body), `"authorCanApprove":false`) {
		t.Fatalf("environments: %s", r.Body)
	}
	r = h.must(200, "PATCH", "/apps/pricing/environments/prod", map[string]any{"requiredApprovals": 2, "logSampleRate": 0.5})
	if !strings.Contains(string(r.Body), `"requiredApprovals":2`) {
		t.Fatalf("policy: %s", r.Body)
	}
	h.mustCode(400, "INVALID_ARGUMENT", "PATCH", "/apps/pricing/environments/prod", map[string]any{"requiredApprovals": 9})
	h.mustCode(404, "NOT_FOUND", "PATCH", "/apps/pricing/environments/qa", map[string]any{})

	r = h.must(201, "POST", "/apps/pricing/environments/prod/credentials", map[string]any{"label": "k8s prod"})
	var cc model.CredentialCreated
	r.JSON(t, &cc)
	if !strings.HasPrefix(cc.Secret, "gbx_cred_prod_") {
		t.Fatalf("credential: %s", r.Body)
	}
	r = h.must(200, "GET", "/apps/pricing/environments/prod/credentials", nil)
	if strings.Contains(string(r.Body), cc.Secret) || !strings.Contains(string(r.Body), cc.Credential.ID) {
		t.Fatalf("credential list: %s", r.Body)
	}
	h.mustCode(404, "NOT_FOUND", "GET", "/sidecar/v1/head", nil, bearerTok(cc.Secret)) // nothing published
	h.must(200, "POST", "/apps/pricing/environments/prod/credentials/"+cc.Credential.ID+"/revoke", nil)
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/sidecar/v1/head", nil, bearerTok(cc.Secret))
	h.mustCode(404, "NOT_FOUND", "POST", "/apps/pricing/environments/prod/credentials/cred_nope/revoke", nil)

	// Members and PATs.
	r = h.must(201, "POST", "/members", map[string]any{"email": "Raj@Example.com", "name": "Raj", "role": "author", "apps": []string{"pricing"}})
	var m model.Member
	r.JSON(t, &m)
	h.mustCode(409, "NAME_TAKEN", "POST", "/members", map[string]any{"email": "raj@example.com", "role": "viewer"})
	h.must(200, "GET", "/members", nil)
	r = h.must(200, "PATCH", "/members/"+m.ID, map[string]any{"email": "raj@example.com", "role": "approver"})
	if !strings.Contains(string(r.Body), `"role":"approver"`) {
		t.Fatalf("member update: %s", r.Body)
	}
	h.mustCode(404, "NOT_FOUND", "PATCH", "/members/mem_nope", map[string]any{"email": "a@b.co", "role": "viewer"})
	r = h.must(201, "POST", "/tokens", map[string]any{"label": "ci", "expiresInDays": 7}, as("raj@example.com"))
	var tok struct{ Token string }
	r.JSON(t, &tok)
	r = h.must(200, "GET", "/session", nil, bearerTok(tok.Token), as(""))
	if !strings.Contains(string(r.Body), "raj@example.com") {
		t.Fatalf("PAT session: %s", r.Body)
	}
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/session", nil, bearerTok("gbx_pat_wrong"))
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/session", nil, bearerTok("something"))
	h.must(204, "DELETE", "/members/"+m.ID, nil)
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/session", nil, bearerTok(tok.Token))
	h.mustCode(404, "NOT_FOUND", "DELETE", "/members/"+m.ID, nil)

	// Sign-in endpoints in dev mode.
	req, _ := http.NewRequest("GET", h.ts.URL+"/api/v1/auth/login?returnTo=/apps", nil)
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/apps" {
		t.Fatalf("login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	h.must(204, "POST", "/auth/logout", nil)
	h.mustCode(404, "NOT_FOUND", "GET", "/nothing/here", nil)
}

// Cookie sessions (as created by the OIDC callback) need CSRF on writes.
func TestSessionCookieAndCSRF(t *testing.T) {
	h := newHarness(t, func(c *Config, d *Deps) { c.Auth = "oidc"; d.OIDC = &auth.OIDC{} })
	ctx := context.Background()
	m := model.Member{ID: "mem_1", Email: "ana@example.com", Role: model.RoleAdmin, Apps: []string{}}
	if err := h.st.CreateMember(ctx, m); err != nil {
		t.Fatal(err)
	}
	cookie := "cookie-value"
	if err := h.st.CreateSession(ctx, model.WebSession{ID: h.s.hasher.Hash(cookie), MemberID: m.ID, CSRF: "tok", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c := hdr("Cookie", sessionCookie+"="+cookie)
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/session", nil, as(""))
	r := h.must(200, "GET", "/session", nil, c)
	if !strings.Contains(string(r.Body), `"csrfToken":"tok"`) {
		t.Fatalf("session: %s", r.Body)
	}
	h.mustCode(403, "FORBIDDEN", "POST", "/apps", map[string]any{"name": "app_one"}, c)
	h.must(201, "POST", "/apps", map[string]any{"name": "app_one"}, c, hdr("X-CSRF-Token", "tok"))
	h.must(204, "POST", "/auth/logout", nil, c, hdr("X-CSRF-Token", "tok"))
	h.mustCode(401, "UNAUTHENTICATED", "GET", "/session", nil, c)
}

func TestCORSAndUI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/index.html", "<html>gearbox</html>")
	writeFile(t, dir+"/app.js", "console.log(1)")
	h := newHarness(t, func(c *Config, _ *Deps) { c.UIDir, c.UIOrigin = dir, "http://localhost:5173" })
	code, b := get(t, h.ts.URL+"/apps/lead_service/decisions")
	if code != 200 || !strings.Contains(string(b), "gearbox") {
		t.Fatalf("spa fallback: %d %s", code, b)
	}
	code, b = get(t, h.ts.URL+"/app.js")
	if code != 200 || !strings.Contains(string(b), "console") {
		t.Fatalf("asset: %d", code)
	}
	if code, _ = get(t, h.ts.URL+"/missing.js"); code != 404 {
		t.Fatalf("missing asset: %d", code)
	}
	req, _ := http.NewRequest("OPTIONS", h.ts.URL+"/api/v1/apps", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || res.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight: %d %v", res.StatusCode, res.Header)
	}
	if code, _ = get(t, h.ts.URL+"/healthz"); code != 200 {
		t.Fatal("healthz")
	}
	if code, _ = get(t, h.ts.URL+"/readyz"); code != 200 {
		t.Fatal("readyz")
	}
}
