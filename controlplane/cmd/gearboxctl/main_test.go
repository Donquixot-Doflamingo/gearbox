package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ORG/gearbox/controlplane/internal/client"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/seed"
	"github.com/ORG/gearbox/controlplane/internal/server"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine/bundle"
)

func startServer(t *testing.T) string {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	mem, _ := store.NewMemory("")
	_, key, _ := ed25519.GenerateKey(nil)
	cfg := server.Config{Auth: "dev", PublicURL: "http://" + ts.Listener.Addr().String(), SigningKeyID: "test", ObjectStore: "file://" + t.TempDir(), DataDir: t.TempDir()}
	s, err := server.New(context.Background(), cfg, server.Deps{Store: store.New(mem), SigningKey: key, Secret: []byte("s"), NoBackground: true, Engine: engineapi.Lite{}, Bundler: engineapi.LiteBundler{}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.Close(); _ = s.Close() })
	if _, err := seed.Demo(context.Background(), &client.Client{BaseURL: ts.URL}, seed.Options{}); err != nil {
		t.Fatal(err)
	}
	return ts.URL
}

func ctl(t *testing.T, args ...string) string {
	t.Helper()
	o, err := parseArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := run(context.Background(), o, &buf); err != nil {
		t.Fatalf("gearboxctl %s: %v\n%s", strings.Join(args, " "), err, buf.String())
	}
	return buf.String()
}

func ctlErr(t *testing.T, args ...string) error {
	t.Helper()
	o, _ := parseArgs(args)
	return run(context.Background(), o, &bytes.Buffer{})
}

func TestCLI(t *testing.T) {
	url := startServer(t)
	t.Setenv("GEARBOX_CONFIG_DIR", t.TempDir())
	t.Setenv("GEARBOX_SERVER_URL", url)
	t.Setenv("GEARBOX_APP", "lead_service")
	t.Setenv("GEARBOX_TOKEN", "")

	if out := ctl(t, "login"); !strings.Contains(out, "logged in") {
		t.Fatal(out)
	}
	if out := ctl(t, "whoami"); !strings.Contains(out, "dev@localhost") {
		t.Fatal(out)
	}
	if out := ctl(t, "apps", "ls"); !strings.Contains(out, "lead_service") {
		t.Fatal(out)
	}

	dir := t.TempDir()
	if out := ctl(t, "pull", dir); !strings.Contains(out, "13 files") {
		t.Fatal(out)
	}
	if out := ctl(t, "validate", dir); !strings.Contains(out, "lead_routing") {
		t.Fatal(out)
	}
	if out := ctl(t, "test", dir); !strings.Contains(out, "PASS  lead_routing / fast track") {
		t.Fatal(out)
	}
	if out := ctl(t, "test", "--remote", "lead_routing"); !strings.Contains(out, "PASS") {
		t.Fatal(out)
	}

	// Edit a pulled file and push it back with a version cut.
	p := filepath.Join(dir, "segments", "blocked_cities.yaml")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "Srinagar") {
		t.Fatalf("pulled members missing: %s", b)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "- Srinagar", "- Srinagar\n    - Shimla", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := ctl(t, "push", dir, "--cut", "-m", "add Shimla")
	if !strings.Contains(out, "blocked_cities") || !strings.Contains(out, "cut v2") {
		t.Fatalf("push: %s", out)
	}
	if out := ctl(t, "version", "cut", "segments/blocked_cities"); !strings.Contains(out, "nothing to cut") {
		t.Fatal(out)
	}
	ctl(t, "schema", "pull", "lead_input", "-o", filepath.Join(dir, "x.json"))
	if out := ctl(t, "schema", "push", filepath.Join(dir, "x.json")); !strings.Contains(out, "unchanged") {
		t.Fatal(out)
	}
	if out := ctl(t, "decision", "eval", "lead_routing", "-i", writeTmp(t, `{"lead":{"id":"L-1","city":"Pune"},"car":{"year":2020,"km":1,"price":1}}`)); !strings.Contains(out, "decisionVersion") {
		t.Fatal(out)
	}
	csv := writeTmpNamed(t, "members.csv", "key\nLeh\nSrinagar\n")
	if out := ctl(t, "segment", "members", "upload", "blocked_cities", "-f", csv); !strings.Contains(out, "2 members") {
		t.Fatal(out)
	}

	// Data release in dev, then change request + rollout + kill in stage.
	if out := ctl(t, "release", "data", "segments/blocked_cities@2", "--env", "dev"); !strings.Contains(out, "generation 2") {
		t.Fatal(out)
	}
	items := []string{"change", "open", "--env", "stage", "--title", "stage it"}
	items = append(items, "schemas/lead_input@1", "schemas/eligibility_input@1", "schemas/eligibility_output@1", "schemas/routing_output@1",
		"schemas/segment_attrs_city_price@1", "schemas/city_zone_value@1", "segments/blocked_cities@2", "segments/metro_premium@1",
		"segments/beta_10pct@1", "lookups/city_zone@1", "gates/new_inspection_flow@1", "experiments/callback_timing@1", "decisions/lead_routing@1")
	out = ctl(t, items...)
	if !strings.Contains(out, "cr_2 opened for stage: open") {
		t.Fatalf("change open: %s", out)
	}
	if err := ctlErr(t, "release", "change-request", "cr_2", "--env", "stage"); !client.IsCode(err, "NOT_APPROVED") {
		t.Fatalf("unapproved release: %v", err)
	}
	if out := ctl(t, "cr", "approve", "cr_2"); !strings.Contains(out, "approved") {
		t.Fatal(out)
	}
	if out := ctl(t, "cr", "ls", "--env", "stage"); !strings.Contains(out, "cr_2") {
		t.Fatal(out)
	}
	if out := ctl(t, "release", "publish", "--cr", "cr_2", "--env", "stage"); !strings.Contains(out, "generation 1") {
		t.Fatal(out)
	}
	if out := ctl(t, "release", "kill", "new_inspection_flow", "--env", "stage", "--reason", "test"); !strings.Contains(out, "generation 2") {
		t.Fatal(out)
	}
	if out := ctl(t, "release", "rollback", "1", "--env", "stage", "--reason", "undo"); !strings.Contains(out, "break-glass") {
		t.Fatal(out)
	}
	if out := ctl(t, "decide", "lead_routing", "--env", "stage", "-i", writeTmp(t, "lead: {id: L-1, city: Pune}\ncar: {year: 2020, km: 1, price: 1}\n")); !strings.Contains(out, `"generation": 3`) {
		t.Fatal(out)
	}

	// Offline bundle export (borrows a temporary credential).
	bf := filepath.Join(t.TempDir(), "lead_service-stage.bundle")
	if out := ctl(t, "bundle", "export", "--env", "stage", "-o", bf); !strings.Contains(out, "generation 3") {
		t.Fatal(out)
	}
	data, _ := os.ReadFile(bf)
	env, blobs, err := bundle.ReadFile(data)
	if errors.Is(err, bundle.ErrNotImplemented) {
		env, blobs, err = engineapi.ReadLiteFile(data)
	}
	if err != nil || len(env) == 0 || len(blobs) != 13 {
		t.Fatalf("bundle file: %v %d", err, len(blobs))
	}

	kd := t.TempDir()
	out = ctl(t, "keys", "generate", "--out", kd, "--id", "k1")
	if !strings.HasPrefix(out, "trusted key: k1:") {
		t.Fatal(out)
	}
	if err := ctlErr(t, "keys", "generate", "--out", kd); err == nil {
		t.Fatal("overwrote an existing key")
	}
	if out2 := ctl(t, "keys", "generate", "--out", kd, "--if-missing"); out2 != out {
		t.Fatalf("--if-missing changed the key: %s vs %s", out2, out)
	}
	if err := ctlErr(t, "nope"); err != errUsage {
		t.Fatalf("unknown command: %v", err)
	}
}

func writeTmp(t *testing.T, s string) string { return writeTmpNamed(t, "in.json", s) }

func writeTmpNamed(t *testing.T, name, s string) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	os.Exit(m.Run())
}
