package server

import (
	"context"
	"testing"

	"github.com/ORG/gearbox/controlplane/internal/seed"
)

func seedDemo(t *testing.T, h *harness) seed.Result {
	t.Helper()
	res, err := seed.Demo(context.Background(), h.client(h.user), seed.Options{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return res
}

func TestSeedDemo(t *testing.T) {
	h := newHarness(t)
	res := seedDemo(t, h)
	if res.Generation != 1 || res.Credential == "" {
		t.Fatalf("seed result %+v", res)
	}
	// Seeding again is idempotent: nothing new to publish.
	res2, err := seed.Demo(context.Background(), h.client(h.user), seed.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.AlreadySeeded || res2.Generation != 1 {
		t.Fatalf("second seed %+v", res2)
	}
}
