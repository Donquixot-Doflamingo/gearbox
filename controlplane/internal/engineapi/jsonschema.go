package engineapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ORG/gearbox/engine"
)

// schemaNode is the restricted JSON Schema 2020-12 subset of SchemaSpec.
type schemaNode map[string]any

// schemaOf decodes a SchemaSpec ({jsonSchema, examples}) or a bare schema.
func schemaOf(spec json.RawMessage) (root schemaNode, err error) {
	var m map[string]any
	if err := json.Unmarshal(spec, &m); err != nil {
		return nil, fmt.Errorf("schema is not a JSON object: %w", err)
	}
	if js, ok := m["jsonSchema"].(map[string]any); ok {
		return js, nil
	}
	return m, nil
}

type validator struct {
	root schemaNode
	errs []engine.FieldError
}

func (v *validator) add(path, msg string) {
	if path == "" {
		path = "/"
	}
	v.errs = append(v.errs, engine.FieldError{Path: path, Message: msg})
}

func types(s schemaNode) []string {
	switch t := s["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func jsonType(x any) string {
	switch t := x.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if t == math.Trunc(t) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeOK(want []string, got string) bool {
	for _, w := range want {
		if w == got || (w == "number" && got == "integer") {
			return true
		}
	}
	return false
}

func (v *validator) resolve(s schemaNode) schemaNode {
	for i := 0; i < 32; i++ {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		if !ok {
			return s
		}
		defs, _ := v.root["$defs"].(map[string]any)
		next, _ := defs[name].(map[string]any)
		if next == nil {
			return s
		}
		s = next
	}
	return s
}

var formats = map[string]func(string) bool{
	"date-time": func(s string) bool { _, err := time.Parse(time.RFC3339, s); return err == nil },
	"date":      func(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil },
	"email": func(s string) bool {
		_, err := mail.ParseAddress(s)
		return err == nil && !strings.ContainsAny(s, "<> ")
	},
	"uuid": func(s string) bool {
		return regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString(s)
	},
}

func num(s schemaNode, k string) (float64, bool) {
	f, ok := s[k].(float64)
	return f, ok
}

func (v *validator) check(s schemaNode, x any, path string, depth int) {
	if depth > 64 {
		v.add(path, "schema nests too deeply")
		return
	}
	s = v.resolve(s)
	got := jsonType(x)
	if ts := types(s); len(ts) > 0 && !typeOK(ts, got) {
		v.add(path, fmt.Sprintf("expected %s, got %s", strings.Join(ts, " or "), got))
		return
	}
	if c, ok := s["const"]; ok && !jsonEqual(c, x) {
		v.add(path, "must equal the constant value")
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, ev := range e {
			if jsonEqual(ev, x) {
				found = true
				break
			}
		}
		if !found {
			v.add(path, "value is not one of the allowed values")
		}
	}
	switch t := x.(type) {
	case float64:
		if m, ok := num(s, "minimum"); ok && t < m {
			v.add(path, fmt.Sprintf("must be >= %v", m))
		}
		if m, ok := num(s, "maximum"); ok && t > m {
			v.add(path, fmt.Sprintf("must be <= %v", m))
		}
		if m, ok := num(s, "exclusiveMinimum"); ok && t <= m {
			v.add(path, fmt.Sprintf("must be > %v", m))
		}
		if m, ok := num(s, "exclusiveMaximum"); ok && t >= m {
			v.add(path, fmt.Sprintf("must be < %v", m))
		}
	case string:
		n := float64(utf8.RuneCountInString(t))
		if m, ok := num(s, "minLength"); ok && n < m {
			v.add(path, fmt.Sprintf("must be at least %v characters", m))
		}
		if m, ok := num(s, "maxLength"); ok && n > m {
			v.add(path, fmt.Sprintf("must be at most %v characters", m))
		}
		if p, ok := s["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(t) {
				v.add(path, "does not match pattern "+p)
			}
		}
		if f, ok := s["format"].(string); ok {
			if fn := formats[f]; fn != nil && !fn(t) {
				v.add(path, "is not a valid "+f)
			}
		}
	case []any:
		n := float64(len(t))
		if m, ok := num(s, "minItems"); ok && n < m {
			v.add(path, fmt.Sprintf("must have at least %v items", m))
		}
		if m, ok := num(s, "maxItems"); ok && n > m {
			v.add(path, fmt.Sprintf("must have at most %v items", m))
		}
		prefix, _ := s["prefixItems"].([]any)
		items, _ := s["items"].(map[string]any)
		for i, it := range t {
			p := fmt.Sprintf("%s/%d", path, i)
			if i < len(prefix) {
				if ps, ok := prefix[i].(map[string]any); ok {
					v.check(ps, it, p, depth+1)
				}
				continue
			}
			if items != nil {
				v.check(items, it, p, depth+1)
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					if _, present := t[name]; !present {
						v.add(path+"/"+esc(name), "required property missing")
					}
				}
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := path + "/" + esc(k)
			if ps, ok := props[k].(map[string]any); ok {
				v.check(ps, t[k], p, depth+1)
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					v.add(p, "property not allowed")
				}
			case map[string]any:
				v.check(ap, t[k], p, depth+1)
			}
		}
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// validateDoc validates doc against a SchemaSpec (or bare schema).
func validateDoc(schemaSpec, doc json.RawMessage) (bool, []engine.FieldError, error) {
	root, err := schemaOf(schemaSpec)
	if err != nil {
		return false, nil, err
	}
	var x any
	if err := json.Unmarshal(doc, &x); err != nil {
		return false, []engine.FieldError{{Path: "/", Message: "document is not valid JSON"}}, nil
	}
	v := &validator{root: root}
	v.check(root, x, "", 0)
	return len(v.errs) == 0, v.errs, nil
}

var allowedSchemaKeys = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "prefixItems": true,
	"enum": true, "const": true, "minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"minLength": true, "maxLength": true, "pattern": true, "format": true, "minItems": true, "maxItems": true,
	"description": true, "default": true, "$defs": true, "$ref": true, "x-gearbox-pii": true, "title": true, "$schema": true,
	"examples": true,
}

var allowedTypes = map[string]bool{"object": true, "array": true, "string": true, "number": true, "integer": true, "boolean": true, "null": true}

// lintSchema checks that a JSON Schema stays within the supported subset.
func lintSchema(s map[string]any, path string, depth int, out *[]engine.Problem) {
	if depth > 32 {
		*out = append(*out, engine.Problem{Code: "SCHEMA_TOO_DEEP", Severity: "error", Path: path, Message: "objects nest deeper than 32 levels"})
		return
	}
	for k, x := range s {
		p := path + "/" + esc(k)
		if !allowedSchemaKeys[k] && !strings.HasPrefix(k, "x-") {
			*out = append(*out, engine.Problem{Code: "UNSUPPORTED_KEYWORD", Severity: "error", Path: p, Message: fmt.Sprintf("keyword %q is not supported", k)})
			continue
		}
		switch k {
		case "type":
			for _, t := range types(s) {
				if !allowedTypes[t] {
					*out = append(*out, engine.Problem{Code: "PARSE_ERROR", Severity: "error", Path: p, Message: fmt.Sprintf("unknown type %q", t)})
				}
			}
		case "pattern":
			if ps, ok := x.(string); ok {
				if _, err := regexp.Compile(ps); err != nil {
					*out = append(*out, engine.Problem{Code: "PARSE_ERROR", Severity: "error", Path: p, Message: "pattern is not valid RE2: " + err.Error()})
				}
			}
		case "$ref":
			if rs, ok := x.(string); ok && !strings.HasPrefix(rs, "#/$defs/") {
				*out = append(*out, engine.Problem{Code: "UNSUPPORTED_KEYWORD", Severity: "error", Path: p, Message: "only local $ref (#/$defs/…) is supported"})
			}
		case "properties", "$defs":
			if m, ok := x.(map[string]any); ok {
				for name, sub := range m {
					if sm, ok := sub.(map[string]any); ok {
						lintSchema(sm, p+"/"+esc(name), depth+1, out)
					}
				}
			}
		case "items", "additionalProperties":
			if sm, ok := x.(map[string]any); ok {
				lintSchema(sm, p, depth+1, out)
			}
		case "prefixItems":
			if arr, ok := x.([]any); ok {
				for i, sub := range arr {
					if sm, ok := sub.(map[string]any); ok {
						lintSchema(sm, fmt.Sprintf("%s/%d", p, i), depth+1, out)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------- compatibility

func checkCompat(prevSpec, nextSpec json.RawMessage) (bool, []engine.SchemaChange, error) {
	prev, err := schemaOf(prevSpec)
	if err != nil {
		return false, nil, err
	}
	next, err := schemaOf(nextSpec)
	if err != nil {
		return false, nil, err
	}
	var ch []engine.SchemaChange
	compat(prev, next, "", &ch, 0)
	sort.SliceStable(ch, func(i, j int) bool { return ch[i].Path < ch[j].Path })
	ok := true
	for _, c := range ch {
		if c.Breaking {
			ok = false
		}
	}
	if ch == nil {
		ch = []engine.SchemaChange{}
	}
	return ok, ch, nil
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func reqSet(s schemaNode) map[string]bool {
	m := map[string]bool{}
	if r, ok := s["required"].([]any); ok {
		for _, x := range r {
			if n, ok := x.(string); ok {
				m[n] = true
			}
		}
	}
	return m
}

func compat(prev, next schemaNode, path string, out *[]engine.SchemaChange, depth int) {
	if depth > 32 {
		return
	}
	p := path
	if p == "" {
		p = "/"
	}
	pt, nt := types(prev), types(next)
	if len(pt) > 0 {
		ns := set(nt)
		if len(nt) == 0 {
			*out = append(*out, engine.SchemaChange{Path: p, Kind: "type_widened", Breaking: false})
		} else {
			for _, t := range pt {
				if ns[t] || (t == "integer" && ns["number"]) {
					continue
				}
				if t == "null" {
					*out = append(*out, engine.SchemaChange{Path: p, Kind: "null_removed", Breaking: true})
				} else {
					*out = append(*out, engine.SchemaChange{Path: p, Kind: "type_narrowed", Breaking: true})
				}
			}
			ps := set(pt)
			for _, t := range nt {
				if ps[t] {
					continue
				}
				kind := "type_widened"
				if t == "null" {
					kind = "null_added"
				}
				*out = append(*out, engine.SchemaChange{Path: p, Kind: kind, Breaking: false})
			}
		}
	} else if len(nt) > 0 {
		*out = append(*out, engine.SchemaChange{Path: p, Kind: "type_narrowed", Breaking: true})
	}
	pe, pok := prev["enum"].([]any)
	ne, nok := next["enum"].([]any)
	switch {
	case nok && !pok:
		*out = append(*out, engine.SchemaChange{Path: p, Kind: "enum_narrowed", Breaking: true})
	case nok && pok:
		for _, v := range pe {
			found := false
			for _, w := range ne {
				if jsonEqual(v, w) {
					found = true
				}
			}
			if !found {
				*out = append(*out, engine.SchemaChange{Path: p, Kind: "enum_narrowed", Breaking: true})
				break
			}
		}
		if len(ne) > len(pe) {
			*out = append(*out, engine.SchemaChange{Path: p, Kind: "enum_widened", Breaking: false})
		}
	case pok && !nok:
		*out = append(*out, engine.SchemaChange{Path: p, Kind: "enum_widened", Breaking: false})
	}
	for _, k := range []string{"minimum", "exclusiveMinimum", "minLength", "minItems"} {
		a, aok := num(prev, k)
		b, bok := num(next, k)
		if bok && (!aok || b > a) {
			*out = append(*out, engine.SchemaChange{Path: p, Kind: k + "_raised", Breaking: true})
		}
	}
	for _, k := range []string{"maximum", "exclusiveMaximum", "maxLength", "maxItems"} {
		a, aok := num(prev, k)
		b, bok := num(next, k)
		if bok && (!aok || b < a) {
			*out = append(*out, engine.SchemaChange{Path: p, Kind: k + "_lowered", Breaking: true})
		}
	}
	pp, _ := prev["properties"].(map[string]any)
	np, _ := next["properties"].(map[string]any)
	preq, nreq := reqSet(prev), reqSet(next)
	names := map[string]bool{}
	for k := range pp {
		names[k] = true
	}
	for k := range np {
		names[k] = true
	}
	for k := range names {
		cp := path + "/" + esc(k)
		a, aok := pp[k].(map[string]any)
		b, bok := np[k].(map[string]any)
		switch {
		case aok && !bok:
			*out = append(*out, engine.SchemaChange{Path: cp, Kind: "property_removed", Breaking: true})
		case !aok && bok:
			*out = append(*out, engine.SchemaChange{Path: cp, Kind: "property_added", Breaking: nreq[k]})
		default:
			compat(a, b, cp, out, depth+1)
		}
		if nreq[k] && !preq[k] && aok {
			*out = append(*out, engine.SchemaChange{Path: cp, Kind: "became_required", Breaking: true})
		}
	}
	if ap, ok := next["additionalProperties"].(bool); ok && !ap {
		if prevAP, ok := prev["additionalProperties"].(bool); !ok || prevAP {
			*out = append(*out, engine.SchemaChange{Path: p, Kind: "additional_properties_closed", Breaking: true})
		}
	}
	if a, ok := prev["items"].(map[string]any); ok {
		if b, ok := next["items"].(map[string]any); ok {
			compat(a, b, path+"/items", out, depth+1)
		}
	}
}
