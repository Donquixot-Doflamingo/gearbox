// Package defs reads and writes definition files (docs/04-reference.md §10):
// YAML or JSON, one resource per file, the engine Resource shape plus
// description:
//
//	kind: schemas
//	name: lead_input
//	version: 1
//	description: …
//	spec: { … }
//	segmentMembers: [ … ]      # static segments
//	lookupRows: { key: value } # lookups
package defs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is one definition file.
type File struct {
	Kind           string                     `json:"kind" yaml:"kind"`
	Name           string                     `json:"name" yaml:"name"`
	Version        int64                      `json:"version,omitempty" yaml:"version,omitempty"`
	Description    string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Spec           json.RawMessage            `json:"spec" yaml:"-"`
	SegmentMembers []string                   `json:"segmentMembers,omitempty" yaml:"segmentMembers,omitempty"`
	LookupRows     map[string]json.RawMessage `json:"lookupRows,omitempty" yaml:"-"`
	Path           string                     `json:"-" yaml:"-"`
}

// KindOrder is the dependency order for pushing.
var KindOrder = []string{"schemas", "segments", "lookups", "gates", "layers", "experiments", "decisions"}

func kindRank(k string) int {
	for i, x := range KindOrder {
		if x == k {
			return i
		}
	}
	return len(KindOrder)
}

// toJSONable converts yaml.v3 output (map[string]any, []any) for json.
func toJSONable(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = toJSONable(x)
		}
		return t
	case map[any]any:
		m := map[string]any{}
		for k, x := range t {
			m[fmt.Sprint(k)] = toJSONable(x)
		}
		return m
	case []any:
		for i, x := range t {
			t[i] = toJSONable(x)
		}
		return t
	}
	return v
}

// YAMLToJSON converts a YAML (or JSON) document to JSON.
func YAMLToJSON(data []byte) (json.RawMessage, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(toJSONable(v))
}

// Parse decodes YAML or JSON (JSON is YAML) into a File. kindHint fills a
// missing kind (from the directory name).
func Parse(data []byte, kindHint string) (File, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return File{}, err
	}
	b, err := json.Marshal(toJSONable(v))
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, err
	}
	if f.Kind == "" {
		f.Kind = kindHint
	}
	if f.Name == "" {
		return f, fmt.Errorf("missing name")
	}
	if len(f.Spec) == 0 || string(f.Spec) == "null" {
		return f, fmt.Errorf("missing spec")
	}
	return f, nil
}

// ReadFile reads one file.
func ReadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	f, err := Parse(b, filepath.Base(filepath.Dir(path)))
	if err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	f.Path = path
	return f, nil
}

// Load reads every *.yaml/*.yml/*.json under dir, sorted in dependency
// order (kind, then name).
func Load(dir string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".json":
		default:
			return nil
		}
		f, err := ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(files, func(i, j int) bool {
		if kindRank(files[i].Kind) != kindRank(files[j].Kind) {
			return kindRank(files[i].Kind) < kindRank(files[j].Kind)
		}
		return files[i].Name < files[j].Name
	})
	return files, nil
}

// Marshal renders a File as YAML (or JSON when asJSON).
func Marshal(f File, asJSON bool) ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if asJSON {
		var buf map[string]any
		_ = json.Unmarshal(b, &buf)
		return json.MarshalIndent(buf, "", "  ")
	}
	// Round-trip through any so YAML gets plain maps.
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	m := v.(map[string]any)
	// Keep a readable key order.
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range []string{"kind", "name", "version", "description", "spec", "segmentMembers", "lookupRows"} {
		val, ok := m[k]
		if !ok {
			continue
		}
		var vn yaml.Node
		if err := vn.Encode(val); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &vn)
	}
	return yaml.Marshal(node)
}
