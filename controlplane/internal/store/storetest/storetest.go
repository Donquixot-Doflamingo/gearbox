// Package storetest is a conformance suite every store.Backend must pass.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

// Run executes the suite. newBackend must return an empty backend.
func Run(t *testing.T, newBackend func(t *testing.T) store.Backend) {
	t.Run("CRUD", func(t *testing.T) { testCRUD(t, newBackend(t)) })
	t.Run("List", func(t *testing.T) { testList(t, newBackend(t)) })
	t.Run("Records", func(t *testing.T) { testRecords(t, newBackend(t)) })
	t.Run("Lock", func(t *testing.T) { testLock(t, newBackend(t)) })
	t.Run("ConcurrentGenerations", func(t *testing.T) { testGenerations(t, newBackend(t)) })
	t.Run("Counter", func(t *testing.T) { testCounter(t, newBackend(t)) })
}

func testCRUD(t *testing.T, b store.Backend) {
	ctx := context.Background()
	if _, err := b.Get(ctx, "c", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	v, err := b.Put(ctx, "c", "x", []byte(`{"a":1}`), store.CreateOnly)
	if err != nil || v != 1 {
		t.Fatalf("create: %v %d", err, v)
	}
	if _, err := b.Put(ctx, "c", "x", []byte(`{}`), store.CreateOnly); !errors.Is(err, store.ErrExists) {
		t.Fatalf("create twice: %v", err)
	}
	if _, err := b.Put(ctx, "c", "x", []byte(`{"a":2}`), 7); !errors.Is(err, store.ErrVersion) {
		t.Fatalf("cas stale: %v", err)
	}
	if _, err := b.Put(ctx, "c", "nope", []byte(`{}`), 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cas missing: %v", err)
	}
	v, err = b.Put(ctx, "c", "x", []byte(`{"a":2}`), 1)
	if err != nil || v != 2 {
		t.Fatalf("cas: %v %d", err, v)
	}
	v, err = b.Put(ctx, "c", "x", []byte(`{"a":3}`), store.Upsert)
	if err != nil || v != 3 {
		t.Fatalf("upsert: %v %d", err, v)
	}
	d, err := b.Get(ctx, "c", "x")
	if err != nil || string(d.Data) != `{"a":3}` || d.Ver != 3 {
		t.Fatalf("get: %v %s %d", err, d.Data, d.Ver)
	}
	if err := b.Delete(ctx, "c", "x"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "c", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func testList(t *testing.T, b store.Backend) {
	ctx := context.Background()
	for _, id := range []string{"a/1", "a/2", "a/3", "b/1", "a/4"} {
		if _, err := b.Put(ctx, "l", id, []byte(fmt.Sprintf(`{"id":%q}`, id)), store.CreateOnly); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := b.List(ctx, "l", store.ListQuery{Prefix: "a/", Limit: 2})
	if err != nil || len(docs) != 2 || docs[0].ID != "a/1" || docs[1].ID != "a/2" {
		t.Fatalf("page1: %v %+v", err, docs)
	}
	docs, _ = b.List(ctx, "l", store.ListQuery{Prefix: "a/", After: "a/2"})
	if len(docs) != 2 || docs[0].ID != "a/3" {
		t.Fatalf("page2: %+v", docs)
	}
	docs, _ = b.List(ctx, "l", store.ListQuery{Prefix: "a/", Desc: true, After: "a/3"})
	if len(docs) != 2 || docs[0].ID != "a/2" || docs[1].ID != "a/1" {
		t.Fatalf("desc: %+v", docs)
	}
	docs, _ = b.List(ctx, "l", store.ListQuery{Filter: func(d store.Doc) bool { return d.ID[2] == '1' }})
	if len(docs) != 2 {
		t.Fatalf("filter: %+v", docs)
	}
}

func testRecords(t *testing.T, b store.Backend) {
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	var recs []store.Record
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		app := "x"
		if i%2 == 1 {
			app = "y"
		}
		recs = append(recs, store.Record{ID: store.NewRecordID(ts), TS: ts, Fields: map[string]string{"app": app}, Data: []byte(fmt.Sprintf(`{"i":%d}`, i))})
	}
	expired := store.Record{ID: store.NewRecordID(base), TS: base, Fields: map[string]string{"app": "x"}, Data: []byte(`{"i":-1}`), ExpireAt: time.Now().Add(-time.Second)}
	if err := b.Append(ctx, "r", append(recs, expired)); err != nil {
		t.Fatal(err)
	}
	got, err := b.Query(ctx, "r", store.RecordQuery{Eq: map[string]string{"app": "x"}, Limit: 3})
	if err != nil || len(got) != 3 || string(got[0].Data) != `{"i":8}` {
		t.Fatalf("query: %v %d %s", err, len(got), first(got))
	}
	got, _ = b.Query(ctx, "r", store.RecordQuery{Eq: map[string]string{"app": "x"}, Before: got[2].ID})
	if len(got) != 2 || string(got[1].Data) != `{"i":0}` {
		t.Fatalf("cursor/expiry: %d %s", len(got), first(got))
	}
	got, _ = b.Query(ctx, "r", store.RecordQuery{From: base.Add(2 * time.Minute), To: base.Add(4 * time.Minute)})
	if len(got) != 2 {
		t.Fatalf("time range: %d", len(got))
	}
}

func first(r []store.Record) string {
	if len(r) == 0 {
		return ""
	}
	return string(r[0].Data)
}

func testLock(t *testing.T, b store.Backend) {
	ctx := context.Background()
	unlock, err := b.Lock(ctx, "k", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := b.Lock(cctx, "k", time.Second); err == nil {
		t.Fatal("second lock should wait")
	}
	unlock()
	u2, err := b.Lock(ctx, "k", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

// testGenerations publishes concurrently through Store.CommitRelease and
// checks generations are strictly increasing with no gaps or duplicates.
func testGenerations(t *testing.T, b store.Backend) {
	ctx := context.Background()
	s := store.New(b)
	if err := s.CreateApp(ctx, model.App{Name: "app", Environments: []string{"dev"}}, []model.EnvState{{App: "app", Name: "dev", Live: map[string]model.LiveEntry{}}}); err != nil {
		t.Fatal(err)
	}
	const workers, each = 8, 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int64]bool{}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				for {
					st, ver, err := s.GetEnv(ctx, "app", "dev")
					if err != nil {
						t.Error(err)
						return
					}
					rel := model.ReleaseRecord{App: "app", State: st}
					rel.Environment = "dev"
					rel.Generation = st.Generation + 1
					rel.PublishedAt = time.Now()
					err = s.CommitRelease(ctx, rel, ver)
					if errors.Is(err, store.ErrVersion) {
						continue
					}
					if err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					if seen[rel.Generation] {
						t.Errorf("generation %d committed twice", rel.Generation)
					}
					seen[rel.Generation] = true
					mu.Unlock()
					break
				}
			}
		}()
	}
	wg.Wait()
	st, _, _ := s.GetEnv(ctx, "app", "dev")
	if st.Generation != workers*each || len(seen) != workers*each {
		t.Fatalf("generation %d, distinct %d", st.Generation, len(seen))
	}
}

func testCounter(t *testing.T, b store.Backend) {
	s := store.New(b)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int64]bool{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.Next(context.Background(), "c")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			seen[n] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("counter values not unique: %d", len(seen))
	}
}
