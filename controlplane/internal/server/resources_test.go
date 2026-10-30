package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const app = "/apps/lead_service"

func TestResourcesAndSchemaRule(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)

	// List with filter and pagination.
	r := h.must(200, "GET", app+"/schemas?limit=2", nil)
	var page struct {
		Items []struct {
			Name          string         `json:"name"`
			LatestVersion int64          `json:"latestVersion"`
			DraftChanged  bool           `json:"draftChanged"`
			Live          map[string]any `json:"live"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	r.JSON(t, &page)
	if len(page.Items) != 2 || page.NextCursor == "" || page.Items[0].LatestVersion != 1 || page.Items[0].DraftChanged {
		t.Fatalf("page 1: %s", r.Body)
	}
	if _, ok := page.Items[0].Live["dev"]; !ok {
		t.Fatalf("schema not live in dev: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/schemas?limit=50&cursor="+page.NextCursor, nil)
	page.NextCursor = ""
	r.JSON(t, &page)
	if len(page.Items) != 4 || page.NextCursor != "" {
		t.Fatalf("page 2: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/decisions?q=ROUTE", nil)
	if !strings.Contains(string(r.Body), `"subtype":"graph"`) {
		t.Fatalf("decision summary: %s", r.Body)
	}
	h.mustCode(400, "INVALID_ARGUMENT", "GET", app+"/decisions?limit=500", nil)
	h.mustCode(400, "INVALID_ARGUMENT", "GET", app+"/widgets", nil)
	h.mustCode(404, "NOT_FOUND", "GET", "/apps/nope_app/decisions", nil)

	// Schema registry first.
	noSchema := map[string]any{"name": "bare_rules", "spec": map[string]any{"entry": "a", "nodes": []any{map[string]any{"id": "a", "type": "rules", "rules": []any{}}}}}
	r = h.mustCode(422, "SCHEMA_REQUIRED", "POST", app+"/decisions", noSchema)
	if !strings.Contains(string(r.Body), "/spec/nodes/0/inputSchema") {
		t.Fatalf("SCHEMA_REQUIRED paths: %s", r.Body)
	}
	missing := map[string]any{"name": "bad_ref", "spec": map[string]any{"entry": "a", "nodes": []any{map[string]any{"id": "a", "type": "rules",
		"inputSchema": map[string]any{"name": "lead_input", "version": 9}, "outputSchema": map[string]any{"name": "routing_output", "version": 1}, "rules": []any{}}}}}
	h.mustCode(422, "SCHEMA_NOT_FOUND", "POST", app+"/decisions", missing)
	seg := map[string]any{"name": "ghost_attrs", "spec": map[string]any{"type": "dynamic", "dynamic": map[string]any{"attributesSchema": map[string]any{"name": "nope", "version": 1}, "when": map[string]any{"field": "x", "op": "EXISTS"}}}}
	h.mustCode(422, "SCHEMA_NOT_FOUND", "POST", app+"/segments", seg)
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/decisions", map[string]any{"name": "Bad-Name", "spec": map[string]any{"entry": "a", "nodes": []any{}}})
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/decisions", map[string]any{"name": "no_nodes", "spec": map[string]any{"entry": "a", "nodes": []any{}}})
	h.mustCode(409, "NAME_TAKEN", "POST", app+"/schemas", map[string]any{"name": "lead_input", "spec": map[string]any{"jsonSchema": map[string]any{"type": "object"}}})

	// A valid single-rule decision; draft validation stored with the draft.
	quote := map[string]any{"name": "quick_quote", "description": "one rule node", "spec": map[string]any{
		"entry": "q", "fallback": map[string]any{"route": "manual_review"},
		"nodes": []any{map[string]any{"id": "q", "type": "rules", "inputSchema": map[string]any{"name": "eligibility_input", "version": 1},
			"outputSchema": map[string]any{"name": "eligibility_output", "version": 1},
			"rules":        []any{map[string]any{"id": "cheap", "when": map[string]any{"field": "price", "op": "LT", "value": 100000}, "then": map[string]any{"eligible": false, "reason": "too_cheap"}}},
			"default":      map[string]any{"eligible": true, "reason": "ok"}}}}}
	r = h.must(201, "POST", app+"/decisions", quote)
	var res struct {
		Draft struct {
			Revision   int64 `json:"revision"`
			Validation struct {
				Valid bool `json:"valid"`
			} `json:"validation"`
			Spec json.RawMessage `json:"spec"`
		} `json:"draft"`
	}
	r.JSON(t, &res)
	if res.Draft.Revision != 1 || !res.Draft.Validation.Valid {
		t.Fatalf("created: %s", r.Body)
	}
	h.must(200, "GET", app+"/decisions/quick_quote", nil)
	h.mustCode(404, "NOT_FOUND", "GET", app+"/decisions/missing_one", nil)

	// Revision conflicts.
	save := map[string]any{"revision": 1, "spec": quote["spec"], "description": "v2"}
	r = h.must(200, "PUT", app+"/decisions/quick_quote/draft", save)
	r.JSON(t, &res)
	if res.Draft.Revision != 2 {
		t.Fatalf("revision after save: %d", res.Draft.Revision)
	}
	r = h.mustCode(409, "REVISION_CONFLICT", "PUT", app+"/decisions/quick_quote/draft", save)
	if !strings.Contains(string(r.Body), "revision 2; you sent 1") {
		t.Fatalf("conflict detail: %s", r.Body)
	}
	h.mustCode(422, "VALIDATION_FAILED", "PUT", app+"/decisions/quick_quote/draft", map[string]any{"spec": quote["spec"]})

	// Validate: always 200, problems for invalid specs.
	r = h.must(200, "POST", app+"/decisions/quick_quote/validate", nil)
	if !strings.Contains(string(r.Body), `"valid":true`) {
		t.Fatalf("validate draft: %s", r.Body)
	}
	bad := map[string]any{"spec": map[string]any{"entry": "q", "nodes": []any{map[string]any{"id": "q", "type": "rules", "rules": []any{}, "next": "nowhere"}}}}
	r = h.must(200, "POST", app+"/decisions/quick_quote/validate", bad)
	var vr struct {
		Valid    bool `json:"valid"`
		Problems []struct {
			Code string `json:"code"`
			Path string `json:"path"`
		} `json:"problems"`
	}
	r.JSON(t, &vr)
	codes := map[string]bool{}
	for _, p := range vr.Problems {
		codes[p.Code] = true
	}
	if vr.Valid || !codes["SCHEMA_REQUIRED"] || !codes["UNKNOWN_NODE"] {
		t.Fatalf("validate bad: %s", r.Body)
	}

	// Cut a version; identical drafts cannot be cut twice.
	h.mustCode(409, "REVISION_CONFLICT", "POST", app+"/decisions/quick_quote/versions", map[string]any{"revision": 1})
	r = h.must(201, "POST", app+"/decisions/quick_quote/versions", map[string]any{"revision": 2, "message": "first"})
	if !strings.Contains(string(r.Body), `"version":1`) || !strings.Contains(string(r.Body), `sha256:`) {
		t.Fatalf("cut: %s", r.Body)
	}
	h.mustCode(409, "NO_CHANGES", "POST", app+"/decisions/quick_quote/versions", map[string]any{"revision": 2})
	h.must(200, "GET", app+"/decisions/quick_quote/versions", nil)
	h.must(200, "GET", app+"/decisions/quick_quote/versions/1", nil)
	h.mustCode(404, "NOT_FOUND", "GET", app+"/decisions/quick_quote/versions/7", nil)

	// Diff a version against a changed draft.
	spec2 := quote["spec"].(map[string]any)
	spec2["fallback"] = map[string]any{"route": "standard"}
	h.must(200, "PUT", app+"/decisions/quick_quote/draft", map[string]any{"revision": 2, "spec": spec2})
	r = h.must(200, "GET", app+"/decisions/quick_quote/diff?from=1&to=draft", nil)
	var diff struct {
		Unified string `json:"unified"`
		Changes []struct {
			Op, Path string
		} `json:"changes"`
	}
	r.JSON(t, &diff)
	if !strings.Contains(diff.Unified, "-  route: manual_review") || !strings.Contains(diff.Unified, "+  route: standard") || len(diff.Changes) != 1 || diff.Changes[0].Path != "/fallback/route" {
		t.Fatalf("diff: %s", r.Body)
	}
	h.mustCode(400, "INVALID_ARGUMENT", "GET", app+"/decisions/quick_quote/diff?from=x&to=draft", nil)

	// Usages.
	r = h.must(200, "GET", app+"/schemas/eligibility_input/usages", nil)
	for _, want := range []string{`"name":"lead_routing"`, `"name":"quick_quote"`, `"usesVersion":1`, `/nodes/1/inputSchema`} {
		if !strings.Contains(string(r.Body), want) {
			t.Fatalf("usages missing %s: %s", want, r.Body)
		}
	}
	h.must(200, "GET", app+"/segments/metro_premium/usages", nil)

	// Archive: live or used → IN_USE; unused → 204 then 404.
	h.mustCode(409, "IN_USE", "DELETE", app+"/schemas/eligibility_input", nil)
	h.mustCode(409, "IN_USE", "DELETE", app+"/decisions/lead_routing", nil)
	h.must(201, "POST", app+"/layers", map[string]any{"name": "spare_layer", "spec": map[string]any{"allocations": []any{}}})
	h.must(204, "DELETE", app+"/layers/spare_layer", nil)
	h.mustCode(404, "NOT_FOUND", "GET", app+"/layers/spare_layer", nil)
	h.mustCode(409, "NAME_TAKEN", "POST", app+"/layers", map[string]any{"name": "spare_layer", "spec": map[string]any{"allocations": []any{}}})
}

func TestSchemaVersioning(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	r := h.must(200, "POST", app+"/schemas/lead_input/versions/1/validate-document", map[string]any{"document": map[string]any{"lead": map[string]any{"id": "L-1", "city": "Pune"}, "car": map[string]any{"make": "Maruti", "year": 2019}}})
	if !strings.Contains(string(r.Body), `"valid":false`) || !strings.Contains(string(r.Body), "/car/price") {
		t.Fatalf("validate-document: %s", r.Body)
	}
	h.mustCode(404, "NOT_FOUND", "POST", app+"/schemas/lead_input/versions/5/validate-document", map[string]any{"document": 1})

	narrowed := map[string]any{"jsonSchema": map[string]any{"type": "object", "required": []any{"city", "price", "zone"}, "properties": map[string]any{"city": map[string]any{"type": "string"}, "price": map[string]any{"type": "number"}, "zone": map[string]any{"type": "string"}}}}
	r = h.must(200, "POST", app+"/schemas/segment_attrs_city_price/compatibility", map[string]any{"spec": narrowed})
	if !strings.Contains(string(r.Body), `"compatible":false`) || !strings.Contains(string(r.Body), "property_added") {
		t.Fatalf("compatibility: %s", r.Body)
	}
	res := h.must(200, "GET", app+"/schemas/segment_attrs_city_price", nil).Map(t)
	rev := res["draft"].(map[string]any)["revision"]
	h.must(200, "PUT", app+"/schemas/segment_attrs_city_price/draft", map[string]any{"revision": rev, "spec": narrowed})
	r = h.mustCode(422, "BREAKING_CHANGE", "POST", app+"/schemas/segment_attrs_city_price/versions", map[string]any{"revision": 2})
	if !strings.Contains(string(r.Body), "/spec/jsonSchema/zone") {
		t.Fatalf("breaking paths: %s", r.Body)
	}
	r = h.must(201, "POST", app+"/schemas/segment_attrs_city_price/versions", map[string]any{"revision": 2, "allowBreaking": true, "message": "add zone"})
	if !strings.Contains(string(r.Body), `"breaking":true`) {
		t.Fatalf("breaking flag: %s", r.Body)
	}
	// Decisions on the old version keep validating against it.
	h.must(200, "POST", app+"/segments/metro_premium/validate", nil)
}

func TestSegmentsAndLookups(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	r := h.must(200, "PUT", app+"/segments/blocked_cities/members", csvBody("key\nSrinagar\nLeh\nShimla\n\nLeh\n"))
	if !strings.Contains(string(r.Body), `"total":3`) {
		t.Fatalf("csv members: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/segments/blocked_cities/members?prefix=S&limit=1", nil)
	var mp struct {
		Items      []string `json:"items"`
		Total      int      `json:"total"`
		NextCursor string   `json:"nextCursor"`
	}
	r.JSON(t, &mp)
	if mp.Total != 2 || len(mp.Items) != 1 || mp.Items[0] != "Shimla" || mp.NextCursor == "" {
		t.Fatalf("members page: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/segments/blocked_cities", nil)
	if !strings.Contains(string(r.Body), `"memberCount":3`) {
		t.Fatalf("memberCount: %s", r.Body)
	}
	h.mustCode(422, "VALIDATION_FAILED", "PUT", app+"/segments/metro_premium/members", map[string]any{"members": []string{"x"}})

	// Membership checks for all three segment types.
	check := func(seg string, body map[string]any, member bool, reasonPrefix string) {
		t.Helper()
		r := h.must(200, "POST", app+"/segments/"+seg+"/check", body)
		var c struct {
			Member bool   `json:"member"`
			Reason string `json:"reason"`
		}
		r.JSON(t, &c)
		if c.Member != member || !strings.HasPrefix(c.Reason, reasonPrefix) {
			t.Fatalf("check %s %v: %s", seg, body, r.Body)
		}
	}
	check("blocked_cities", map[string]any{"key": "Shimla"}, true, "listed")
	check("blocked_cities", map[string]any{"key": "Shimla", "source": "version", "version": 1}, false, "not_listed")
	check("metro_premium", map[string]any{"source": "draft", "attributes": map[string]any{"city": "Gurgaon", "price": 950000}}, true, "conditions_matched")
	check("metro_premium", map[string]any{"attributes": map[string]any{"city": "Pune", "price": 950000}}, false, "conditions_failed")
	check("beta_10pct", map[string]any{"unit": "L-1"}, false, "bucket")

	// Lookup rows: CSV with JSON or plain values, validated against valueSchema.
	r = h.must(200, "PUT", app+"/lookups/city_zone/rows", csvBody("key,value\nDelhi,\"{\"\"zone\"\":\"\"north\"\",\"\"tier\"\":1}\"\nChennai,\"{\"\"zone\"\":\"\"south\"\",\"\"tier\"\":2}\"\n"))
	if !strings.Contains(string(r.Body), `"total":2`) {
		t.Fatalf("csv rows: %s", r.Body)
	}
	r = h.must(200, "GET", app+"/lookups/city_zone/rows?key=Chennai", nil)
	if !strings.Contains(string(r.Body), `"tier":2`) {
		t.Fatalf("row by key: %s", r.Body)
	}
	h.must(200, "GET", app+"/lookups/city_zone/rows?limit=1", nil)
	r = h.mustCode(422, "VALIDATION_FAILED", "PUT", app+"/lookups/city_zone/rows", map[string]any{"rows": []any{map[string]any{"key": "X", "value": map[string]any{"zone": "up", "tier": 9}}}})
	if !strings.Contains(string(r.Body), "/rows/0/value/tier") {
		t.Fatalf("row errors: %s", r.Body)
	}
	h.mustCode(422, "VALIDATION_FAILED", "PUT", app+"/lookups/city_zone/rows", map[string]any{"rows": []any{map[string]any{"key": "A", "value": map[string]any{"zone": "north", "tier": 1}}, map[string]any{"key": "A", "value": map[string]any{"zone": "north", "tier": 1}}}})
	r = h.must(200, "GET", app+"/lookups/city_zone", nil)
	if !strings.Contains(string(r.Body), `"rowCount":2`) {
		t.Fatalf("rowCount: %s", r.Body)
	}
}

func TestEvaluateAndTests(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	in := map[string]any{"lead": map[string]any{"id": "L-1029384", "city": "Gurgaon", "source": "app"}, "car": map[string]any{"year": 2019, "km": 48200, "price": 525000}}
	r := h.must(200, "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"source": "draft", "environment": "dev", "input": in, "ctx": map[string]any{"now": "2026-10-30T10:00:00Z", "unit": "L-1"}})
	var res struct {
		Output          map[string]any `json:"output"`
		DecisionVersion string         `json:"decisionVersion"`
		FallbackUsed    bool           `json:"fallbackUsed"`
	}
	r.JSON(t, &res)
	if res.DecisionVersion != "lead_routing@draft" || res.Output == nil {
		t.Fatalf("evaluate: %s", r.Body)
	}
	r = h.must(200, "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"source": "version", "version": 1, "input": in})
	r.JSON(t, &res)
	if res.DecisionVersion != "lead_routing@1" {
		t.Fatalf("evaluate version: %s", r.Body)
	}
	// Lite and real engines both reject input that fails the entry schema
	// only for rules entry nodes; lead_routing starts with a transform.
	h.mustCode(404, "NOT_FOUND", "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"source": "version", "version": 9, "input": in})
	h.mustCode(422, "VALIDATION_FAILED", "POST", app+"/decisions/lead_routing/evaluate", map[string]any{"source": "draft"})
	r = h.must(200, "POST", app+"/decisions/lead_routing/tests/run", map[string]any{"source": "draft"})
	if !strings.Contains(string(r.Body), `"passed":1`) {
		t.Fatalf("tests/run: %s", r.Body)
	}
	h.must(200, "POST", app+"/decisions/lead_routing/tests/run", nil)
	h.mustCode(404, "NOT_FOUND", "POST", app+"/decisions/lead_routing/tests/run", map[string]any{"environment": "qa"})
}

func TestRBACAndScoping(t *testing.T) {
	h := newHarness(t)
	seedDemo(t, h)
	h.must(201, "POST", "/members", map[string]any{"email": "vera@example.com", "role": "viewer"})
	h.must(201, "POST", "/members", map[string]any{"email": "otto@example.com", "role": "author", "apps": []string{"other_app"}})
	h.must(201, "POST", "/apps", map[string]any{"name": "other_app", "environments": []string{"dev", "prod"}})

	viewer, scoped := as("vera@example.com"), as("otto@example.com")
	h.must(200, "GET", app+"/decisions", nil, viewer)
	h.mustCode(403, "FORBIDDEN", "POST", app+"/schemas", map[string]any{"name": "nope", "spec": map[string]any{"jsonSchema": map[string]any{}}}, viewer)
	h.mustCode(403, "FORBIDDEN", "POST", "/apps", map[string]any{"name": "nope"}, viewer)
	h.mustCode(403, "FORBIDDEN", "GET", "/webhooks", nil, viewer)

	r := h.must(200, "GET", "/apps", nil, scoped)
	if strings.Contains(string(r.Body), "lead_service") || !strings.Contains(string(r.Body), "other_app") {
		t.Fatalf("scoped apps: %s", r.Body)
	}
	h.mustCode(404, "NOT_FOUND", "GET", app, nil, scoped)
	h.must(201, "POST", "/apps/other_app/schemas", map[string]any{"name": "thing", "spec": map[string]any{"jsonSchema": map[string]any{"type": "string"}}}, scoped)
	h.mustCode(403, "FORBIDDEN", "GET", "/audit?app=lead_service", nil, scoped)
	r = h.must(200, "GET", "/audit", nil, scoped)
	if strings.Contains(string(r.Body), `"app":"lead_service"`) {
		t.Fatalf("scoped audit leaks: %s", r.Body)
	}
	// Unknown dev role header.
	h.mustCode(400, "INVALID_ARGUMENT", "GET", "/session", nil, as("new@example.com"), hdr("X-Gearbox-Dev-Role", "owner"))
	_ = http.StatusOK
	_ = json.Valid
}
