// Package seed creates the demo app (lead_service) through the API, so the
// demo exercises the same validation as real use.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ORG/gearbox/controlplane/internal/client"
	"github.com/ORG/gearbox/controlplane/internal/defs"
)

// App is the demo app name.
const App = "lead_service"

// Result is what the seeder created.
type Result struct {
	Generation     int64
	CredentialID   string
	Credential     string // secret (shown once)
	AlreadySeeded  bool
	ResourcesCount int
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// DemoFiles are the demo definitions (the OpenAPI examples plus what they
// reference).
func DemoFiles() []defs.File {
	return []defs.File{
		{Kind: "schemas", Name: "lead_input", Description: "A used-car lead as sent by the lead service", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["lead", "car"], "properties": {
    "lead": {"type": "object", "required": ["id", "city"], "properties": {
      "id": {"type": "string"}, "first_name": {"type": ["string", "null"]}, "last_name": {"type": ["string", "null"]},
      "phone": {"type": "string", "x-gearbox-pii": true}, "city": {"type": "string"}, "source": {"enum": ["app", "web", "partner"]}}},
    "car": {"type": "object", "required": ["year", "km", "price"], "properties": {
      "make": {"type": "string"}, "model": {"type": "string"}, "year": {"type": "integer", "minimum": 1980},
      "km": {"type": "integer", "minimum": 0}, "price": {"type": "number", "minimum": 0}}},
    "inspections": {"type": "array", "maxItems": 50, "items": {"type": "object", "required": ["part", "score"],
      "properties": {"part": {"type": "string"}, "score": {"type": "number"}}}}}},
  "examples": [{"lead": {"id": "L-1", "city": "Pune", "source": "web"}, "car": {"year": 2021, "km": 12000, "price": 690000}}]}`)},
		{Kind: "schemas", Name: "eligibility_input", Description: "Normalised lead for the eligibility rules", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["city", "ageYears", "km", "price", "inspectionAvg"], "properties": {
    "city": {"type": "string"}, "source": {"enum": ["app", "web", "partner"]}, "ageYears": {"type": "integer"},
    "km": {"type": "integer", "minimum": 0}, "price": {"type": "number", "minimum": 0}, "inspectionAvg": {"type": "number"}}}}`)},
		{Kind: "schemas", Name: "eligibility_output", Description: "Eligibility verdict", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["eligible", "reason"], "properties": {
    "eligible": {"type": "boolean"}, "lane": {"enum": ["fast", "standard"]}, "reason": {"type": "string"}}}}`)},
		{Kind: "schemas", Name: "routing_output", Description: "Where the lead goes", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["route"], "properties": {
    "route": {"enum": ["fast_track", "standard", "reject", "manual_review"]}, "reason": {"type": "string"}}}}`)},
		{Kind: "schemas", Name: "segment_attrs_city_price", Description: "Attributes for city/price segments", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["city", "price"], "properties": {"city": {"type": "string"}, "price": {"type": "number"}}}}`)},
		{Kind: "schemas", Name: "city_zone_value", Description: "Value of the city_zone lookup", Spec: raw(`{
  "jsonSchema": {"type": "object", "required": ["zone", "tier"], "properties": {"zone": {"enum": ["north", "south", "east", "west"]}, "tier": {"type": "integer", "minimum": 1, "maximum": 3}}}}`)},

		{Kind: "segments", Name: "blocked_cities", Description: "Cities we do not serve", Spec: raw(`{"type": "static", "static": {"keyType": "string"}}`),
			SegmentMembers: []string{"Leh", "Port Blair", "Srinagar"}},
		{Kind: "segments", Name: "metro_premium", Description: "Leads in a metro city for cars above 8 lakh", Spec: raw(`{"type": "dynamic", "dynamic": {
  "attributesSchema": {"name": "segment_attrs_city_price", "version": 1},
  "when": {"all": [{"field": "city", "op": "IN", "value": ["Delhi", "Gurgaon", "Mumbai", "Bengaluru"]}, {"field": "price", "op": "GT", "value": 800000}]}}}`)},
		{Kind: "segments", Name: "beta_10pct", Description: "A stable 10% of leads", Spec: raw(`{"type": "rollout", "rollout": {"percent": 10, "salt": "beta_10pct"}}`)},

		{Kind: "lookups", Name: "city_zone", Description: "City to sales zone", Spec: raw(`{"keyType": "string", "valueSchema": {"name": "city_zone_value", "version": 1}}`),
			LookupRows: map[string]json.RawMessage{
				"Bengaluru": raw(`{"zone": "south", "tier": 1}`), "Delhi": raw(`{"zone": "north", "tier": 1}`),
				"Gurgaon": raw(`{"zone": "north", "tier": 1}`), "Mumbai": raw(`{"zone": "west", "tier": 1}`),
				"Pune": raw(`{"zone": "west", "tier": 2}`), "Kolkata": raw(`{"zone": "east", "tier": 1}`),
			}},

		{Kind: "gates", Name: "new_inspection_flow", Description: "New inspection booking flow", Spec: raw(`{
  "enabled": true, "attributesSchema": {"name": "segment_attrs_city_price", "version": 1},
  "rules": [{"id": "metros", "when": {"field": "city", "op": "IN", "value": ["Delhi", "Mumbai"]}, "value": true, "rollout": {"percent": 50, "salt": "new_inspection_flow.metros"}}],
  "default": false}`)},

		{Kind: "experiments", Name: "callback_timing", Description: "Does calling back faster convert better?", Spec: raw(`{
  "status": "running", "hypothesis": "Calling back within 5 minutes raises inspection bookings", "salt": "callback_timing",
  "holdoutPercent": 0,
  "variants": [{"name": "control", "weight": 50, "parameters": {"delayMinutes": 30}}, {"name": "fast", "weight": 50, "parameters": {"delayMinutes": 5}}]}`)},

		{Kind: "decisions", Name: "lead_routing", Description: "Route a lead to fast track, standard or review", Spec: raw(`{
  "entry": "normalize",
  "nodes": [
    {"id": "normalize", "type": "transform", "outputSchema": {"name": "eligibility_input", "version": 1},
     "fields": [
       {"target": "city", "expr": "input.lead.city"}, {"target": "source", "expr": "input.lead.source"},
       {"target": "ageYears", "expr": "ctx.now.Year() - input.car.year"}, {"target": "km", "expr": "input.car.km"},
       {"target": "price", "expr": "input.car.price"}, {"target": "inspectionAvg", "expr": "mean(map(input.inspections ?? [], .score)) ?? 0"}],
     "next": "eligibility"},
    {"id": "eligibility", "type": "rules", "inputSchema": {"name": "eligibility_input", "version": 1},
     "outputSchema": {"name": "eligibility_output", "version": 1}, "mode": "first_match",
     "rules": [
       {"id": "r1", "name": "Blocked city", "when": {"segment": "blocked_cities", "op": "IN_SEGMENT", "key": "input.city"}, "then": {"eligible": false, "reason": "city_not_served"}},
       {"id": "r2", "name": "Too old", "when": {"field": "ageYears", "op": "GT", "value": 12}, "then": {"eligible": false, "reason": "car_too_old"}},
       {"id": "r4", "name": "Fast lane", "rollout": {"percent": 25, "salt": "eligibility.r4"},
        "when": {"all": [
          {"field": "price", "op": "BETWEEN", "value": 300000, "valueTo": 2500000},
          {"field": "source", "op": "IN", "value": ["app", "web"]},
          {"field": "inspectionAvg", "op": "GTE", "value": 7},
          {"segment": "metro_premium", "op": "NOT_IN_SEGMENT", "attributes": {"city": "input.city", "price": "input.price"}}]},
        "then": {"eligible": true, "lane": "fast", "reason": "fast_lane"}}],
     "default": {"eligible": true, "lane": "standard", "reason": "default"}, "next": "output"},
    {"id": "output", "type": "output", "fields": [
      {"target": "route", "expr": "nodes.eligibility.eligible ? (nodes.eligibility.lane == \"fast\" ? \"fast_track\" : \"standard\") : \"reject\""},
      {"target": "reason", "expr": "nodes.eligibility.reason"}]}],
  "inputSchema": {"name": "lead_input", "version": 1},
  "output": {"schema": {"name": "routing_output", "version": 1}},
  "fallback": {"route": "manual_review", "reason": "fallback"},
  "logging": {"sample": 1, "fields": ["lead.city", "car.year", "output.route"]},
  "tests": [{"name": "fast track", "ctx": {"now": "2026-10-30T10:00:00Z", "unit": "L-1029386"}, "input": {"lead": {"id": "L-1029384", "city": "Gurgaon", "source": "app"}, "car": {"year": 2019, "km": 48200, "price": 525000}, "inspections": [{"part": "engine", "score": 8}]}, "expect": {"route": "fast_track"}, "expectPath": ["normalize:ok", "eligibility:r4", "output:ok"]}, {"name": "outside rollout gets standard", "ctx": {"now": "2026-10-30T10:00:00Z", "unit": "L-1029384"}, "input": {"lead": {"id": "L-2", "city": "Gurgaon", "source": "app"}, "car": {"year": 2019, "km": 48200, "price": 525000}, "inspections": [{"part": "engine", "score": 8}]}, "expect": {"route": "standard", "reason": "default"}}, {"name": "blocked city", "ctx": {"now": "2026-10-30T10:00:00Z", "unit": "L-3"}, "input": {"lead": {"id": "L-3", "city": "Srinagar", "source": "web"}, "car": {"year": 2020, "km": 1000, "price": 400000}}, "expect": {"route": "reject", "reason": "city_not_served"}}, {"name": "too old", "ctx": {"now": "2026-10-30T10:00:00Z", "unit": "L-4"}, "input": {"lead": {"id": "L-4", "city": "Pune", "source": "web"}, "car": {"year": 2010, "km": 1000, "price": 400000}}, "expect": {"route": "reject", "reason": "car_too_old"}}, {"name": "metro premium is not fast", "ctx": {"now": "2026-10-30T10:00:00Z", "unit": "L-1029386"}, "input": {"lead": {"id": "L-5", "city": "Mumbai", "source": "app"}, "car": {"year": 2022, "km": 1000, "price": 950000}, "inspections": [{"part": "engine", "score": 9}]}, "expect": {"route": "standard"}}, {"name": "missing car fails the input schema", "input": {"lead": {"id": "L-6", "city": "Pune"}}, "expectError": "INPUT_SCHEMA"}]}`)},
	}
}

// Options for Demo / Dir.
type Options struct {
	Env        string    // environment to publish to (default dev)
	Credential string    // register this secret as the env credential (optional)
	Log        io.Writer // progress (optional)
	// RegisterCredential registers a fixed secret (GEARBOX_SEED_CREDENTIAL);
	// the HTTP API never accepts caller-chosen secrets, so only in-process
	// seeding can do this.
	RegisterCredential func(ctx context.Context, app, env, secret string) (id string, err error)
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// Demo seeds the demo app through c (typically an in-process system client).
func Demo(ctx context.Context, c *client.Client, opt Options) (Result, error) {
	return seedFiles(ctx, c, App, DemoFiles(), opt)
}

// Dir seeds from a definitions directory (GEARBOX_SEED_DIR), falling back
// to the built-in demo when the directory does not exist.
func Dir(ctx context.Context, c *client.Client, dir string, opt Options) (Result, error) {
	if dir == "" {
		return Demo(ctx, c, opt)
	}
	if _, err := os.Stat(dir); err != nil {
		logf(opt.Log, "seed: %s not found; using the built-in demo definitions", dir)
		return Demo(ctx, c, opt)
	}
	files, err := defs.Load(dir)
	if err != nil {
		return Result{}, err
	}
	return seedFiles(ctx, c, App, files, opt)
}

func seedFiles(ctx context.Context, c *client.Client, app string, files []defs.File, opt Options) (Result, error) {
	env := opt.Env
	if env == "" {
		env = "dev"
	}
	var res Result
	err := c.Do(ctx, http.MethodPost, "/apps", map[string]any{"name": app, "description": "Lead routing and pricing decisions (demo)", "owners": []string{"team-leads"}}, nil)
	if client.IsCode(err, "NAME_TAKEN") {
		res.AlreadySeeded = true
		logf(opt.Log, "seed: app %s exists; updating its definitions", app)
	} else if err != nil {
		return res, fmt.Errorf("create app: %w", err)
	}
	var items []map[string]any
	for _, f := range files {
		// Push and cut one at a time: dependents need cut schema versions.
		if _, err := c.Push(ctx, app, []defs.File{f}, opt.Log); err != nil {
			return res, err
		}
		v, cut, err := c.CutVersion(ctx, app, f.Kind, f.Name, "seed", false)
		if err != nil {
			return res, fmt.Errorf("cut %s/%s: %w", f.Kind, f.Name, err)
		}
		if cut {
			logf(opt.Log, "seed: cut %s/%s@%d", f.Kind, f.Name, v.Version)
		}
		r, err := c.GetResource(ctx, app, f.Kind, f.Name)
		if err != nil {
			return res, err
		}
		if lv, ok := r.Live[env]; !ok || lv.Version != r.LatestVersion {
			items = append(items, map[string]any{"kind": f.Kind, "name": f.Name, "version": r.LatestVersion})
		}
	}
	res.ResourcesCount = len(files)
	if len(items) > 0 {
		var cr struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := c.Do(ctx, http.MethodPost, client.Path("apps", app, "change-requests"), map[string]any{"environment": env, "title": "Seed demo definitions", "items": items}, &cr); err != nil {
			return res, fmt.Errorf("open change request: %w", err)
		}
		if cr.Status != "approved" {
			if err := c.Do(ctx, http.MethodPost, client.Path("apps", app, "change-requests", cr.ID, "approve"), map[string]any{"comment": "seed"}, &cr); err != nil {
				return res, fmt.Errorf("approve %s: %w", cr.ID, err)
			}
		}
		var rel struct {
			Generation int64 `json:"generation"`
		}
		if err := c.Do(ctx, http.MethodPost, client.Path("apps", app, "environments", env, "releases"), map[string]any{"type": "change_request", "changeRequestId": cr.ID}, &rel); err != nil {
			return res, fmt.Errorf("publish %s: %w", cr.ID, err)
		}
		res.Generation = rel.Generation
		logf(opt.Log, "seed: published %s to %s/%s as generation %d", cr.ID, app, env, rel.Generation)
	} else {
		var envs struct {
			Items []struct {
				Name       string `json:"name"`
				Generation int64  `json:"generation"`
			} `json:"items"`
		}
		if err := c.Do(ctx, http.MethodGet, client.Path("apps", app, "environments"), nil, &envs); err == nil {
			for _, e := range envs.Items {
				if e.Name == env {
					res.Generation = e.Generation
				}
			}
		}
		logf(opt.Log, "seed: %s/%s already up to date (generation %d)", app, env, res.Generation)
	}
	if !res.AlreadySeeded || opt.Credential != "" {
		var created struct {
			Credential struct {
				ID string `json:"id"`
			} `json:"credential"`
			Secret string `json:"secret"`
		}
		if opt.Credential != "" {
			if opt.RegisterCredential == nil {
				return res, fmt.Errorf("a fixed seed credential needs in-process seeding")
			}
			id, err := opt.RegisterCredential(ctx, app, env, opt.Credential)
			if err != nil && !client.IsCode(err, "NAME_TAKEN") && !isNameTaken(err) {
				return res, err
			}
			created.Credential.ID = id
			created.Secret = opt.Credential
		} else if err := c.Do(ctx, http.MethodPost, client.Path("apps", app, "environments", env, "credentials"), map[string]any{"label": "seed"}, &created); err != nil {
			return res, fmt.Errorf("create credential: %w", err)
		}
		res.CredentialID, res.Credential = created.Credential.ID, created.Secret
	}
	return res, nil
}

func isNameTaken(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NAME_TAKEN")
}
