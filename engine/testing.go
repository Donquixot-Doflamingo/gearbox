// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (c *compiled) runTests(ctx context.Context, name string, version int64) ([]TestResult, error) {
	var targets []*decision
	if name == "" {
		for _, info := range c.decisionInfos() {
			targets = append(targets, c.decisions[info.Name].live)
		}
	} else {
		e := c.decisions[name]
		if e == nil {
			return nil, &Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("decision %q is not in the snapshot", name)}
		}
		d := e.live
		if version > 0 {
			d = e.versions[version]
			if d == nil {
				return nil, &Error{Reason: "DECISION_NOT_FOUND", Message: fmt.Sprintf("version %d of %q is not in the snapshot", version, name)}
			}
		}
		targets = append(targets, d)
	}
	var out []TestResult
	for _, d := range targets {
		for _, tc := range d.tests {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			out = append(out, c.runTest(ctx, d, tc))
		}
	}
	if out == nil {
		out = []TestResult{}
	}
	return out, nil
}

func (c *compiled) runTest(ctx context.Context, d *decision, tc testCase) TestResult {
	name := tc.name
	if len(c.decisions) > 1 {
		name = d.name + ": " + tc.name
	}
	res := TestResult{Name: name}
	now := tc.now
	if now.IsZero() {
		now = time.Now()
	}
	var input any
	if len(tc.input) > 0 {
		if err := json.Unmarshal(tc.input, &input); err != nil {
			res.Error = "input is not valid JSON"
			return res
		}
	}
	start := time.Now()
	r, err := c.decideVersion(ctx, d, Request{Decision: d.name, UnitID: tc.unit, RequestID: "test", Now: now, Debug: true}, input)
	res.Micros = time.Since(start).Microseconds()
	res.Path = r.Path
	if err != nil {
		var ee *Error
		code := err.Error()
		if errors.As(err, &ee) {
			code = ee.Reason
		}
		if tc.expectError != "" && tc.expectError == code {
			res.OK = true
			return res
		}
		res.Error = err.Error()
		return res
	}
	res.Actual = r.Output
	if tc.expectError != "" {
		res.Error = fmt.Sprintf("expected error %s, got output", tc.expectError)
		res.Expected = encodeJSON(map[string]any{"error": tc.expectError})
		return res
	}
	res.OK = true
	if tc.hasExpect {
		res.Expected = encodeJSON(tc.expect)
		var actual any
		_ = json.Unmarshal(r.Output, &actual)
		res.Mismatches = partialMatch(tc.expect, actual, "")
		if len(res.Mismatches) > 0 {
			res.OK = false
		}
	}
	if len(tc.expectPath) > 0 {
		var got []string
		for _, s := range r.Path {
			got = append(got, s.NodeID+":"+s.Outcome)
		}
		if strings.Join(got, ",") != strings.Join(tc.expectPath, ",") {
			res.OK = false
			res.Mismatches = append(res.Mismatches, Mismatch{Path: "path", Expected: encodeJSON(toAnyList(tc.expectPath)), Actual: encodeJSON(toAnyList(got))})
		}
	}
	return res
}

func toAnyList(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// partialMatch compares expected with actual: objects match on listed keys
// (dotted keys address nested fields), everything else must be equal.
func partialMatch(expected, actual any, path string) []Mismatch {
	em, ok := expected.(map[string]any)
	if !ok {
		if valuesEqual(expected, actual) {
			return nil
		}
		return []Mismatch{{Path: orRoot(path), Expected: encodeJSON(expected), Actual: encodeJSON(actual)}}
	}
	am, _ := actual.(map[string]any)
	keys := make([]string, 0, len(em))
	for k := range em {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Mismatch
	for _, k := range keys {
		var av any
		found := false
		if am != nil {
			av, found = am[k]
		}
		p := path + "/" + ptrEscape(k)
		if !found && strings.Contains(k, ".") && am != nil {
			if parts, err := parseFieldPath(k); err == nil {
				av, found = getPath(am, parts)
				p = path + "/" + strings.ReplaceAll(k, ".", "/")
			}
		}
		if !found {
			out = append(out, Mismatch{Path: p, Expected: encodeJSON(em[k]), Actual: json.RawMessage("null")})
			continue
		}
		out = append(out, partialMatch(em[k], av, p)...)
	}
	return out
}

// decideVersion runs a specific compiled decision (tests, benchmarks).
func (c *compiled) decideVersion(ctx context.Context, d *decision, req Request, input any) (Result, error) {
	req.PinVersion = d.version
	if d.version == 0 {
		// Drafts are always the only version present.
		req.PinVersion = 0
	}
	return c.decide(ctx, req, input, true)
}

func (c *compiled) benchmark(ctx context.Context, n int) (map[string]int64, error) {
	if n <= 0 {
		n = 1000
	}
	out := map[string]int64{}
	for _, info := range c.decisionInfos() {
		d := c.decisions[info.Name].live
		var samples []int64
		for _, tc := range d.tests {
			var input any
			if len(tc.input) > 0 {
				if err := json.Unmarshal(tc.input, &input); err != nil {
					continue
				}
			}
			now := tc.now
			if now.IsZero() {
				now = time.Now()
			}
			req := Request{Decision: d.name, UnitID: tc.unit, RequestID: "bench", Now: now}
			for i := 0; i < n; i++ {
				if i%64 == 0 {
					if err := ctx.Err(); err != nil {
						return out, err
					}
				}
				start := time.Now()
				_, _ = c.decideVersion(ctx, d, req, deepCopy(input))
				samples = append(samples, time.Since(start).Nanoseconds())
			}
		}
		if len(samples) == 0 {
			continue
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		idx := int(float64(len(samples))*0.99+0.5) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(samples) {
			idx = len(samples) - 1
		}
		micros := (samples[idx] + 999) / 1000
		out[d.name] = micros
	}
	return out, nil
}
