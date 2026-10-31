// Package engine compiles Gearbox definitions and evaluates decisions.
//
// This file is the CONTRACT between the engine and its two users, the sidecar
// and the control plane. Its exported names and behaviour are fixed; the
// bodies delegate to the implementation in the other files of this package.
// Changing a signature here needs agreement from both users.
//
// Definitions are plain JSON matching the OpenAPI components in
// contracts/openapi/gearbox-admin.v1.yaml (SchemaSpec, DecisionSpec,
// SegmentSpec, LookupSpec, GateSpec, ExperimentSpec, LayerSpec), so the control
// plane stores them without knowing their structure.
//
// The engine does no I/O and keeps no global state.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Version of the engine's evaluation semantics. Bundles state the minimum
// engine version they need; bump the minor version when semantics change.
const Version = "1.0.0"

// ErrNotImplemented is kept for compatibility; no engine function returns it.
var ErrNotImplemented = errors.New("engine: not implemented")

// Kind of resource, as used in API paths.
type Kind string

const (
	KindSchema     Kind = "schemas"
	KindDecision   Kind = "decisions"
	KindSegment    Kind = "segments"
	KindLookup     Kind = "lookups"
	KindGate       Kind = "gates"
	KindExperiment Kind = "experiments"
	KindLayer      Kind = "layers"
)

// Resource is one versioned definition. Version 0 means "draft".
type Resource struct {
	Kind    Kind            `json:"kind"`
	Name    string          `json:"name"`
	Version int64           `json:"version"`
	Spec    json.RawMessage `json:"spec"`
	// Static segment members (HMACs when the segment is pii) and lookup rows.
	SegmentMembers []string                   `json:"segmentMembers,omitempty"`
	LookupRows     map[string]json.RawMessage `json:"lookupRows,omitempty"`
}

// Ref names a resource version.
type Ref struct {
	Kind    Kind   `json:"kind"`
	Name    string `json:"name"`
	Version int64  `json:"version,omitempty"`
}

// Rollout splits a decision between two versions by unit bucket.
type Rollout struct {
	Decision    string `json:"decision"`
	FromVersion int64  `json:"fromVersion"`
	ToVersion   int64  `json:"toVersion"`
	Percent     int    `json:"percent"`
	Salt        string `json:"salt"`
}

// Set is everything one snapshot is compiled from: the live version of every
// resource of an app in one environment, plus the "from" versions of rollouts.
type Set struct {
	App         string     `json:"app"`
	Environment string     `json:"environment"`
	Generation  int64      `json:"generation"`
	Resources   []Resource `json:"resources"`
	Rollouts    []Rollout  `json:"rollouts,omitempty"`
	KilledGates []string   `json:"killedGates,omitempty"`
}

// Problem is one validation finding. Codes match ValidationProblem.code in
// the OpenAPI contract (SCHEMA_REQUIRED, SCHEMA_NOT_FOUND, TYPE_MISMATCH, ...).
type Problem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // error | warning
	Path     string `json:"path"`     // JSON pointer into the spec
	Message  string `json:"message"`
}

// Validation is the result of validating one resource.
type Validation struct {
	Valid        bool      `json:"valid"`
	Problems     []Problem `json:"problems"`
	Dependencies []Ref     `json:"dependencies,omitempty"`
}

// Resolver gives the engine access to other resources while validating a
// single resource (for example the schemas a rules node points at).
type Resolver interface {
	// Get returns a version (version 0 = the live/latest one the caller
	// chooses) or ok=false when it does not exist.
	Get(kind Kind, name string, version int64) (Resource, bool)
}

// ValidateResource checks one resource: JSON shape, schema-registry rules
// (rules/table nodes need registered input and output schemas), expression
// compilation and type checks, graph shape, cost limits and PII rules.
// It never returns an error for invalid input; problems are in the result.
func ValidateResource(r Resource, deps Resolver) Validation {
	return validate(r, deps)
}

// Dependencies lists the resources a spec reads (schemas with versions,
// segments, lookups, gates, experiments, sub-decisions by name).
func Dependencies(r Resource) ([]Ref, error) { return dependencies(r) }

// CheckSchemaCompatibility compares a proposed schema spec with the previous
// version. Breaking: removed property, newly required, narrowed type/enum,
// removed null.
func CheckSchemaCompatibility(prev, next json.RawMessage) (compatible bool, changes []SchemaChange, err error) {
	return checkSchemaCompatibility(prev, next)
}

// SchemaChange is one difference between two schema versions.
type SchemaChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Breaking bool   `json:"breaking"`
}

// ValidateDocument checks a JSON document against a schema spec.
func ValidateDocument(schemaSpec json.RawMessage, doc json.RawMessage) (valid bool, errs []FieldError, err error) {
	return validateDocument(schemaSpec, doc)
}

// FieldError is one document validation error.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Compile builds an immutable, concurrency-safe Snapshot. It fails if any
// resource is invalid, if Set requires a newer engine, or if limits are
// exceeded. Compile does not run test cases; call (*Snapshot).RunTests.
func Compile(set Set) (*Snapshot, error) { return CompileWithOptions(set, CompileOptions{}) }

// Snapshot is a compiled Set. Safe for concurrent use; never mutated.
type Snapshot struct {
	set Set
	c   *compiled
}

// Generation of the set this snapshot was compiled from.
func (s *Snapshot) Generation() int64 { return s.set.Generation }

// DecisionInfo describes one decision available in the snapshot.
type DecisionInfo struct {
	Name    string `json:"name"`
	Version int64  `json:"version"`
	Kind    string `json:"kind"` // graph | rules | table | transform
}

// Decisions lists the live decisions.
func (s *Snapshot) Decisions() []DecisionInfo {
	if s == nil || s.c == nil {
		return nil
	}
	return s.c.decisionInfos()
}

// Request is one decision call.
type Request struct {
	Decision   string
	Input      json.RawMessage
	UnitID     string
	RequestID  string
	Now        time.Time // zero = time.Now() at call start
	PinVersion int64     // 0 = live (respecting rollouts)
	Trace      bool      // per-node timings
	// Debug adds each node's input and output to the path (test console only).
	Debug bool
	// AI is called for ai nodes; nil means AI nodes fail and take onError.
	AI AICaller
}

// AICaller performs one AI node call (implemented by the sidecar's Hub
// client). The engine renders the prompt, applies the timeout from the node,
// validates the answer against responseSchema and handles shadow mode.
type AICaller interface {
	Call(ctx context.Context, call AICall) (AIAnswer, error)
}

// AICall is one rendered AI request.
type AICall struct {
	Decision        string
	DecisionVersion int64
	Node            string
	Model           string
	System          string
	User            string
	ResponseSchema  json.RawMessage
	MaxOutputTokens int
	Timeout         time.Duration
	CallID          string
}

// AIAnswer is the Hub's reply.
type AIAnswer struct {
	Output       json.RawMessage
	Model        string
	InputTokens  int
	OutputTokens int
}

// Step is one executed node.
type Step struct {
	NodeID   string          `json:"nodeId"`
	NodeType string          `json:"nodeType"`
	Outcome  string          `json:"outcome"`
	Micros   int64           `json:"micros,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// Result of a decision call. Field meanings match DecideResult in the
// OpenAPI contract and DecideResponse in engine.proto.
type Result struct {
	Output          json.RawMessage `json:"output"`
	DecisionVersion string          `json:"decisionVersion"` // "lead_routing@17"
	DependencyHash  string          `json:"dependencyHash"`
	Generation      int64           `json:"generation"`
	FallbackUsed    bool            `json:"fallbackUsed"`
	EvalMicros      int64           `json:"evalMicros"`
	EvaluatedAt     time.Time       `json:"evaluatedAt"`
	Path            []Step          `json:"path"`
	Exposures       []Exposure      `json:"-"` // for the exposure log
}

// Exposure is one experiment assignment made during a call.
type Exposure struct {
	Experiment        string
	ExperimentVersion int64
	Variant           string
	Reason            string
}

// Error is returned by Decide for caller errors. Reason values:
// DECISION_NOT_FOUND, INPUT_SCHEMA, INPUT_TOO_LARGE, NODE_ERROR.
type Error struct {
	Reason  string
	Message string
	Fields  []FieldError
	Node    string
}

func (e *Error) Error() string { return e.Reason + ": " + e.Message }

// Decide evaluates one decision.
func (s *Snapshot) Decide(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.c == nil {
		return Result{}, errNotReady
	}
	return s.c.decide(ctx, req, nil, false)
}

// GateResult and VariantResult mirror the RPCs of the same name.
type GateResult struct {
	Value   bool
	Version int64
	RuleID  string
}

type VariantResult struct {
	InExperiment bool
	Variant      string
	Parameters   json.RawMessage
	Reason       string
	Version      int64
}

// CheckGate evaluates a gate for a unit.
func (s *Snapshot) CheckGate(gate, unitID string, attributes json.RawMessage) (GateResult, error) {
	if s == nil || s.c == nil {
		return GateResult{}, errNotReady
	}
	return s.c.apiGate(gate, unitID, attributes)
}

// GetVariant assigns a unit to an experiment variant.
func (s *Snapshot) GetVariant(experiment, unitID string, attributes json.RawMessage) (VariantResult, error) {
	if s == nil || s.c == nil {
		return VariantResult{}, errNotReady
	}
	return s.c.apiVariant(experiment, unitID, attributes)
}

// CheckSegment tests membership (used by the control plane's check endpoint).
func (s *Snapshot) CheckSegment(segment, key, unit string, attributes json.RawMessage) (member bool, reason string, err error) {
	if s == nil || s.c == nil {
		return false, "", errNotReady
	}
	return s.c.apiSegment(segment, key, unit, attributes)
}

// TestResult is one test case outcome; fields match TestRunResult.results[].
type TestResult struct {
	Name       string          `json:"name"`
	OK         bool            `json:"ok"`
	Actual     json.RawMessage `json:"actual,omitempty"`
	Expected   json.RawMessage `json:"expected,omitempty"`
	Mismatches []Mismatch      `json:"mismatches,omitempty"`
	Error      string          `json:"error,omitempty"`
	Micros     int64           `json:"micros"`
	Path       []Step          `json:"path,omitempty"`
}

type Mismatch struct {
	Path     string          `json:"path"`
	Expected json.RawMessage `json:"expected"`
	Actual   json.RawMessage `json:"actual"`
}

// RunTests runs the test cases of one decision (all decisions when name is
// empty). Version 0 = live version.
func (s *Snapshot) RunTests(ctx context.Context, decision string, version int64) ([]TestResult, error) {
	if s == nil || s.c == nil {
		return nil, errNotReady
	}
	return s.c.runTests(ctx, decision, version)
}

// Benchmark runs each test case n times and returns p99 micros per decision
// (publish-time budget check).
func (s *Snapshot) Benchmark(ctx context.Context, n int) (map[string]int64, error) {
	if s == nil || s.c == nil {
		return nil, errNotReady
	}
	return s.c.benchmark(ctx, n)
}

// Catalogue returns the function/operator catalogue for GET /meta/expr.
func Catalogue() json.RawMessage {
	return append(json.RawMessage(nil), catalogueJSON...)
}
