// Package engineapi is the control plane's seam over the engine and bundle
// packages (engine/api.go, engine/bundle/api.go).
//
// The data-plane team implements those packages in parallel. Until every
// function is real, the control plane must keep working, so this package
// offers three modes (GEARBOX_ENGINE):
//
//   - real: call the engine and bundle packages only.
//   - lite: a small built-in engine (structure, schema-registry rules,
//     JSON Schema subset, field conditions, bucketing) that cannot evaluate
//     Expr expressions; decisions return their fallback. Good for UI and API
//     development and deterministic tests.
//   - auto (default): call the real packages and fall back to lite for any
//     function that still returns ErrNotImplemented.
package engineapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

// Engine is what the control plane needs from the engine package.
type Engine interface {
	Name() string
	Version() string
	ValidateResource(r engine.Resource, deps engine.Resolver) engine.Validation
	CheckSchemaCompatibility(prev, next json.RawMessage) (bool, []engine.SchemaChange, error)
	ValidateDocument(schemaSpec, doc json.RawMessage) (bool, []engine.FieldError, error)
	Compile(set engine.Set) (Snapshot, error)
	Catalogue() json.RawMessage
}

// Snapshot mirrors *engine.Snapshot.
type Snapshot interface {
	Generation() int64
	Decisions() []engine.DecisionInfo
	Decide(ctx context.Context, req engine.Request) (engine.Result, error)
	CheckGate(gate, unitID string, attributes json.RawMessage) (engine.GateResult, error)
	GetVariant(experiment, unitID string, attributes json.RawMessage) (engine.VariantResult, error)
	CheckSegment(segment, key, unit string, attributes json.RawMessage) (bool, string, error)
	RunTests(ctx context.Context, decision string, version int64) ([]engine.TestResult, error)
	Benchmark(ctx context.Context, n int) (map[string]int64, error)
}

// Bundler is what the control plane needs from the bundle package.
type Bundler interface {
	Name() string
	Build(set engine.Set, meta bundle.Meta, signer bundle.Signer) (bundle.Built, error)
	VerifyEnvelope(envelope []byte, keys bundle.TrustedKeys) (int64, []string, error)
	WriteFile(b bundle.Built) ([]byte, error)
}

// Select returns the engine and bundler for a mode.
func Select(mode string) (Engine, Bundler, error) {
	switch mode {
	case "", "auto":
		return Hybrid{}, HybridBundler{}, nil
	case "real":
		return Real{}, RealBundler{}, nil
	case "lite":
		return Lite{}, LiteBundler{}, nil
	}
	return nil, nil, fmt.Errorf("engineapi: unknown GEARBOX_ENGINE %q (auto, real, lite)", mode)
}

// RealImplemented reports whether engine.Compile and bundle.Build are
// implemented (used for logging and to skip real-engine tests).
func RealImplemented() (engineOK, bundleOK bool) {
	_, err := engine.Compile(engine.Set{App: "probe", Environment: "probe"})
	engineOK = !errors.Is(err, engine.ErrNotImplemented)
	_, err = bundle.Build(engine.Set{App: "probe", Environment: "probe", Generation: 1}, bundle.Meta{}, bundle.Signer{})
	bundleOK = !errors.Is(err, bundle.ErrNotImplemented)
	return
}

// ---------------------------------------------------------------- real

// Real delegates to the engine package.
type Real struct{}

func (Real) Name() string    { return "engine" }
func (Real) Version() string { return engine.Version }
func (Real) ValidateResource(r engine.Resource, deps engine.Resolver) engine.Validation {
	return engine.ValidateResource(r, deps)
}
func (Real) CheckSchemaCompatibility(prev, next json.RawMessage) (bool, []engine.SchemaChange, error) {
	return engine.CheckSchemaCompatibility(prev, next)
}
func (Real) ValidateDocument(s, d json.RawMessage) (bool, []engine.FieldError, error) {
	return engine.ValidateDocument(s, d)
}
func (Real) Compile(set engine.Set) (Snapshot, error) {
	s, err := engine.Compile(set)
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (Real) Catalogue() json.RawMessage { return engine.Catalogue() }

// RealBundler delegates to the bundle package.
type RealBundler struct{}

func (RealBundler) Name() string { return "bundle" }
func (RealBundler) Build(set engine.Set, meta bundle.Meta, signer bundle.Signer) (bundle.Built, error) {
	return bundle.Build(set, meta, signer)
}
func (RealBundler) VerifyEnvelope(env []byte, keys bundle.TrustedKeys) (int64, []string, error) {
	return bundle.VerifyEnvelope(env, keys)
}
func (RealBundler) WriteFile(b bundle.Built) ([]byte, error) { return bundle.WriteFile(b) }

// ---------------------------------------------------------------- hybrid

func notImpl(err error) bool {
	return errors.Is(err, engine.ErrNotImplemented) || errors.Is(err, bundle.ErrNotImplemented)
}

// Hybrid prefers the real engine and falls back to Lite per function.
type Hybrid struct{}

func (Hybrid) Name() string    { return "auto" }
func (Hybrid) Version() string { return engine.Version }

func (Hybrid) ValidateResource(r engine.Resource, deps engine.Resolver) engine.Validation {
	v := engine.ValidateResource(r, deps)
	for _, p := range v.Problems {
		if p.Code == "NOT_IMPLEMENTED" {
			return Lite{}.ValidateResource(r, deps)
		}
	}
	return v
}

func (Hybrid) CheckSchemaCompatibility(prev, next json.RawMessage) (bool, []engine.SchemaChange, error) {
	ok, ch, err := engine.CheckSchemaCompatibility(prev, next)
	if notImpl(err) {
		return Lite{}.CheckSchemaCompatibility(prev, next)
	}
	return ok, ch, err
}

func (Hybrid) ValidateDocument(s, d json.RawMessage) (bool, []engine.FieldError, error) {
	ok, errs, err := engine.ValidateDocument(s, d)
	if notImpl(err) {
		return Lite{}.ValidateDocument(s, d)
	}
	return ok, errs, err
}

func (Hybrid) Compile(set engine.Set) (Snapshot, error) {
	s, err := engine.Compile(set)
	if notImpl(err) {
		return Lite{}.Compile(set)
	}
	if err != nil {
		return nil, err
	}
	return &hybridSnapshot{real: s, set: set}, nil
}

func (Hybrid) Catalogue() json.RawMessage {
	c := engine.Catalogue()
	var probe struct {
		Functions []json.RawMessage `json:"functions"`
	}
	if json.Unmarshal(c, &probe) == nil && len(probe.Functions) == 0 {
		return Lite{}.Catalogue()
	}
	return c
}

// hybridSnapshot falls back to a lite snapshot for unimplemented methods.
type hybridSnapshot struct {
	real *engine.Snapshot
	set  engine.Set
	lite Snapshot
}

func (h *hybridSnapshot) l() Snapshot {
	if h.lite == nil {
		h.lite, _ = Lite{}.Compile(h.set)
		if h.lite == nil {
			h.lite = &liteSnapshot{set: h.set}
		}
	}
	return h.lite
}

func (h *hybridSnapshot) Generation() int64 { return h.real.Generation() }
func (h *hybridSnapshot) Decisions() []engine.DecisionInfo {
	if d := h.real.Decisions(); d != nil {
		return d
	}
	return h.l().Decisions()
}
func (h *hybridSnapshot) Decide(ctx context.Context, req engine.Request) (engine.Result, error) {
	r, err := h.real.Decide(ctx, req)
	if notImpl(err) {
		return h.l().Decide(ctx, req)
	}
	return r, err
}
func (h *hybridSnapshot) CheckGate(g, u string, a json.RawMessage) (engine.GateResult, error) {
	r, err := h.real.CheckGate(g, u, a)
	if notImpl(err) {
		return h.l().CheckGate(g, u, a)
	}
	return r, err
}
func (h *hybridSnapshot) GetVariant(e, u string, a json.RawMessage) (engine.VariantResult, error) {
	r, err := h.real.GetVariant(e, u, a)
	if notImpl(err) {
		return h.l().GetVariant(e, u, a)
	}
	return r, err
}
func (h *hybridSnapshot) CheckSegment(s, k, u string, a json.RawMessage) (bool, string, error) {
	m, reason, err := h.real.CheckSegment(s, k, u, a)
	if notImpl(err) {
		return h.l().CheckSegment(s, k, u, a)
	}
	return m, reason, err
}
func (h *hybridSnapshot) RunTests(ctx context.Context, d string, v int64) ([]engine.TestResult, error) {
	r, err := h.real.RunTests(ctx, d, v)
	if notImpl(err) {
		return h.l().RunTests(ctx, d, v)
	}
	return r, err
}
func (h *hybridSnapshot) Benchmark(ctx context.Context, n int) (map[string]int64, error) {
	r, err := h.real.Benchmark(ctx, n)
	if notImpl(err) {
		return h.l().Benchmark(ctx, n)
	}
	return r, err
}

// HybridBundler prefers the real bundle package.
type HybridBundler struct{}

func (HybridBundler) Name() string { return "auto" }
func (HybridBundler) Build(set engine.Set, meta bundle.Meta, signer bundle.Signer) (bundle.Built, error) {
	b, err := bundle.Build(set, meta, signer)
	if notImpl(err) {
		return LiteBundler{}.Build(set, meta, signer)
	}
	return b, err
}
func (HybridBundler) VerifyEnvelope(env []byte, keys bundle.TrustedKeys) (int64, []string, error) {
	g, n, err := bundle.VerifyEnvelope(env, keys)
	if notImpl(err) {
		return LiteBundler{}.VerifyEnvelope(env, keys)
	}
	return g, n, err
}
func (HybridBundler) WriteFile(b bundle.Built) ([]byte, error) {
	out, err := bundle.WriteFile(b)
	if notImpl(err) {
		return LiteBundler{}.WriteFile(b)
	}
	return out, err
}
