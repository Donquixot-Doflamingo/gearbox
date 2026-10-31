package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process Backend. With a directory it snapshots its whole
// state to <dir>/store.json (atomically, at most once per second and on
// Close) and reloads it on start, which is enough for single-node
// development. Record collections are bounded rings (MaxRecords per
// collection; oldest dropped first).
type Memory struct {
	mu      sync.RWMutex
	docs    map[string]map[string]Doc // coll -> id -> doc
	recs    map[string][]Record       // coll -> sorted by ID ascending
	locks   map[string]*memLock
	locksMu sync.Mutex

	dir        string
	dirty      bool
	maxRecords int
	stop       chan struct{}
	done       chan struct{}
	now        func() time.Time
}

type memLock struct {
	ch chan struct{} // capacity 1: holding = sent
}

// DefaultMaxRecords bounds each record collection in memory.
const DefaultMaxRecords = 200_000

// NewMemory returns an empty in-memory backend. dir may be empty (no
// persistence).
func NewMemory(dir string) (*Memory, error) {
	m := &Memory{
		docs:       map[string]map[string]Doc{},
		recs:       map[string][]Record{},
		locks:      map[string]*memLock{},
		dir:        dir,
		maxRecords: DefaultMaxRecords,
		now:        time.Now,
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		if err := m.load(); err != nil {
			return nil, err
		}
		m.stop = make(chan struct{})
		m.done = make(chan struct{})
		go m.flusher()
	}
	return m, nil
}

// SetMaxRecords changes the per-collection record bound (tests).
func (m *Memory) SetMaxRecords(n int) { m.mu.Lock(); m.maxRecords = n; m.mu.Unlock() }

type snapshot struct {
	Docs map[string]map[string]snapDoc `json:"docs"`
	Recs map[string][]snapRec          `json:"recs"`
}

type snapDoc struct {
	Data json.RawMessage `json:"d"`
	Ver  int64           `json:"v"`
}

type snapRec struct {
	ID       string            `json:"id"`
	TS       time.Time         `json:"ts"`
	Fields   map[string]string `json:"f,omitempty"`
	Data     json.RawMessage   `json:"d"`
	ExpireAt time.Time         `json:"e,omitempty"`
}

func (m *Memory) path() string { return filepath.Join(m.dir, "store.json") }

func (m *Memory) load() error {
	b, err := os.ReadFile(m.path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: read snapshot: %w", err)
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("store: decode snapshot %s: %w", m.path(), err)
	}
	for coll, docs := range s.Docs {
		mm := map[string]Doc{}
		for id, d := range docs {
			mm[id] = Doc{ID: id, Data: []byte(d.Data), Ver: d.Ver}
		}
		m.docs[coll] = mm
	}
	for coll, recs := range s.Recs {
		out := make([]Record, 0, len(recs))
		for _, r := range recs {
			out = append(out, Record{ID: r.ID, TS: r.TS, Fields: r.Fields, Data: []byte(r.Data), ExpireAt: r.ExpireAt})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		m.recs[coll] = out
	}
	return nil
}

func (m *Memory) flusher() {
	defer close(m.done)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			if err := m.Flush(); err != nil {
				slog.Error("store snapshot failed", "err", err)
			}
			return
		case <-t.C:
			if err := m.Flush(); err != nil {
				slog.Error("store snapshot failed", "err", err)
			}
		}
	}
}

// Flush writes the snapshot now if anything changed.
func (m *Memory) Flush() error {
	if m.dir == "" {
		return nil
	}
	m.mu.Lock()
	if !m.dirty {
		m.mu.Unlock()
		return nil
	}
	now := m.now()
	s := snapshot{Docs: map[string]map[string]snapDoc{}, Recs: map[string][]snapRec{}}
	for coll, docs := range m.docs {
		mm := make(map[string]snapDoc, len(docs))
		for id, d := range docs {
			mm[id] = snapDoc{Data: json.RawMessage(d.Data), Ver: d.Ver}
		}
		s.Docs[coll] = mm
	}
	for coll, recs := range m.recs {
		out := make([]snapRec, 0, len(recs))
		for _, r := range recs {
			if !r.ExpireAt.IsZero() && !r.ExpireAt.After(now) {
				continue
			}
			out = append(out, snapRec{ID: r.ID, TS: r.TS, Fields: r.Fields, Data: json.RawMessage(r.Data), ExpireAt: r.ExpireAt})
		}
		s.Recs[coll] = out
	}
	m.dirty = false
	m.mu.Unlock()

	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path())
}

func (m *Memory) Close() error {
	if m.stop != nil {
		close(m.stop)
		<-m.done
		m.stop = nil
	}
	return nil
}

func (m *Memory) Get(_ context.Context, coll, id string) (Doc, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.docs[coll][id]
	if !ok {
		return Doc{}, ErrNotFound
	}
	return Doc{ID: d.ID, Data: append([]byte(nil), d.Data...), Ver: d.Ver}, nil
}

func (m *Memory) Put(_ context.Context, coll, id string, data []byte, expect int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.docs[coll]
	if c == nil {
		c = map[string]Doc{}
		m.docs[coll] = c
	}
	cur, exists := c[id]
	switch {
	case expect == CreateOnly:
		if exists {
			return 0, ErrExists
		}
	case expect > 0:
		if !exists {
			return 0, ErrNotFound
		}
		if cur.Ver != expect {
			return 0, ErrVersion
		}
	}
	nv := cur.Ver + 1
	c[id] = Doc{ID: id, Data: append([]byte(nil), data...), Ver: nv}
	m.dirty = true
	return nv, nil
}

func (m *Memory) Delete(_ context.Context, coll, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.docs[coll][id]; !ok {
		return ErrNotFound
	}
	delete(m.docs[coll], id)
	m.dirty = true
	return nil
}

func (m *Memory) List(_ context.Context, coll string, q ListQuery) ([]Doc, error) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.docs[coll]))
	for id := range m.docs[coll] {
		if strings.HasPrefix(id, q.Prefix) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if q.Desc {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	docs := make([]Doc, 0, len(ids))
	for _, id := range ids {
		d := m.docs[coll][id]
		docs = append(docs, Doc{ID: id, Data: append([]byte(nil), d.Data...), Ver: d.Ver})
	}
	m.mu.RUnlock()

	out := make([]Doc, 0)
	for _, d := range docs {
		if q.After != "" {
			if !q.Desc && d.ID <= q.After {
				continue
			}
			if q.Desc && d.ID >= q.After {
				continue
			}
		}
		if q.Filter != nil && !q.Filter(d) {
			continue
		}
		out = append(out, d)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) Append(_ context.Context, coll string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.recs[coll]
	for _, r := range recs {
		r.Data = append([]byte(nil), r.Data...)
		// Most appends are newest; insert in order otherwise.
		if n := len(cur); n == 0 || cur[n-1].ID < r.ID {
			cur = append(cur, r)
			continue
		}
		i := sort.Search(len(cur), func(i int) bool { return cur[i].ID >= r.ID })
		cur = append(cur, Record{})
		copy(cur[i+1:], cur[i:])
		cur[i] = r
	}
	if over := len(cur) - m.maxRecords; m.maxRecords > 0 && over > 0 {
		cur = append([]Record(nil), cur[over:]...)
	}
	m.recs[coll] = cur
	m.dirty = true
	return nil
}

func (m *Memory) Query(_ context.Context, coll string, q RecordQuery) ([]Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	cur := m.recs[coll]
	out := make([]Record, 0)
	for i := len(cur) - 1; i >= 0; i-- {
		r := cur[i]
		if !q.matches(r, now) {
			continue
		}
		r.Data = append([]byte(nil), r.Data...)
		out = append(out, r)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (m *Memory) Lock(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	m.locksMu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &memLock{ch: make(chan struct{}, 1)}
		m.locks[key] = l
	}
	m.locksMu.Unlock()
	select {
	case l.ch <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	release := func() { once.Do(func() { <-l.ch }) }
	// In-process holders cannot crash without the process; ttl only guards
	// against a forgotten unlock.
	timer := time.AfterFunc(ttl, release)
	return func() { timer.Stop(); release() }, nil
}
