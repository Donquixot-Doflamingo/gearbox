// SPDX-License-Identifier: MIT

// Package jsonschema compiles and evaluates the restricted JSON Schema
// 2020-12 subset Gearbox supports (see SchemaSpec in the OpenAPI contract):
// type (one or a list, "null" for nullable), properties, required,
// additionalProperties, items, prefixItems, enum, const, minimum, maximum,
// exclusiveMinimum, exclusiveMaximum, minLength, maxLength, pattern (RE2),
// format (date-time, date, email, uuid), minItems, maxItems, description,
// default, $defs with local $ref, and x-gearbox-pii.
//
// A compiled *Schema is immutable and safe for concurrent use. Validation of a
// valid document allocates nothing.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxDepth is the deepest nesting of schema objects accepted.
const MaxDepth = 32

// TypeSet is a set of JSON types. Zero means "any type".
type TypeSet uint8

const (
	TNull TypeSet = 1 << iota
	TBool
	TInt
	TNum
	TStr
	TArr
	TObj
)

// TAll is every type.
const TAll = TNull | TBool | TInt | TNum | TStr | TArr | TObj

var typeNames = []struct {
	t    TypeSet
	name string
}{{TNull, "null"}, {TBool, "boolean"}, {TInt, "integer"}, {TNum, "number"}, {TStr, "string"}, {TArr, "array"}, {TObj, "object"}}

func (t TypeSet) String() string {
	if t == 0 {
		return "any"
	}
	var parts []string
	for _, n := range typeNames {
		if t&n.t != 0 && !(n.t == TInt && t&TNum != 0) {
			parts = append(parts, n.name)
		}
	}
	return strings.Join(parts, "|")
}

// Has reports whether every type in o is allowed by t (integer is a number).
func (t TypeSet) Has(o TypeSet) bool {
	if t == 0 {
		return true
	}
	if t&TNum != 0 {
		t |= TInt
	}
	return o != 0 && t&o == o
}

// Any reports whether t allows any of the types in o.
func (t TypeSet) Any(o TypeSet) bool {
	if t == 0 || o == 0 {
		return true
	}
	if t&TNum != 0 {
		t |= TInt
	}
	if o&TNum != 0 {
		o |= TInt
	}
	return t&o != 0
}

// Schema is one compiled schema node.
type Schema struct {
	Types       TypeSet
	Properties  map[string]*Schema
	PropOrder   []string
	Required    []string
	required    map[string]bool
	AddlFalse   bool    // additionalProperties: false
	Addl        *Schema // additionalProperties: {schema}
	AddlTrue    bool    // additionalProperties: true written explicitly
	Items       *Schema
	PrefixItems []*Schema
	Enum        []any
	HasConst    bool
	Const       any
	Minimum     *float64
	Maximum     *float64
	ExMinimum   *float64
	ExMaximum   *float64
	MinLength   *int
	MaxLength   *int
	MinItems    *int
	MaxItems    *int
	Pattern     *regexp.Regexp
	Format      string
	PII         bool
	Description string
	Default     any
	HasDefault  bool

	Ref    string
	target *Schema
}

// Issue is a problem found while compiling a schema.
type Issue struct {
	Path    string // JSON pointer into the schema
	Code    string // UNKNOWN_KEYWORD, INVALID_SCHEMA, INVALID_REGEX, REF_NOT_FOUND, TOO_DEEP
	Message string
}

func (i Issue) Error() string { return i.Path + ": " + i.Message }

// Compile parses raw schema JSON. It returns the schema and every issue;
// the schema is nil when there are issues.
func Compile(raw json.RawMessage) (*Schema, []Issue) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, []Issue{{Path: "", Code: "INVALID_SCHEMA", Message: "invalid JSON: " + err.Error()}}
	}
	return CompileValue(doc)
}

// CompileValue compiles an already decoded schema document.
func CompileValue(doc any) (*Schema, []Issue) {
	c := &compiler{byPtr: map[string]*Schema{}}
	root := c.node(doc, "", 0)
	for _, r := range c.refs {
		ptr := strings.TrimPrefix(r.Ref, "#")
		t, ok := c.byPtr[ptr]
		if !ok {
			c.issue(r.Ref, "REF_NOT_FOUND", fmt.Sprintf("$ref %q does not resolve to a local definition", r.Ref))
			continue
		}
		r.target = t
	}
	// Detect pure $ref cycles (a -> b -> a with nothing in between).
	for _, r := range c.refs {
		seen := map[*Schema]bool{}
		for s := r; s != nil && s.target != nil; s = s.target {
			if seen[s] {
				c.issue(r.Ref, "INVALID_SCHEMA", "$ref cycle without any structure")
				break
			}
			seen[s] = true
		}
	}
	if len(c.issues) > 0 {
		return nil, c.issues
	}
	return root, nil
}

type compiler struct {
	byPtr  map[string]*Schema
	refs   []*Schema
	issues []Issue
}

func (c *compiler) issue(path, code, msg string) {
	c.issues = append(c.issues, Issue{Path: path, Code: code, Message: msg})
}

func escapePtr(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

var ignoredKeywords = map[string]bool{
	"$schema": true, "$id": true, "$comment": true, "title": true, "examples": true,
	"readOnly": true, "writeOnly": true, "deprecated": true, "$anchor": true,
}

func (c *compiler) node(v any, ptr string, depth int) *Schema {
	s := &Schema{}
	c.byPtr[ptr] = s
	if depth > MaxDepth {
		c.issue(ptr, "TOO_DEEP", fmt.Sprintf("schema nests deeper than %d levels", MaxDepth))
		return s
	}
	switch t := v.(type) {
	case bool:
		if !t {
			// "false" schema: nothing validates. Model as an empty enum.
			s.Enum = []any{}
		}
		return s
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			c.keyword(s, k, t[k], ptr, depth)
		}
	default:
		c.issue(ptr, "INVALID_SCHEMA", "schema must be an object or boolean")
	}
	return s
}

func (c *compiler) keyword(s *Schema, k string, v any, ptr string, depth int) {
	kp := ptr + "/" + escapePtr(k)
	num := func() *float64 {
		f, ok := v.(float64)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", k+" must be a number")
			return nil
		}
		return &f
	}
	count := func() *int {
		f, ok := v.(float64)
		if !ok || f < 0 || f != math.Trunc(f) {
			c.issue(kp, "INVALID_SCHEMA", k+" must be a non-negative integer")
			return nil
		}
		n := int(f)
		return &n
	}
	switch k {
	case "type":
		switch tv := v.(type) {
		case string:
			t, ok := parseType(tv)
			if !ok {
				c.issue(kp, "INVALID_SCHEMA", "unknown type "+strconv.Quote(tv))
			}
			s.Types |= t
		case []any:
			if len(tv) == 0 {
				c.issue(kp, "INVALID_SCHEMA", "type list must not be empty")
			}
			for i, e := range tv {
				str, _ := e.(string)
				t, ok := parseType(str)
				if !ok {
					c.issue(kp+"/"+strconv.Itoa(i), "INVALID_SCHEMA", "unknown type "+fmt.Sprint(e))
				}
				s.Types |= t
			}
		default:
			c.issue(kp, "INVALID_SCHEMA", "type must be a string or a list of strings")
		}
	case "properties":
		m, ok := v.(map[string]any)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "properties must be an object")
			return
		}
		s.Properties = make(map[string]*Schema, len(m))
		for name := range m {
			s.PropOrder = append(s.PropOrder, name)
		}
		sort.Strings(s.PropOrder)
		for _, name := range s.PropOrder {
			s.Properties[name] = c.node(m[name], kp+"/"+escapePtr(name), depth+1)
		}
	case "required":
		l, ok := v.([]any)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "required must be a list of strings")
			return
		}
		s.required = map[string]bool{}
		for i, e := range l {
			str, ok := e.(string)
			if !ok {
				c.issue(kp+"/"+strconv.Itoa(i), "INVALID_SCHEMA", "required entries must be strings")
				continue
			}
			if !s.required[str] {
				s.Required = append(s.Required, str)
				s.required[str] = true
			}
		}
	case "additionalProperties":
		switch av := v.(type) {
		case bool:
			s.AddlFalse = !av
			s.AddlTrue = av
		case map[string]any:
			s.Addl = c.node(av, kp, depth+1)
		default:
			c.issue(kp, "INVALID_SCHEMA", "additionalProperties must be a boolean or a schema")
		}
	case "items":
		if _, ok := v.(map[string]any); !ok {
			if _, isBool := v.(bool); !isBool {
				c.issue(kp, "INVALID_SCHEMA", "items must be a schema")
				return
			}
		}
		s.Items = c.node(v, kp, depth+1)
	case "prefixItems":
		l, ok := v.([]any)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "prefixItems must be a list of schemas")
			return
		}
		for i, e := range l {
			s.PrefixItems = append(s.PrefixItems, c.node(e, kp+"/"+strconv.Itoa(i), depth+1))
		}
	case "enum":
		l, ok := v.([]any)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "enum must be a list")
			return
		}
		s.Enum = l
		if s.Enum == nil {
			s.Enum = []any{}
		}
	case "const":
		s.HasConst, s.Const = true, v
	case "minimum":
		s.Minimum = num()
	case "maximum":
		s.Maximum = num()
	case "exclusiveMinimum":
		s.ExMinimum = num()
	case "exclusiveMaximum":
		s.ExMaximum = num()
	case "minLength":
		s.MinLength = count()
	case "maxLength":
		s.MaxLength = count()
	case "minItems":
		s.MinItems = count()
	case "maxItems":
		s.MaxItems = count()
	case "pattern":
		str, ok := v.(string)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "pattern must be a string")
			return
		}
		re, err := regexp.Compile(str)
		if err != nil {
			c.issue(kp, "INVALID_REGEX", "pattern is not valid RE2: "+err.Error())
			return
		}
		s.Pattern = re
	case "format":
		str, _ := v.(string)
		switch str {
		case "date-time", "date", "email", "uuid":
			s.Format = str
		default:
			c.issue(kp, "INVALID_SCHEMA", "unsupported format "+strconv.Quote(fmt.Sprint(v))+" (date-time, date, email, uuid)")
		}
	case "description":
		s.Description, _ = v.(string)
	case "default":
		s.Default, s.HasDefault = v, true
	case "$defs", "definitions":
		m, ok := v.(map[string]any)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", k+" must be an object")
			return
		}
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			c.node(m[n], kp+"/"+escapePtr(n), depth+1)
		}
	case "$ref":
		str, ok := v.(string)
		if !ok || !strings.HasPrefix(str, "#") {
			c.issue(kp, "INVALID_SCHEMA", "only local $ref (\"#/...\") is supported")
			return
		}
		s.Ref = str
		c.refs = append(c.refs, s)
	case "x-gearbox-pii":
		b, ok := v.(bool)
		if !ok {
			c.issue(kp, "INVALID_SCHEMA", "x-gearbox-pii must be a boolean")
		}
		s.PII = b
	default:
		if ignoredKeywords[k] || strings.HasPrefix(k, "x-") {
			return
		}
		c.issue(kp, "UNKNOWN_KEYWORD", "keyword "+strconv.Quote(k)+" is not supported by Gearbox schemas")
	}
}

func parseType(s string) (TypeSet, bool) {
	switch s {
	case "null":
		return TNull, true
	case "boolean":
		return TBool, true
	case "integer":
		return TInt, true
	case "number":
		return TNum | TInt, true
	case "string":
		return TStr, true
	case "array":
		return TArr, true
	case "object":
		return TObj, true
	}
	return 0, false
}

// Resolve follows $ref links and returns the schema that carries structure.
func (s *Schema) Resolve() *Schema {
	for i := 0; s != nil && s.target != nil && i < 64; i++ {
		s = s.target
	}
	return s
}

// IsPII reports whether this node (or its $ref target) is marked PII.
func (s *Schema) IsPII() bool {
	for i := 0; s != nil && i < 64; i++ {
		if s.PII {
			return true
		}
		s = s.target
	}
	return false
}

// EffectiveTypes returns the declared types, following $ref, and inferring
// from enum/const when no type is given. Zero means any.
func (s *Schema) EffectiveTypes() TypeSet {
	s = s.Resolve()
	if s == nil {
		return 0
	}
	if s.Types != 0 {
		return s.Types
	}
	vals := s.Enum
	if s.HasConst {
		vals = []any{s.Const}
	}
	if vals == nil {
		if s.Properties != nil {
			return 0
		}
		return 0
	}
	var t TypeSet
	for _, v := range vals {
		t |= TypeOf(v)
	}
	return t
}

// IsRequired reports whether name is in required.
func (s *Schema) IsRequired(name string) bool {
	s = s.Resolve()
	return s != nil && s.required[name]
}

// Property returns the schema of a property. known=false means the object is
// closed (it declares properties and does not allow additional ones) and the
// name is not among them. A nil schema with known=true means "any".
func (s *Schema) Property(name string) (sub *Schema, known bool) {
	s = s.Resolve()
	if s == nil {
		return nil, true
	}
	if p, ok := s.Properties[name]; ok {
		return p, true
	}
	if s.Addl != nil {
		return s.Addl, true
	}
	if s.Properties != nil && !s.AddlTrue {
		return nil, false
	}
	if s.AddlFalse {
		return nil, false
	}
	return nil, true
}

// Element returns the schema of array elements (nil = any).
func (s *Schema) Element() *Schema {
	s = s.Resolve()
	if s == nil {
		return nil
	}
	return s.Items
}

// MaxItemsOf returns maxItems for an array schema.
func (s *Schema) MaxItemsOf() (int, bool) {
	s = s.Resolve()
	if s == nil || s.MaxItems == nil {
		return 0, false
	}
	return *s.MaxItems, true
}

// TypeOf returns the JSON type of a decoded value.
func TypeOf(v any) TypeSet {
	switch t := v.(type) {
	case nil:
		return TNull
	case bool:
		return TBool
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return TInt | TNum
		}
		return TNum
	case float32:
		return TypeOf(float64(t))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return TInt | TNum
	case string:
		return TStr
	case []any:
		return TArr
	case map[string]any:
		return TObj
	}
	return 0
}

// PIIPaths lists the paths (segments; "[]" for array elements) of every node
// marked x-gearbox-pii, up to depth MaxDepth.
func (s *Schema) PIIPaths() [][]string {
	var out [][]string
	var walk func(s *Schema, path []string, depth int)
	walk = func(n *Schema, path []string, depth int) {
		if n == nil || depth > MaxDepth {
			return
		}
		if n.IsPII() {
			out = append(out, append([]string(nil), path...))
			return
		}
		n = n.Resolve()
		for _, name := range n.PropOrder {
			walk(n.Properties[name], append(path, name), depth+1)
		}
		if n.Addl != nil {
			walk(n.Addl, append(path, "*"), depth+1)
		}
		if n.Items != nil {
			walk(n.Items, append(path, "[]"), depth+1)
		}
		for _, p := range n.PrefixItems {
			walk(p, append(path, "[]"), depth+1)
		}
	}
	walk(s, nil, 0)
	return out
}
