package engineapi

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Dep is one reference from a spec to another resource.
type Dep struct {
	Kind    string // schemas, segments, lookups, gates, experiments, layers, decisions
	Name    string
	Version int64  // schemas only (pinned)
	Where   string // JSON pointer into the spec
	// Missing marks a schema slot that is required but empty (SchemaRequired).
	Invalid bool
}

// Keys whose values are data slots or inline schemas; never walked.
var skipKeys = map[string]bool{
	"tests": true, "then": true, "fallback": true, "parameters": true, "value": true, "valueTo": true,
	"examples": true, "expect": true, "defaults": true, "responseSchema": true, "jsonSchema": true,
	"prompt": true, "position": true, "description": true,
}

var schemaKeys = map[string]bool{"inputSchema": true, "outputSchema": true, "attributesSchema": true, "valueSchema": true, "schema": true}

// Deps extracts the references of a spec of the given kind. Schemas have no
// dependencies. The result is sorted by Where for stable output.
func Deps(kind string, spec json.RawMessage) []Dep {
	if kind == "schemas" || len(spec) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(spec, &v) != nil {
		return nil
	}
	var out []Dep
	walk(v, "", "", &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Where < out[j].Where })
	return out
}

func esc(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func walk(v any, path, parentKey string, out *[]Dep) {
	switch t := v.(type) {
	case []any:
		for i, x := range t {
			walk(x, path+"/"+strconv.Itoa(i), parentKey, out)
		}
	case map[string]any:
		nodeType, _ := t["type"].(string)
		for k, x := range t {
			p := path + "/" + esc(k)
			if skipKeys[k] {
				// RulesNode/TableNode/LookupNode "default" is data; SwitchNode
				// "default" is a node id. Neither is a reference.
				continue
			}
			if k == "default" {
				continue
			}
			if schemaKeys[k] {
				if ref, ok := x.(map[string]any); ok {
					name, _ := ref["name"].(string)
					ver, _ := ref["version"].(float64)
					if name != "" {
						*out = append(*out, Dep{Kind: "schemas", Name: name, Version: int64(ver), Where: p})
						continue
					}
				}
				continue
			}
			s, isStr := x.(string)
			switch {
			case k == "segment" && isStr:
				*out = append(*out, Dep{Kind: "segments", Name: s, Where: p})
				continue
			case k == "gate" && isStr:
				*out = append(*out, Dep{Kind: "gates", Name: s, Where: p})
				continue
			case k == "experiment" && isStr:
				*out = append(*out, Dep{Kind: "experiments", Name: s, Where: p})
				continue
			case k == "layer" && isStr:
				*out = append(*out, Dep{Kind: "layers", Name: s, Where: p})
				continue
			case k == "lookup" && isStr && nodeType == "lookup":
				*out = append(*out, Dep{Kind: "lookups", Name: s, Where: p})
				continue
			case k == "decision" && isStr && nodeType == "subdecision":
				*out = append(*out, Dep{Kind: "decisions", Name: s, Where: p})
				continue
			}
			walk(x, p, k, out)
		}
	}
}

// SchemaSlot is a rules/table node schema slot.
type SchemaSlot struct {
	Where   string // /nodes/1/inputSchema
	Node    string
	Missing bool
	Name    string
	Version int64
}

// RequiredSchemaSlots returns the inputSchema/outputSchema slots of every
// rules and table node of a decision spec, marking missing ones (the
// schema-registry-first rule).
func RequiredSchemaSlots(spec json.RawMessage) []SchemaSlot {
	var d struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(spec, &d) != nil {
		return nil
	}
	var out []SchemaSlot
	for i, n := range d.Nodes {
		var typ, id string
		_ = json.Unmarshal(n["type"], &typ)
		_ = json.Unmarshal(n["id"], &id)
		if typ != "rules" && typ != "table" {
			continue
		}
		for _, slot := range []string{"inputSchema", "outputSchema"} {
			s := SchemaSlot{Where: "/nodes/" + strconv.Itoa(i) + "/" + slot, Node: id}
			var ref struct {
				Name    string `json:"name"`
				Version int64  `json:"version"`
			}
			raw, ok := n[slot]
			if !ok || string(raw) == "null" || json.Unmarshal(raw, &ref) != nil || ref.Name == "" || ref.Version < 1 {
				s.Missing = true
			} else {
				s.Name, s.Version = ref.Name, ref.Version
			}
			out = append(out, s)
		}
	}
	return out
}

// DecisionSubtype is the shape of a decision's entry node (graph when it has
// more than one node that is not an output node).
func DecisionSubtype(spec json.RawMessage) string {
	var d struct {
		Entry string `json:"entry"`
		Nodes []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"nodes"`
	}
	if json.Unmarshal(spec, &d) != nil {
		return ""
	}
	core := 0
	entryType := ""
	for _, n := range d.Nodes {
		if n.Type != "output" {
			core++
		}
		if n.ID == d.Entry {
			entryType = n.Type
		}
	}
	if core <= 1 && (entryType == "rules" || entryType == "table" || entryType == "transform") {
		return entryType
	}
	return "graph"
}
