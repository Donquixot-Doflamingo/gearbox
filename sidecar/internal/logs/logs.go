// SPDX-License-Identifier: MIT

// Package logs ships decision, update and exposure records. Recording never
// blocks: each log has a bounded buffer; when it is full the record is
// dropped and counted (gearbox_log_dropped_total{log}). A single goroutine
// drains the buffers in batches to the configured sinks.
package logs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ORG/gearbox/engine"
)

// MaxBatch is the largest batch sent to a sink (the API accepts 1000).
const MaxBatch = 1000

// DecisionRecord matches DecisionLogRecord in the OpenAPI contract.
type DecisionRecord struct {
	Type            string         `json:"type"`
	TS              time.Time      `json:"ts"`
	RequestID       string         `json:"requestId"`
	TraceID         string         `json:"traceId,omitempty"`
	SidecarID       string         `json:"sidecarId,omitempty"`
	Decision        string         `json:"decision"`
	DecisionVersion string         `json:"decisionVersion"`
	DependencyHash  string         `json:"dependencyHash,omitempty"`
	Generation      int64          `json:"generation"`
	UnitID          string         `json:"unitId,omitempty"`
	EvaluatedAt     time.Time      `json:"evaluatedAt"`
	Path            []engine.Step  `json:"path,omitempty"`
	FallbackUsed    bool           `json:"fallbackUsed"`
	EvalMicros      int64          `json:"evalMicros"`
	Status          string         `json:"status"`
	InputHMAC       string         `json:"inputHmac,omitempty"`
	OutputHMAC      string         `json:"outputHmac,omitempty"`
	Input           map[string]any `json:"input,omitempty"`
	Output          map[string]any `json:"output,omitempty"`
	AI              []AIRecord     `json:"ai,omitempty"`
}

// AIRecord is one AI call in a decision record.
type AIRecord struct {
	Node       string `json:"node"`
	Model      string `json:"model,omitempty"`
	LatencyMs  int64  `json:"latencyMs"`
	CacheHit   bool   `json:"cacheHit"`
	AnswerHash string `json:"answerHash,omitempty"`
	Result     string `json:"result"`
}

// Change is one resource difference in an update record.
type Change struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	From   int64  `json:"from,omitempty"`
	To     int64  `json:"to,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Tests summarises test results in an update record.
type Tests struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

// UpdateRecord matches UpdateLogRecord.
type UpdateRecord struct {
	Type           string    `json:"type"`
	TS             time.Time `json:"ts"`
	SidecarID      string    `json:"sidecarId"`
	FromGeneration int64     `json:"fromGeneration"`
	ToGeneration   int64     `json:"toGeneration"`
	Result         string    `json:"result"` // swapped | kept_previous | loaded_from_disk | up_to_date
	Changed        []Change  `json:"changed,omitempty"`
	Signature      string    `json:"signature,omitempty"` // valid | invalid | missing
	DownloadMs     int64     `json:"downloadMs,omitempty"`
	CompileMs      int64     `json:"compileMs,omitempty"`
	Tests          *Tests    `json:"tests,omitempty"`
	Error          string    `json:"error,omitempty"`
}

// ExposureRecord matches ExposureRecord.
type ExposureRecord struct {
	Type              string    `json:"type"`
	TS                time.Time `json:"ts"`
	UnitID            string    `json:"unitId"`
	Experiment        string    `json:"experiment"`
	ExperimentVersion string    `json:"experimentVersion"`
	Variant           string    `json:"variant,omitempty"`
	Reason            string    `json:"reason"`
	RequestID         string    `json:"requestId,omitempty"`
	SidecarID         string    `json:"sidecarId,omitempty"`
}

// Sink receives batches of records (any of the three types).
type Sink interface {
	Write(ctx context.Context, records []any) error
}

// Logger buffers records and ships them.
type Logger struct {
	decisions chan any
	updates   chan any
	exposures chan any
	sinks     []Sink
	dropped   func(log string, n int)
	key       []byte
	every     time.Duration
	batch     int
	wake      chan struct{}
	done      chan struct{}
}

// New returns a logger with one buffer of size per log.
func New(size int, key []byte, sinks []Sink, dropped func(log string, n int)) *Logger {
	if dropped == nil {
		dropped = func(string, int) {}
	}
	return &Logger{
		decisions: make(chan any, size), updates: make(chan any, size/16+16), exposures: make(chan any, size),
		sinks: sinks, dropped: dropped, key: key, every: time.Second, batch: MaxBatch,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

// SetBatch sets the records per upload (1..MaxBatch).
func (l *Logger) SetBatch(n int) {
	if n >= 1 && n <= MaxBatch {
		l.batch = n
	}
}

// SetFlushInterval changes the batch interval (tests).
func (l *Logger) SetFlushInterval(d time.Duration) { l.every = d }

func (l *Logger) put(ch chan any, log string, rec any) {
	select {
	case ch <- rec:
		if len(ch) >= l.batch {
			select {
			case l.wake <- struct{}{}:
			default:
			}
		}
	default:
		l.dropped(log, 1)
	}
}

// Decision records a decision (the caller applies sampling).
func (l *Logger) Decision(r *DecisionRecord) {
	r.Type = "decision"
	l.put(l.decisions, "decision", r)
}

// Update records a sync attempt (never sampled).
func (l *Logger) Update(r *UpdateRecord) {
	r.Type = "update"
	l.put(l.updates, "update", r)
}

// Exposure records an experiment assignment (never sampled).
func (l *Logger) Exposure(r *ExposureRecord) {
	r.Type = "exposure"
	l.put(l.exposures, "exposure", r)
}

// HMAC returns "hmac:<hex>" of data with the installation key.
func (l *Logger) HMAC(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	m := hmac.New(sha256.New, l.key)
	m.Write(data)
	return "hmac:" + hex.EncodeToString(m.Sum(nil))
}

// Run drains the buffers until ctx is done, then flushes what is left.
func (l *Logger) Run(ctx context.Context) {
	defer close(l.done)
	t := time.NewTicker(l.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			l.flush(flushCtx)
			cancel()
			return
		case <-t.C:
			l.flush(ctx)
		case <-l.wake:
			l.flush(ctx)
		}
	}
}

// Done is closed after Run returned.
func (l *Logger) Done() <-chan struct{} { return l.done }

func drain(ch chan any, max int, into []any) []any {
	for len(into) < max {
		select {
		case r := <-ch:
			into = append(into, r)
		default:
			return into
		}
	}
	return into
}

func (l *Logger) flush(ctx context.Context) {
	for {
		batch := make([]any, 0, 64)
		batch = drain(l.updates, l.batch, batch)
		batch = drain(l.exposures, l.batch, batch)
		batch = drain(l.decisions, l.batch, batch)
		if len(batch) == 0 {
			return
		}
		for _, s := range l.sinks {
			if err := s.Write(ctx, batch); err != nil {
				counts := map[string]int{}
				for _, r := range batch {
					counts[kindOf(r)]++
				}
				for k, n := range counts {
					l.dropped(k, n)
				}
				slog.Warn("log sink failed; records dropped", "records", len(batch), "error", err)
			}
		}
		if len(batch) < l.batch {
			return
		}
	}
}

func kindOf(r any) string {
	switch r.(type) {
	case *DecisionRecord:
		return "decision"
	case *UpdateRecord:
		return "update"
	case *ExposureRecord:
		return "exposure"
	}
	return "unknown"
}

// StdoutSink writes JSON lines.
type StdoutSink struct {
	mu sync.Mutex
	W  io.Writer
}

func (s *StdoutSink) Write(_ context.Context, records []any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bw := bufio.NewWriter(s.W)
	enc := json.NewEncoder(bw)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// HTTPSink posts batches to /sidecar/v1/logs.
type HTTPSink struct {
	URL        string
	Credential string
	Client     *http.Client
}

func (s *HTTPSink) Write(ctx context.Context, records []any) error {
	body, err := json.Marshal(map[string]any{"records": records})
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 200 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.Credential)
		resp, err := s.Client.Do(req)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return nil
		}
		last = fmt.Errorf("POST logs: HTTP %d", resp.StatusCode)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return last
		}
	}
	return last
}
