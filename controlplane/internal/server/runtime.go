package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

const runtimeSidecarID = "control-plane"

func (s *Server) runtimeAuth(ctx context.Context, p *Principal, app, env string) (engineapi.Snapshot, model.EnvState, error) {
	if p.Kind == "credential" {
		if p.CredApp != app || p.CredEnv != env {
			return nil, model.EnvState{}, errForbidden("This credential is for %s/%s.", p.CredApp, p.CredEnv)
		}
	} else if err := p.require(model.RoleViewer, app); err != nil {
		return nil, model.EnvState{}, err
	}
	return s.runtimeSnapshot(ctx, app, env)
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env := r.PathValue("app"), r.PathValue("env")
	var in struct {
		Decision  string          `json:"decision"`
		Input     json.RawMessage `json:"input"`
		UnitID    string          `json:"unitId"`
		RequestID string          `json:"requestId"`
		Options   struct {
			Trace      bool       `json:"trace"`
			PinVersion int64      `json:"pinVersion"`
			Now        *time.Time `json:"now"`
			ForceLog   bool       `json:"forceLog"`
		} `json:"options"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	snap, st, err := s.runtimeAuth(ctx, p, app, env)
	if err != nil {
		return err
	}
	if in.RequestID == "" {
		in.RequestID = auth.RandomID("req_")
	}
	req := engine.Request{Decision: in.Decision, Input: in.Input, UnitID: in.UnitID, RequestID: in.RequestID, PinVersion: in.Options.PinVersion, Trace: in.Options.Trace}
	if in.Options.Now != nil {
		req.Now = *in.Options.Now
	}
	res, err := snap.Decide(ctx, req)
	if err != nil {
		var ee *engine.Error
		if errors.As(err, &ee) && ee.Reason == "NODE_ERROR" {
			return problemf(http.StatusInternalServerError, "NODE_ERROR", "Decision failed", "%s", ee.Message)
		}
		if pr := engineErrorProblem(err, http.StatusBadRequest); pr != nil {
			return pr
		}
		return err
	}
	if res.Generation == 0 {
		res.Generation = st.Generation
	}
	out := toDecideResult(res, in.RequestID, true)
	s.logDecision(ctx, app, env, st, in.UnitID, in.RequestID, out, res.Exposures, in.Options.ForceLog)
	writeJSON(w, http.StatusOK, out)
	return nil
}

// logDecision stores a sampled decision record and every exposure.
func (s *Server) logDecision(ctx context.Context, app, env string, st model.EnvState, unit, requestID string, out decideResult, exps []engine.Exposure, force bool) {
	now := s.now()
	exp := now.Add(s.cfg.LogRetention)
	if force || rand.Float64() < st.Policy.LogSampleRate {
		path := make([]engine.Step, 0, len(out.Path))
		for _, st := range out.Path {
			st.Input, st.Output = nil, nil // never store payloads (PII)
			path = append(path, st)
		}
		status := "OK"
		rec := map[string]any{
			"type": "decision", "ts": now, "requestId": requestID, "sidecarId": runtimeSidecarID,
			"decision": out.DecisionVersionName(), "decisionVersion": out.DecisionVersion, "dependencyHash": out.DependencyHash,
			"generation": st.Generation, "evaluatedAt": out.EvaluatedAt, "path": path, "fallbackUsed": out.FallbackUsed,
			"evalMicros": out.EvalMicros, "status": status,
		}
		if unit != "" {
			rec["unitId"] = unit
		}
		data, _ := json.Marshal(rec)
		f := map[string]string{"app": app, "env": env, "decision": out.DecisionVersionName(), "requestId": requestID, "unitId": unit, "fallback": strconv.FormatBool(out.FallbackUsed), "version": out.versionPart()}
		_ = s.st.AppendRecords(context.WithoutCancel(ctx), store.RecDecisions, []store.Record{{ID: store.NewRecordID(now), TS: now, Fields: f, Data: data, ExpireAt: exp}})
	}
	var recs []store.Record
	for _, e := range exps {
		recs = append(recs, s.exposureRecord(app, env, unit, requestID, e.Experiment, fmt.Sprintf("%s@%d", e.Experiment, e.ExperimentVersion), e.Variant, e.Reason))
	}
	if len(recs) > 0 {
		_ = s.st.AppendRecords(context.WithoutCancel(ctx), store.RecExposures, recs)
	}
}

func (s *Server) exposureRecord(app, env, unit, requestID, experiment, version, variant, reason string) store.Record {
	now := s.now()
	rec := map[string]any{"type": "exposure", "ts": now, "unitId": unit, "experiment": experiment, "experimentVersion": version, "reason": reason, "sidecarId": runtimeSidecarID}
	if variant != "" {
		rec["variant"] = variant
	}
	if requestID != "" {
		rec["requestId"] = requestID
	}
	data, _ := json.Marshal(rec)
	return store.Record{ID: store.NewRecordID(now), TS: now, Fields: map[string]string{"app": app, "env": env, "experiment": experiment, "unitId": unit}, Data: data, ExpireAt: now.Add(s.cfg.LogRetention)}
}

// DecisionVersionName returns "lead_routing" from "lead_routing@17".
func (d decideResult) DecisionVersionName() string {
	for i := len(d.DecisionVersion) - 1; i >= 0; i-- {
		if d.DecisionVersion[i] == '@' {
			return d.DecisionVersion[:i]
		}
	}
	return d.DecisionVersion
}

func (d decideResult) versionPart() string {
	n := d.DecisionVersionName()
	if len(n) < len(d.DecisionVersion) {
		return d.DecisionVersion[len(n)+1:]
	}
	return ""
}

func (s *Server) handleCheckGate(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env, name := r.PathValue("app"), r.PathValue("env"), r.PathValue("name")
	var in struct {
		UnitID     string          `json:"unitId"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	snap, st, err := s.runtimeAuth(ctx, p, app, env)
	if err != nil {
		return err
	}
	g, err := snap.CheckGate(name, in.UnitID, in.Attributes)
	if err != nil {
		if pr := engineErrorProblem(err, http.StatusBadRequest); pr != nil {
			return pr
		}
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": g.Value, "gateVersion": fmt.Sprintf("%s@%d", name, g.Version), "ruleId": g.RuleID, "generation": st.Generation})
	return nil
}

func (s *Server) handleAssignVariant(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env, name := r.PathValue("app"), r.PathValue("env"), r.PathValue("name")
	var in struct {
		UnitID     string          `json:"unitId"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	snap, st, err := s.runtimeAuth(ctx, p, app, env)
	if err != nil {
		return err
	}
	v, err := snap.GetVariant(name, in.UnitID, in.Attributes)
	if err != nil {
		if pr := engineErrorProblem(err, http.StatusBadRequest); pr != nil {
			return pr
		}
		return err
	}
	ver := fmt.Sprintf("%s@%d", name, v.Version)
	out := map[string]any{"inExperiment": v.InExperiment, "reason": v.Reason, "experimentVersion": ver, "generation": st.Generation}
	if v.Variant != "" {
		out["variant"] = v.Variant
	}
	if len(v.Parameters) > 0 {
		out["parameters"] = v.Parameters
	}
	if v.InExperiment {
		_ = s.st.AppendRecords(context.WithoutCancel(ctx), store.RecExposures, []store.Record{s.exposureRecord(app, env, in.UnitID, "", name, ver, v.Variant, v.Reason)})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
