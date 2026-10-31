// SPDX-License-Identifier: MIT

// Package syncer keeps the sidecar's active bundle current: it installs
// bundles (verify, decode, compile, run tests, swap), keeps a last-known-good
// copy on disk, follows the control plane's change feed and head pointer, and
// sends heartbeats.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
	"github.com/ORG/gearbox/sidecar/internal/logs"
	"github.com/ORG/gearbox/sidecar/internal/obs"
	"github.com/ORG/gearbox/sidecar/internal/state"
)

// Loader installs bundles into the state. It serialises installs; the
// request path never waits on it (it only reads the atomic pointer).
type Loader struct {
	mu              sync.Mutex
	State           *state.State
	Keys            bundle.TrustedKeys
	PIIKey          []byte
	MaxDecompressed int64
	Logger          *logs.Logger
	Metrics         *obs.Metrics
	SidecarID       string
	CacheDir        string // empty: no last-known-good cache
	App, Env        string // expected app/env (empty: accept any, offline)
	OnSwap          func(*state.Active)
}

// Install describes one install attempt.
type Install struct {
	Envelope      []byte
	Blobs         map[string][]byte
	Source        string // network | cache | fallback | file
	AllowUnsigned bool
	DownloadMs    int64
	Started       time.Time
	WriteCache    bool
	AllowSameGen  bool // offline reloads
}

// InstallError is a rejected bundle.
type InstallError struct {
	Reason string // signature | generation | open | compile | tests | app
	Err    error
}

func (e *InstallError) Error() string { return e.Reason + ": " + e.Err.Error() }
func (e *InstallError) Unwrap() error { return e.Err }

func signatureState(env []byte, err error) string {
	switch {
	case errors.Is(err, bundle.ErrSignature), errors.Is(err, bundle.ErrUnknownKey), errors.Is(err, bundle.ErrUnsigned):
		return "invalid"
	case bundle.SignatureKeyID(env) == "":
		return "missing"
	}
	return "valid"
}

// Install verifies, compiles and tests a bundle and swaps it in. It writes
// one update record either way.
func (l *Loader) Install(ctx context.Context, in Install) (*state.Active, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if in.Started.IsZero() {
		in.Started = time.Now()
	}
	prev := l.State.Current()
	var fromGen int64
	if prev != nil {
		fromGen = prev.Generation
	}
	rec := &logs.UpdateRecord{TS: time.Now().UTC(), SidecarID: l.SidecarID, FromGeneration: fromGen, DownloadMs: in.DownloadMs}
	fail := func(reason string, err error) (*state.Active, error) {
		rec.Result = "kept_previous"
		rec.Error = err.Error()
		if rec.Signature == "" {
			rec.Signature = signatureState(in.Envelope, err)
		}
		l.Metrics.SyncFailures.Inc(reason)
		l.Metrics.Updates.Inc(rec.Result)
		l.Logger.Update(rec)
		slog.Warn("bundle rejected; keeping the previous one", "source", in.Source, "reason", reason, "generation", rec.ToGeneration, "error", err)
		return nil, &InstallError{Reason: reason, Err: err}
	}
	var (
		gen int64
		err error
	)
	if in.AllowUnsigned {
		gen, _, err = bundle.VerifyEnvelopeUnsigned(in.Envelope, l.Keys)
	} else {
		gen, _, err = bundle.VerifyEnvelope(in.Envelope, l.Keys)
	}
	rec.ToGeneration = gen
	if err != nil {
		reason := "signature"
		if errors.Is(err, bundle.ErrEngineTooOld) {
			reason = "engine_version"
		} else if errors.Is(err, bundle.ErrFormat) {
			reason = "format"
		}
		return fail(reason, err)
	}
	rec.Signature = signatureState(in.Envelope, nil)
	if prev != nil && (gen < prev.Generation || gen == prev.Generation && !in.AllowSameGen) {
		return fail("generation", fmt.Errorf("generation %d is not newer than the active generation %d", gen, prev.Generation))
	}
	opened, err := bundle.Open(in.Envelope, in.Blobs, l.Keys, in.AllowUnsigned, l.MaxDecompressed)
	if err != nil {
		return fail("open", err)
	}
	if l.App != "" && (opened.App != l.App || opened.Environment != l.Env) {
		return fail("app", fmt.Errorf("bundle is for %s/%s, this sidecar serves %s/%s", opened.App, opened.Environment, l.App, l.Env))
	}
	compileStart := time.Now()
	snap, err := engine.CompileWithOptions(opened.Set, engine.CompileOptions{PIIKey: l.PIIKey})
	if err != nil {
		return fail("compile", err)
	}
	results, err := snap.RunTests(ctx, "", 0)
	rec.CompileMs = time.Since(compileStart).Milliseconds()
	if err != nil {
		return fail("tests", err)
	}
	tests := &logs.Tests{}
	var firstFail string
	for _, r := range results {
		if r.OK {
			tests.Passed++
		} else {
			tests.Failed++
			if firstFail == "" {
				firstFail = r.Name + ": " + r.Error
			}
		}
	}
	rec.Tests = tests
	if tests.Failed > 0 {
		return fail("tests", fmt.Errorf("%d test case(s) failed, first: %s", tests.Failed, firstFail))
	}
	info, _ := bundle.Inspect(in.Envelope)
	act := &state.Active{
		Snap: snap, App: opened.App, Env: opened.Environment, Generation: gen,
		Envelope: in.Envelope, Blobs: in.Blobs, Entries: map[string]state.Entry{},
		SwappedAt: time.Now(), Source: in.Source,
	}
	for _, e := range info.Resources {
		if e.Live {
			act.Entries[e.Kind+"/"+e.Name] = state.Entry{Version: e.Version, SHA256: e.SHA256}
		}
	}
	rec.Changed = diff(prev, act)
	l.State.Swap(act)
	l.Metrics.Generation.Set(float64(gen))
	l.Metrics.SwapDuration.Observe(time.Since(in.Started).Seconds())
	rec.Result = "swapped"
	if in.Source == "cache" || in.Source == "fallback" {
		rec.Result = "loaded_from_disk"
	}
	l.Metrics.Updates.Inc(rec.Result)
	l.Logger.Update(rec)
	slog.Info("bundle active", "generation", gen, "source", in.Source, "decisions", len(snap.Decisions()), "tests_passed", tests.Passed)
	if in.WriteCache && l.CacheDir != "" {
		if err := l.writeCache(act); err != nil {
			slog.Warn("could not write last-known-good bundle", "error", err)
		}
	}
	if l.OnSwap != nil {
		l.OnSwap(act)
	}
	return act, nil
}

func diff(prev, next *state.Active) []logs.Change {
	var out []logs.Change
	keys := map[string]bool{}
	for k := range next.Entries {
		keys[k] = true
	}
	if prev != nil {
		for k := range prev.Entries {
			keys[k] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		kind, name := splitKey(k)
		n, inNext := next.Entries[k]
		var p state.Entry
		inPrev := false
		if prev != nil {
			p, inPrev = prev.Entries[k]
		}
		switch {
		case inNext && !inPrev:
			out = append(out, logs.Change{Kind: kind, Name: name, To: n.Version, SHA256: n.SHA256})
		case !inNext && inPrev:
			out = append(out, logs.Change{Kind: kind, Name: name, From: p.Version})
		case n.SHA256 != p.SHA256 || n.Version != p.Version:
			out = append(out, logs.Change{Kind: kind, Name: name, From: p.Version, To: n.Version, SHA256: n.SHA256})
		}
	}
	return out
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// CachePath is where the last-known-good bundle is kept.
func (l *Loader) CachePath() string {
	name := "last-known-good.bundle"
	if l.App != "" {
		name = l.App + "-" + l.Env + ".bundle"
	}
	return filepath.Join(l.CacheDir, name)
}

func (l *Loader) writeCache(act *state.Active) error {
	built := bundle.Built{Envelope: act.Envelope}
	for h, d := range act.Blobs {
		built.Blobs = append(built.Blobs, bundle.Blob{SHA256: h, Data: d})
	}
	data, err := bundle.WriteFile(built)
	if err != nil {
		return err
	}
	return WriteAtomic(l.CachePath(), data)
}

// LoadCache installs the last-known-good bundle (re-verified).
func (l *Loader) LoadCache(ctx context.Context) error {
	if l.CacheDir == "" {
		return os.ErrNotExist
	}
	return l.LoadFile(ctx, l.CachePath(), "cache", false)
}

// LoadFile installs a bundle file.
func (l *Loader) LoadFile(ctx context.Context, path, source string, allowUnsigned bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	env, blobs, err := bundle.ReadFile(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	_, err = l.Install(ctx, Install{Envelope: env, Blobs: blobs, Source: source, AllowUnsigned: allowUnsigned})
	return err
}

// WriteAtomic writes data to path via a temp file, fsync and rename.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
