package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

// decideResult is DecideResult / EvaluateResult on the wire.
type decideResult struct {
	Output          json.RawMessage `json:"output"`
	DecisionVersion string          `json:"decisionVersion"`
	DependencyHash  string          `json:"dependencyHash"`
	Generation      *int64          `json:"generation,omitempty"`
	FallbackUsed    bool            `json:"fallbackUsed"`
	EvalMicros      int64           `json:"evalMicros"`
	RequestID       string          `json:"requestId,omitempty"`
	EvaluatedAt     time.Time       `json:"evaluatedAt"`
	Path            []engine.Step   `json:"path"`
	Error           *Problem        `json:"error,omitempty"`
}

func toDecideResult(res engine.Result, requestID string, withGen bool) decideResult {
	out := decideResult{
		Output: res.Output, DecisionVersion: res.DecisionVersion, DependencyHash: res.DependencyHash,
		FallbackUsed: res.FallbackUsed, EvalMicros: res.EvalMicros, RequestID: requestID,
		EvaluatedAt: res.EvaluatedAt.UTC(), Path: res.Path,
	}
	if len(out.Output) == 0 {
		out.Output = json.RawMessage("null")
	}
	if out.Path == nil {
		out.Path = []engine.Step{}
	}
	if withGen {
		g := res.Generation
		out.Generation = &g
	}
	return out
}

// engineErrorProblem maps a Decide error.
func engineErrorProblem(err error, status int) *Problem {
	var ee *engine.Error
	if !errors.As(err, &ee) {
		return nil
	}
	code := "INVALID_ARGUMENT"
	switch ee.Reason {
	case "DECISION_NOT_FOUND", "GATE_NOT_FOUND", "EXPERIMENT_NOT_FOUND", "SEGMENT_NOT_FOUND":
		return errNotFound("%s", ee.Message)
	case "NODE_ERROR":
		code = "NODE_ERROR"
	}
	p := problemf(status, code, strings.ReplaceAll(strings.ToLower(ee.Reason), "_", " "), "%s", ee.Message)
	if ee.Reason == "INPUT_SCHEMA" || ee.Reason == "INPUT_TOO_LARGE" {
		p.Code = ee.Reason
		if status == http.StatusBadRequest {
			p.Code = "INVALID_ARGUMENT"
		}
	}
	for _, f := range ee.Fields {
		p.Errors = append(p.Errors, FieldError{Path: "/input" + strings.TrimSuffix(f.Path, "/"), Code: ee.Reason, Message: f.Message})
	}
	if ee.Node != "" {
		p.Errors = append(p.Errors, FieldError{Path: "/nodes/" + ee.Node, Code: ee.Reason, Message: ee.Message})
	}
	return p
}

type sourceSel struct {
	Source      string `json:"source"`
	Version     int64  `json:"version"`
	Environment string `json:"environment"`
}

// targetDecision resolves source/version to a resource for an eval set.
func (s *Server) target(ctx context.Context, rec model.ResourceRecord, sel sourceSel) (engine.Resource, *model.DataRef, error) {
	if sel.Source == "version" || (sel.Source == "" && sel.Version > 0) {
		if sel.Version < 1 {
			return engine.Resource{}, nil, errInvalid("version is required when source is version")
		}
		v, err := s.loadVersion(ctx, rec.App, rec.Kind, rec.Name, sel.Version)
		if errors.Is(err, store.ErrNotFound) {
			return engine.Resource{}, nil, errNotFound("%s/%s has no version %d.", rec.Kind, rec.Name, sel.Version)
		}
		if err != nil {
			return engine.Resource{}, nil, err
		}
		return engine.Resource{Kind: engine.Kind(rec.Kind), Name: rec.Name, Version: v.Version, Spec: v.Spec}, v.Data, nil
	}
	return engine.Resource{Kind: engine.Kind(rec.Kind), Name: rec.Name, Version: 0, Spec: rec.Draft.Spec}, rec.Draft.Data, nil
}

// compileTarget builds and compiles an eval set for a resource.
func (s *Server) compileTarget(ctx context.Context, rec model.ResourceRecord, sel sourceSel) (engineapi.Snapshot, engine.Resource, error) {
	if sel.Environment == "" {
		sel.Environment = "dev"
	}
	t, data, err := s.target(ctx, rec, sel)
	if err != nil {
		return nil, t, err
	}
	set, errs, err := s.evalSet(ctx, rec.App, sel.Environment, t, data)
	if err != nil {
		return nil, t, err
	}
	if len(errs) > 0 {
		return nil, t, errUnprocessable("VALIDATION_FAILED", "Dependencies missing", "%s", errs[0].Message).with(errs...)
	}
	snap, err := s.eng.Compile(set)
	if err != nil {
		return nil, t, compileProblem(err, nil)
	}
	return snap, t, nil
}

func (s *Server) handleEvaluate(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindDecisions, r.PathValue("name"))
	if err != nil {
		return err
	}
	var in struct {
		sourceSel
		Input json.RawMessage `json:"input"`
		Ctx   struct {
			Now  *time.Time `json:"now"`
			Unit string     `json:"unit"`
		} `json:"ctx"`
		SkipAI *bool `json:"skipAI"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	snap, t, err := s.compileTarget(ctx, rec, in.sourceSel)
	if err != nil {
		return err
	}
	req := engine.Request{Decision: rec.Name, Input: in.Input, UnitID: in.Ctx.Unit, PinVersion: t.Version, Trace: true, Debug: true}
	if in.Ctx.Now != nil {
		req.Now = *in.Ctx.Now
	}
	res, err := snap.Decide(ctx, req)
	if err != nil {
		pr := engineErrorProblem(err, http.StatusUnprocessableEntity)
		if pr == nil {
			return err
		}
		if pr.Code != "NODE_ERROR" {
			return pr
		}
		out := toDecideResult(engine.Result{DecisionVersion: label(rec.Name, t.Version), EvaluatedAt: s.now()}, "", false)
		pr.Type, pr.Instance = "about:blank", r.URL.Path
		out.Error = pr
		writeJSON(w, http.StatusOK, out)
		return nil
	}
	out := toDecideResult(res, "", false)
	if t.Version == 0 && strings.HasSuffix(out.DecisionVersion, "@0") {
		out.DecisionVersion = rec.Name + "@draft"
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func label(name string, v int64) string {
	if v == 0 {
		return name + "@draft"
	}
	return name + "@" + strconv.FormatInt(v, 10)
}

type testRunResult struct {
	Passed  int                 `json:"passed"`
	Failed  int                 `json:"failed"`
	Results []engine.TestResult `json:"results"`
}

func (s *Server) handleRunTests(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindDecisions, r.PathValue("name"))
	if err != nil {
		return err
	}
	var in sourceSel
	if err := decodeOptionalJSON(r, &in); err != nil {
		return err
	}
	snap, t, err := s.compileTarget(ctx, rec, in)
	if err != nil {
		return err
	}
	results, err := snap.RunTests(ctx, rec.Name, t.Version)
	if err != nil {
		if pr := engineErrorProblem(err, http.StatusUnprocessableEntity); pr != nil {
			return pr
		}
		return err
	}
	out := testRunResult{Results: results}
	if out.Results == nil {
		out.Results = []engine.TestResult{}
	}
	for _, t := range results {
		if t.OK {
			out.Passed++
		} else {
			out.Failed++
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// ---------------------------------------------------------------- shadow report

// aiEntry is one DecisionLogRecord.ai[] item. `result` is the AI's answer
// label, or timeout / schema_error / error. `decided` (an extension, see
// contracts/CHANGES-REQUESTED.md) is the label of the path that decided;
// without it the record's output is used.
type aiEntry struct {
	Node      string         `json:"node"`
	Model     string         `json:"model"`
	LatencyMs int64          `json:"latencyMs"`
	CacheHit  bool           `json:"cacheHit"`
	Result    string         `json:"result"`
	Decided   string         `json:"decided"`
	Vars      map[string]any `json:"vars"`
}

func outputLabel(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"route", "label", "decision", "result", "lane"} {
			if v, ok := m[k].(string); ok {
				return v
			}
		}
	}
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func (s *Server) handleShadowReport(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, name, node := r.PathValue("app"), r.PathValue("name"), r.PathValue("nodeId")
	if _, _, err := s.loadResource(ctx, p, app, model.KindDecisions, name); err != nil {
		return err
	}
	q := r.URL.Query()
	env := q.Get("environment")
	if env == "" {
		env = "prod"
	}
	days := 7
	if d := q.Get("days"); d != "" {
		days, _ = strconv.Atoi(d)
	}
	eq := map[string]string{"app": app, "env": env, "decision": name}
	if v := q.Get("version"); v != "" {
		eq["version"] = v
	}
	recs, _, err := s.st.QueryRecords(ctx, store.RecDecisions, store.RecordQuery{Eq: eq, From: s.now().Add(-time.Duration(days) * 24 * time.Hour)}, 200000)
	if err != nil {
		return err
	}
	rep := model.ShadowReport{Matrix: model.ShadowMatrix{Labels: []string{}, Rows: [][]int{}}, Disagreements: []model.ShadowDisagreement{}}
	type pair struct{ decided, ai string }
	counts := map[pair]int{}
	labels := map[string]bool{}
	var lat []int64
	timeouts, schemaFails, agree, compared := 0, 0, 0, 0
	for _, rr := range recs {
		var d struct {
			RequestID string          `json:"requestId"`
			Output    json.RawMessage `json:"output"`
			AI        []aiEntry       `json:"ai"`
		}
		if json.Unmarshal(rr.Data, &d) != nil {
			continue
		}
		for _, a := range d.AI {
			if a.Node != node {
				continue
			}
			rep.Calls++
			lat = append(lat, a.LatencyMs)
			switch a.Result {
			case "timeout":
				timeouts++
				continue
			case "schema_error", "schema_failure", "invalid":
				schemaFails++
				continue
			case "error", "":
				continue
			}
			decided := a.Decided
			if decided == "" {
				decided = outputLabel(d.Output)
			}
			compared++
			labels[decided], labels[a.Result] = true, true
			counts[pair{decided, a.Result}]++
			if decided == a.Result {
				agree++
			} else if len(rep.Disagreements) < 50 {
				rep.Disagreements = append(rep.Disagreements, model.ShadowDisagreement{RequestID: d.RequestID, Decided: decided, AI: a.Result, Vars: a.Vars})
			}
		}
	}
	for l := range labels {
		rep.Matrix.Labels = append(rep.Matrix.Labels, l)
	}
	sort.Strings(rep.Matrix.Labels)
	for _, a := range rep.Matrix.Labels {
		row := make([]int, len(rep.Matrix.Labels))
		for j, b := range rep.Matrix.Labels {
			row[j] = counts[pair{a, b}]
		}
		rep.Matrix.Rows = append(rep.Matrix.Rows, row)
	}
	if compared > 0 {
		rep.Agreement = float64(agree) / float64(compared)
	}
	if rep.Calls > 0 {
		rep.TimeoutRate = float64(timeouts) / float64(rep.Calls)
		rep.SchemaFailureRate = float64(schemaFails) / float64(rep.Calls)
		rep.Latency = &model.ShadowLatency{P50Ms: percentile(lat, 0.5), P95Ms: percentile(lat, 0.95)}
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}
