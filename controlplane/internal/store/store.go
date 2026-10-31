package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/model"
)

// Collections.
const (
	CollApps        = "apps"
	CollEnvs        = "envs"
	CollResources   = "resources"
	CollVersions    = "versions"
	CollCRs         = "change_requests"
	CollReleases    = "releases"
	CollMembers     = "members"
	CollTokens      = "tokens"
	CollCredentials = "credentials"
	CollCredHashes  = "credential_hashes"
	CollSessions    = "sessions"
	CollWebhooks    = "webhooks"
	CollOutbox      = "outbox"
	CollSidecars    = "sidecars"
	CollCounters    = "counters"

	RecAudit      = "audit"
	RecDecisions  = "decision_logs"
	RecUpdates    = "update_logs"
	RecExposures  = "exposure_logs"
	RecHeartbeats = "heartbeats"
)

// Store is the typed persistence layer.
type Store struct {
	B Backend
}

// New wraps a backend.
func New(b Backend) *Store { return &Store{B: b} }

// Close closes the backend.
func (s *Store) Close() error { return s.B.Close() }

func getJSON[T any](ctx context.Context, b Backend, coll, id string) (T, int64, error) {
	var v T
	d, err := b.Get(ctx, coll, id)
	if err != nil {
		return v, 0, err
	}
	if err := json.Unmarshal(d.Data, &v); err != nil {
		return v, 0, fmt.Errorf("store: decode %s/%s: %w", coll, id, err)
	}
	return v, d.Ver, nil
}

func putJSON(ctx context.Context, b Backend, coll, id string, v any, expect int64) (int64, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	return b.Put(ctx, coll, id, data, expect)
}

func listJSON[T any](ctx context.Context, b Backend, coll string, q ListQuery) ([]T, []string, error) {
	docs, err := b.List(ctx, coll, q)
	if err != nil {
		return nil, nil, err
	}
	out := make([]T, 0, len(docs))
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		var v T
		if err := json.Unmarshal(d.Data, &v); err != nil {
			return nil, nil, fmt.Errorf("store: decode %s/%s: %w", coll, d.ID, err)
		}
		out = append(out, v)
		ids = append(ids, d.ID)
	}
	return out, ids, nil
}

// Page is one page of a listing; Next is the cursor for the next page ("" on
// the last page).
type Page[T any] struct {
	Items []T
	Next  string
}

// page lists limit+1 items to know whether a next page exists.
func page[T any](ctx context.Context, b Backend, coll string, q ListQuery, limit int) (Page[T], error) {
	q.Limit = limit + 1
	items, ids, err := listJSON[T](ctx, b, coll, q)
	if err != nil {
		return Page[T]{}, err
	}
	p := Page[T]{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		p.Next = ids[limit-1]
	}
	return p, nil
}

// Update applies fn to the current document with compare-and-set retries.
func update[T any](ctx context.Context, b Backend, coll, id string, fn func(*T) error) (T, error) {
	for attempt := 0; attempt < 50; attempt++ {
		v, ver, err := getJSON[T](ctx, b, coll, id)
		if err != nil {
			return v, err
		}
		if err := fn(&v); err != nil {
			return v, err
		}
		if _, err := putJSON(ctx, b, coll, id, v, ver); err != nil {
			if errors.Is(err, ErrVersion) {
				continue
			}
			return v, err
		}
		return v, nil
	}
	var zero T
	return zero, fmt.Errorf("store: too much contention on %s/%s", coll, id)
}

func pad(n int64) string { return fmt.Sprintf("%015d", n) }

// ---------------------------------------------------------------- apps and environments

func (s *Store) CreateApp(ctx context.Context, a model.App, envs []model.EnvState) error {
	if _, err := putJSON(ctx, s.B, CollApps, a.Name, a, CreateOnly); err != nil {
		return err
	}
	for _, e := range envs {
		if _, err := putJSON(ctx, s.B, CollEnvs, a.Name+"/"+e.Name, e, Upsert); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetApp(ctx context.Context, name string) (model.App, error) {
	a, _, err := getJSON[model.App](ctx, s.B, CollApps, name)
	return a, err
}

func (s *Store) UpdateApp(ctx context.Context, name string, fn func(*model.App) error) (model.App, error) {
	return update(ctx, s.B, CollApps, name, fn)
}

func (s *Store) ListApps(ctx context.Context, after string, limit int, filter func(model.App) bool) (Page[model.App], error) {
	q := ListQuery{After: after}
	if filter != nil {
		q.Filter = func(d Doc) bool {
			var a model.App
			return json.Unmarshal(d.Data, &a) == nil && filter(a)
		}
	}
	return page[model.App](ctx, s.B, CollApps, q, limit)
}

// GetEnv returns the environment state and its document version.
func (s *Store) GetEnv(ctx context.Context, app, env string) (model.EnvState, int64, error) {
	st, ver, err := getJSON[model.EnvState](ctx, s.B, CollEnvs, app+"/"+env)
	if st.Live == nil {
		st.Live = map[string]model.LiveEntry{}
	}
	if st.KilledGates == nil {
		st.KilledGates = map[string]string{}
	}
	return st, ver, err
}

func (s *Store) ListEnvs(ctx context.Context, app string) ([]model.EnvState, error) {
	items, _, err := listJSON[model.EnvState](ctx, s.B, CollEnvs, ListQuery{Prefix: app + "/"})
	return items, err
}

func (s *Store) UpdateEnvPolicy(ctx context.Context, app, env string, p model.EnvironmentPolicy) (model.EnvState, error) {
	return update(ctx, s.B, CollEnvs, app+"/"+env, func(st *model.EnvState) error {
		st.Policy = st.Policy.Apply(p)
		return nil
	})
}

// CommitRelease atomically moves the environment from generation
// rel.Generation-1 to rel.Generation with the new state, then records the
// release. ErrVersion means another publish won the race; the caller must
// rebuild (generations stay strictly increasing).
func (s *Store) CommitRelease(ctx context.Context, rel model.ReleaseRecord, envVer int64) error {
	cur, ver, err := s.GetEnv(ctx, rel.App, rel.Environment)
	if err != nil {
		return err
	}
	if ver != envVer || cur.Generation != rel.Generation-1 {
		return ErrVersion
	}
	st := rel.State
	st.Policy = cur.Policy // policy is not part of a release
	st.Generation = rel.Generation
	at := rel.PublishedAt
	st.PublishedAt = &at
	rel.State = st
	if _, err := putJSON(ctx, s.B, CollEnvs, rel.App+"/"+rel.Environment, st, envVer); err != nil {
		return err
	}
	_, err = putJSON(ctx, s.B, CollReleases, rel.App+"/"+rel.Environment+"/"+pad(rel.Generation), rel, Upsert)
	return err
}

func (s *Store) GetRelease(ctx context.Context, app, env string, gen int64) (model.ReleaseRecord, error) {
	r, _, err := getJSON[model.ReleaseRecord](ctx, s.B, CollReleases, app+"/"+env+"/"+pad(gen))
	return r, err
}

// ListReleases lists newest first.
func (s *Store) ListReleases(ctx context.Context, app, env, after string, limit int) (Page[model.ReleaseRecord], error) {
	return page[model.ReleaseRecord](ctx, s.B, CollReleases, ListQuery{Prefix: app + "/" + env + "/", After: after, Desc: true}, limit)
}

// ---------------------------------------------------------------- resources

func resID(app, kind, name string) string { return app + "/" + kind + "/" + name }

func (s *Store) CreateResource(ctx context.Context, r model.ResourceRecord) error {
	_, err := putJSON(ctx, s.B, CollResources, resID(r.App, r.Kind, r.Name), r, CreateOnly)
	return err
}

// GetResource returns the record and its document version.
func (s *Store) GetResource(ctx context.Context, app, kind, name string) (model.ResourceRecord, int64, error) {
	return getJSON[model.ResourceRecord](ctx, s.B, CollResources, resID(app, kind, name))
}

// PutResource writes with compare-and-set on the document version.
func (s *Store) PutResource(ctx context.Context, r model.ResourceRecord, ver int64) error {
	_, err := putJSON(ctx, s.B, CollResources, resID(r.App, r.Kind, r.Name), r, ver)
	return err
}

func (s *Store) ListResources(ctx context.Context, app, kind, after string, limit int, filter func(model.ResourceRecord) bool) (Page[model.ResourceRecord], error) {
	prefix := app + "/" + kind + "/"
	if after != "" {
		after = prefix + after
	}
	q := ListQuery{Prefix: prefix, After: after, Filter: func(d Doc) bool {
		var r model.ResourceRecord
		if json.Unmarshal(d.Data, &r) != nil || r.Archived {
			return false
		}
		return filter == nil || filter(r)
	}}
	p, err := page[model.ResourceRecord](ctx, s.B, CollResources, q, limit)
	if p.Next != "" {
		p.Next = strings.TrimPrefix(p.Next, prefix)
	}
	return p, err
}

// AllResources lists every non-archived resource of an app (all kinds).
func (s *Store) AllResources(ctx context.Context, app string) ([]model.ResourceRecord, error) {
	items, _, err := listJSON[model.ResourceRecord](ctx, s.B, CollResources, ListQuery{Prefix: app + "/", Filter: func(d Doc) bool {
		var r model.ResourceRecord
		return json.Unmarshal(d.Data, &r) == nil && !r.Archived
	}})
	return items, err
}

func verID(app, kind, name string, v int64) string {
	return resID(app, kind, name) + "/" + pad(v)
}

// CreateVersion stores an immutable version; ErrExists when that number is
// taken (concurrent cut).
func (s *Store) CreateVersion(ctx context.Context, v model.VersionRecord) error {
	_, err := putJSON(ctx, s.B, CollVersions, verID(v.App, v.Kind, v.Name, v.Version), v, CreateOnly)
	return err
}

func (s *Store) GetVersion(ctx context.Context, app, kind, name string, v int64) (model.VersionRecord, error) {
	r, _, err := getJSON[model.VersionRecord](ctx, s.B, CollVersions, verID(app, kind, name, v))
	return r, err
}

// ListVersions lists newest first; the cursor is a version number.
func (s *Store) ListVersions(ctx context.Context, app, kind, name, after string, limit int) (Page[model.VersionRecord], error) {
	prefix := resID(app, kind, name) + "/"
	q := ListQuery{Prefix: prefix, Desc: true}
	if after != "" {
		n, err := strconv.ParseInt(after, 10, 64)
		if err != nil {
			return Page[model.VersionRecord]{}, fmt.Errorf("store: bad cursor")
		}
		q.After = prefix + pad(n)
	}
	p, err := page[model.VersionRecord](ctx, s.B, CollVersions, q, limit)
	if p.Next != "" {
		n, _ := strconv.ParseInt(strings.TrimPrefix(p.Next, prefix), 10, 64)
		p.Next = strconv.FormatInt(n, 10)
	}
	return p, err
}

// AllVersions of one resource, oldest first.
func (s *Store) AllVersions(ctx context.Context, app, kind, name string) ([]model.VersionRecord, error) {
	items, _, err := listJSON[model.VersionRecord](ctx, s.B, CollVersions, ListQuery{Prefix: resID(app, kind, name) + "/"})
	return items, err
}

// AllAppVersions lists every version of every resource in an app.
func (s *Store) AllAppVersions(ctx context.Context, app string) ([]model.VersionRecord, error) {
	items, _, err := listJSON[model.VersionRecord](ctx, s.B, CollVersions, ListQuery{Prefix: app + "/"})
	return items, err
}

// ---------------------------------------------------------------- counters

// Next increments a named counter and returns the new value.
func (s *Store) Next(ctx context.Context, name string) (int64, error) {
	type counter struct {
		N int64 `json:"n"`
	}
	for attempt := 0; attempt < 100; attempt++ {
		c, ver, err := getJSON[counter](ctx, s.B, CollCounters, name)
		if errors.Is(err, ErrNotFound) {
			if _, err := putJSON(ctx, s.B, CollCounters, name, counter{N: 1}, CreateOnly); err == nil {
				return 1, nil
			} else if !errors.Is(err, ErrExists) {
				return 0, err
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		c.N++
		if _, err := putJSON(ctx, s.B, CollCounters, name, c, ver); err == nil {
			return c.N, nil
		} else if !errors.Is(err, ErrVersion) {
			return 0, err
		}
	}
	return 0, fmt.Errorf("store: counter %s contended", name)
}

// ---------------------------------------------------------------- change requests

type crRecord struct {
	model.ChangeRequest
	App string `json:"app"`
}

func crKey(app, id string) (string, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "cr_"), 10, 64)
	if err != nil || !strings.HasPrefix(id, "cr_") {
		return "", ErrNotFound
	}
	return app + "/" + pad(n), nil
}

func (s *Store) CreateCR(ctx context.Context, cr model.ChangeRequest) (model.ChangeRequest, error) {
	n, err := s.Next(ctx, "cr/"+cr.App)
	if err != nil {
		return cr, err
	}
	cr.ID = "cr_" + strconv.FormatInt(n, 10)
	_, err = putJSON(ctx, s.B, CollCRs, cr.App+"/"+pad(n), crRecord{ChangeRequest: cr, App: cr.App}, CreateOnly)
	return cr, err
}

func (s *Store) GetCR(ctx context.Context, app, id string) (model.ChangeRequest, error) {
	k, err := crKey(app, id)
	if err != nil {
		return model.ChangeRequest{}, err
	}
	r, _, err := getJSON[crRecord](ctx, s.B, CollCRs, k)
	r.ChangeRequest.App = r.App
	return r.ChangeRequest, err
}

func (s *Store) UpdateCR(ctx context.Context, app, id string, fn func(*model.ChangeRequest) error) (model.ChangeRequest, error) {
	k, err := crKey(app, id)
	if err != nil {
		return model.ChangeRequest{}, err
	}
	r, err := update(ctx, s.B, CollCRs, k, func(r *crRecord) error {
		r.ChangeRequest.App = r.App
		return fn(&r.ChangeRequest)
	})
	r.ChangeRequest.App = r.App
	return r.ChangeRequest, err
}

// ListCRs lists newest first; the cursor is a change request id.
func (s *Store) ListCRs(ctx context.Context, app, after string, limit int, filter func(model.ChangeRequest) bool) (Page[model.ChangeRequest], error) {
	q := ListQuery{Prefix: app + "/", Desc: true}
	if after != "" {
		k, err := crKey(app, after)
		if err != nil {
			return Page[model.ChangeRequest]{}, fmt.Errorf("store: bad cursor")
		}
		q.After = k
	}
	if filter != nil {
		q.Filter = func(d Doc) bool {
			var r crRecord
			return json.Unmarshal(d.Data, &r) == nil && filter(r.ChangeRequest)
		}
	}
	p, err := page[crRecord](ctx, s.B, CollCRs, q, limit)
	out := Page[model.ChangeRequest]{}
	for _, r := range p.Items {
		r.ChangeRequest.App = r.App
		out.Items = append(out.Items, r.ChangeRequest)
	}
	if p.Next != "" && len(out.Items) > 0 {
		out.Next = out.Items[len(out.Items)-1].ID
	}
	return out, err
}

// ---------------------------------------------------------------- members, tokens, sessions

func (s *Store) CreateMember(ctx context.Context, m model.Member) error {
	if _, err := s.MemberByEmail(ctx, m.Email); err == nil {
		return ErrExists
	}
	_, err := putJSON(ctx, s.B, CollMembers, m.ID, m, CreateOnly)
	return err
}

func (s *Store) GetMember(ctx context.Context, id string) (model.Member, error) {
	m, _, err := getJSON[model.Member](ctx, s.B, CollMembers, id)
	return m, err
}

func (s *Store) MemberByEmail(ctx context.Context, email string) (model.Member, error) {
	items, _, err := listJSON[model.Member](ctx, s.B, CollMembers, ListQuery{Filter: func(d Doc) bool {
		var m model.Member
		return json.Unmarshal(d.Data, &m) == nil && strings.EqualFold(m.Email, email)
	}, Limit: 1})
	if err != nil {
		return model.Member{}, err
	}
	if len(items) == 0 {
		return model.Member{}, ErrNotFound
	}
	return items[0], nil
}

func (s *Store) ListMembers(ctx context.Context) ([]model.Member, error) {
	items, _, err := listJSON[model.Member](ctx, s.B, CollMembers, ListQuery{})
	return items, err
}

func (s *Store) UpdateMember(ctx context.Context, id string, fn func(*model.Member) error) (model.Member, error) {
	return update(ctx, s.B, CollMembers, id, fn)
}

func (s *Store) DeleteMember(ctx context.Context, id string) error {
	return s.B.Delete(ctx, CollMembers, id)
}

func (s *Store) CreateToken(ctx context.Context, t model.Token) error {
	_, err := putJSON(ctx, s.B, CollTokens, t.Hash, t, CreateOnly)
	return err
}

func (s *Store) TokenByHash(ctx context.Context, hash string) (model.Token, error) {
	t, _, err := getJSON[model.Token](ctx, s.B, CollTokens, hash)
	return t, err
}

func (s *Store) CreateSession(ctx context.Context, ws model.WebSession) error {
	_, err := putJSON(ctx, s.B, CollSessions, ws.ID, ws, CreateOnly)
	return err
}

func (s *Store) GetSession(ctx context.Context, id string) (model.WebSession, error) {
	ws, _, err := getJSON[model.WebSession](ctx, s.B, CollSessions, id)
	return ws, err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	err := s.B.Delete(ctx, CollSessions, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------- credentials

type credHash struct {
	App string `json:"app"`
	Env string `json:"env"`
	ID  string `json:"id"`
}

func (s *Store) CreateCredential(ctx context.Context, c model.CredentialRecord) error {
	if _, err := putJSON(ctx, s.B, CollCredHashes, c.Hash, credHash{App: c.App, Env: c.Environment, ID: c.ID}, CreateOnly); err != nil {
		return err
	}
	_, err := putJSON(ctx, s.B, CollCredentials, c.App+"/"+c.Environment+"/"+c.ID, c, CreateOnly)
	return err
}

func (s *Store) CredentialByHash(ctx context.Context, hash string) (model.CredentialRecord, error) {
	h, _, err := getJSON[credHash](ctx, s.B, CollCredHashes, hash)
	if err != nil {
		return model.CredentialRecord{}, err
	}
	c, _, err := getJSON[model.CredentialRecord](ctx, s.B, CollCredentials, h.App+"/"+h.Env+"/"+h.ID)
	return c, err
}

func (s *Store) ListCredentials(ctx context.Context, app, env string) ([]model.CredentialRecord, error) {
	items, _, err := listJSON[model.CredentialRecord](ctx, s.B, CollCredentials, ListQuery{Prefix: app + "/" + env + "/"})
	return items, err
}

func (s *Store) UpdateCredential(ctx context.Context, app, env, id string, fn func(*model.CredentialRecord) error) (model.CredentialRecord, error) {
	return update(ctx, s.B, CollCredentials, app+"/"+env+"/"+id, fn)
}

// ---------------------------------------------------------------- webhooks and outbox

func (s *Store) CreateWebhook(ctx context.Context, w model.WebhookRecord) error {
	_, err := putJSON(ctx, s.B, CollWebhooks, w.ID, w, CreateOnly)
	return err
}

func (s *Store) ListWebhooks(ctx context.Context) ([]model.WebhookRecord, error) {
	items, _, err := listJSON[model.WebhookRecord](ctx, s.B, CollWebhooks, ListQuery{})
	return items, err
}

func (s *Store) GetWebhook(ctx context.Context, id string) (model.WebhookRecord, error) {
	w, _, err := getJSON[model.WebhookRecord](ctx, s.B, CollWebhooks, id)
	return w, err
}

func (s *Store) UpdateWebhook(ctx context.Context, id string, fn func(*model.WebhookRecord) error) (model.WebhookRecord, error) {
	return update(ctx, s.B, CollWebhooks, id, fn)
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	return s.B.Delete(ctx, CollWebhooks, id)
}

func (s *Store) EnqueueOutbox(ctx context.Context, it model.OutboxItem) error {
	_, err := putJSON(ctx, s.B, CollOutbox, it.ID, it, CreateOnly)
	return err
}

// ClaimOutbox leases up to n due items for lease.
func (s *Store) ClaimOutbox(ctx context.Context, now time.Time, n int, lease time.Duration) ([]model.OutboxItem, error) {
	docs, err := s.B.List(ctx, CollOutbox, ListQuery{Filter: func(d Doc) bool {
		var it model.OutboxItem
		return json.Unmarshal(d.Data, &it) == nil && !it.NextAt.After(now) && !it.LeaseUntil.After(now)
	}, Limit: n})
	if err != nil {
		return nil, err
	}
	var out []model.OutboxItem
	for _, d := range docs {
		var it model.OutboxItem
		if err := json.Unmarshal(d.Data, &it); err != nil {
			continue
		}
		it.LeaseUntil = now.Add(lease)
		if _, err := putJSON(ctx, s.B, CollOutbox, it.ID, it, d.Ver); err != nil {
			continue // someone else claimed it
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Store) PutOutbox(ctx context.Context, it model.OutboxItem) error {
	_, err := putJSON(ctx, s.B, CollOutbox, it.ID, it, Upsert)
	return err
}

func (s *Store) DeleteOutbox(ctx context.Context, id string) error {
	err := s.B.Delete(ctx, CollOutbox, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------- fleet

func (s *Store) UpsertSidecar(ctx context.Context, r model.SidecarRecord) error {
	_, err := putJSON(ctx, s.B, CollSidecars, r.App+"/"+r.Environment+"/"+r.Heartbeat.ID, r, Upsert)
	return err
}

// ListSidecars returns sidecars seen since `since`.
func (s *Store) ListSidecars(ctx context.Context, app, env string, since time.Time) ([]model.SidecarRecord, error) {
	items, _, err := listJSON[model.SidecarRecord](ctx, s.B, CollSidecars, ListQuery{Prefix: app + "/" + env + "/", Filter: func(d Doc) bool {
		var r model.SidecarRecord
		return json.Unmarshal(d.Data, &r) == nil && !r.LastSeenAt.Before(since)
	}})
	return items, err
}

// ---------------------------------------------------------------- records

// AppendAudit records an audit event (kept forever).
func (s *Store) AppendAudit(ctx context.Context, ev model.AuditEvent) (model.AuditEvent, error) {
	if ev.ID == "" {
		ev.ID = NewRecordID(ev.At)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	return ev, s.B.Append(ctx, RecAudit, []Record{{ID: ev.ID, TS: ev.At, Fields: map[string]string{"app": ev.App, "actor": ev.Actor}, Data: data}})
}

// ListAudit lists newest first.
func (s *Store) ListAudit(ctx context.Context, eq map[string]string, before string, limit int, filter func(model.AuditEvent) bool) ([]model.AuditEvent, string, error) {
	q := RecordQuery{Eq: eq, Before: before, Limit: limit + 1}
	if filter != nil {
		q.Filter = func(r Record) bool {
			var ev model.AuditEvent
			return json.Unmarshal(r.Data, &ev) == nil && filter(ev)
		}
	}
	recs, err := s.B.Query(ctx, RecAudit, q)
	if err != nil {
		return nil, "", err
	}
	out := make([]model.AuditEvent, 0, len(recs))
	for _, r := range recs {
		var ev model.AuditEvent
		if json.Unmarshal(r.Data, &ev) == nil {
			out = append(out, ev)
		}
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}

// AppendRecords stores raw log records.
func (s *Store) AppendRecords(ctx context.Context, coll string, recs []Record) error {
	return s.B.Append(ctx, coll, recs)
}

// QueryRecords returns up to limit records plus the next cursor.
func (s *Store) QueryRecords(ctx context.Context, coll string, q RecordQuery, limit int) ([]Record, string, error) {
	q.Limit = limit + 1
	recs, err := s.B.Query(ctx, coll, q)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(recs) > limit {
		recs = recs[:limit]
		next = recs[limit-1].ID
	}
	return recs, next, nil
}

// SortStrings is a helper used by callers that need deterministic output.
func SortStrings(s []string) []string { sort.Strings(s); return s }
