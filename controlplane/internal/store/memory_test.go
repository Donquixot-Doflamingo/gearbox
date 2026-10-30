package store_test

import (
	"context"
	"testing"

	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/controlplane/internal/store/storetest"
)

func TestMemoryConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Backend {
		m, err := store.NewMemory("")
		if err != nil {
			t.Fatal(err)
		}
		return m
	})
}

func TestMemorySnapshot(t *testing.T) {
	dir := t.TempDir()
	m, err := store.NewMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := m.Put(ctx, "c", "a", []byte(`{"x":1}`), store.CreateOnly); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(ctx, "r", []store.Record{{ID: "1", Data: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := store.NewMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	d, err := m2.Get(ctx, "c", "a")
	if err != nil || string(d.Data) != `{"x":1}` || d.Ver != 1 {
		t.Fatalf("reload: %v %s", err, d.Data)
	}
	recs, _ := m2.Query(ctx, "r", store.RecordQuery{})
	if len(recs) != 1 {
		t.Fatalf("records not reloaded")
	}
}

func TestMemoryRingBound(t *testing.T) {
	m, _ := store.NewMemory("")
	m.SetMaxRecords(3)
	ctx := context.Background()
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		_ = m.Append(ctx, "r", []store.Record{{ID: id, Data: []byte(`{}`)}})
	}
	recs, _ := m.Query(ctx, "r", store.RecordQuery{})
	if len(recs) != 3 || recs[0].ID != "5" || recs[2].ID != "3" {
		t.Fatalf("ring: %+v", recs)
	}
}
