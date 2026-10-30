// SPDX-License-Identifier: MIT

// Package defs loads Gearbox definitions from a directory of YAML or JSON
// files (offline mode, `--definitions-dir`).
//
// Every *.yaml, *.yml and *.json file (recursively, hidden files skipped)
// holds one or more documents (YAML "---" separated, or a JSON list). A
// document is one of:
//
//   - a resource in the engine's form: {kind: decisions, name, version, spec,
//     segmentMembers, lookupRows}
//   - a resource in manifest form: {apiVersion: gearbox/v1, kind: Decision,
//     metadata: {name, version}, spec, members, rows}
//   - set settings: {app, environment, rollouts, killedGates} (at most one)
//
// version defaults to 1. Lookup rows may be any YAML values.
package defs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ORG/gearbox/engine"
)

var singularKinds = map[string]engine.Kind{
	"schema": engine.KindSchema, "decision": engine.KindDecision, "segment": engine.KindSegment,
	"lookup": engine.KindLookup, "gate": engine.KindGate, "experiment": engine.KindExperiment, "layer": engine.KindLayer,
}

func kindOf(s string) (engine.Kind, bool) {
	l := strings.ToLower(strings.TrimSpace(s))
	if k, ok := singularKinds[l]; ok {
		return k, true
	}
	for _, k := range singularKinds {
		if string(k) == l {
			return k, true
		}
	}
	return "", false
}

// Load reads a definitions directory into a Set (generation 0; the caller
// sets it).
func Load(dir string) (engine.Set, error) {
	files, err := List(dir)
	if err != nil {
		return engine.Set{}, err
	}
	set := engine.Set{App: "local", Environment: "local"}
	settings := false
	seen := map[string]string{}
	for _, f := range files {
		docs, err := readDocs(f)
		if err != nil {
			return engine.Set{}, fmt.Errorf("%s: %w", f, err)
		}
		for i, d := range docs {
			where := fmt.Sprintf("%s (document %d)", f, i+1)
			if _, hasApp := d["app"]; hasApp && d["spec"] == nil {
				if settings {
					return engine.Set{}, fmt.Errorf("%s: set settings appear twice", where)
				}
				settings = true
				if err := applySettings(&set, d); err != nil {
					return engine.Set{}, fmt.Errorf("%s: %w", where, err)
				}
				continue
			}
			r, err := toResource(d)
			if err != nil {
				return engine.Set{}, fmt.Errorf("%s: %w", where, err)
			}
			k := fmt.Sprintf("%s/%s@%d", r.Kind, r.Name, r.Version)
			if prev, dup := seen[k]; dup {
				return engine.Set{}, fmt.Errorf("%s: %s is also defined in %s", where, k, prev)
			}
			seen[k] = where
			set.Resources = append(set.Resources, r)
		}
	}
	if len(set.Resources) == 0 {
		return engine.Set{}, fmt.Errorf("no definitions found in %s", dir)
	}
	return set, nil
}

// List returns the definition files in dir, sorted.
func List(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if path != dir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".yaml", ".yml", ".json":
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// Fingerprint summarises names, sizes and mtimes (for --watch).
func Fingerprint(dir string) (string, error) {
	files, err := List(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s|%d|%d\n", f, st.Size(), st.ModTime().UnixNano())
	}
	return b.String(), nil
}

func readDocs(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		switch t := v.(type) {
		case nil:
		case map[string]any:
			out = append(out, t)
		case []any:
			for _, e := range t {
				m, ok := e.(map[string]any)
				if !ok {
					return nil, errors.New("list entries must be objects")
				}
				out = append(out, m)
			}
		default:
			return nil, errors.New("a document must be an object or a list of objects")
		}
	}
	return out, nil
}

func toJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(normalize(v))
	return b, err
}

// normalize converts YAML maps with non-string keys.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalize(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = normalize(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = normalize(e)
		}
		return t
	}
	return v
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	case nil:
		return 0, false
	}
	return 0, false
}

func toResource(d map[string]any) (engine.Resource, error) {
	var r engine.Resource
	kindStr, _ := d["kind"].(string)
	kind, ok := kindOf(kindStr)
	if !ok {
		return r, fmt.Errorf("unknown kind %q", kindStr)
	}
	r.Kind = kind
	name, _ := d["name"].(string)
	version, hasVersion := asInt(d["version"])
	if md, ok := d["metadata"].(map[string]any); ok {
		if n, ok := md["name"].(string); ok {
			name = n
		}
		if v, ok := asInt(md["version"]); ok {
			version, hasVersion = v, true
		}
	}
	if name == "" {
		return r, errors.New("name is required")
	}
	if !hasVersion {
		version = 1
	}
	r.Name, r.Version = name, version
	spec, ok := d["spec"]
	if !ok {
		return r, errors.New("spec is required")
	}
	raw, err := toJSON(spec)
	if err != nil {
		return r, err
	}
	r.Spec = raw
	members := d["segmentMembers"]
	if members == nil {
		members = d["members"]
	}
	if l, ok := members.([]any); ok {
		for _, m := range l {
			r.SegmentMembers = append(r.SegmentMembers, fmt.Sprint(m))
		}
	}
	rows := d["lookupRows"]
	if rows == nil {
		rows = d["rows"]
	}
	if m, ok := normalize(rows).(map[string]any); ok {
		r.LookupRows = make(map[string]json.RawMessage, len(m))
		for k, v := range m {
			b, err := toJSON(v)
			if err != nil {
				return r, err
			}
			r.LookupRows[k] = b
		}
	}
	return r, nil
}

func applySettings(set *engine.Set, d map[string]any) error {
	raw, err := toJSON(d)
	if err != nil {
		return err
	}
	var s struct {
		App         string           `json:"app"`
		Environment string           `json:"environment"`
		Rollouts    []engine.Rollout `json:"rollouts"`
		KilledGates []string         `json:"killedGates"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if s.App != "" {
		set.App = s.App
	}
	if s.Environment != "" {
		set.Environment = s.Environment
	}
	set.Rollouts, set.KilledGates = s.Rollouts, s.KilledGates
	return nil
}
