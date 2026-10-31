// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
	"github.com/ORG/gearbox/sidecar/internal/app"
	"github.com/ORG/gearbox/sidecar/internal/config"
	"github.com/ORG/gearbox/sidecar/internal/logs"
)

// memSink collects log records.
type memSink struct {
	mu   sync.Mutex
	recs []any
}

func (m *memSink) Write(_ context.Context, r []any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, r...)
	return nil
}

func (m *memSink) updates() []*logs.UpdateRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*logs.UpdateRecord
	for _, r := range m.recs {
		if u, ok := r.(*logs.UpdateRecord); ok {
			out = append(out, u)
		}
	}
	return out
}

func (m *memSink) count(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.recs {
		switch r.(type) {
		case *logs.DecisionRecord:
			if kind == "decision" {
				n++
			}
		case *logs.ExposureRecord:
			if kind == "exposure" {
				n++
			}
		}
	}
	return n
}

// fakeCP is a minimal control plane: head, blobs, SSE watch, heartbeat.
type fakeCP struct {
	t          *testing.T
	mu         sync.Mutex
	head       map[string]any
	objects    map[string][]byte
	events     chan int64
	heartbeats atomic.Int32
	headCalls  atomic.Int32
	authFail   atomic.Int32
}

func newFakeCP(t *testing.T) (*fakeCP, *httptest.Server) {
	cp := &fakeCP{t: t, objects: map[string][]byte{}, events: make(chan int64, 8)}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer gbx_cred_test" {
			cp.authFail.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v1/sidecar/v1/head", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		cp.headCalls.Add(1)
		cp.mu.Lock()
		head := cp.head
		cp.mu.Unlock()
		if head == nil {
			http.NotFound(w, r)
			return
		}
		etag := fmt.Sprintf(`"g%v"`, head["generation"])
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_ = json.NewEncoder(w).Encode(head)
	})
	mux.HandleFunc("/api/v1/sidecar/v1/blobs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("sig") == "" {
			cp.authFail.Add(1) // blob URLs are self-authenticating
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := strings.TrimPrefix(r.URL.Path, "/api/v1/sidecar/v1/blobs/")
		cp.mu.Lock()
		d, ok := cp.objects[h]
		cp.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(d)
	})
	mux.HandleFunc("/api/v1/sidecar/v1/watch", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, ": hello\n\n")
		fl.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case g := <-cp.events:
				fmt.Fprintf(w, "event: generation\ndata: {\"generation\": %d}\n\n", g)
				fl.Flush()
			case <-time.After(200 * time.Millisecond):
				_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
				fl.Flush()
			}
		}
	})
	mux.HandleFunc("/api/v1/sidecar/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var hb map[string]any
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil || hb["id"] == nil || hb["engineVersion"] != engine.Version {
			http.Error(w, "bad heartbeat", http.StatusBadRequest)
			return
		}
		cp.heartbeats.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return cp, srv
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// publish makes a built bundle the head. headGen overrides the advertised
// generation (to simulate lies).
func (cp *fakeCP) publish(app, env string, b bundle.Built, headGen int64, notify bool) {
	cp.mu.Lock()
	es := sum(b.Envelope)
	cp.objects[es] = b.Envelope
	var resources []map[string]string
	for _, bl := range b.Blobs {
		cp.objects[bl.SHA256] = bl.Data
		resources = append(resources, map[string]string{"sha256": bl.SHA256, "url": "/sidecar/v1/blobs/" + bl.SHA256 + "?sig=x&exp=1"})
	}
	cp.head = map[string]any{"app": app, "environment": env, "generation": headGen, "envelope": map[string]string{"url": "/sidecar/v1/blobs/" + es + "?sig=x&exp=1", "sha256": es}, "resources": resources}
	cp.mu.Unlock()
	if notify {
		cp.events <- headGen
	}
}

func loadLeadSet(t *testing.T) engine.Set {
	b, err := os.ReadFile("../../../engine/testdata/lead_routing/set.json")
	if err != nil {
		t.Fatal(err)
	}
	var s engine.Set
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func withMembers(s engine.Set, gen int64, version int64, members ...string) engine.Set {
	s.Generation = gen
	res := make([]engine.Resource, len(s.Resources))
	copy(res, s.Resources)
	for i, r := range res {
		if r.Name == "blocked_cities" {
			r.Version = version
			r.SegmentMembers = append(append([]string(nil), r.SegmentMembers...), members...)
			res[i] = r
		}
	}
	s.Resources = res
	return s
}

func socketDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "gbx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func dial(t *testing.T, socket string) gearboxv1.EngineServiceClient {
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return gearboxv1.NewEngineServiceClient(conn)
}

const leadInput = `{"lead":{"id":"L-9","city":"%s","source":"app","phone":"+91-1"},"car":{"year":2019,"km":48200,"price":525000},"inspections":[{"part":"engine","score":8}]}`

func TestEndToEnd(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := bundle.Signer{KeyID: "test-2026", PrivateKey: priv}
	trusted := "test-2026:" + base64.StdEncoding.EncodeToString(pub)
	cp, srv := newFakeCP(t)
	set := loadLeadSet(t)
	set.Generation = 42
	b42, err := bundle.Build(set, bundle.Meta{ReleaseType: "change_request", ReleaseID: "rel_42"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	cp.publish("lead_service", "prod", b42, 42, false)

	dir := socketDir(t)
	cacheDir := t.TempDir()
	env := map[string]string{
		"GEARBOX_SERVER_URL": srv.URL, "GEARBOX_APP": "lead_service", "GEARBOX_ENV": "prod",
		"GEARBOX_CREDENTIAL": "gbx_cred_test", "GEARBOX_TRUSTED_KEYS": trusted,
		"GEARBOX_SOCKET": filepath.Join(dir, "engine.sock"), "GEARBOX_CACHE_DIR": cacheDir,
		"GEARBOX_POLL_INTERVAL": "1s", "GEARBOX_FETCH_JITTER": "0s", "GEARBOX_HEARTBEAT_INTERVAL": "100ms",
		"GEARBOX_LOG_SAMPLE": "1", "GEARBOX_SIDECAR_ID": "pod-1", "GEARBOX_HMAC_KEY": "k",
	}
	cfg, err := config.Load(nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	a, err := app.New(cfg, app.Options{LogSinks: []logs.Sink{sink}, MetricsAddr: "127.0.0.1:0", LogFlush: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case <-a.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("sidecar never became ready")
	}
	st, err := os.Stat(cfg.Socket)
	if err != nil || st.Mode().Perm() != 0o660 || st.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode = %v %v", st.Mode(), err)
	}
	c := dial(t, cfg.Socket)
	ctxRPC := context.Background()

	// Decide over the Unix socket.
	resp, err := c.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "lead_routing", UnitId: "L-1029386", RequestId: "req-1", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(fmt.Sprintf(leadInput, "Gurgaon"))}, Options: &gearboxv1.DecideOptions{Trace: true}})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.OutputJson) != `{"reason":"fast_lane","route":"fast_track"}` || resp.BundleGeneration != 42 || resp.DecisionVersion != "lead_routing@17" || resp.RequestId != "req-1" || len(resp.Path) != 3 || !strings.HasPrefix(resp.DependencyHash, "sha256:") {
		t.Fatalf("decide = %v", resp)
	}
	// Errors carry ErrorInfo reasons.
	_, err = c.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "nope", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(`{}`)}})
	if status.Code(err) != codes.NotFound || reasonOf(err) != "DECISION_NOT_FOUND" {
		t.Fatalf("not found = %v", err)
	}
	_, err = c.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "lead_routing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(`{"lead":{}}`)}})
	if status.Code(err) != codes.InvalidArgument || reasonOf(err) != "INPUT_SCHEMA" {
		t.Fatalf("schema = %v", err)
	}
	big := make([]byte, cfg.MaxInputBytes+1)
	_, err = c.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "lead_routing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: big}})
	if status.Code(err) != codes.InvalidArgument || reasonOf(err) != "INPUT_TOO_LARGE" {
		t.Fatalf("too large = %v", err)
	}
	// Batch with one failing item.
	br, err := c.DecideBatch(ctxRPC, &gearboxv1.DecideBatchRequest{Items: []*gearboxv1.DecideRequest{
		{Decision: "lead_routing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(fmt.Sprintf(leadInput, "Srinagar"))}},
		{Decision: "missing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(`{}`)}},
	}})
	if err != nil || len(br.Items) != 2 || br.Items[0].Response == nil || !strings.Contains(string(br.Items[0].Response.OutputJson), "city_not_served") || br.Items[1].Error.GetReason() != "DECISION_NOT_FOUND" || br.Items[1].Error.Code != int32(codes.NotFound) {
		t.Fatalf("batch = %v %v", br, err)
	}
	// Status and health.
	stat, err := c.GetStatus(ctxRPC, &gearboxv1.GetStatusRequest{})
	if err != nil || !stat.Ready || stat.BundleGeneration != 42 || stat.App != "lead_service" || stat.EngineVersion != engine.Version || len(stat.Decisions) != 1 || stat.Decisions[0].Version != "17" || stat.Decisions[0].Kind != "graph" {
		t.Fatalf("status = %v %v", stat, err)
	}
	conn, _ := grpc.NewClient("unix://"+cfg.Socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	hr, err := healthpb.NewHealthClient(conn).Check(ctxRPC, &healthpb.HealthCheckRequest{})
	conn.Close()
	if err != nil || hr.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v %v", hr, err)
	}

	// Publish generation 43 through the change feed: swap.
	b43, _ := bundle.Build(withMembers(set, 43, 4, "Agra"), bundle.Meta{ReleaseType: "data"}, signer)
	cp.publish("lead_service", "prod", b43, 43, true)
	waitFor(t, "generation 43", 5*time.Second, func() bool { return a.State.Generation() == 43 })
	resp, err = c.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "lead_routing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(fmt.Sprintf(leadInput, "Agra"))}})
	if err != nil || !strings.Contains(string(resp.OutputJson), "city_not_served") || resp.BundleGeneration != 43 {
		t.Fatalf("after swap = %v %v", resp, err)
	}
	waitFor(t, "swap update record", 2*time.Second, func() bool {
		for _, u := range sink.updates() {
			if u.Result == "swapped" && u.FromGeneration == 42 && u.ToGeneration == 43 {
				if len(u.Changed) != 1 || u.Changed[0].Name != "blocked_cities" || u.Changed[0].From != 3 || u.Changed[0].To != 4 || u.Signature != "valid" || u.Tests == nil || u.Tests.Passed != 6 {
					t.Fatalf("update record = %+v changed=%+v", u, u.Changed)
				}
				return true
			}
		}
		return false
	})

	// Tampered envelope (generation 44, signature over the old manifest).
	b44, _ := bundle.Build(withMembers(set, 44, 5, "Agra", "Kota"), bundle.Meta{}, signer)
	sm := &gearboxv1.SignedManifest{}
	_ = proto.Unmarshal(b44.Envelope, sm)
	m := &gearboxv1.Manifest{}
	_ = proto.Unmarshal(sm.Manifest, m)
	m.ReleaseId = "evil"
	sm.Manifest, _ = proto.Marshal(m)
	b44.Envelope, _ = proto.Marshal(sm)
	cp.publish("lead_service", "prod", b44, 44, true)
	waitFor(t, "tampered rejection", 5*time.Second, func() bool {
		for _, u := range sink.updates() {
			if u.ToGeneration == 44 && u.Result == "kept_previous" && u.Signature == "invalid" {
				return true
			}
		}
		return false
	})
	if a.State.Generation() != 43 {
		t.Fatalf("generation after tampered bundle = %d", a.State.Generation())
	}

	// Older generation behind a newer head: rejected.
	b41, _ := bundle.Build(withMembers(set, 41, 9, "Kota"), bundle.Meta{}, signer)
	cp.publish("lead_service", "prod", b41, 45, true)
	waitFor(t, "older generation rejection", 5*time.Second, func() bool {
		for _, u := range sink.updates() {
			if u.ToGeneration == 41 && u.Result == "kept_previous" && strings.Contains(u.Error, "not newer") {
				return true
			}
		}
		return false
	})

	// Failing tests keep the previous bundle (Pune blocked breaks "too old").
	b46, _ := bundle.Build(withMembers(set, 46, 6, "Agra", "Pune"), bundle.Meta{}, signer)
	cp.publish("lead_service", "prod", b46, 46, true)
	waitFor(t, "failing tests rejection", 5*time.Second, func() bool {
		for _, u := range sink.updates() {
			if u.ToGeneration == 46 && u.Result == "kept_previous" && u.Tests != nil && u.Tests.Failed > 0 {
				return true
			}
		}
		return false
	})
	if a.State.Generation() != 43 {
		t.Fatalf("generation after failing tests = %d", a.State.Generation())
	}

	// Logs, heartbeats, metrics.
	waitFor(t, "decision logs", 2*time.Second, func() bool { return sink.count("decision") >= 3 })
	waitFor(t, "heartbeats", 3*time.Second, func() bool { return cp.heartbeats.Load() >= 2 })
	mresp, err := http.Get("http://" + a.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	for _, want := range []string{"gearbox_bundle_generation 43", `gearbox_decisions_total{decision="lead_routing",version="lead_routing@17",status="OK",fallback="false"}`, "gearbox_sync_lag_seconds", "gearbox_decision_breaker_open_total", `gearbox_sync_failures_total{reason="tests"} 1`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if r, _ := http.Get("http://" + a.MetricsAddr() + "/readyz"); r == nil || r.StatusCode != 200 {
		t.Fatal("readyz")
	}
	if cp.authFail.Load() != 0 {
		t.Fatalf("%d unauthenticated requests", cp.authFail.Load())
	}
	// PII never logged in clear.
	sink.mu.Lock()
	for _, r := range sink.recs {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "+91-1") {
			t.Fatalf("PII in log record: %s", b)
		}
	}
	sink.mu.Unlock()

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.Socket); !os.IsNotExist(err) {
		t.Fatal("socket not removed on shutdown")
	}

	// Restart with the control plane gone: serves the last-known-good bundle.
	srv.Close()
	env["GEARBOX_SERVER_URL"] = "http://127.0.0.1:1"
	cfg2, _ := config.Load(nil, func(k string) string { return env[k] })
	sink2 := &memSink{}
	a2, err := app.New(cfg2, app.Options{LogSinks: []logs.Sink{sink2}, MetricsAddr: "-", LogFlush: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- a2.Run(ctx2) }()
	select {
	case <-a2.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("restart did not load the cache")
	}
	if a2.State.Generation() != 43 || a2.State.Current().Source != "cache" {
		t.Fatalf("restart = %d %s", a2.State.Generation(), a2.State.Current().Source)
	}
	c2 := dial(t, cfg2.Socket)
	if r, err := c2.Decide(ctxRPC, &gearboxv1.DecideRequest{Decision: "lead_routing", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(fmt.Sprintf(leadInput, "Agra"))}}); err != nil || r.BundleGeneration != 43 {
		t.Fatalf("decide after restart = %v %v", r, err)
	}
	cancel2()
	<-done2
	found := false
	for _, u := range sink2.updates() {
		if u.Result == "loaded_from_disk" && u.ToGeneration == 43 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no loaded_from_disk record: %+v", sink2.updates())
	}
}

func reasonOf(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(interface{ GetReason() string }); ok {
			return info.GetReason()
		}
	}
	return ""
}
