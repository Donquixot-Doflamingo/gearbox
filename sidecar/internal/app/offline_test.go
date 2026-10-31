// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
	"github.com/ORG/gearbox/sidecar/internal/app"
	"github.com/ORG/gearbox/sidecar/internal/config"
	"github.com/ORG/gearbox/sidecar/internal/logs"
)

const defsYAML = `
app: demo
environment: dev
---
apiVersion: gearbox/v1
kind: Schema
metadata: { name: price_in }
spec:
  jsonSchema: { type: object, required: [price], properties: { price: { type: number } } }
---
kind: schemas
name: band_out
version: 1
spec:
  jsonSchema: { type: object, properties: { band: { type: string } } }
---
apiVersion: gearbox/v1
kind: Decision
metadata: { name: band }
spec:
  entry: r
  nodes:
    - id: r
      type: rules
      inputSchema: { name: price_in, version: 1 }
      outputSchema: { name: band_out, version: 1 }
      rules:
        - { id: high, when: { field: price, op: GT, value: 100 }, then: { band: "%s" } }
      default: { band: low }
  tests:
    - { name: high, input: { price: 500 }, expect: { band: "%s" } }
`

func writeDefs(t *testing.T, dir, band string) {
	t.Helper()
	y := []byte(sprintf(defsYAML, band, band))
	if err := os.WriteFile(filepath.Join(dir, "defs.yaml"), y, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefinitionsDirWatch(t *testing.T) {
	defDir := t.TempDir()
	writeDefs(t, defDir, "high")
	sock := filepath.Join(socketDir(t), "e.sock")
	cfg, err := config.Load([]string{"--definitions-dir", defDir, "--watch", "--allow-unsigned"}, func(k string) string {
		return map[string]string{"GEARBOX_SOCKET": sock}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg, app.Options{LogSinks: []logs.Sink{&memSink{}}, MetricsAddr: "-"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-a.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	c := dial(t, sock)
	decideBand := func() string {
		r, err := c.Decide(context.Background(), &gearboxv1.DecideRequest{Decision: "band", Input: &gearboxv1.DecideRequest_InputJson{InputJson: []byte(`{"price":500}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return string(r.OutputJson)
	}
	if got := decideBand(); got != `{"band":"high"}` {
		t.Fatalf("first = %s", got)
	}
	st, _ := c.GetStatus(context.Background(), &gearboxv1.GetStatusRequest{})
	if st.App != "demo" || st.Environment != "dev" {
		t.Fatalf("status = %v", st)
	}
	time.Sleep(10 * time.Millisecond)
	writeDefs(t, defDir, "premium")
	waitFor(t, "recompile", 5*time.Second, func() bool { return a.State.Generation() == 2 })
	if got := decideBand(); got != `{"band":"premium"}` {
		t.Fatalf("after change = %s", got)
	}
}

func sprintf(format string, args ...any) string { return fmtSprintf(format, args...) }

var fmtSprintf = fmt.Sprintf
