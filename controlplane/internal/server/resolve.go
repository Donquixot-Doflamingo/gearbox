package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/ORG/gearbox/controlplane/internal/engineapi"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/objstore"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// ---------------------------------------------------------------- caches

// versionCache keeps immutable versions and data blobs.
type versionCache struct {
	mu   sync.Mutex
	max  int
	vers map[string]model.VersionRecord
	data map[string]any // sha -> []string | []model.LookupRow
}

func newVersionCache(max int) *versionCache {
	return &versionCache{max: max, vers: map[string]model.VersionRecord{}, data: map[string]any{}}
}

func (c *versionCache) getV(k string) (model.VersionRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vers[k]
	return v, ok
}

func (c *versionCache) putV(k string, v model.VersionRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.vers) >= c.max {
		c.vers = map[string]model.VersionRecord{}
	}
	c.vers[k] = v
}

func (c *versionCache) getD(sha string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[sha]
	return v, ok
}

func (c *versionCache) putD(sha string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= 64 {
		c.data = map[string]any{}
	}
	c.data[sha] = v
}

func (s *Server) loadVersion(ctx context.Context, app, kind, name string, v int64) (model.VersionRecord, error) {
	k := app + "/" + kind + "/" + name + "/" + itoa(v)
	if r, ok := s.versions.getV(k); ok {
		return r, nil
	}
	r, err := s.st.GetVersion(ctx, app, kind, name, v)
	if err != nil {
		return r, err
	}
	s.versions.putV(k, r)
	return r, nil
}

func (s *Server) loadMembers(ctx context.Context, ref *model.DataRef) ([]string, error) {
	if ref == nil {
		return []string{}, nil
	}
	if v, ok := s.versions.getD(ref.SHA256); ok {
		if m, ok := v.([]string); ok {
			return m, nil
		}
	}
	b, err := s.obj.Get(ctx, objstore.DataKey(ref.SHA256))
	if err != nil {
		return nil, fmt.Errorf("load segment members %s: %w", ref.SHA256, err)
	}
	var m []string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	s.versions.putD(ref.SHA256, m)
	return m, nil
}

func (s *Server) loadRows(ctx context.Context, ref *model.DataRef) ([]model.LookupRow, error) {
	if ref == nil {
		return []model.LookupRow{}, nil
	}
	if v, ok := s.versions.getD(ref.SHA256); ok {
		if m, ok := v.([]model.LookupRow); ok {
			return m, nil
		}
	}
	b, err := s.obj.Get(ctx, objstore.DataKey(ref.SHA256))
	if err != nil {
		return nil, fmt.Errorf("load lookup rows %s: %w", ref.SHA256, err)
	}
	var rows []model.LookupRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	s.versions.putD(ref.SHA256, rows)
	return rows, nil
}

// storeData writes members/rows JSON to the object store, content-addressed.
func (s *Server) storeData(ctx context.Context, v any, count int) (*model.DataRef, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	sha := objstore.SHA256Hex(b)
	key := objstore.DataKey(sha)
	if ok, _ := s.obj.Exists(ctx, key); !ok {
		if err := s.obj.Put(ctx, key, b); err != nil {
			return nil, err
		}
	}
	return &model.DataRef{SHA256: sha, Count: count}, nil
}

// engineResource builds an engine.Resource with its large data.
func (s *Server) engineResource(ctx context.Context, kind, name string, version int64, spec json.RawMessage, data *model.DataRef, withData bool) (engine.Resource, error) {
	r := engine.Resource{Kind: engine.Kind(kind), Name: name, Version: version, Spec: spec}
	if !withData || data == nil {
		if withData && kind == model.KindSegments {
			r.SegmentMembers = []string{}
		}
		return r, nil
	}
	switch kind {
	case model.KindSegments:
		m, err := s.loadMembers(ctx, data)
		if err != nil {
			return r, err
		}
		r.SegmentMembers = m
	case model.KindLookups:
		rows, err := s.loadRows(ctx, data)
		if err != nil {
			return r, err
		}
		r.LookupRows = make(map[string]json.RawMessage, len(rows))
		for _, row := range rows {
			r.LookupRows[row.Key] = row.Value
		}
	}
	return r, nil
}

// ---------------------------------------------------------------- resolver

// resolver answers engine lookups while validating one resource: a pinned
// version, else the latest version, else the draft (version 0).
type resolver struct {
	s   *Server
	ctx context.Context
	app string
	// overrides win (the resource being validated, by kind/name).
	overrides map[string]engine.Resource
}

func (rv resolver) Get(kind engine.Kind, name string, version int64) (engine.Resource, bool) {
	if o, ok := rv.overrides[string(kind)+"/"+name]; ok && (version == 0 || version == o.Version) {
		return o, true
	}
	if version > 0 {
		v, err := rv.s.loadVersion(rv.ctx, rv.app, string(kind), name, version)
		if err != nil {
			return engine.Resource{}, false
		}
		return engine.Resource{Kind: kind, Name: name, Version: version, Spec: v.Spec}, true
	}
	rec, _, err := rv.s.st.GetResource(rv.ctx, rv.app, string(kind), name)
	if err != nil || rec.Archived {
		return engine.Resource{}, false
	}
	if rec.LatestVersion > 0 {
		v, err := rv.s.loadVersion(rv.ctx, rv.app, string(kind), name, rec.LatestVersion)
		if err == nil {
			return engine.Resource{Kind: kind, Name: name, Version: v.Version, Spec: v.Spec}, true
		}
	}
	return engine.Resource{Kind: kind, Name: name, Version: 0, Spec: rec.Draft.Spec}, true
}

// ---------------------------------------------------------------- set assembly

// setBuilder collects a resource closure for engine.Compile.
type setBuilder struct {
	s         *Server
	ctx       context.Context
	app       string
	env       string
	res       map[string]engine.Resource // kind/name/version
	order     []string
	overrides map[string]engine.Resource // kind/name -> resource (drafts, test targets)
	// byName resolves a by-name reference to the versions to include.
	byName func(kind, name string) ([]int64, bool)
	errs   []FieldError
}

func (b *setBuilder) key(kind, name string, v int64) string { return kind + "/" + name + "/" + itoa(v) }

func (b *setBuilder) add(kind, name string, v int64, from string) {
	k := b.key(kind, name, v)
	if _, ok := b.res[k]; ok {
		return
	}
	var r engine.Resource
	if o, ok := b.overrides[kind+"/"+name]; ok && o.Version == v {
		r = o
	} else {
		vr, err := b.s.loadVersion(b.ctx, b.app, kind, name, v)
		if err != nil {
			msg := fmt.Sprintf("%s/%s@%d does not exist", kind, name, v)
			if !errors.Is(err, store.ErrNotFound) {
				msg = err.Error()
			}
			b.errs = append(b.errs, FieldError{Path: "/" + kind + "/" + name, Code: "NOT_FOUND", Message: msg + from})
			return
		}
		r, err = b.s.engineResource(b.ctx, kind, name, v, vr.Spec, vr.Data, true)
		if err != nil {
			b.errs = append(b.errs, FieldError{Path: "/" + kind + "/" + name, Code: "INTERNAL", Message: err.Error()})
			return
		}
	}
	b.res[k] = r
	b.order = append(b.order, k)
	self := fmt.Sprintf(" (used by %s/%s@%d)", kind, name, v)
	for _, d := range engineapi.Deps(kind, r.Spec) {
		if d.Kind == model.KindSchemas {
			if d.Version > 0 {
				b.add(d.Kind, d.Name, d.Version, self)
			}
			continue
		}
		vs, ok := b.byName(d.Kind, d.Name)
		if !ok {
			code := "DEPENDENCY_NOT_LIVE"
			b.errs = append(b.errs, FieldError{Path: "/" + kind + "/" + name + d.Where, Code: code,
				Message: fmt.Sprintf("%s/%s@%d uses %s %q, which is not live in %s", kind, name, v, d.Kind, d.Name, b.env)})
			continue
		}
		for _, dv := range vs {
			b.add(d.Kind, d.Name, dv, self)
		}
	}
}

func (b *setBuilder) resources() []engine.Resource {
	out := make([]engine.Resource, 0, len(b.order))
	keys := append([]string(nil), b.order...)
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, b.res[k])
	}
	return out
}

func liveVersions(st model.EnvState, kind, name string) ([]int64, bool) {
	e, ok := st.Live[model.LiveKey(kind, name)]
	if !ok {
		return nil, false
	}
	if e.Rolling && e.RolloutFrom > 0 && e.RolloutFrom != e.Version {
		return []int64{e.Version, e.RolloutFrom}, true
	}
	return []int64{e.Version}, true
}

// fullSet assembles the bundle content for an environment state.
func (s *Server) fullSet(ctx context.Context, app string, st model.EnvState, gen int64) (engine.Set, []FieldError) {
	b := &setBuilder{s: s, ctx: ctx, app: app, env: st.Name, res: map[string]engine.Resource{}, overrides: map[string]engine.Resource{},
		byName: func(kind, name string) ([]int64, bool) { return liveVersions(st, kind, name) }}
	keys := make([]string, 0, len(st.Live))
	for k := range st.Live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := st.Live[k]
		vs, _ := liveVersions(st, e.Kind, e.Name)
		for _, v := range vs {
			b.add(e.Kind, e.Name, v, "")
		}
	}
	set := engine.Set{App: app, Environment: st.Name, Generation: gen, Resources: b.resources()}
	for _, k := range keys {
		e := st.Live[k]
		if e.Rolling {
			set.Rollouts = append(set.Rollouts, engine.Rollout{Decision: e.Name, FromVersion: e.RolloutFrom, ToVersion: e.Version, Percent: e.RolloutPercent, Salt: e.RolloutSalt})
		}
	}
	for g := range st.KilledGates {
		set.KilledGates = append(set.KilledGates, g)
	}
	sort.Strings(set.KilledGates)
	return set, b.errs
}

// evalSet assembles a target resource (a draft as version 0, or a version)
// plus its dependency closure: live versions in env, else latest versions,
// else drafts. Used by evaluate, tests, segment checks and version cuts.
func (s *Server) evalSet(ctx context.Context, app, env string, target engine.Resource, targetData *model.DataRef) (engine.Set, []FieldError, error) {
	st, _, err := s.st.GetEnv(ctx, app, env)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return engine.Set{}, nil, errNotFound("App %s has no environment %s.", app, env)
		}
		return engine.Set{}, nil, err
	}
	tr, err := s.engineResource(ctx, string(target.Kind), target.Name, target.Version, target.Spec, targetData, true)
	if err != nil {
		return engine.Set{}, nil, err
	}
	b := &setBuilder{s: s, ctx: ctx, app: app, env: env, res: map[string]engine.Resource{}, overrides: map[string]engine.Resource{string(target.Kind) + "/" + target.Name: tr}}
	b.byName = func(kind, name string) ([]int64, bool) {
		if o, ok := b.overrides[kind+"/"+name]; ok {
			return []int64{o.Version}, true
		}
		if e, ok := st.Live[model.LiveKey(kind, name)]; ok {
			return []int64{e.Version}, true
		}
		rec, _, err := s.st.GetResource(ctx, app, kind, name)
		if err != nil || rec.Archived {
			return nil, false
		}
		if rec.LatestVersion > 0 {
			return []int64{rec.LatestVersion}, true
		}
		dr, err := s.engineResource(ctx, kind, name, 0, rec.Draft.Spec, rec.Draft.Data, true)
		if err != nil {
			return nil, false
		}
		b.overrides[kind+"/"+name] = dr
		return []int64{0}, true
	}
	b.add(string(target.Kind), target.Name, target.Version, "")
	set := engine.Set{App: app, Environment: env, Generation: st.Generation, Resources: b.resources()}
	for g := range st.KilledGates {
		set.KilledGates = append(set.KilledGates, g)
	}
	sort.Strings(set.KilledGates)
	return set, b.errs, nil
}

// compileProblem turns a compile error into a 422.
func compileProblem(err error, errs []FieldError) *Problem {
	p := errUnprocessable("VALIDATION_FAILED", "Does not compile", "The definitions do not compile: %v", err)
	var ce *engineapi.CompileError
	if errors.As(err, &ce) {
		for _, m := range ce.Problems {
			p.Errors = append(p.Errors, FieldError{Path: "/", Code: "COMPILE", Message: m})
		}
	}
	return p.with(errs...)
}

// ---------------------------------------------------------------- runtime snapshots

type snapEntry struct {
	gen  int64
	snap engineapi.Snapshot
}

type snapshotCache struct {
	mu sync.Mutex
	m  map[string]snapEntry
}

func newSnapshotCache() *snapshotCache { return &snapshotCache{m: map[string]snapEntry{}} }

func (c *snapshotCache) invalidate(app, env string) {
	c.mu.Lock()
	delete(c.m, app+"/"+env)
	c.mu.Unlock()
}

// runtimeSnapshot returns the compiled snapshot of an environment's current
// generation (404 when nothing is published).
func (s *Server) runtimeSnapshot(ctx context.Context, app, env string) (engineapi.Snapshot, model.EnvState, error) {
	st, _, err := s.st.GetEnv(ctx, app, env)
	if errors.Is(err, store.ErrNotFound) {
		return nil, st, errNotFound("App %s has no environment %s.", app, env)
	}
	if err != nil {
		return nil, st, err
	}
	if st.Generation == 0 {
		return nil, st, errNotFound("Nothing is published to %s/%s yet.", app, env)
	}
	k := app + "/" + env
	s.snaps.mu.Lock()
	defer s.snaps.mu.Unlock()
	if e, ok := s.snaps.m[k]; ok && e.gen == st.Generation {
		return e.snap, st, nil
	}
	set, errs := s.fullSet(ctx, app, st, st.Generation)
	if len(errs) > 0 {
		return nil, st, problemf(500, "INTERNAL", "Bundle incomplete", "The published state of %s/%s cannot be assembled: %s", app, env, errs[0].Message)
	}
	snap, err := s.eng.Compile(set)
	if err != nil {
		return nil, st, fmt.Errorf("compile %s/%s generation %d: %w", app, env, st.Generation, err)
	}
	s.snaps.m[k] = snapEntry{gen: st.Generation, snap: snap}
	return snap, st, nil
}
