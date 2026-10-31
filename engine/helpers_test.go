// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadSet(t testing.TB, path string) Set {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s Set
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func leadSet(t testing.TB) Set { return loadSet(t, "testdata/lead_routing/set.json") }

func mustCompile(t testing.TB, s Set) *Snapshot {
	t.Helper()
	snap, err := Compile(s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return snap
}

// res builds a resource from a JSON spec string.
func res(kind Kind, name string, version int64, spec string) Resource {
	return Resource{Kind: kind, Name: name, Version: version, Spec: json.RawMessage(spec)}
}

func schemaRes(name string, version int64, jsonSchema string) Resource {
	return res(KindSchema, name, version, `{"jsonSchema":`+jsonSchema+`}`)
}

func decide(t testing.TB, s *Snapshot, decision, input, unit string) Result {
	t.Helper()
	r, err := s.Decide(context.Background(), Request{Decision: decision, Input: json.RawMessage(input), UnitID: unit})
	if err != nil {
		t.Fatalf("decide %s: %v", decision, err)
	}
	return r
}

func pathString(p []Step) string {
	var out []string
	for _, s := range p {
		out = append(out, s.NodeID+":"+s.Outcome)
	}
	return strings.Join(out, ",")
}

// mapResolver serves resources for ValidateResource tests.
type mapResolver map[string]Resource

func (m mapResolver) Get(kind Kind, name string, version int64) (Resource, bool) {
	if version == 0 {
		var best Resource
		found := false
		for _, r := range m {
			if r.Kind == kind && r.Name == name && r.Version >= best.Version {
				best, found = r, true
			}
		}
		return best, found
	}
	r, ok := m[key(kind, name, version)]
	return r, ok
}

func resolverOf(rs ...Resource) mapResolver {
	m := mapResolver{}
	for _, r := range rs {
		m[key(r.Kind, r.Name, r.Version)] = r
	}
	return m
}

func problemCodes(v Validation) string {
	var out []string
	for _, p := range v.Problems {
		if p.Severity == "error" {
			out = append(out, p.Code)
		}
	}
	return strings.Join(out, ",")
}
