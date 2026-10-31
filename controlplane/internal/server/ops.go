package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

const fleetWindow = 10 * time.Minute

func sidecarView(rec model.SidecarRecord, gen int64) model.Sidecar {
	hb := rec.Heartbeat
	s := model.Sidecar{ID: hb.ID, Node: hb.Node, SidecarVersion: hb.SidecarVersion, EngineVersion: hb.EngineVersion, Generation: hb.Generation,
		LastSyncAt: hb.LastSyncAt, LastSeenAt: rec.LastSeenAt, P99Micros: hb.P99Micros, LastError: hb.LastError}
	switch {
	case hb.Generation >= gen:
		s.Status = "in_sync"
	case hb.LastError != "":
		s.Status = "error"
	default:
		s.Status = "behind"
	}
	return s
}

func (s *Server) fleet(ctx context.Context, app string, st model.EnvState) ([]model.SidecarRecord, []model.Sidecar, model.FleetSummary, error) {
	recs, err := s.st.ListSidecars(ctx, app, st.Name, s.now().Add(-fleetWindow))
	if err != nil {
		return nil, nil, model.FleetSummary{}, err
	}
	sum := model.FleetSummary{Generations: map[string]int{}, Versions: map[string]int{}}
	views := make([]model.Sidecar, 0, len(recs))
	var lags []float64
	var pubAt time.Time
	if st.PublishedAt != nil {
		pubAt = *st.PublishedAt
	}
	for _, r := range recs {
		v := sidecarView(r, st.Generation)
		views = append(views, v)
		sum.Total++
		if v.Status == "in_sync" {
			sum.OnLatest++
			if v.LastSyncAt != nil && !pubAt.IsZero() {
				lag := v.LastSyncAt.Sub(pubAt).Seconds()
				if lag < 0 {
					lag = 0
				}
				lags = append(lags, lag)
			}
		}
		sum.Generations[strconv.FormatInt(v.Generation, 10)]++
		if v.SidecarVersion != "" {
			sum.Versions[v.SidecarVersion]++
		}
	}
	if len(lags) > 0 {
		sort.Float64s(lags)
		m := lags[len(lags)/2]
		sum.MedianSyncLagSeconds = &m
	}
	rank := map[string]int{"error": 0, "behind": 1, "in_sync": 2}
	sort.SliceStable(views, func(i, j int) bool {
		if rank[views[i].Status] != rank[views[j].Status] {
			return rank[views[i].Status] < rank[views[j].Status]
		}
		if views[i].Generation != views[j].Generation {
			return views[i].Generation < views[j].Generation
		}
		return views[i].ID < views[j].ID
	})
	return recs, views, sum, nil
}

func (s *Server) handleListSidecars(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	st, _, err := s.loadEnv(ctx, p, r.PathValue("app"), r.PathValue("env"))
	if err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	_, views, sum, err := s.fleet(ctx, st.App, st)
	if err != nil {
		return err
	}
	items, next, err := pageSlice(views, pg)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		Summary    model.FleetSummary `json:"summary"`
		Items      []model.Sidecar    `json:"items"`
		NextCursor string             `json:"nextCursor,omitempty"`
	}{sum, nonNil(items), encodeCursor(next)})
	return nil
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, env := r.PathValue("app"), r.PathValue("env")
	st, _, err := s.loadEnv(ctx, p, app, env)
	if err != nil {
		return err
	}
	recs, _, sum, err := s.fleet(ctx, app, st)
	if err != nil {
		return err
	}
	ov := model.Overview{Generation: st.Generation, Sidecars: sum, LatencySeries: []model.LatencyPoint{}, RecentReleases: []model.Release{}}
	var calls, fallbackCalls float64
	for _, rec := range recs {
		hb := rec.Heartbeat
		if hb.P99Micros > ov.P99Micros {
			ov.P99Micros = hb.P99Micros
		}
		calls += hb.CallsPerMinute
		fallbackCalls += hb.CallsPerMinute * hb.FallbackRate
	}
	ov.CallsPerMinute = calls
	if calls > 0 {
		ov.FallbackRate = fallbackCalls / calls
	}
	for _, e := range st.Live {
		if e.Kind == model.KindDecisions {
			ov.Decisions++
		}
	}
	crs, err := s.st.ListCRs(ctx, app, "", 10000, func(cr model.ChangeRequest) bool {
		return cr.Environment == env && (cr.Status == model.CROpen || cr.Status == model.CRApproved)
	})
	if err != nil {
		return err
	}
	ov.PendingChangeRequests = len(crs.Items)
	// 48 half-hour points for the last 24 h; p99 = worst sidecar in the slot.
	now := s.now()
	end := now.Truncate(30 * time.Minute).Add(30 * time.Minute)
	start := end.Add(-24 * time.Hour)
	buckets := make([]int64, 48)
	hbs, _, err := s.st.QueryRecords(ctx, store.RecHeartbeats, store.RecordQuery{Eq: map[string]string{"app": app, "env": env}, From: start}, 500000)
	if err != nil {
		return err
	}
	for _, h := range hbs {
		var hb model.Heartbeat
		if json.Unmarshal(h.Data, &hb) != nil {
			continue
		}
		i := int(h.TS.Sub(start) / (30 * time.Minute))
		if i >= 0 && i < 48 && hb.P99Micros > buckets[i] {
			buckets[i] = hb.P99Micros
		}
	}
	for i, v := range buckets {
		ov.LatencySeries = append(ov.LatencySeries, model.LatencyPoint{At: start.Add(time.Duration(i) * 30 * time.Minute), P99Micros: v})
	}
	rels, err := s.st.ListReleases(ctx, app, env, "", 5)
	if err != nil {
		return err
	}
	for _, rr := range rels.Items {
		ov.RecentReleases = append(ov.RecentReleases, releaseView(rr))
	}
	writeJSON(w, http.StatusOK, ov)
	return nil
}

// queryLogs runs a log query and writes {items, nextCursor}.
func (s *Server) queryLogs(w http.ResponseWriter, r *http.Request, p *Principal, coll string, params map[string]string) error {
	ctx := r.Context()
	app, env := r.PathValue("app"), r.PathValue("env")
	if _, _, err := s.loadEnv(ctx, p, app, env); err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	eq := map[string]string{"app": app, "env": env}
	q := r.URL.Query()
	for param, field := range params {
		if v := q.Get(param); v != "" {
			eq[field] = v
		}
	}
	rq := store.RecordQuery{Eq: eq, Before: pg.Cursor}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return errInvalid("from must be RFC 3339")
		}
		rq.From = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return errInvalid("to must be RFC 3339")
		}
		rq.To = t
	}
	recs, next, err := s.st.QueryRecords(ctx, coll, rq, pg.Limit)
	if err != nil {
		return err
	}
	items := make([]json.RawMessage, 0, len(recs))
	for _, rec := range recs {
		items = append(items, rec.Data)
	}
	writeJSON(w, http.StatusOK, list(items, next))
	return nil
}

func (s *Server) handleSearchDecisionLogs(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return s.queryLogs(w, r, p, store.RecDecisions, map[string]string{"decision": "decision", "version": "version", "requestId": "requestId", "unitId": "unitId", "fallback": "fallback"})
}

func (s *Server) handleUpdateLogs(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return s.queryLogs(w, r, p, store.RecUpdates, map[string]string{"result": "result"})
}

func (s *Server) handleExposureLogs(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return s.queryLogs(w, r, p, store.RecExposures, map[string]string{"experiment": "experiment", "unitId": "unitId"})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleViewer, ""); err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	eq := map[string]string{}
	q := r.URL.Query()
	if v := q.Get("app"); v != "" {
		if !p.allowedApp(v) {
			return errForbidden("Your role does not cover app %s.", v)
		}
		eq["app"] = v
	}
	if v := q.Get("actor"); v != "" {
		eq["actor"] = v
	}
	var filter func(model.AuditEvent) bool
	if len(p.Member.Apps) > 0 {
		filter = func(ev model.AuditEvent) bool { return ev.App != "" && slices.Contains(p.Member.Apps, ev.App) }
	}
	evs, next, err := s.st.ListAudit(r.Context(), eq, pg.Cursor, pg.Limit, filter)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, list(evs, next))
	return nil
}

// ---------------------------------------------------------------- webhooks

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	ws, err := s.st.ListWebhooks(r.Context())
	if err != nil {
		return err
	}
	out := make([]model.Webhook, 0, len(ws))
	for _, wh := range ws {
		out = append(out, wh.Webhook)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	var in model.WebhookInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errInvalid("url must be an absolute http(s) URL")
	}
	for _, e := range in.Events {
		if !slices.Contains(model.WebhookEvents, e) {
			return errInvalid("unknown event %q (allowed: %v)", e, model.WebhookEvents)
		}
	}
	wh := model.WebhookRecord{Webhook: model.Webhook{ID: auth.RandomID("wh_"), URL: in.URL, Events: in.Events}, Secret: "whsec_" + auth.RandomString(32), CreatedBy: p.Actor(), CreatedAt: s.now()}
	if wh.Events == nil {
		wh.Events = []string{}
	}
	if err := s.st.CreateWebhook(r.Context(), wh); err != nil {
		return err
	}
	s.audit(r.Context(), p, "webhook.created", "", "", "webhooks/"+wh.ID, map[string]any{"url": wh.URL, "events": wh.Events})
	writeJSON(w, http.StatusCreated, struct {
		model.Webhook
		Secret string `json:"secret"`
	}{wh.Webhook, wh.Secret})
	return nil
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	id := r.PathValue("webhookId")
	if err := s.st.DeleteWebhook(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("Webhook %s does not exist.", id)
		}
		return err
	}
	s.audit(r.Context(), p, "webhook.deleted", "", "", "webhooks/"+id, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- meta

func (s *Server) handleExprCatalogue(w http.ResponseWriter, r *http.Request, p *Principal) error {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.eng.Catalogue())
	return nil
}

func (s *Server) handleServerVersion(w http.ResponseWriter, r *http.Request, _ *Principal) error {
	writeJSON(w, http.StatusOK, map[string]string{"server": Version, "engine": engine.Version, "minSidecar": MinSidecar})
	return nil
}
