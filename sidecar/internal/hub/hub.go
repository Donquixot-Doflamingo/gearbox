// SPDX-License-Identifier: MIT

// Package hub implements engine.AICaller against the Integration Hub AI
// completions API (contracts/openapi/integration-hub-ai.yaml, a placeholder).
// Only this package changes when the Hub's real contract arrives.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/sidecar/internal/obs"
)

// ErrBreakerOpen is returned while the Hub breaker is open.
var ErrBreakerOpen = errors.New("hub: circuit breaker open")

type ctxKey struct{}

// WithRequestID attaches the caller's request ID (sent as metadata).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// Client calls the Hub.
type Client struct {
	url       string
	tokenFile string
	token     atomic.Pointer[string]
	tokenAt   atomic.Int64
	http      *http.Client
	cb        *gobreaker.CircuitBreaker
	m         *obs.Metrics
}

// New returns a Hub client. tokenFile is re-read every minute (rotation).
func New(baseURL, tokenFile string, m *obs.Metrics) *Client {
	c := &Client{
		url:       strings.TrimRight(baseURL, "/") + "/v1/ai/completions",
		tokenFile: tokenFile,
		http:      &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second}},
		m:         m,
	}
	c.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:    "integration-hub",
		Timeout: 30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: func(_ string, from, to gobreaker.State) {
			slog.Warn("integration hub breaker", "from", from.String(), "to", to.String())
		},
	})
	return c
}

func (c *Client) bearer() string {
	if c.tokenFile == "" {
		return ""
	}
	if p := c.token.Load(); p != nil && time.Since(time.Unix(0, c.tokenAt.Load())) < time.Minute {
		return *p
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		if p := c.token.Load(); p != nil {
			return *p
		}
		return ""
	}
	t := strings.TrimSpace(string(b))
	c.token.Store(&t)
	c.tokenAt.Store(time.Now().UnixNano())
	return t
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model           string            `json:"model"`
	Messages        []message         `json:"messages"`
	ResponseSchema  json.RawMessage   `json:"response_schema,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Temperature     float64           `json:"temperature"`
	TimeoutMs       int64             `json:"timeout_ms,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type completionResponse struct {
	Output json.RawMessage `json:"output"`
	Model  string          `json:"model"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// HTTPError is a non-2xx Hub answer.
type HTTPError struct {
	Status int
	Code   string
	Msg    string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("hub: HTTP %d %s %s", e.Status, e.Code, e.Msg) }

// Call implements engine.AICaller.
func (c *Client) Call(ctx context.Context, call engine.AICall) (engine.AIAnswer, error) {
	start := time.Now()
	res, err := c.cb.Execute(func() (interface{}, error) { return c.do(ctx, call) })
	result := "ok"
	switch {
	case errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests):
		err, result = ErrBreakerOpen, "breaker_open"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		result = "timeout"
	case err != nil:
		result = "error"
	}
	if c.m != nil {
		c.m.AICalls.Inc(call.Decision, call.Node, "call", result)
		c.m.AIDuration.Observe(time.Since(start).Seconds(), call.Decision, call.Node)
	}
	if err != nil {
		return engine.AIAnswer{}, err
	}
	return res.(engine.AIAnswer), nil
}

func (c *Client) do(ctx context.Context, call engine.AICall) (engine.AIAnswer, error) {
	timeout := call.Timeout
	margin := timeout / 10
	if margin < 10*time.Millisecond {
		margin = 10 * time.Millisecond
	}
	body := completionRequest{
		Model:           call.Model,
		ResponseSchema:  call.ResponseSchema,
		MaxOutputTokens: call.MaxOutputTokens,
		TimeoutMs:       (timeout - margin).Milliseconds(),
		Metadata: map[string]string{
			"decision": fmt.Sprintf("%s@%d", call.Decision, call.DecisionVersion),
			"node":     call.Node,
			"call_id":  call.CallID,
		},
	}
	if body.TimeoutMs < 1 {
		body.TimeoutMs = 1
	}
	if id, ok := ctx.Value(ctxKey{}).(string); ok && id != "" {
		body.Metadata["request_id"] = id
	}
	if call.System != "" {
		body.Messages = append(body.Messages, message{Role: "system", Content: call.System})
	}
	body.Messages = append(body.Messages, message{Role: "user", Content: call.User})
	b, err := json.Marshal(body)
	if err != nil {
		return engine.AIAnswer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return engine.AIAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", call.CallID)
	if t := c.bearer(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return engine.AIAnswer{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return engine.AIAnswer{}, err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return engine.AIAnswer{}, &HTTPError{Status: resp.StatusCode, Code: e.Code, Msg: e.Message}
	}
	var cr completionResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return engine.AIAnswer{}, fmt.Errorf("hub: invalid response: %w", err)
	}
	if len(cr.Output) == 0 {
		return engine.AIAnswer{}, errors.New("hub: response has no output")
	}
	return engine.AIAnswer{Output: cr.Output, Model: cr.Model, InputTokens: cr.Usage.InputTokens, OutputTokens: cr.Usage.OutputTokens}, nil
}

// RecordShadow implements engine.AIShadowRecorder.
func (c *Client) RecordShadow(call engine.AICall, ans engine.AIAnswer, latency time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = "error"
		if errors.Is(err, context.DeadlineExceeded) {
			result = "timeout"
		} else if strings.Contains(err.Error(), "responseSchema") {
			result = "schema_invalid"
		}
	}
	if c.m != nil {
		c.m.AICalls.Inc(call.Decision, call.Node, "shadow", result)
	}
	slog.Debug("ai shadow call", "decision", call.Decision, "node", call.Node, "model", ans.Model, "latency_ms", latency.Milliseconds(), "result", result)
}
