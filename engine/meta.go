// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"strings"
)

// DecisionMeta describes how the sidecar should treat a decision's calls
// (budget, logging). It never contains PII paths: logging fields that read
// PII are rejected at validation.
type DecisionMeta struct {
	Name         string   `json:"name"`
	Version      int64    `json:"version"`
	Kind         string   `json:"kind"`
	BudgetMicros int64    `json:"budgetMicros"`
	LogSample    *float64 `json:"logSample,omitempty"`
	LogFields    []string `json:"logFields,omitempty"`
	AIPayloads   bool     `json:"aiPayloads"`
	HasAI        bool     `json:"hasAI"`
}

// DecisionMeta returns metadata for a decision version (0 = live).
func (s *Snapshot) DecisionMeta(name string, version int64) (DecisionMeta, bool) {
	if s == nil || s.c == nil {
		return DecisionMeta{}, false
	}
	d := s.c.findDecision(name, version)
	if d == nil {
		return DecisionMeta{}, false
	}
	m := DecisionMeta{Name: d.name, Version: d.version, Kind: d.kind, BudgetMicros: d.budget, LogSample: d.logSample, AIPayloads: d.aiPayloads, HasAI: d.hasAI}
	for _, f := range d.logFields {
		m.LogFields = append(m.LogFields, f.name)
	}
	return m, true
}

func (c *compiled) findDecision(name string, version int64) *decision {
	e := c.decisions[name]
	if e == nil {
		return nil
	}
	if version > 0 {
		return e.versions[version]
	}
	return e.live
}

// findByVersionText resolves "name@17".
func (c *compiled) findByVersionText(v string) *decision {
	i := strings.LastIndexByte(v, '@')
	if i < 0 {
		return nil
	}
	e := c.decisions[v[:i]]
	if e == nil {
		return nil
	}
	for _, d := range e.versions {
		if d.versionText == v {
			return d
		}
	}
	return nil
}

// LogFields picks the decision's logging.fields from an input and output
// (decisionVersion as returned in Result, "name@17"). Keys are the field
// names as written in the spec. Fields that read PII never appear.
func (s *Snapshot) LogFields(decisionVersion string, input, output json.RawMessage) (in, out map[string]any) {
	if s == nil || s.c == nil {
		return nil, nil
	}
	d := s.c.findByVersionText(decisionVersion)
	if d == nil || len(d.logFields) == 0 {
		return nil, nil
	}
	var iv, ov any
	_ = json.Unmarshal(input, &iv)
	_ = json.Unmarshal(output, &ov)
	for _, f := range d.logFields {
		src, dst := iv, &in
		if f.output {
			src, dst = ov, &out
		}
		v, ok := getPath(src, f.parts)
		if !ok {
			continue
		}
		if *dst == nil {
			*dst = map[string]any{}
		}
		(*dst)[f.name] = v
	}
	return in, out
}

// App and Environment of the compiled set.
func (s *Snapshot) App() string         { return s.set.App }
func (s *Snapshot) Environment() string { return s.set.Environment }

// Set returns the set the snapshot was compiled from.
func (s *Snapshot) Set() Set { return s.set }

// DecideParsed is Decide with an input that is already decoded JSON (nil,
// bool, float64, string, []any, map[string]any); req.Input is ignored. The
// engine never modifies the input.
func (s *Snapshot) DecideParsed(ctx context.Context, req Request, input any) (Result, error) {
	if s == nil || s.c == nil {
		return Result{}, &Error{Reason: "NOT_READY", Message: "snapshot is not compiled"}
	}
	return s.c.decide(ctx, req, input, true)
}

var errNotReady = &Error{Reason: "NOT_READY", Message: "snapshot is not compiled"}
