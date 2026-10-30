package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/feed"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
	"github.com/ORG/gearbox/engine/bundle"
)

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var in model.ReleaseCreate
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	rel, err := s.Publish(r.Context(), p, r.PathValue("app"), r.PathValue("env"), in)
	if err != nil {
		serverMetrics.failed.Add(1)
		return err
	}
	serverMetrics.publishes.Add(1)
	writeJSON(w, http.StatusCreated, rel.Release)
	return nil
}

// Publish builds, signs and uploads a new generation for app+env.
func (s *Server) Publish(ctx context.Context, p *Principal, app, env string, in model.ReleaseCreate) (model.ReleaseRecord, error) {
	a, err := s.loadApp(ctx, p, app)
	if err != nil {
		return model.ReleaseRecord{}, err
	}
	if _, _, err := s.loadEnv(ctx, p, app, env); err != nil {
		return model.ReleaseRecord{}, err
	}
	need := model.RoleAuthor
	switch in.Type {
	case model.ReleaseRollout, model.ReleaseRollback, model.ReleaseKillSwitch:
		need = model.RoleApprover
	}
	if err := p.require(need, app); err != nil {
		return model.ReleaseRecord{}, err
	}

	// One publish at a time per app+environment (across replicas).
	lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	unlock, err := s.st.B.Lock(lctx, "publish/"+app+"/"+env, 5*time.Minute)
	if err != nil {
		return model.ReleaseRecord{}, problemf(http.StatusConflict, "BUSY", "Publish in progress", "Another publish to %s/%s is still running.", app, env)
	}
	defer unlock()

	st, envVer, err := s.st.GetEnv(ctx, app, env)
	if err != nil {
		return model.ReleaseRecord{}, err
	}
	gen := st.Generation + 1
	rel := model.ReleaseRecord{App: app}
	rel.Environment, rel.Type, rel.Generation, rel.Reason = env, in.Type, gen, in.Reason
	rel.PublishedAt, rel.PublishedBy = s.now(), p.Actor()
	var cr *model.ChangeRequest
	ns := st.Clone()

	switch in.Type {
	case model.ReleaseChangeRequest:
		if in.ChangeRequestID == "" {
			return rel, errInvalid("changeRequestId is required for a change_request release")
		}
		c, err := s.st.GetCR(ctx, app, in.ChangeRequestID)
		if errors.Is(err, store.ErrNotFound) {
			return rel, errNotFound("Change request %s does not exist.", in.ChangeRequestID)
		}
		if err != nil {
			return rel, err
		}
		if c.Environment != env {
			return rel, errUnprocessable("VALIDATION_FAILED", "Wrong environment", "Change request %s targets %s, not %s.", c.ID, c.Environment, env)
		}
		if c.Status != model.CRApproved {
			return rel, errConflict("NOT_APPROVED", "Not approved", "Change request %s is %s; only approved change requests can be published.", c.ID, c.Status)
		}
		cr = &c
		ns = applyItems(st, c.Items)
		rel.ChangeRequestID = c.ID
	case model.ReleaseData:
		if !st.Policy.AllowDataReleases {
			return rel, problemf(http.StatusForbidden, "FORBIDDEN", "Data releases disabled", "%s does not allow data releases without a change request.", env)
		}
		if len(in.Items) == 0 {
			return rel, errInvalid("items is required for a data release")
		}
		if pr := s.checkItems(ctx, app, in.Items, []string{model.KindSegments, model.KindLookups}); pr != nil {
			return rel, pr
		}
		ns = applyItems(st, in.Items)
	case model.ReleaseRollout:
		if in.Decision == "" || in.Percent == nil {
			return rel, errInvalid("decision and percent are required for a rollout release")
		}
		k := model.LiveKey(model.KindDecisions, in.Decision)
		e, ok := st.Live[k]
		if !ok || !e.Rolling {
			return rel, errConflict("NOT_ROLLING_OUT", "No rollout", "Decision %s is not rolling out in %s.", in.Decision, env)
		}
		if *in.Percent >= 100 {
			e = model.LiveEntry{Kind: e.Kind, Name: e.Name, Version: e.Version}
		} else {
			e.RolloutPercent = *in.Percent
		}
		ns.Live[k] = e
	case model.ReleaseRollback:
		if in.ToGeneration < 1 || in.ToGeneration >= gen {
			return rel, errInvalid("toGeneration must be an earlier generation (1..%d)", st.Generation)
		}
		old, err := s.st.GetRelease(ctx, app, env, in.ToGeneration)
		if errors.Is(err, store.ErrNotFound) {
			return rel, errNotFound("Generation %d does not exist in %s.", in.ToGeneration, env)
		}
		if err != nil {
			return rel, err
		}
		ns = old.State.Clone()
		ns.Policy = st.Policy
		rel.ChangeRequestID = in.ChangeRequestID
		rel.BreakGlass = in.ChangeRequestID == ""
		if rel.Reason == "" {
			rel.Reason = fmt.Sprintf("rollback to generation %d", in.ToGeneration)
		}
	case model.ReleaseKillSwitch:
		if in.Gate == "" {
			return rel, errInvalid("gate is required for a kill_switch release")
		}
		if _, ok := st.Live[model.LiveKey(model.KindGates, in.Gate)]; !ok {
			return rel, errUnprocessable("VALIDATION_FAILED", "Gate not live", "Gate %s is not live in %s.", in.Gate, env)
		}
		killed := in.Killed == nil || *in.Killed
		if killed {
			reason := in.Reason
			if reason == "" {
				reason = "killed"
			}
			ns.KilledGates[in.Gate] = reason
		} else {
			delete(ns.KilledGates, in.Gate)
		}
	default:
		return rel, errInvalid("unknown release type %q", in.Type)
	}

	set, _, err := s.verifyState(ctx, app, ns, gen)
	if err != nil {
		return rel, err
	}
	if err := s.checkBudgets(ctx, set); err != nil {
		return rel, err
	}
	meta := bundle.Meta{ReleaseType: in.Type, ReleaseID: fmt.Sprintf("%s/%s/%d", app, env, gen), PublishedBy: p.Actor()}
	built, err := s.bun.Build(set, meta, s.signer)
	if err != nil {
		return rel, fmt.Errorf("build bundle: %w", err)
	}
	limit := int64(a.BundleLimitMB) << 20
	if built.TotalBytes > limit {
		return rel, errUnprocessable("BUNDLE_TOO_LARGE", "Bundle too large", "The bundle is %d bytes; the app's limit is %d MB.", built.TotalBytes, a.BundleLimitMB)
	}
	needed := make([]string, 0, len(built.Blobs))
	if g, n, err := s.bun.VerifyEnvelope(built.Envelope, s.trusted); err == nil && g == gen {
		needed = n
	} else {
		if err != nil {
			slog.WarnContext(ctx, "could not verify freshly built envelope; using built blob list", "err", err)
		}
		for _, b := range built.Blobs {
			needed = append(needed, b.SHA256)
		}
	}
	// Upload blobs (content-addressed, skip existing), then the envelope.
	for _, b := range built.Blobs {
		key := objstore.BlobKey(b.SHA256)
		if ok, _ := s.obj.Exists(ctx, key); ok {
			continue
		}
		if err := s.obj.Put(ctx, key, b.Data); err != nil {
			return rel, fmt.Errorf("upload blob: %w", err)
		}
	}
	envKey := objstore.EnvelopeKey(app, env, gen)
	if err := s.obj.Put(ctx, envKey, built.Envelope); err != nil {
		return rel, fmt.Errorf("upload envelope: %w", err)
	}
	if err := s.obj.Put(ctx, objstore.BlobKey(built.EnvelopeSHA256), built.Envelope); err != nil {
		return rel, fmt.Errorf("upload envelope blob: %w", err)
	}

	rel.ManifestHash = "sha256:" + built.EnvelopeSHA256
	rel.BundleBytes = built.TotalBytes
	rel.MinEngineVersion = built.MinEngine
	rel.EnvelopeKey, rel.EnvelopeSHA256, rel.BlobHashes = envKey, built.EnvelopeSHA256, needed
	rel.State = ns
	rel.Contents = s.contents(ctx, app, env, st, set, ns)
	if err := s.st.CommitRelease(ctx, rel, envVer); err != nil {
		if errors.Is(err, store.ErrVersion) {
			return rel, errConflict("CONFLICT", "Concurrent publish", "%s/%s changed while publishing; retry.", app, env)
		}
		return rel, err
	}
	head, _ := json.Marshal(model.HeadPointer{Generation: gen, Envelope: envKey, SHA256: built.EnvelopeSHA256})
	if err := s.obj.Put(ctx, objstore.HeadKey(app, env), head); err != nil {
		slog.ErrorContext(ctx, "head pointer write failed (the API head is still correct)", "app", app, "env", env, "err", err)
	}
	if cr != nil {
		_, err := s.st.UpdateCR(ctx, app, cr.ID, func(c *model.ChangeRequest) error {
			c.Status = model.CRPublished
			g := gen
			c.PublishedGeneration = &g
			return nil
		})
		if err != nil {
			slog.ErrorContext(ctx, "marking change request published failed", "cr", cr.ID, "err", err)
		}
	}
	s.snaps.invalidate(app, env)
	s.auditBG(ctx, p, "release.published", app, env, fmt.Sprintf("releases/%s/%d", env, gen), rel.BreakGlass,
		map[string]any{"type": in.Type, "generation": gen, "manifestHash": rel.ManifestHash, "changeRequestId": rel.ChangeRequestID, "reason": rel.Reason})
	s.pub.Publish(context.WithoutCancel(ctx), feed.Event{App: app, Environment: env, Generation: gen})
	event := "release.published"
	if in.Type == model.ReleaseRollback {
		event = "release.rolled_back"
	}
	s.notify(ctx, event, map[string]any{"app": app, "environment": env, "generation": gen, "release": rel.Release})
	slog.InfoContext(ctx, "published", "app", app, "env", env, "generation", gen, "type", in.Type, "bytes", rel.BundleBytes)
	return rel, nil
}

// checkBudgets runs the engine benchmark and enforces budgetMicros.
func (s *Server) checkBudgets(ctx context.Context, set engine.Set) error {
	if !s.cfg.PublishBenchmark {
		return nil
	}
	snap, err := s.eng.Compile(set)
	if err != nil {
		return compileProblem(err, nil)
	}
	p99, err := snap.Benchmark(ctx, 50)
	if err != nil {
		slog.WarnContext(ctx, "benchmark failed; budget not checked", "err", err)
		return nil
	}
	var over []FieldError
	for _, d := range snap.Decisions() {
		budget := int64(200)
		for _, r := range set.Resources {
			if r.Kind == engine.KindDecision && r.Name == d.Name && r.Version == d.Version {
				var spec struct {
					BudgetMicros *int64 `json:"budgetMicros"`
				}
				if json.Unmarshal(r.Spec, &spec) == nil && spec.BudgetMicros != nil {
					budget = *spec.BudgetMicros
				}
			}
		}
		if got, ok := p99[d.Name]; ok && float64(got) > float64(budget)*s.cfg.BudgetFactor {
			over = append(over, FieldError{Path: "/decisions/" + d.Name + "/budgetMicros", Code: "COST_TOO_HIGH", Message: fmt.Sprintf("p99 %dµs exceeds the %dµs budget", got, budget)})
		}
	}
	if len(over) > 0 {
		return errUnprocessable("BUDGET_EXCEEDED", "Too slow", "%d decisions exceed their evaluation budget.", len(over)).with(over...)
	}
	return nil
}

// contents lists the bundle's resources with change flags.
func (s *Server) contents(ctx context.Context, app, env string, prev model.EnvState, set engine.Set, ns model.EnvState) []model.ReleaseContent {
	prevKeys := map[string]bool{}
	if prev.Generation > 0 {
		if old, err := s.st.GetRelease(ctx, app, env, prev.Generation); err == nil {
			for _, c := range old.Contents {
				k := c.Kind + "/" + c.Name + "@" + strconv.FormatInt(c.Version, 10)
				if c.Rollout != nil && c.Rollout.Rollout != nil {
					k += fmt.Sprintf("~%d/%d", c.Rollout.Rollout.FromVersion, c.Rollout.Rollout.Percent)
				}
				prevKeys[k] = true
			}
		}
	}
	rollFrom := map[string]int64{}
	for _, ro := range set.Rollouts {
		rollFrom[ro.Decision] = ro.FromVersion
	}
	out := []model.ReleaseContent{}
	seen := map[string]bool{}
	for _, r := range set.Resources {
		if from, ok := rollFrom[r.Name]; ok && r.Kind == engine.KindDecision && r.Version == from {
			continue // described by the rollout of the new version
		}
		k := string(r.Kind) + "/" + r.Name + "@" + strconv.FormatInt(r.Version, 10)
		if seen[k] {
			continue
		}
		seen[k] = true
		c := model.ReleaseContent{Kind: string(r.Kind), Name: r.Name, Version: r.Version}
		if e, ok := ns.Live[model.LiveKey(string(r.Kind), r.Name)]; ok && e.Rolling && e.Version == r.Version {
			ls := e.State()
			c.Rollout = &ls
			k += fmt.Sprintf("~%d/%d", e.RolloutFrom, e.RolloutPercent)
		}
		c.Changed = !prevKeys[k]
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env := r.PathValue("app"), r.PathValue("env")
	if _, _, err := s.loadEnv(ctx, p, app, env); err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	after := ""
	if pg.Cursor != "" {
		n, err := strconv.ParseInt(pg.Cursor, 10, 64)
		if err != nil {
			return errInvalid("cursor is not valid")
		}
		after = app + "/" + env + "/" + fmt.Sprintf("%015d", n)
	}
	page, err := s.st.ListReleases(ctx, app, env, after, pg.Limit)
	if err != nil {
		return err
	}
	out := make([]model.Release, 0, len(page.Items))
	for _, rr := range page.Items {
		out = append(out, releaseView(rr))
	}
	next := ""
	if page.Next != "" && len(out) > 0 {
		next = strconv.FormatInt(out[len(out)-1].Generation, 10)
	}
	writeJSON(w, http.StatusOK, list(out, next))
	return nil
}

func releaseView(rr model.ReleaseRecord) model.Release {
	r := rr.Release
	if r.Contents == nil {
		r.Contents = []model.ReleaseContent{}
	}
	return r
}

func (s *Server) handleGetRelease(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env := r.PathValue("app"), r.PathValue("env")
	if _, _, err := s.loadEnv(ctx, p, app, env); err != nil {
		return err
	}
	gen, err := strconv.ParseInt(r.PathValue("generation"), 10, 64)
	if err != nil {
		return errInvalid("generation must be an integer")
	}
	rr, err := s.st.GetRelease(ctx, app, env, gen)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("Generation %d does not exist in %s.", gen, env)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, releaseView(rr))
	return nil
}
