package mongostore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/controlplane/internal/store/storetest"
)

func TestDBName(t *testing.T) {
	for uri, want := range map[string]string{
		"mongodb://localhost:27017":               "def",
		"mongodb://localhost:27017/gbx":           "gbx",
		"mongodb://u:p@h1,h2/prod?replicaSet=rs0": "prod",
		"mongodb+srv://cluster.example.com/?w=1":  "def",
	} {
		if got := DBName(uri, "def"); got != want {
			t.Errorf("DBName(%q) = %q, want %q", uri, got, want)
		}
	}
}

// TestConformance runs the backend suite against a real MongoDB when
// GEARBOX_TEST_MONGO_URI is set (each subtest gets its own database).
func TestConformance(t *testing.T) {
	uri := os.Getenv("GEARBOX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("GEARBOX_TEST_MONGO_URI not set")
	}
	n := 0
	storetest.Run(t, func(t *testing.T) store.Backend {
		n++
		b, err := Open(context.Background(), uri, fmt.Sprintf("gearbox_test_%d_%d", time.Now().UnixNano(), n))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Drop(context.Background()); _ = b.Close() })
		return b
	})
}
