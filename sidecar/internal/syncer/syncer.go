// SPDX-License-Identifier: MIT

package syncer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
	"github.com/ORG/gearbox/sidecar/internal/logs"
	"github.com/ORG/gearbox/sidecar/internal/state"
)

// Head is BundleHead from the control-plane API.
type Head struct {
	App         string `json:"app"`
	Environment string `json:"environment"`
	Generation  int64  `json:"generation"`
	Envelope    struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"envelope"`
	Resources []struct {
		SHA256 string `json:"sha256"`
		URL    string `json:"url"`
	} `json:"resources"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Syncer follows the control plane.
type Syncer struct {
	Loader       *Loader
	BaseURL      string // control-plane API base, e.g. https://cp/api/v1
	Credential   string
	HTTP         *http.Client
	PollInterval time.Duration
	Feed         bool
	FetchJitter  time.Duration
	MaxBundle    int64

	etag     string
	rejected string // generation+envelope hash of the last rejected bundle
	rejErr   error
	trigger  chan int64
	lastFail string
	once     sync.Once
}

// APIBase turns GEARBOX_SERVER_URL into the API base (appends /api/v1 unless
// the URL already ends with it).
func APIBase(serverURL string) string {
	s := strings.TrimRight(serverURL, "/")
	if strings.HasSuffix(s, "/api/v1") {
		return s
	}
	return s + "/api/v1"
}

func (s *Syncer) init() {
	s.once.Do(func() {
		s.trigger = make(chan int64, 1)
		if s.HTTP == nil {
			s.HTTP = &http.Client{Timeout: 60 * time.Second}
		}
	})
}

// Backoff returns the delay for the n-th consecutive failure: 1s doubling to
// 60s, with ±20% jitter.
func Backoff(n int) time.Duration {
	d := time.Second
	for i := 1; i < n && d < time.Minute; i++ {
		d *= 2
	}
	if d > time.Minute {
		d = time.Minute
	}
	j := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(d) * j)
}

// Run checks the head now, then on every poll interval, after every change
// feed event, and with backoff after failures, until ctx is done.
func (s *Syncer) Run(ctx context.Context) {
	s.init()
	if s.Feed {
		go s.watch(ctx)
	}
	failures := 0
	for {
		err := s.Check(ctx)
		wait := s.PollInterval
		if err != nil && ctx.Err() == nil {
			failures++
			if b := Backoff(failures); b < wait {
				wait = b
			}
		} else {
			failures = 0
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-s.trigger:
			t.Stop()
		}
	}
}

// Check fetches the head and installs a newer generation if there is one.
func (s *Syncer) Check(ctx context.Context) error {
	s.init()
	err := s.check(ctx)
	st := s.Loader.State
	if err == nil {
		st.SyncOK(time.Now())
		s.lastFail = ""
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	st.SyncFailed(err)
	var ie *InstallError
	if !errors.As(err, &ie) {
		// Network and protocol errors: one update record per distinct error.
		reason := "network"
		var he *httpStatusError
		if errors.As(err, &he) {
			reason = fmt.Sprintf("http_%d", he.status)
		}
		s.Loader.Metrics.SyncFailures.Inc(reason)
		if err.Error() != s.lastFail {
			s.Loader.Logger.Update(&logs.UpdateRecord{TS: time.Now().UTC(), SidecarID: s.Loader.SidecarID, FromGeneration: st.Generation(), ToGeneration: st.Generation(), Result: "kept_previous", Error: err.Error()})
			slog.Warn("sync failed", "error", err)
		}
	}
	s.lastFail = err.Error()
	return err
}

type httpStatusError struct {
	status int
	url    string
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.url, e.status) }

func (s *Syncer) get(ctx context.Context, rawURL string, header http.Header, limit int64) (*http.Response, []byte, error) {
	u, err := s.resolve(rawURL)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if s.sameHost(u) {
		req.Header.Set("Authorization", "Bearer "+s.Credential)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp, nil, err
	}
	if int64(len(body)) > limit {
		return resp, nil, fmt.Errorf("%w: %s is larger than %d bytes", bundle.ErrTooLarge, redact(u), limit)
	}
	return resp, body, nil
}

// resolve makes relative URLs (filesystem object store) absolute.
func (s *Syncer) resolve(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return raw, nil
	}
	base, err := url.Parse(s.BaseURL + "/")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(raw, "/") {
		// Paths like /sidecar/v1/blobs/… are relative to the API base.
		if !strings.HasPrefix(raw, base.Path) {
			raw = strings.TrimRight(base.Path, "/") + raw
		}
		return base.ResolveReference(&url.URL{Path: raw, RawQuery: u.RawQuery}).String(), nil
	}
	return base.ResolveReference(u).String(), nil
}

func (s *Syncer) sameHost(u string) bool {
	a, err1 := url.Parse(u)
	b, err2 := url.Parse(s.BaseURL)
	return err1 == nil && err2 == nil && a.Host == b.Host
}

func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Syncer) check(ctx context.Context) error {
	cur := s.Loader.State.Current()
	headURL := s.BaseURL + "/sidecar/v1/head"
	if cur != nil && len(cur.Blobs) > 0 && len(cur.Blobs) <= 500 {
		have := make([]string, 0, len(cur.Blobs))
		for h := range cur.Blobs {
			have = append(have, h)
		}
		headURL += "?have=" + strings.Join(have, ",")
	}
	hdr := http.Header{"Accept": {"application/json"}}
	if s.etag != "" && cur != nil {
		hdr.Set("If-None-Match", s.etag)
	}
	resp, body, err := s.get(ctx, headURL, hdr, 4<<20)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil
	case http.StatusNotFound:
		return nil // nothing published yet
	case http.StatusOK:
	default:
		return &httpStatusError{status: resp.StatusCode, url: redact(headURL)}
	}
	var head Head
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("head: %w", err)
	}
	if cur != nil && head.Generation <= cur.Generation {
		s.etag = resp.Header.Get("ETag")
		return nil
	}
	rejKey := fmt.Sprintf("%d/%s", head.Generation, head.Envelope.SHA256)
	if rejKey == s.rejected {
		return s.rejErr // same bad bundle: do not download it again
	}
	err = s.fetchAndInstall(ctx, cur, head, resp.Header.Get("ETag"))
	var ie *InstallError
	if errors.As(err, &ie) {
		s.rejected, s.rejErr = rejKey, err
	}
	return err
}

func (s *Syncer) fetchAndInstall(ctx context.Context, cur *state.Active, head Head, etag string) error {
	if s.FetchJitter > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(rand.Int64N(int64(s.FetchJitter)))):
		}
	}
	started := time.Now()
	_, env, err := s.get(ctx, head.Envelope.URL, nil, 16<<20)
	if err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	if head.Envelope.SHA256 != "" && shaHex(env) != head.Envelope.SHA256 {
		return s.reject(env, head.Generation, started, fmt.Errorf("%w: envelope sha256 does not match the head", bundle.ErrHash))
	}
	_, needed, err := bundle.VerifyEnvelope(env, s.Loader.Keys)
	if err != nil {
		return s.reject(env, head.Generation, started, err)
	}
	urls := map[string]string{}
	for _, r := range head.Resources {
		urls[r.SHA256] = r.URL
	}
	blobs := make(map[string][]byte, len(needed))
	var total int64 = int64(len(env))
	for _, h := range needed {
		if cur != nil {
			if d, ok := cur.Blobs[h]; ok {
				blobs[h] = d
				continue
			}
		}
		u, ok := urls[h]
		if !ok {
			return s.reject(env, head.Generation, started, fmt.Errorf("%w: no download URL for blob %s", bundle.ErrMissingBlob, h))
		}
		resp, d, err := s.get(ctx, u, nil, s.MaxBundle-total)
		if err != nil {
			return fmt.Errorf("blob %s: %w", h, err)
		}
		if resp.StatusCode != http.StatusOK {
			return &httpStatusError{status: resp.StatusCode, url: redact(u)}
		}
		if shaHex(d) != h {
			return s.reject(env, head.Generation, started, fmt.Errorf("%w: blob %s", bundle.ErrHash, h))
		}
		total += int64(len(d))
		if total > s.MaxBundle {
			return s.reject(env, head.Generation, started, fmt.Errorf("%w: bundle exceeds %d bytes", bundle.ErrTooLarge, s.MaxBundle))
		}
		blobs[h] = d
	}
	if _, err := s.Loader.Install(ctx, Install{Envelope: env, Blobs: blobs, Source: "network", DownloadMs: time.Since(started).Milliseconds(), Started: started, WriteCache: true}); err != nil {
		return err
	}
	s.etag = etag
	return nil
}

// reject records a bundle that failed before Install could look at it.
func (s *Syncer) reject(env []byte, gen int64, started time.Time, err error) error {
	l := s.Loader
	rec := &logs.UpdateRecord{TS: time.Now().UTC(), SidecarID: l.SidecarID, FromGeneration: l.State.Generation(), ToGeneration: gen, Result: "kept_previous", Error: err.Error(), DownloadMs: time.Since(started).Milliseconds(), Signature: signatureState(env, err)}
	reason := "verify"
	if errors.Is(err, bundle.ErrSignature) || errors.Is(err, bundle.ErrUnknownKey) || errors.Is(err, bundle.ErrUnsigned) {
		reason = "signature"
	}
	l.Metrics.SyncFailures.Inc(reason)
	l.Metrics.Updates.Inc(rec.Result)
	l.Logger.Update(rec)
	slog.Warn("bundle rejected; keeping the previous one", "generation", gen, "error", err)
	return &InstallError{Reason: reason, Err: err}
}

// watch follows /sidecar/v1/watch (Server-Sent Events) and triggers a head
// check on every newer generation event. It reconnects with backoff.
func (s *Syncer) watch(ctx context.Context) {
	failures := 0
	for ctx.Err() == nil {
		connected, err := s.watchOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			failures = 0
		}
		failures++
		slog.Debug("change feed disconnected", "error", err)
		t := time.NewTimer(Backoff(failures))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (s *Syncer) watchOnce(ctx context.Context) (connected bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/sidecar/v1/watch", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Authorization", "Bearer "+s.Credential)
	client := &http.Client{Transport: s.HTTP.Transport} // no overall timeout on a stream
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, &httpStatusError{status: resp.StatusCode, url: "/sidecar/v1/watch"}
	}
	// A ping arrives every 15 s; treat 45 s of silence as a dead stream.
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(45 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-alive:
				if !ok {
					return
				}
				t.Reset(45 * time.Second)
			case <-t.C:
				resp.Body.Close()
				return
			}
		}
	}()
	defer close(alive)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 4096), 1<<20)
	var event string
	var data bytes.Buffer
	for sc.Scan() {
		select {
		case alive <- struct{}{}:
		default:
		}
		line := sc.Text()
		switch {
		case line == "":
			if event == "generation" {
				var ev struct {
					Generation int64 `json:"generation"`
				}
				if json.Unmarshal(data.Bytes(), &ev) == nil && ev.Generation > s.Loader.State.Generation() {
					select {
					case s.trigger <- ev.Generation:
					default:
					}
				}
			}
			event = ""
			data.Reset()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return true, sc.Err()
}

// ---- heartbeat

// Heartbeat matches the Heartbeat schema.
type Heartbeat struct {
	ID             string     `json:"id"`
	Node           string     `json:"node,omitempty"`
	SidecarVersion string     `json:"sidecarVersion"`
	EngineVersion  string     `json:"engineVersion"`
	Generation     int64      `json:"generation"`
	LastSyncAt     *time.Time `json:"lastSyncAt,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	P99Micros      int64      `json:"p99Micros"`
	CallsPerMinute float64    `json:"callsPerMinute"`
	FallbackRate   float64    `json:"fallbackRate"`
}

// Heartbeater posts heartbeats.
type Heartbeater struct {
	BaseURL    string
	Credential string
	HTTP       *http.Client
	Every      time.Duration
	Build      func() Heartbeat
}

// Run posts a heartbeat now and then every interval.
func (h *Heartbeater) Run(ctx context.Context) {
	t := time.NewTicker(h.Every)
	defer t.Stop()
	for {
		if err := h.Send(ctx); err != nil && ctx.Err() == nil {
			slog.Debug("heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Send posts one heartbeat.
func (h *Heartbeater) Send(ctx context.Context) error {
	b, err := json.Marshal(h.Build())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/sidecar/v1/heartbeat", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Credential)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("heartbeat: HTTP %d", resp.StatusCode)
	}
	return nil
}

// EngineVersion is reported in heartbeats and status.
const EngineVersion = engine.Version
