// SPDX-License-Identifier: MIT

// Package gearbox is the Go client for the Gearbox sidecar.
//
//	client, err := gearbox.Dial(ctx, os.Getenv("GEARBOX_TARGET")) // unix:///var/run/gearbox/engine.sock
//	if err != nil { return err }
//	defer client.Close()
//	d, err := client.Decide(ctx, "lead_routing", lead,
//	    gearbox.WithUnit(lead.ID),
//	    gearbox.WithTimeout(5*time.Millisecond),
//	    gearbox.WithFallback(map[string]any{"route": "manual_review"}))
//	// d.Output["route"], d.Version ("lead_routing@17"), d.FallbackUsed
//
// Every call has a deadline (default 5 ms). With WithFallback, any error
// returns the fallback value and a nil error (Decision.FromClientFallback
// is true). The client never retries against the local sidecar.
package gearbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
)

// DefaultTimeout is the per-call deadline when WithTimeout is not used.
const DefaultTimeout = 5 * time.Millisecond

// Client talks to one sidecar. It is safe for concurrent use; create one per
// process and reuse it.
type Client struct {
	conn     *grpc.ClientConn
	api      gearboxv1.EngineServiceClient
	observer func(decision string, d time.Duration, outcome string)
	timeout  time.Duration
}

// DialOption configures Dial.
type DialOption func(*dialConfig)

type dialConfig struct {
	token    string
	extra    []grpc.DialOption
	observer func(string, time.Duration, string)
	timeout  time.Duration
	wait     bool
}

// WithToken sends "authorization: Bearer <token>" (loopback TCP listener).
func WithToken(token string) DialOption { return func(c *dialConfig) { c.token = token } }

// WithGRPCOptions adds raw gRPC dial options.
func WithGRPCOptions(o ...grpc.DialOption) DialOption {
	return func(c *dialConfig) { c.extra = append(c.extra, o...) }
}

// WithObserver is called after every call with the decision (gate or
// experiment name for those calls), latency and outcome: "ok", "fallback"
// (the sidecar used the decision fallback), "client_fallback" or "error".
// Use it to feed your own latency histogram (gearbox_client_duration_seconds).
func WithObserver(fn func(decision string, d time.Duration, outcome string)) DialOption {
	return func(c *dialConfig) { c.observer = fn }
}

// WithDefaultTimeout changes the deadline used when a call has no WithTimeout.
func WithDefaultTimeout(d time.Duration) DialOption { return func(c *dialConfig) { c.timeout = d } }

// WithWaitReady makes Dial wait (within ctx) until the sidecar reports a
// loaded bundle (for platforms without native sidecar ordering).
func WithWaitReady() DialOption { return func(c *dialConfig) { c.wait = true } }

type tokenCreds struct{ token string }

func (t tokenCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + t.token}, nil
}
func (tokenCreds) RequireTransportSecurity() bool { return false }

// Dial connects to the sidecar. target is "unix:///path/engine.sock" (or
// "unix:/path") or "host:port" for the loopback TCP listener. An empty target
// uses unix:///var/run/gearbox/engine.sock.
func Dial(ctx context.Context, target string, opts ...DialOption) (*Client, error) {
	cfg := dialConfig{timeout: DefaultTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	if target == "" {
		target = "unix:///var/run/gearbox/engine.sock"
	}
	dopts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if cfg.token != "" {
		dopts = append(dopts, grpc.WithPerRPCCredentials(tokenCreds{cfg.token}))
	}
	dopts = append(dopts, cfg.extra...)
	conn, err := grpc.NewClient(target, dopts...)
	if err != nil {
		return nil, fmt.Errorf("gearbox: dial %s: %w", target, err)
	}
	c := &Client{conn: conn, api: gearboxv1.NewEngineServiceClient(conn), observer: cfg.observer, timeout: cfg.timeout}
	if cfg.wait {
		for {
			st, err := c.api.GetStatus(ctx, &gearboxv1.GetStatusRequest{}, grpc.WaitForReady(true))
			if err == nil && st.Ready {
				break
			}
			select {
			case <-ctx.Done():
				conn.Close()
				return nil, fmt.Errorf("gearbox: sidecar not ready: %w", ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Error is a sidecar error. Branch on Reason (DECISION_NOT_FOUND,
// INPUT_SCHEMA, INPUT_TOO_LARGE, NODE_ERROR, NOT_READY, ...), never on Message.
type Error struct {
	Code    codes.Code
	Reason  string
	Message string
	Node    string
	Fields  map[string]string // INPUT_SCHEMA: JSON pointer → problem
}

func (e *Error) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("gearbox: %s: %s", e.Reason, e.Message)
	}
	return fmt.Sprintf("gearbox: %s: %s", e.Code, e.Message)
}

func toError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	e := &Error{Code: st.Code(), Message: st.Message()}
	for _, d := range st.Details() {
		switch t := d.(type) {
		case *errdetails.ErrorInfo:
			e.Reason = t.Reason
			e.Node = t.Metadata["node"]
		case *errdetails.BadRequest:
			e.Fields = map[string]string{}
			for _, f := range t.FieldViolations {
				e.Fields[f.Field] = f.Description
			}
		}
	}
	return e
}

// Step is one executed node.
type Step struct {
	NodeID, NodeType, Outcome string
	Micros                    int64
}

// Decision is the result of Decide.
type Decision struct {
	Output             map[string]any // nil when the output is not a JSON object (see Raw)
	Raw                []byte         // output JSON
	Version            string         // "lead_routing@17"
	DependencyHash     string
	Generation         int64
	FallbackUsed       bool // the sidecar used a fallback edge or the decision fallback
	FromClientFallback bool // the call failed and WithFallback's value was returned
	RequestID          string
	EvalMicros         int64
	EvaluatedAt        time.Time
	Path               []Step
	Err                error // with FromClientFallback: the error that caused it
}

// CallOption configures one call.
type CallOption func(*callConfig)

type callConfig struct {
	unit, requestID string
	timeout         time.Duration
	fallback        any
	hasFallback     bool
	trace, forceLog bool
	pin             int64
	now             time.Time
}

// WithUnit sets the stable unit (user ID, lead ID…) for rollouts and experiments.
func WithUnit(id string) CallOption { return func(c *callConfig) { c.unit = id } }

// WithTimeout sets the call deadline (default 5 ms). AI decisions need the
// node timeout plus a few milliseconds.
func WithTimeout(d time.Duration) CallOption { return func(c *callConfig) { c.timeout = d } }

// WithFallback returns v (and a nil error) when the call fails for any reason.
func WithFallback(v any) CallOption {
	return func(c *callConfig) { c.fallback, c.hasFallback = v, true }
}

// WithRequestID sets the request ID echoed in logs (generated if empty).
func WithRequestID(id string) CallOption { return func(c *callConfig) { c.requestID = id } }

// WithTrace asks for per-node timings in Decision.Path.
func WithTrace() CallOption { return func(c *callConfig) { c.trace = true } }

// WithForceLog writes a decision-log record regardless of sampling.
func WithForceLog() CallOption { return func(c *callConfig) { c.forceLog = true } }

// WithPinVersion evaluates a specific version present in the bundle (replay).
func WithPinVersion(v int64) CallOption { return func(c *callConfig) { c.pin = v } }

// WithNow sets ctx.now (replay of a logged call).
func WithNow(t time.Time) CallOption { return func(c *callConfig) { c.now = t } }

type traceKey struct{}

// WithTraceparent stores a W3C traceparent in ctx; calls send it as gRPC
// metadata. Incoming gRPC metadata "traceparent" is propagated automatically.
func WithTraceparent(ctx context.Context, traceparent string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceparent)
}

func (c *Client) callCtx(ctx context.Context, cfg *callConfig) (context.Context, context.CancelFunc) {
	tp, _ := ctx.Value(traceKey{}).(string)
	if tp == "" {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get("traceparent"); len(v) > 0 {
				tp = v[0]
			}
		}
	}
	if tp != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "traceparent", tp)
	}
	d := cfg.timeout
	if d <= 0 {
		d = c.timeout
	}
	return context.WithTimeout(ctx, d)
}

func encodeInput(input any) ([]byte, error) {
	switch v := input.(type) {
	case json.RawMessage:
		return v, nil
	case []byte:
		return v, nil
	case string:
		if json.Valid([]byte(v)) {
			return []byte(v), nil
		}
	}
	return json.Marshal(input)
}

func newCallConfig(opts []CallOption) *callConfig {
	cfg := &callConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func (cfg *callConfig) request(decision string, raw []byte) *gearboxv1.DecideRequest {
	req := &gearboxv1.DecideRequest{Decision: decision, UnitId: cfg.unit, RequestId: cfg.requestID, Input: &gearboxv1.DecideRequest_InputJson{InputJson: raw}}
	if cfg.trace || cfg.forceLog || cfg.pin > 0 || !cfg.now.IsZero() {
		req.Options = &gearboxv1.DecideOptions{Trace: cfg.trace, ForceLog: cfg.forceLog}
		if cfg.pin > 0 {
			req.Options.PinVersion = strconv.FormatInt(cfg.pin, 10)
		}
		if !cfg.now.IsZero() {
			req.Options.Now = timestamppb.New(cfg.now)
		}
	}
	return req
}

func fromResponse(r *gearboxv1.DecideResponse) Decision {
	d := Decision{
		Raw: r.OutputJson, Version: r.DecisionVersion, DependencyHash: r.DependencyHash, Generation: r.BundleGeneration,
		FallbackUsed: r.FallbackUsed, RequestID: r.RequestId, EvalMicros: r.EvalMicros,
	}
	if r.EvaluatedAt != nil {
		d.EvaluatedAt = r.EvaluatedAt.AsTime()
	}
	if len(r.OutputJson) > 0 && r.OutputJson[0] == '{' {
		_ = json.Unmarshal(r.OutputJson, &d.Output)
	}
	for _, s := range r.Path {
		d.Path = append(d.Path, Step{NodeID: s.NodeId, NodeType: s.NodeType, Outcome: s.Outcome, Micros: s.Micros})
	}
	return d
}

func clientFallback(v any, cause error) Decision {
	d := Decision{FromClientFallback: true, FallbackUsed: true, Err: cause}
	raw, err := json.Marshal(v)
	if err == nil {
		d.Raw = raw
		if m, ok := v.(map[string]any); ok {
			d.Output = m
		} else if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &d.Output)
		}
	}
	return d
}

func (c *Client) observe(name string, start time.Time, outcome string) {
	if c.observer != nil {
		c.observer(name, time.Since(start), outcome)
	}
}

// Decide evaluates one decision. input is any JSON-encodable value, or
// json.RawMessage / []byte with JSON.
func (c *Client) Decide(ctx context.Context, decision string, input any, opts ...CallOption) (Decision, error) {
	start := time.Now()
	cfg := newCallConfig(opts)
	fail := func(err error) (Decision, error) {
		if cfg.hasFallback {
			c.observe(decision, start, "client_fallback")
			return clientFallback(cfg.fallback, err), nil
		}
		c.observe(decision, start, "error")
		return Decision{}, err
	}
	raw, err := encodeInput(input)
	if err != nil {
		return fail(fmt.Errorf("gearbox: encode input: %w", err))
	}
	cctx, cancel := c.callCtx(ctx, cfg)
	defer cancel()
	resp, err := c.api.Decide(cctx, cfg.request(decision, raw))
	if err != nil {
		return fail(toError(err))
	}
	d := fromResponse(resp)
	outcome := "ok"
	if d.FallbackUsed {
		outcome = "fallback"
	}
	c.observe(decision, start, outcome)
	return d, nil
}

// BatchItem is one decision in DecideBatch.
type BatchItem struct {
	Decision string
	Input    any
	Unit     string
}

// BatchResult is one DecideBatch result: Decision or Err.
type BatchResult struct {
	Decision Decision
	Err      error
}

// DecideBatch evaluates up to 100 decisions in one round trip. Items fail
// independently; the returned error is only for the call as a whole.
// WithFallback is not applied per item (check each Err).
func (c *Client) DecideBatch(ctx context.Context, items []BatchItem, opts ...CallOption) ([]BatchResult, error) {
	start := time.Now()
	cfg := newCallConfig(opts)
	req := &gearboxv1.DecideBatchRequest{}
	for _, it := range items {
		raw, err := encodeInput(it.Input)
		if err != nil {
			return nil, fmt.Errorf("gearbox: encode input of %s: %w", it.Decision, err)
		}
		ic := *cfg
		if it.Unit != "" {
			ic.unit = it.Unit
		}
		req.Items = append(req.Items, ic.request(it.Decision, raw))
	}
	cctx, cancel := c.callCtx(ctx, cfg)
	defer cancel()
	resp, err := c.api.DecideBatch(cctx, req)
	if err != nil {
		c.observe("batch", start, "error")
		return nil, toError(err)
	}
	out := make([]BatchResult, len(resp.Items))
	for i, it := range resp.Items {
		if it.Error != nil {
			out[i].Err = &Error{Code: codes.Code(it.Error.Code), Reason: it.Error.Reason, Message: it.Error.Message}
			continue
		}
		out[i].Decision = fromResponse(it.Response)
	}
	c.observe("batch", start, "ok")
	return out, nil
}

// Gate is a gate result.
type Gate struct {
	Value      bool
	Version    string // "new_checkout@5"
	RuleID     string // matched rule, "default", "killed", "disabled", "no_unit"
	Generation int64
}

// CheckGate evaluates a gate. attributes may be nil. With WithFallback(bool)
// errors return that value.
func (c *Client) CheckGate(ctx context.Context, gate, unit string, attributes any, opts ...CallOption) (Gate, error) {
	start := time.Now()
	cfg := newCallConfig(opts)
	var raw []byte
	if attributes != nil {
		b, err := encodeInput(attributes)
		if err != nil {
			return Gate{}, err
		}
		raw = b
	}
	cctx, cancel := c.callCtx(ctx, cfg)
	defer cancel()
	resp, err := c.api.CheckGate(cctx, &gearboxv1.CheckGateRequest{Gate: gate, UnitId: unit, AttributesJson: raw, RequestId: cfg.requestID})
	if err != nil {
		if b, ok := cfg.fallback.(bool); ok && cfg.hasFallback {
			c.observe(gate, start, "client_fallback")
			return Gate{Value: b, RuleID: "client_fallback"}, nil
		}
		c.observe(gate, start, "error")
		return Gate{}, toError(err)
	}
	c.observe(gate, start, "ok")
	return Gate{Value: resp.Value, Version: resp.GateVersion, RuleID: resp.RuleId, Generation: resp.BundleGeneration}, nil
}

// Variant is an experiment assignment.
type Variant struct {
	InExperiment bool
	Name         string
	Parameters   map[string]any
	RawParams    []byte
	Reason       string // assigned, holdout, not_targeted, layer_conflict, stopped, no_unit
	Version      string
	Generation   int64
}

// GetVariant assigns unit to an experiment variant. On error with
// WithFallback(string) the named variant is returned (not in experiment).
func (c *Client) GetVariant(ctx context.Context, experiment, unit string, attributes any, opts ...CallOption) (Variant, error) {
	start := time.Now()
	cfg := newCallConfig(opts)
	var raw []byte
	if attributes != nil {
		b, err := encodeInput(attributes)
		if err != nil {
			return Variant{}, err
		}
		raw = b
	}
	cctx, cancel := c.callCtx(ctx, cfg)
	defer cancel()
	resp, err := c.api.GetVariant(cctx, &gearboxv1.GetVariantRequest{Experiment: experiment, UnitId: unit, AttributesJson: raw, RequestId: cfg.requestID})
	if err != nil {
		if name, ok := cfg.fallback.(string); ok && cfg.hasFallback {
			c.observe(experiment, start, "client_fallback")
			return Variant{Name: name, Reason: "client_fallback"}, nil
		}
		c.observe(experiment, start, "error")
		return Variant{}, toError(err)
	}
	v := Variant{InExperiment: resp.InExperiment, Name: resp.Variant, RawParams: resp.ParametersJson, Reason: resp.Reason, Version: resp.ExperimentVersion, Generation: resp.BundleGeneration}
	if len(resp.ParametersJson) > 0 && resp.ParametersJson[0] == '{' {
		_ = json.Unmarshal(resp.ParametersJson, &v.Parameters)
	}
	c.observe(experiment, start, "ok")
	return v, nil
}

// DecisionInfo describes one decision in the active bundle.
type DecisionInfo struct{ Name, Version, Kind string }

// Status is the sidecar's state.
type Status struct {
	Ready                      bool
	App, Environment           string
	Generation                 int64
	LastSyncTime, LastSwapTime time.Time
	LastSyncError              string
	SidecarVersion             string
	EngineVersion              string
	Decisions                  []DecisionInfo
}

// Status returns readiness, active generation and sync state (1 s deadline
// unless the context has an earlier one).
func (c *Client) Status(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r, err := c.api.GetStatus(ctx, &gearboxv1.GetStatusRequest{})
	if err != nil {
		return Status{}, toError(err)
	}
	s := Status{Ready: r.Ready, App: r.App, Environment: r.Environment, Generation: r.BundleGeneration, LastSyncError: r.LastSyncError, SidecarVersion: r.SidecarVersion, EngineVersion: r.EngineVersion}
	if r.LastSyncTime != nil {
		s.LastSyncTime = r.LastSyncTime.AsTime()
	}
	if r.LastSwapTime != nil {
		s.LastSwapTime = r.LastSwapTime.AsTime()
	}
	for _, d := range r.Decisions {
		s.Decisions = append(s.Decisions, DecisionInfo{Name: d.Name, Version: d.Version, Kind: d.Kind})
	}
	return s, nil
}

// IsReason reports whether err is a sidecar error with the given reason.
func IsReason(err error, reason string) bool {
	var e *Error
	return errors.As(err, &e) && strings.EqualFold(e.Reason, reason)
}
