package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/apispec"
	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

// canonical re-encodes JSON with sorted keys and strips read-only counters.
func canonical(kind string, raw json.RawMessage) (json.RawMessage, map[string]any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil, errors.New("spec must be a JSON object")
	}
	switch kind {
	case model.KindSegments:
		if st, ok := m["static"].(map[string]any); ok {
			delete(st, "memberCount")
		}
	case model.KindLookups:
		delete(m, "rowCount")
	}
	b, err := json.Marshal(m)
	return b, m, err
}

// viewSpec adds read-only counters for display.
func viewSpec(kind string, spec json.RawMessage, data *model.DataRef) json.RawMessage {
	if kind != model.KindSegments && kind != model.KindLookups {
		return spec
	}
	var m map[string]any
	if json.Unmarshal(spec, &m) != nil {
		return spec
	}
	n := 0
	if data != nil {
		n = data.Count
	}
	switch kind {
	case model.KindSegments:
		if t, _ := m["type"].(string); t == "static" {
			st, _ := m["static"].(map[string]any)
			if st == nil {
				st = map[string]any{}
				m["static"] = st
			}
			st["memberCount"] = n
		}
	case model.KindLookups:
		m["rowCount"] = n
	}
	b, _ := json.Marshal(m)
	return b
}

func contentHash(spec json.RawMessage, data *model.DataRef) string {
	h := sha256.New()
	h.Write(spec)
	if data != nil {
		h.Write([]byte("\n" + data.SHA256))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func validKind(kind string) error {
	if !model.ValidKind(kind) {
		return errNotFound("Unknown resource kind %q.", kind)
	}
	return nil
}

// ---------------------------------------------------------------- spec checks

// schemaRule enforces the schema-registry-first rule: rules/table nodes need
// inputSchema and outputSchema, and every pinned schema must be registered.
func (s *Server) schemaRule(ctx context.Context, app, kind string, spec json.RawMessage, prefix string) *Problem {
	if kind == model.KindDecisions {
		var missing []FieldError
		for _, sl := range engineapi.RequiredSchemaSlots(spec) {
			if sl.Missing {
				slot := sl.Where[strings.LastIndex(sl.Where, "/")+1:]
				missing = append(missing, FieldError{Path: prefix + sl.Where, Code: "SCHEMA_REQUIRED", Message: fmt.Sprintf("%s is required for node %q", slot, sl.Node)})
			}
		}
		if len(missing) > 0 {
			return errUnprocessable("SCHEMA_REQUIRED", "Rule node needs schemas",
				"Every rules and table node needs a registered inputSchema and outputSchema. Register them under Schemas first.").with(missing...)
		}
	}
	var notFound []FieldError
	for _, d := range engineapi.Deps(kind, spec) {
		if d.Kind != model.KindSchemas || d.Version < 1 {
			continue
		}
		if _, err := s.loadVersion(ctx, app, model.KindSchemas, d.Name, d.Version); err != nil {
			notFound = append(notFound, FieldError{Path: prefix + d.Where, Code: "SCHEMA_NOT_FOUND", Message: fmt.Sprintf("schema %s@%d is not registered", d.Name, d.Version)})
		}
	}
	if len(notFound) > 0 {
		return errUnprocessable("SCHEMA_NOT_FOUND", "Schema not registered",
			"A referenced schema version is not registered. Register the schema and cut that version first.").with(notFound...)
	}
	return nil
}

// shapeErrors validates a spec against its kind's contract schema.
func shapeErrors(kind string, m map[string]any, prefix string) []FieldError {
	sc := apispec.Schema(model.SpecSchemaName(kind))
	if sc == nil {
		return nil
	}
	return schemaErrors(sc, m, prefix)
}

func toModelValidation(v engine.Validation) model.ValidationResult {
	out := model.ValidationResult{Valid: v.Valid, Problems: []model.ValidationProblem{}}
	for _, p := range v.Problems {
		out.Problems = append(out.Problems, model.ValidationProblem{Code: p.Code, Severity: p.Severity, Path: p.Path, Message: p.Message})
	}
	for _, d := range v.Dependencies {
		out.Dependencies = append(out.Dependencies, model.ResourceRef{Kind: string(d.Kind), Name: d.Name, Version: d.Version})
	}
	return out
}

// checkSpec runs the save-time checks and returns the canonical spec and the
// engine's validation (stored with the draft).
func (s *Server) checkSpec(ctx context.Context, app, kind, name string, raw json.RawMessage, data *model.DataRef) (json.RawMessage, model.ValidationResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, model.ValidationResult{}, errUnprocessable("VALIDATION_FAILED", "Spec required", "spec is required.").with(FieldError{Path: "/spec", Message: "required"})
	}
	spec, m, err := canonical(kind, raw)
	if err != nil {
		return nil, model.ValidationResult{}, errUnprocessable("VALIDATION_FAILED", "Invalid spec", "spec must be a JSON object.").with(FieldError{Path: "/spec", Message: err.Error()})
	}
	if p := s.schemaRule(ctx, app, kind, spec, "/spec"); p != nil {
		return nil, model.ValidationResult{}, p
	}
	if fes := shapeErrors(kind, m, "/spec"); len(fes) > 0 {
		return nil, model.ValidationResult{}, errUnprocessable("VALIDATION_FAILED", "Invalid spec",
			"The spec does not match %s: %s %s", model.SpecSchemaName(kind), fes[0].Path, fes[0].Message).with(fes...)
	}
	v := s.engineValidate(ctx, app, kind, name, 0, spec, data)
	return spec, v, nil
}

func (s *Server) engineValidate(ctx context.Context, app, kind, name string, version int64, spec json.RawMessage, data *model.DataRef) model.ValidationResult {
	r := engine.Resource{Kind: engine.Kind(kind), Name: name, Version: version, Spec: spec}
	rv := resolver{s: s, ctx: ctx, app: app, overrides: map[string]engine.Resource{kind + "/" + name: r}}
	return toModelValidation(s.eng.ValidateResource(r, rv))
}

// ---------------------------------------------------------------- views

type envMap map[string]model.EnvState

func (s *Server) envStates(ctx context.Context, app string) (envMap, error) {
	sts, err := s.st.ListEnvs(ctx, app)
	if err != nil {
		return nil, err
	}
	out := envMap{}
	for _, st := range sts {
		out[st.Name] = st
	}
	return out, nil
}

func (m envMap) live(kind, name string) map[string]model.LiveState {
	out := map[string]model.LiveState{}
	for env, st := range m {
		if e, ok := st.Live[model.LiveKey(kind, name)]; ok {
			out[env] = e.State()
		}
	}
	return out
}

func (s *Server) resourceView(rec model.ResourceRecord, envs envMap) model.Resource {
	return model.Resource{
		Kind: rec.Kind, Name: rec.Name, Description: rec.Description,
		Draft: model.ResourceDraft{
			Spec: viewSpec(rec.Kind, rec.Draft.Spec, rec.Draft.Data), Revision: rec.Draft.Revision,
			UpdatedAt: rec.Draft.UpdatedAt, UpdatedBy: rec.Draft.UpdatedBy, Validation: rec.Draft.Validation,
		},
		LatestVersion: rec.LatestVersion,
		Live:          envs.live(rec.Kind, rec.Name),
	}
}

func subtype(kind string, spec json.RawMessage) string {
	switch kind {
	case model.KindDecisions:
		return engineapi.DecisionSubtype(spec)
	case model.KindSegments:
		var s struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(spec, &s)
		return s.Type
	}
	return ""
}

// loadResource returns a non-archived resource or 404.
func (s *Server) loadResource(ctx context.Context, p *Principal, app, kind, name string) (model.ResourceRecord, int64, error) {
	if err := validKind(kind); err != nil {
		return model.ResourceRecord{}, 0, err
	}
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return model.ResourceRecord{}, 0, err
	}
	rec, ver, err := s.st.GetResource(ctx, app, kind, name)
	if errors.Is(err, store.ErrNotFound) || (err == nil && rec.Archived) {
		return rec, 0, errNotFound("%s/%s does not exist in %s.", kind, name, app)
	}
	return rec, ver, err
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind := r.PathValue("app"), r.PathValue("kind")
	if err := validKind(kind); err != nil {
		return err
	}
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	page, err := s.st.ListResources(ctx, app, kind, pg.Cursor, pg.Limit, func(rec model.ResourceRecord) bool {
		return q == "" || strings.Contains(strings.ToLower(rec.Name), q) || strings.Contains(strings.ToLower(rec.Description), q)
	})
	if err != nil {
		return err
	}
	envs, err := s.envStates(ctx, app)
	if err != nil {
		return err
	}
	var stats map[string]*model.ResourceStats
	if kind == model.KindDecisions {
		stats = s.decisionStats(ctx, app, "prod")
	}
	out := make([]model.ResourceSummary, 0, len(page.Items))
	for _, rec := range page.Items {
		sum := model.ResourceSummary{
			Kind: rec.Kind, Name: rec.Name, Description: rec.Description, Subtype: subtype(rec.Kind, rec.Draft.Spec),
			LatestVersion: rec.LatestVersion, DraftChanged: rec.LatestHash != contentHash(rec.Draft.Spec, rec.Draft.Data),
			Live: envs.live(rec.Kind, rec.Name), UpdatedAt: rec.UpdatedAt, UpdatedBy: rec.UpdatedBy,
		}
		if st := stats[rec.Name]; st != nil {
			sum.Stats = st
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, list(out, page.Next))
	return nil
}

func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind := r.PathValue("app"), r.PathValue("kind")
	if err := validKind(kind); err != nil {
		return err
	}
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var in model.ResourceCreate
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if !model.NamePattern.MatchString(in.Name) {
		return errUnprocessable("VALIDATION_FAILED", "Invalid name", "Names match ^[a-z][a-z0-9_]{1,62}$.").with(FieldError{Path: "/name", Message: "must match ^[a-z][a-z0-9_]{1,62}$"})
	}
	if len([]rune(in.Description)) > 500 {
		return errUnprocessable("VALIDATION_FAILED", "Description too long", "description is limited to 500 characters.").with(FieldError{Path: "/description", Message: "at most 500 characters"})
	}
	if _, _, err := s.st.GetResource(ctx, app, kind, in.Name); err == nil {
		return errConflict("NAME_TAKEN", "Name taken", "%s/%s already exists (names of archived resources stay taken).", kind, in.Name)
	}
	spec, v, err := s.checkSpec(ctx, app, kind, in.Name, in.Spec, nil)
	if err != nil {
		return err
	}
	now := s.now()
	rec := model.ResourceRecord{
		App: app, Kind: kind, Name: in.Name, Description: in.Description,
		Draft:     model.Draft{Spec: spec, Revision: 1, UpdatedAt: now, UpdatedBy: p.Actor(), Validation: &v},
		CreatedAt: now, UpdatedAt: now, UpdatedBy: p.Actor(),
	}
	if err := s.st.CreateResource(ctx, rec); err != nil {
		if errors.Is(err, store.ErrExists) {
			return errConflict("NAME_TAKEN", "Name taken", "%s/%s already exists.", kind, in.Name)
		}
		return err
	}
	s.audit(ctx, p, "resource.created", app, "", kind+"/"+in.Name, nil)
	envs, err := s.envStates(ctx, app)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, s.resourceView(rec, envs))
	return nil
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request, p *Principal) error {
	rec, _, err := s.loadResource(r.Context(), p, r.PathValue("app"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		return err
	}
	envs, err := s.envStates(r.Context(), rec.App)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.resourceView(rec, envs))
	return nil
}

func (s *Server) handleArchiveResource(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind, name := r.PathValue("app"), r.PathValue("kind"), r.PathValue("name")
	rec, ver, err := s.loadResource(ctx, p, app, kind, name)
	if err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	envs, err := s.envStates(ctx, app)
	if err != nil {
		return err
	}
	if live := envs.live(kind, name); len(live) > 0 {
		var names []string
		for e := range live {
			names = append(names, e)
		}
		sort.Strings(names)
		return errConflict("IN_USE", "Resource in use", "%s/%s is live in %s.", kind, name, strings.Join(names, ", "))
	}
	users, err := s.usages(ctx, app, kind, name, true)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		u := users[0]
		return errConflict("IN_USE", "Resource in use", "%s/%s is used by %s/%s (and %d more).", kind, name, u.Kind, u.Name, len(users)-1)
	}
	rec.Archived = true
	rec.UpdatedAt, rec.UpdatedBy = s.now(), p.Actor()
	if err := s.st.PutResource(ctx, rec, ver); err != nil {
		return err
	}
	s.audit(ctx, p, "resource.archived", app, "", kind+"/"+name, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func revisionConflict(have, sent int64) *Problem {
	return errConflict("REVISION_CONFLICT", "Draft changed", "Draft is at revision %d; you sent %d. Reload and apply your change again.", have, sent)
}

func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind, name := r.PathValue("app"), r.PathValue("kind"), r.PathValue("name")
	rec, ver, err := s.loadResource(ctx, p, app, kind, name)
	if err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return err
	}
	var in model.DraftSave
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &in)
	if _, ok := raw["revision"]; !ok {
		return errUnprocessable("VALIDATION_FAILED", "Revision required", "Send the revision you loaded.").with(FieldError{Path: "/revision", Message: "required"})
	}
	if in.Description != nil && len([]rune(*in.Description)) > 500 {
		return errUnprocessable("VALIDATION_FAILED", "Description too long", "description is limited to 500 characters.").with(FieldError{Path: "/description", Message: "at most 500 characters"})
	}
	if in.Revision != rec.Draft.Revision {
		return revisionConflict(rec.Draft.Revision, in.Revision)
	}
	spec, v, err := s.checkSpec(ctx, app, kind, name, in.Spec, rec.Draft.Data)
	if err != nil {
		return err
	}
	now := s.now()
	rec.Draft.Spec, rec.Draft.Validation = spec, &v
	rec.Draft.Revision++
	rec.Draft.UpdatedAt, rec.Draft.UpdatedBy = now, p.Actor()
	rec.UpdatedAt, rec.UpdatedBy = now, p.Actor()
	if in.Description != nil {
		rec.Description = *in.Description
	}
	if err := s.st.PutResource(ctx, rec, ver); err != nil {
		if errors.Is(err, store.ErrVersion) {
			cur, _, _ := s.st.GetResource(ctx, app, kind, name)
			return revisionConflict(cur.Draft.Revision, in.Revision)
		}
		return err
	}
	s.audit(ctx, p, "draft.saved", app, "", kind+"/"+name, map[string]any{"revision": rec.Draft.Revision})
	envs, err := s.envStates(ctx, app)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.resourceView(rec, envs))
	return nil
}

func (s *Server) handleValidateResource(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind, name := r.PathValue("app"), r.PathValue("kind"), r.PathValue("name")
	rec, _, err := s.loadResource(ctx, p, app, kind, name)
	if err != nil {
		return err
	}
	var in struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		return err
	}
	raw := in.Spec
	if len(raw) == 0 || string(raw) == "null" {
		raw = rec.Draft.Spec
	}
	res := s.validateForEditor(ctx, app, kind, name, raw, rec.Draft.Data)
	writeJSON(w, http.StatusOK, res)
	return nil
}

// validateForEditor turns every check into problems (always 200).
func (s *Server) validateForEditor(ctx context.Context, app, kind, name string, raw json.RawMessage, data *model.DataRef) model.ValidationResult {
	spec, m, err := canonical(kind, raw)
	if err != nil {
		return model.ValidationResult{Valid: false, Problems: []model.ValidationProblem{{Code: "PARSE_ERROR", Severity: "error", Path: "", Message: err.Error()}}}
	}
	var probs []model.ValidationProblem
	if p := s.schemaRule(ctx, app, kind, spec, ""); p != nil {
		for _, fe := range p.Errors {
			probs = append(probs, model.ValidationProblem{Code: fe.Code, Severity: "error", Path: fe.Path, Message: fe.Message})
		}
	}
	for _, fe := range shapeErrors(kind, m, "") {
		probs = append(probs, model.ValidationProblem{Code: "PARSE_ERROR", Severity: "error", Path: fe.Path, Message: fe.Message})
	}
	v := s.engineValidate(ctx, app, kind, name, 0, spec, data)
	seen := map[string]bool{}
	for _, p := range probs {
		seen[p.Code+p.Path] = true
	}
	for _, p := range v.Problems {
		if !seen[p.Code+p.Path] {
			probs = append(probs, p)
		}
	}
	v.Problems = probs
	if v.Problems == nil {
		v.Problems = []model.ValidationProblem{}
	}
	v.Valid = true
	for _, p := range v.Problems {
		if p.Severity == "error" {
			v.Valid = false
		}
	}
	if !v.Valid {
		v.Dependencies = nil
	}
	return v
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	page, err := s.st.ListVersions(ctx, rec.App, rec.Kind, rec.Name, pg.Cursor, pg.Limit)
	if err != nil {
		return errInvalid("%v", err)
	}
	out := make([]model.VersionInfo, 0, len(page.Items))
	for _, v := range page.Items {
		out = append(out, v.Info())
	}
	writeJSON(w, http.StatusOK, list(out, page.Next))
	return nil
}

// testEnv is the environment whose live dependencies a version cut tests
// against: dev when present, else the first environment.
func testEnv(a model.App) string {
	for _, e := range a.Environments {
		if e == "dev" {
			return e
		}
	}
	if len(a.Environments) > 0 {
		return a.Environments[0]
	}
	return "dev"
}

func (s *Server) handleCutVersion(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, kind, name := r.PathValue("app"), r.PathValue("kind"), r.PathValue("name")
	rec, ver, err := s.loadResource(ctx, p, app, kind, name)
	if err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var in struct {
		Revision      int64  `json:"revision"`
		Message       string `json:"message"`
		AllowBreaking bool   `json:"allowBreaking"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if in.Revision != rec.Draft.Revision {
		return revisionConflict(rec.Draft.Revision, in.Revision)
	}
	spec := rec.Draft.Spec
	hash := contentHash(spec, rec.Draft.Data)
	if rec.LatestVersion > 0 && hash == rec.LatestHash {
		return errConflict("NO_CHANGES", "Nothing to cut", "The draft is identical to version %d.", rec.LatestVersion)
	}
	if p := s.schemaRule(ctx, app, kind, spec, "/spec"); p != nil {
		return p
	}
	v := s.engineValidate(ctx, app, kind, name, 0, spec, rec.Draft.Data)
	if !v.Valid {
		pr := errUnprocessable("VALIDATION_FAILED", "Draft is invalid", "Fix the draft's problems before cutting a version.")
		for _, x := range v.Problems {
			if x.Severity == "error" {
				pr.Errors = append(pr.Errors, FieldError{Path: "/spec" + x.Path, Code: x.Code, Message: x.Message})
			}
		}
		return pr
	}
	var breaking *bool
	if kind == model.KindSchemas && rec.LatestVersion > 0 {
		prev, err := s.loadVersion(ctx, app, kind, name, rec.LatestVersion)
		if err != nil {
			return err
		}
		ok, changes, err := s.eng.CheckSchemaCompatibility(prev.Spec, spec)
		if err != nil {
			return errUnprocessable("VALIDATION_FAILED", "Compatibility check failed", "%v", err)
		}
		b := !ok
		breaking = &b
		if !ok && !in.AllowBreaking {
			pr := errUnprocessable("BREAKING_CHANGE", "Breaking schema change",
				"The new version is not backward compatible with version %d. Send allowBreaking=true to cut it anyway; decisions on the old version keep using it.", rec.LatestVersion)
			for _, c := range changes {
				if c.Breaking {
					pr.Errors = append(pr.Errors, FieldError{Path: "/spec/jsonSchema" + strings.TrimSuffix(c.Path, "/"), Code: c.Kind, Message: strings.ReplaceAll(c.Kind, "_", " ")})
				}
			}
			return pr
		}
	} else if kind == model.KindSchemas {
		b := false
		breaking = &b
	}
	if kind == model.KindDecisions {
		a, err := s.loadApp(ctx, p, app)
		if err != nil {
			return err
		}
		set, errs, err := s.evalSet(ctx, app, testEnv(a), engine.Resource{Kind: engine.KindDecision, Name: name, Version: 0, Spec: spec}, nil)
		if err != nil {
			return err
		}
		if len(errs) > 0 {
			return errUnprocessable("VALIDATION_FAILED", "Dependencies missing", "%s", errs[0].Message).with(errs...)
		}
		snap, err := s.eng.Compile(set)
		if err != nil {
			return compileProblem(err, nil)
		}
		results, err := snap.RunTests(ctx, name, 0)
		if err != nil {
			return errUnprocessable("TESTS_FAILED", "Tests could not run", "%v", err)
		}
		if fails := failedTests(name, results); len(fails) > 0 {
			return errUnprocessable("TESTS_FAILED", "Tests failed", "%d of %d test cases failed; versions need every test to pass.", len(fails), len(results)).with(fails...)
		}
	}
	now := s.now()
	vr := model.VersionRecord{App: app, Kind: kind, Name: name, Version: rec.LatestVersion + 1, Spec: spec, Data: rec.Draft.Data,
		Message: in.Message, ContentHash: hash, CreatedAt: now, CreatedBy: p.Actor(), Breaking: breaking}
	if err := s.st.CreateVersion(ctx, vr); err != nil {
		if errors.Is(err, store.ErrExists) {
			return revisionConflict(rec.Draft.Revision, in.Revision)
		}
		return err
	}
	rec.LatestVersion, rec.LatestHash = vr.Version, hash
	rec.UpdatedAt, rec.UpdatedBy = now, p.Actor()
	if err := s.st.PutResource(ctx, rec, ver); err != nil {
		if errors.Is(err, store.ErrVersion) {
			// The draft changed meanwhile; the version itself is valid and
			// stored, so record it on the current document.
			if _, err := s.updateLatest(ctx, app, kind, name, vr.Version, hash); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	s.audit(ctx, p, "version.cut", app, "", fmt.Sprintf("%s/%s@%d", kind, name, vr.Version), map[string]any{"message": in.Message})
	writeJSON(w, http.StatusCreated, vr.Info())
	return nil
}

func (s *Server) updateLatest(ctx context.Context, app, kind, name string, v int64, hash string) (model.ResourceRecord, error) {
	for i := 0; i < 20; i++ {
		rec, ver, err := s.st.GetResource(ctx, app, kind, name)
		if err != nil {
			return rec, err
		}
		if rec.LatestVersion >= v {
			return rec, nil
		}
		rec.LatestVersion, rec.LatestHash = v, hash
		if err := s.st.PutResource(ctx, rec, ver); err == nil {
			return rec, nil
		} else if !errors.Is(err, store.ErrVersion) {
			return rec, err
		}
	}
	return model.ResourceRecord{}, store.ErrVersion
}

func failedTests(decision string, results []engine.TestResult) []FieldError {
	var out []FieldError
	for i, t := range results {
		if t.OK {
			continue
		}
		msg := t.Error
		if msg == "" && len(t.Mismatches) > 0 {
			m := t.Mismatches[0]
			msg = fmt.Sprintf("%s: expected %s, got %s", m.Path, string(m.Expected), string(m.Actual))
		}
		if msg == "" {
			msg = "output did not match expect"
		}
		out = append(out, FieldError{Path: fmt.Sprintf("/decisions/%s/tests/%d", decision, i), Code: "TEST_FAILED", Message: t.Name + ": " + msg})
	}
	return out
}

func parseVersion(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, errInvalid("version must be a positive integer")
	}
	return n, nil
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		return err
	}
	n, err := parseVersion(r.PathValue("version"))
	if err != nil {
		return err
	}
	v, err := s.loadVersion(ctx, rec.App, rec.Kind, rec.Name, n)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("%s/%s has no version %d.", rec.Kind, rec.Name, n)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, model.ResourceVersion{Kind: v.Kind, Name: v.Name, Info: v.Info(), Spec: viewSpec(v.Kind, v.Spec, v.Data)})
	return nil
}

// specAt returns the spec for "draft" or a version number.
func (s *Server) specAt(ctx context.Context, rec model.ResourceRecord, which string) (json.RawMessage, error) {
	if which == "draft" {
		return viewSpec(rec.Kind, rec.Draft.Spec, rec.Draft.Data), nil
	}
	n, err := strconv.ParseInt(which, 10, 64)
	if err != nil || n < 1 {
		return nil, errInvalid("from/to must be a version number or \"draft\"")
	}
	v, err := s.loadVersion(ctx, rec.App, rec.Kind, rec.Name, n)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("%s/%s has no version %d.", rec.Kind, rec.Name, n)
	}
	if err != nil {
		return nil, err
	}
	return viewSpec(v.Kind, v.Spec, v.Data), nil
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		return err
	}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	a, err := s.specAt(ctx, rec, from)
	if err != nil {
		return err
	}
	b, err := s.specAt(ctx, rec, to)
	if err != nil {
		return err
	}
	unified, changes := diffSpecs(a, b, fmt.Sprintf("%s/%s@%s", rec.Kind, rec.Name, from), fmt.Sprintf("%s/%s@%s", rec.Kind, rec.Name, to))
	writeJSON(w, http.StatusOK, map[string]any{"unified": unified, "changes": changes})
	return nil
}

// usages scans drafts and versions of every resource of the app.
// excludeSelf drops the resource's own versions. latestOnly limits the
// scan to drafts and latest versions (archive checks).
func (s *Server) usages(ctx context.Context, app, kind, name string, latestOnly bool) ([]model.Usage, error) {
	recs, err := s.st.AllResources(ctx, app)
	if err != nil {
		return nil, err
	}
	out := []model.Usage{}
	seen := map[string]bool{}
	addFrom := func(ukind, uname string, uver int64, spec json.RawMessage) {
		if ukind == kind && uname == name {
			return
		}
		for _, d := range engineapi.Deps(ukind, spec) {
			if d.Kind != kind || d.Name != name {
				continue
			}
			u := model.Usage{Kind: ukind, Name: uname, Version: uver, Where: d.Where}
			if kind == model.KindSchemas {
				u.UsesVersion = d.Version
			}
			k := fmt.Sprintf("%s/%s/%d/%s", ukind, uname, uver, d.Where)
			if !seen[k] {
				seen[k] = true
				out = append(out, u)
			}
		}
	}
	for _, rec := range recs {
		addFrom(rec.Kind, rec.Name, 0, rec.Draft.Spec)
		if latestOnly {
			if rec.LatestVersion > 0 {
				if v, err := s.loadVersion(ctx, app, rec.Kind, rec.Name, rec.LatestVersion); err == nil {
					addFrom(rec.Kind, rec.Name, v.Version, v.Spec)
				}
			}
			continue
		}
		vs, err := s.st.AllVersions(ctx, app, rec.Kind, rec.Name)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			addFrom(v.Kind, v.Name, v.Version, v.Spec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version > b.Version
	})
	return out, nil
}

func (s *Server) handleUsages(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		return err
	}
	us, err := s.usages(ctx, rec.App, rec.Kind, rec.Name, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": us})
	return nil
}

// ---------------------------------------------------------------- schemas

func (s *Server) handleValidateDocument(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindSchemas, r.PathValue("name"))
	if err != nil {
		return err
	}
	n, err := parseVersion(r.PathValue("version"))
	if err != nil {
		return err
	}
	v, err := s.loadVersion(ctx, rec.App, model.KindSchemas, rec.Name, n)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("schema %s has no version %d.", rec.Name, n)
	}
	if err != nil {
		return err
	}
	var in struct {
		Document json.RawMessage `json:"document"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if len(in.Document) == 0 {
		in.Document = json.RawMessage("null")
	}
	ok, errs, err := s.eng.ValidateDocument(v.Spec, in.Document)
	if err != nil {
		return errUnprocessable("VALIDATION_FAILED", "Schema unusable", "%v", err)
	}
	out := []FieldError{}
	for _, e := range errs {
		out = append(out, FieldError{Path: e.Path, Message: e.Message})
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": ok, "errors": out})
	return nil
}

func (s *Server) handleCompatibility(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	rec, _, err := s.loadResource(ctx, p, r.PathValue("app"), model.KindSchemas, r.PathValue("name"))
	if err != nil {
		return err
	}
	var in struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if rec.LatestVersion == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"compatible": true, "changes": []engine.SchemaChange{}})
		return nil
	}
	prev, err := s.loadVersion(ctx, rec.App, model.KindSchemas, rec.Name, rec.LatestVersion)
	if err != nil {
		return err
	}
	ok, changes, err := s.eng.CheckSchemaCompatibility(prev.Spec, in.Spec)
	if err != nil {
		return errUnprocessable("VALIDATION_FAILED", "Compatibility check failed", "%v", err)
	}
	if changes == nil {
		changes = []engine.SchemaChange{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"compatible": ok, "changes": changes})
	return nil
}

// decisionStats aggregates sampled decision logs of the last hour.
func (s *Server) decisionStats(ctx context.Context, app, env string) map[string]*model.ResourceStats {
	now := s.now()
	recs, _, err := s.st.QueryRecords(ctx, store.RecDecisions, store.RecordQuery{Eq: map[string]string{"app": app, "env": env}, From: now.Add(-time.Hour)}, 100000)
	if err != nil || len(recs) == 0 {
		return nil
	}
	type agg struct {
		micros   []int64
		fallback int
	}
	by := map[string]*agg{}
	for _, r := range recs {
		var d struct {
			Decision     string `json:"decision"`
			EvalMicros   int64  `json:"evalMicros"`
			FallbackUsed bool   `json:"fallbackUsed"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			continue
		}
		a := by[d.Decision]
		if a == nil {
			a = &agg{}
			by[d.Decision] = a
		}
		a.micros = append(a.micros, d.EvalMicros)
		if d.FallbackUsed {
			a.fallback++
		}
	}
	out := map[string]*model.ResourceStats{}
	for name, a := range by {
		out[name] = &model.ResourceStats{P99Micros: percentile(a.micros, 0.99), CallsPerMinute: float64(len(a.micros)) / 60, FallbackRate: float64(a.fallback) / float64(len(a.micros))}
	}
	return out
}

func percentile(xs []int64, q float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]int64(nil), xs...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	idx := int(float64(len(c)-1) * q)
	return c[idx]
}
