// SPDX-License-Identifier: MIT

// Package sidecartest starts an in-process gearbox sidecar serving a Set on
// a temporary Unix socket (and optionally loopback TCP). For tests of client
// libraries and applications.
package sidecartest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
	"github.com/ORG/gearbox/sidecar/internal/app"
	"github.com/ORG/gearbox/sidecar/internal/config"
	"github.com/ORG/gearbox/sidecar/internal/logs"
)

// Sidecar is a running test sidecar.
type Sidecar struct {
	Socket  string // path of the Unix socket
	Target  string // "unix://" + Socket
	TCPAddr string // set when Options.TCPToken is set
}

// Options for Start.
type Options struct {
	TCPAddr  string // e.g. "127.0.0.1:17443"; needs TCPToken
	TCPToken string
}

type discard struct{}

func (discard) Write(context.Context, []any) error { return nil }

// Start compiles set into an unsigned bundle file and serves it until the
// test ends.
func Start(t testing.TB, set engine.Set, opts ...Options) *Sidecar {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if set.Generation == 0 {
		set.Generation = 1
	}
	if set.App == "" {
		set.App, set.Environment = "test", "test"
	}
	built, err := bundle.Build(set, bundle.Meta{ReleaseType: "change_request"}, bundle.Signer{})
	if err != nil {
		t.Fatalf("sidecartest: build: %v", err)
	}
	data, err := bundle.WriteFile(built)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "gbxst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	file := filepath.Join(dir, "set.bundle")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "engine.sock")
	env := map[string]string{"GEARBOX_SOCKET": sock, "GEARBOX_LOG_SINK": "none"}
	if o.TCPAddr != "" {
		tok := filepath.Join(dir, "token")
		_ = os.WriteFile(tok, []byte(o.TCPToken), 0o600)
		env["GEARBOX_TCP_ADDR"], env["GEARBOX_TCP_TOKEN_FILE"] = o.TCPAddr, tok
	}
	cfg, err := config.Load([]string{"--bundle-file", file, "--allow-unsigned"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg, app.Options{LogSinks: []logs.Sink{discard{}}, MetricsAddr: "-"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-a.Ready():
	case <-time.After(10 * time.Second):
		t.Fatalf("sidecartest: not ready: %s", a.State.LastError())
	case err := <-done:
		t.Fatalf("sidecartest: stopped: %v", err)
	}
	return &Sidecar{Socket: sock, Target: fmt.Sprintf("unix://%s", sock), TCPAddr: o.TCPAddr}
}
