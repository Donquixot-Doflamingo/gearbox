package apispec

import (
	"bytes"
	"os"
	"testing"
)

func TestSpecInSync(t *testing.T) {
	want, err := os.ReadFile("../../../contracts/openapi/gearbox-admin.v1.yaml")
	if err != nil {
		t.Skipf("contract not reachable from here: %v", err)
	}
	if !bytes.Equal(want, YAML()) {
		t.Fatal("embedded OpenAPI copy differs from contracts/openapi/gearbox-admin.v1.yaml; run `go generate ./internal/apispec`")
	}
}

func TestLoad(t *testing.T) {
	ops, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 60 {
		t.Fatalf("only %d operations", len(ops))
	}
	if Schema("DecisionSpec") == nil {
		t.Fatal("DecisionSpec missing")
	}
}
