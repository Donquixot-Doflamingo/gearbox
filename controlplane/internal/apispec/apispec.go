// Package apispec embeds the control-plane OpenAPI contract
// (contracts/openapi/gearbox-admin.v1.yaml) so the server can validate
// requests against it and tests can validate responses.
//
// The embedded file is a byte-for-byte copy; TestSpecInSync fails when it
// drifts from the contract. Refresh it with `go generate ./internal/apispec`.
package apispec

//go:generate cp ../../../contracts/openapi/gearbox-admin.v1.yaml gearbox-admin.v1.yaml

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed gearbox-admin.v1.yaml
var raw []byte

// YAML returns the embedded contract.
func YAML() []byte { return raw }

var (
	once   sync.Once
	doc    *openapi3.T
	docErr error
)

// Load parses the embedded contract once. The document is shared and must be
// treated as read-only.
func Load() (*openapi3.T, error) {
	once.Do(func() {
		l := openapi3.NewLoader()
		l.IsExternalRefsAllowed = false
		doc, docErr = l.LoadFromData(raw)
		if docErr == nil {
			// Resolve refs eagerly; whole-document validation is skipped because
			// kin-openapi (3.0 semantics) rejects 3.1 sibling keywords such as
			// `examples` next to `allOf`.
			docErr = l.ResolveRefsIn(doc, nil)
		}
	})
	return doc, docErr
}

// MustLoad is Load for init paths.
func MustLoad() *openapi3.T {
	d, err := Load()
	if err != nil {
		panic(fmt.Sprintf("apispec: %v", err))
	}
	return d
}

// Operation is one operation of the contract.
type Operation struct {
	ID       string
	Method   string
	Path     string // as in the contract, e.g. /apps/{app}
	PathItem *openapi3.PathItem
	Op       *openapi3.Operation
}

// Operations lists every operation keyed by operationId.
func Operations() (map[string]Operation, error) {
	d, err := Load()
	if err != nil {
		return nil, err
	}
	out := map[string]Operation{}
	for path, item := range d.Paths.Map() {
		for method, op := range item.Operations() {
			out[op.OperationID] = Operation{ID: op.OperationID, Method: strings.ToUpper(method), Path: path, PathItem: item, Op: op}
		}
	}
	return out, nil
}

// Schema returns a component schema by name.
func Schema(name string) *openapi3.Schema {
	d := MustLoad()
	ref := d.Components.Schemas[name]
	if ref == nil {
		return nil
	}
	return ref.Value
}

// VisitJSON validates a decoded JSON value against a component schema.
func VisitJSON(name string, v any) error {
	s := Schema(name)
	if s == nil {
		return fmt.Errorf("apispec: unknown schema %q", name)
	}
	return s.VisitJSON(v, openapi3.MultiErrors())
}

// Validate is a convenience for tests; it reports whether the document loads.
func Validate(ctx context.Context) error {
	_, err := Load()
	return err
}
