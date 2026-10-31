// SPDX-License-Identifier: MIT

// Package state holds the active snapshot (read lock-free through an
// atomic.Pointer) and the sync status.
package state

import (
	"sync/atomic"
	"time"

	"github.com/ORG/gearbox/engine"
)

// Entry is one resource of the active bundle (for update-log diffs).
type Entry struct {
	Version int64
	SHA256  string
}

// Active is an installed, verified and tested bundle.
type Active struct {
	Snap       *engine.Snapshot
	App, Env   string
	Generation int64
	Envelope   []byte
	Blobs      map[string][]byte
	Entries    map[string]Entry // "kind/name" (live entries)
	SwappedAt  time.Time
	Source     string // network | cache | fallback | file | definitions
}

// State is shared by the gRPC server, the sync loop and the HTTP endpoints.
type State struct {
	cur       atomic.Pointer[Active]
	lastSync  atomic.Int64
	lastErr   atomic.Pointer[string]
	startedAt time.Time
}

// New returns an empty state.
func New() *State { return &State{startedAt: time.Now()} }

// Current returns the active bundle or nil.
func (s *State) Current() *Active { return s.cur.Load() }

// Swap installs a new bundle and returns the previous one.
func (s *State) Swap(a *Active) *Active { return s.cur.Swap(a) }

// Ready reports whether a verified bundle is active.
func (s *State) Ready() bool { return s.cur.Load() != nil }

// Generation of the active bundle (0 when none).
func (s *State) Generation() int64 {
	if a := s.cur.Load(); a != nil {
		return a.Generation
	}
	return 0
}

// SyncOK records a successful sync check.
func (s *State) SyncOK(t time.Time) {
	s.lastSync.Store(t.UnixNano())
	s.lastErr.Store(nil)
}

// SyncFailed records a failed sync check.
func (s *State) SyncFailed(err error) {
	msg := err.Error()
	s.lastErr.Store(&msg)
}

// LastSync is the time of the last successful sync check (zero if none).
func (s *State) LastSync() time.Time {
	n := s.lastSync.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// LastError is the last sync error ("" after a success).
func (s *State) LastError() string {
	if p := s.lastErr.Load(); p != nil {
		return *p
	}
	return ""
}

// SyncLagSeconds is the time since the last successful sync check (or since
// start when there was none).
func (s *State) SyncLagSeconds() float64 {
	t := s.LastSync()
	if t.IsZero() {
		t = s.startedAt
	}
	return time.Since(t).Seconds()
}
