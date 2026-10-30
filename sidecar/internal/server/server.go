// SPDX-License-Identifier: MIT

// Package server implements gearbox.v1.EngineService on top of the active
// engine snapshot.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ORG/gearbox/engine"
	gearboxv1 "github.com/ORG/gearbox/gen/go/gearbox/v1"
	"github.com/ORG/gearbox/sidecar/internal/breaker"
	"github.com/ORG/gearbox/sidecar/internal/hub"
	"github.com/ORG/gearbox/sidecar/internal/logs"
	"github.com/ORG/gearbox/sidecar/internal/obs"
	"github.com/ORG/gearbox/sidecar/internal/state"
)

// Domain of google.rpc.ErrorInfo.
const Domain = "gearbox.dev"

// MaxBatch is the DecideBatch item limit.
const MaxBatch = 100

// Version of the sidecar (set by the binary).
var Version = "dev"

// Server implements EngineService.
type Server struct {
	gearboxv1.UnimplementedEngineServiceServer

	State         *state.State
	Metrics       *obs.Metrics
	Logger        *logs.Logger
	Breakers      *breaker.Set
	Hub           *hub.Client // nil: AI nodes take onError
	SidecarID     string
	MaxInputBytes int
	LogSample     float64

	calls     atomic.Uint64
	fallbacks atomic.Uint64
	statsMu   sync.Mutex
	lastStats statsSnap
}

type statsSnap struct {
	at        time.Time
	calls     uint64
	fallbacks uint64
	buckets   []uint64
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}

// grpcError builds a status with ErrorInfo (and BadRequest field violations).
func grpcError(code codes.Code, reason, msg string, md map[string]string, fields []engine.FieldError) error {
	st := status.New(code, msg)
	info := &errdetails.ErrorInfo{Reason: reason, Domain: Domain, Metadata: md}
	if len(fields) > 0 {
		br := &errdetails.BadRequest{}
		for _, f := range fields {
			br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{Field: f.Path, Description: f.Message})
		}
		if s, err := st.WithDetails(info, br); err == nil {
			return s.Err()
		}
	}
	if s, err := st.WithDetails(info); err == nil {
		return s.Err()
	}
	return st.Err()
}

// codeFor maps engine reasons to gRPC codes.
func codeFor(reason string) codes.Code {
	switch reason {
	case "DECISION_NOT_FOUND", "GATE_NOT_FOUND", "EXPERIMENT_NOT_FOUND", "SEGMENT_NOT_FOUND":
		return codes.NotFound
	case "INPUT_SCHEMA", "INPUT_TOO_LARGE", "INVALID_ARGUMENT":
		return codes.InvalidArgument
	case "NOT_READY":
		return codes.FailedPrecondition
	}
	return codes.Internal
}

func toStatus(err error) error {
	var ee *engine.Error
	switch {
	case errors.As(err, &ee):
		md := map[string]string{}
		if ee.Node != "" {
			md["node"] = ee.Node
		}
		return grpcError(codeFor(ee.Reason), ee.Reason, ee.Message, md, ee.Fields)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	}
	return grpcError(codes.Internal, "NODE_ERROR", err.Error(), nil, nil)
}

var errNotReady = grpcError(codes.FailedPrecondition, "NOT_READY", "no verified bundle is loaded yet", nil, nil)

func traceID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	v := md.Get("traceparent")
	if len(v) == 0 {
		return ""
	}
	parts := strings.Split(v[0], "-")
	if len(parts) >= 2 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}

// aiRecorder wraps the Hub client for one request to capture AI calls for
// the decision log and pass the request ID.
type aiRecorder struct {
	hub       *hub.Client
	requestID string
	mu        sync.Mutex
	calls     []logs.AIRecord
}

func (a *aiRecorder) Call(ctx context.Context, call engine.AICall) (engine.AIAnswer, error) {
	start := time.Now()
	ans, err := a.hub.Call(hub.WithRequestID(ctx, a.requestID), call)
	rec := logs.AIRecord{Node: call.Node, Model: ans.Model, LatencyMs: time.Since(start).Milliseconds(), Result: "ok"}
	if err != nil {
		rec.Result = "error"
		if errors.Is(err, context.DeadlineExceeded) {
			rec.Result = "timeout"
		}
	} else {
		h := sha256.Sum256(ans.Output)
		rec.AnswerHash = "sha256:" + hex.EncodeToString(h[:])
	}
	a.mu.Lock()
	a.calls = append(a.calls, rec)
	a.mu.Unlock()
	return ans, err
}

func (a *aiRecorder) RecordShadow(call engine.AICall, ans engine.AIAnswer, latency time.Duration, err error) {
	a.hub.RecordShadow(call, ans, latency, err)
}

func (a *aiRecorder) records() []logs.AIRecord {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]logs.AIRecord(nil), a.calls...)
}

// Decide implements EngineService.Decide.
func (s *Server) Decide(ctx context.Context, req *gearboxv1.DecideRequest) (*gearboxv1.DecideResponse, error) {
	return s.decide(ctx, req)
}

func (s *Server) input(req *gearboxv1.DecideRequest) ([]byte, error) {
	var raw []byte
	switch in := req.Input.(type) {
	case *gearboxv1.DecideRequest_InputJson:
		raw = in.InputJson
	case *gearboxv1.DecideRequest_InputStruct:
		if in.InputStruct == nil {
			raw = []byte("{}")
			break
		}
		b, err := json.Marshal(in.InputStruct.AsMap())
		if err != nil {
			return nil, grpcError(codes.InvalidArgument, "INPUT_SCHEMA", "input_struct: "+err.Error(), nil, nil)
		}
		raw = b
	default:
		return nil, grpcError(codes.InvalidArgument, "INVALID_ARGUMENT", "input_json or input_struct is required", nil, nil)
	}
	if len(raw) > s.MaxInputBytes {
		return nil, grpcError(codes.InvalidArgument, "INPUT_TOO_LARGE", fmt.Sprintf("input is %d bytes; the limit is %d", len(raw), s.MaxInputBytes), nil, nil)
	}
	return raw, nil
}

func parsePin(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "v")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("pin_version %q must be a positive version number", s)
	}
	return n, nil
}

func (s *Server) decide(ctx context.Context, req *gearboxv1.DecideRequest) (*gearboxv1.DecideResponse, error) {
	act := s.State.Current()
	if act == nil {
		return nil, errNotReady
	}
	if n := len(req.Decision); n < 1 || n > 128 {
		return nil, grpcError(codes.InvalidArgument, "INVALID_ARGUMENT", "decision name must be 1-128 characters", nil, nil)
	}
	raw, err := s.input(req)
	if err != nil {
		return nil, err
	}
	requestID := req.RequestId
	if requestID == "" {
		requestID = newRequestID()
	}
	opts := req.Options
	if opts == nil {
		opts = &gearboxv1.DecideOptions{}
	}
	pin, err := parsePin(opts.PinVersion)
	if err != nil {
		return nil, grpcError(codes.InvalidArgument, "INVALID_ARGUMENT", err.Error(), nil, nil)
	}
	ereq := engine.Request{Decision: req.Decision, Input: raw, UnitID: req.UnitId, RequestID: requestID, PinVersion: pin, Trace: opts.Trace}
	if opts.Now != nil {
		ereq.Now = opts.Now.AsTime()
	}
	snap := act.Snap
	meta, _ := snap.DecisionMeta(req.Decision, pin)
	var rec *aiRecorder
	if meta.HasAI && s.Hub != nil {
		rec = &aiRecorder{hub: s.Hub, requestID: requestID}
		ereq.AI = rec
	}
	var res engine.Result
	evaluated := false
	if !s.Breakers.Allow(req.Decision) {
		var ok bool
		res, ok, err = snap.Fallback(ctx, ereq)
		if !ok {
			res, err = snap.Decide(ctx, ereq)
			evaluated = true
		}
	} else {
		res, err = snap.Decide(ctx, ereq)
		evaluated = true
	}
	if evaluated && err == nil {
		s.Breakers.Record(req.Decision, res.EvalMicros, meta.BudgetMicros)
	}
	s.observe(ctx, act, req, ereq, opts.ForceLog, meta, res, err, raw, rec)
	if err != nil {
		return nil, toStatus(err)
	}
	return toResponse(res, requestID), nil
}

func toResponse(res engine.Result, requestID string) *gearboxv1.DecideResponse {
	out := &gearboxv1.DecideResponse{
		OutputJson: res.Output, DecisionVersion: res.DecisionVersion, BundleGeneration: res.Generation,
		FallbackUsed: res.FallbackUsed, EvalMicros: res.EvalMicros, RequestId: requestID,
		EvaluatedAt: timestamppb.New(res.EvaluatedAt), DependencyHash: res.DependencyHash,
		Path: make([]*gearboxv1.NodeStep, len(res.Path)),
	}
	for i, st := range res.Path {
		out.Path[i] = &gearboxv1.NodeStep{NodeId: st.NodeID, NodeType: st.NodeType, Outcome: st.Outcome, Micros: st.Micros}
	}
	return out
}

func statusName(err error) string {
	if err == nil {
		return "OK"
	}
	return status.Code(toStatus(err)).String()
}

func (s *Server) observe(ctx context.Context, act *state.Active, req *gearboxv1.DecideRequest, ereq engine.Request, force bool, meta engine.DecisionMeta, res engine.Result, err error, input []byte, rec *aiRecorder) {
	version := res.DecisionVersion
	if version == "" {
		version = "unknown"
	}
	st := statusName(err)
	fb := "false"
	if res.FallbackUsed {
		fb = "true"
		s.fallbacks.Add(1)
	}
	s.calls.Add(1)
	if err == nil || res.DecisionVersion != "" {
		s.Metrics.Decisions.Inc(req.Decision, version, st, fb)
		s.Metrics.DecisionDuration.Observe(float64(res.EvalMicros)/1e6, req.Decision, version)
	} else {
		s.Metrics.Decisions.Inc("_unknown", "unknown", st, fb)
	}
	for _, step := range res.Path {
		if step.Error != "" {
			s.Metrics.NodeErrors.Inc(req.Decision, step.NodeID)
		}
	}
	now := time.Now().UTC()
	for _, e := range res.Exposures {
		s.Metrics.Exposures.Inc(e.Experiment)
		s.Logger.Exposure(&logs.ExposureRecord{TS: now, UnitID: req.UnitId, Experiment: e.Experiment, ExperimentVersion: fmt.Sprintf("%s@%d", e.Experiment, e.ExperimentVersion), Variant: e.Variant, Reason: e.Reason, RequestID: ereq.RequestID, SidecarID: s.SidecarID})
	}
	sample := s.LogSample
	if meta.LogSample != nil {
		sample = *meta.LogSample
	}
	if !(force || err != nil || (sample > 0 && mrand.Float64() < sample)) {
		return
	}
	r := &logs.DecisionRecord{
		TS: now, RequestID: ereq.RequestID, TraceID: traceID(ctx), SidecarID: s.SidecarID,
		Decision: req.Decision, DecisionVersion: res.DecisionVersion, DependencyHash: res.DependencyHash,
		Generation: act.Generation, UnitID: req.UnitId, EvaluatedAt: res.EvaluatedAt,
		FallbackUsed: res.FallbackUsed, EvalMicros: res.EvalMicros, Status: st,
		InputHMAC: s.Logger.HMAC(input), OutputHMAC: s.Logger.HMAC(res.Output), AI: rec.records(),
	}
	for _, step := range res.Path {
		r.Path = append(r.Path, engine.Step{NodeID: step.NodeID, NodeType: step.NodeType, Outcome: step.Outcome, Micros: step.Micros, Error: step.Error})
	}
	if res.DecisionVersion != "" {
		r.Input, r.Output = act.Snap.LogFields(res.DecisionVersion, input, res.Output)
	}
	s.Logger.Decision(r)
}

// DecideBatch implements EngineService.DecideBatch.
func (s *Server) DecideBatch(ctx context.Context, req *gearboxv1.DecideBatchRequest) (*gearboxv1.DecideBatchResponse, error) {
	if s.State.Current() == nil {
		return nil, errNotReady
	}
	if len(req.Items) > MaxBatch {
		return nil, grpcError(codes.InvalidArgument, "INVALID_ARGUMENT", fmt.Sprintf("a batch has at most %d items", MaxBatch), nil, nil)
	}
	out := &gearboxv1.DecideBatchResponse{Items: make([]*gearboxv1.DecideBatchItem, len(req.Items))}
	for i, item := range req.Items {
		resp, err := s.decide(ctx, item)
		if err != nil {
			st := status.Convert(err)
			reason := ""
			for _, d := range st.Details() {
				if info, ok := d.(*errdetails.ErrorInfo); ok {
					reason = info.Reason
				}
			}
			out.Items[i] = &gearboxv1.DecideBatchItem{Error: &gearboxv1.ItemError{Code: int32(st.Code()), Reason: reason, Message: st.Message()}}
			continue
		}
		out.Items[i] = &gearboxv1.DecideBatchItem{Response: resp}
	}
	return out, nil
}

// CheckGate implements EngineService.CheckGate.
func (s *Server) CheckGate(ctx context.Context, req *gearboxv1.CheckGateRequest) (*gearboxv1.CheckGateResponse, error) {
	act := s.State.Current()
	if act == nil {
		return nil, errNotReady
	}
	res, err := act.Snap.CheckGate(req.Gate, req.UnitId, req.AttributesJson)
	if err != nil {
		return nil, toStatus(err)
	}
	return &gearboxv1.CheckGateResponse{Value: res.Value, GateVersion: fmt.Sprintf("%s@%d", req.Gate, res.Version), RuleId: res.RuleID, BundleGeneration: act.Generation}, nil
}

// GetVariant implements EngineService.GetVariant.
func (s *Server) GetVariant(ctx context.Context, req *gearboxv1.GetVariantRequest) (*gearboxv1.GetVariantResponse, error) {
	act := s.State.Current()
	if act == nil {
		return nil, errNotReady
	}
	res, err := act.Snap.GetVariant(req.Experiment, req.UnitId, req.AttributesJson)
	if err != nil {
		return nil, toStatus(err)
	}
	version := fmt.Sprintf("%s@%d", req.Experiment, res.Version)
	if req.UnitId != "" {
		s.Metrics.Exposures.Inc(req.Experiment)
		s.Logger.Exposure(&logs.ExposureRecord{TS: time.Now().UTC(), UnitID: req.UnitId, Experiment: req.Experiment, ExperimentVersion: version, Variant: res.Variant, Reason: res.Reason, RequestID: req.RequestId, SidecarID: s.SidecarID})
	}
	return &gearboxv1.GetVariantResponse{InExperiment: res.InExperiment, Variant: res.Variant, ParametersJson: res.Parameters, Reason: res.Reason, ExperimentVersion: version, BundleGeneration: act.Generation}, nil
}

// GetStatus implements EngineService.GetStatus.
func (s *Server) GetStatus(ctx context.Context, _ *gearboxv1.GetStatusRequest) (*gearboxv1.GetStatusResponse, error) {
	out := &gearboxv1.GetStatusResponse{SidecarVersion: Version, EngineVersion: engine.Version, LastSyncError: s.State.LastError()}
	if t := s.State.LastSync(); !t.IsZero() {
		out.LastSyncTime = timestamppb.New(t)
	}
	act := s.State.Current()
	if act == nil {
		return out, nil
	}
	out.Ready = true
	out.App, out.Environment, out.BundleGeneration = act.App, act.Env, act.Generation
	out.LastSwapTime = timestamppb.New(act.SwappedAt)
	for _, d := range act.Snap.Decisions() {
		out.Decisions = append(out.Decisions, &gearboxv1.DecisionInfo{Name: d.Name, Version: strconv.FormatInt(d.Version, 10), Kind: d.Kind})
	}
	return out, nil
}

// Stats returns heartbeat figures since the previous call.
func (s *Server) Stats() (p99Micros int64, callsPerMinute, fallbackRate float64) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	buckets, _ := s.Metrics.DecisionDuration.Snapshot()
	now := time.Now()
	calls, fbs := s.calls.Load(), s.fallbacks.Load()
	prev := s.lastStats
	s.lastStats = statsSnap{at: now, calls: calls, fallbacks: fbs, buckets: buckets}
	if prev.at.IsZero() {
		prev = statsSnap{at: now.Add(-time.Minute), buckets: make([]uint64, len(buckets))}
	}
	deltas := make([]uint64, len(buckets))
	for i := range buckets {
		if i < len(prev.buckets) {
			deltas[i] = buckets[i] - prev.buckets[i]
		}
	}
	p99 := quantile(s.Metrics.DecisionDuration.Bounds(), deltas, 0.99)
	dc := calls - prev.calls
	if mins := now.Sub(prev.at).Minutes(); mins > 0 {
		callsPerMinute = float64(dc) / mins
	}
	if dc > 0 {
		fallbackRate = float64(fbs-prev.fallbacks) / float64(dc)
	}
	return int64(p99 * 1e6), callsPerMinute, fallbackRate
}

func quantile(bounds []float64, deltas []uint64, q float64) float64 {
	var total uint64
	for _, d := range deltas {
		total += d
	}
	if total == 0 {
		return 0
	}
	rank := uint64(float64(total)*q + 0.999999)
	var acc uint64
	for i, d := range deltas {
		acc += d
		if acc >= rank {
			if i < len(bounds) {
				return bounds[i]
			}
			break
		}
	}
	return bounds[len(bounds)-1]
}

// ---- interceptors

// UnaryInterceptor tracks in-flight requests, applies a server-side timeout,
// optionally checks a bearer token and turns panics into INTERNAL errors.
func UnaryInterceptor(m *obs.Metrics, timeout time.Duration, token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		if token != "" && !strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
			md, _ := metadata.FromIncomingContext(ctx)
			ok := false
			for _, v := range md.Get("authorization") {
				if subtleEqual(v, "Bearer "+token) {
					ok = true
				}
			}
			if !ok {
				return nil, status.Error(codes.Unauthenticated, "missing or invalid bearer token")
			}
		}
		m.Inflight.Add(1)
		defer m.Inflight.Add(-1)
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		defer func() {
			if r := recover(); r != nil {
				err = status.Errorf(codes.Internal, "internal error: %v", r)
			}
		}()
		return handler(ctx, req)
	}
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
