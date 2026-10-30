// SPDX-License-Identifier: MIT

package jsonschema

import (
	"fmt"
	"net/mail"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FieldError is one validation error with a JSON pointer into the document.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	// Keyword that failed (required, type, maxItems, ...).
	Keyword string `json:"-"`
}

// MaxErrors caps the number of errors reported for one document.
const MaxErrors = 50

type validator struct {
	path    []string
	errs    []FieldError
	collect bool
	failed  bool
}

func (v *validator) fail(keyword, msg string) {
	v.failed = true
	if !v.collect || len(v.errs) >= MaxErrors {
		return
	}
	var b strings.Builder
	for _, p := range v.path {
		b.WriteByte('/')
		b.WriteString(escapePtr(p))
	}
	v.errs = append(v.errs, FieldError{Path: b.String(), Message: msg, Keyword: keyword})
}

// Valid reports whether doc matches the schema. It does not allocate for
// valid documents.
func (s *Schema) Valid(doc any) bool {
	v := validator{}
	v.check(s, doc, 0)
	return !v.failed
}

// Validate returns every error (up to MaxErrors). An empty result means valid.
func (s *Schema) Validate(doc any) []FieldError {
	if s.Valid(doc) {
		return nil
	}
	v := validator{collect: true, path: make([]string, 0, 16)}
	v.check(s, doc, 0)
	return v.errs
}

func (v *validator) push(seg string) {
	if v.collect {
		v.path = append(v.path, seg)
	}
}

func (v *validator) pop() {
	if v.collect {
		v.path = v.path[:len(v.path)-1]
	}
}

func (v *validator) check(s *Schema, doc any, depth int) {
	if s == nil {
		return
	}
	if depth > 2*MaxDepth+8 {
		v.fail("depth", "document nests too deeply")
		return
	}
	if s.target != nil {
		v.check(s.target, doc, depth+1)
		if !v.collect && v.failed {
			return
		}
	}
	t := TypeOf(doc)
	if t == 0 {
		v.fail("type", fmt.Sprintf("unsupported value of Go type %T", doc))
		return
	}
	if s.Types != 0 && s.Types&t == 0 {
		v.fail("type", "expected "+s.Types.String()+", got "+typeName(doc))
		return
	}
	if s.Enum != nil {
		ok := false
		for _, e := range s.Enum {
			if Equal(e, doc) {
				ok = true
				break
			}
		}
		if !ok {
			v.fail("enum", "value is not one of the allowed values")
		}
	}
	if s.HasConst && !Equal(s.Const, doc) {
		v.fail("const", "value must equal "+compactJSON(s.Const))
	}
	switch d := doc.(type) {
	case string:
		v.checkString(s, d)
	case map[string]any:
		v.checkObject(s, d, depth)
	case []any:
		v.checkArray(s, d, depth)
	case bool, nil:
	default:
		if f, ok := toFloat(doc); ok {
			v.checkNumber(s, f)
		}
	}
}

func (v *validator) checkNumber(s *Schema, f float64) {
	if s.Minimum != nil && f < *s.Minimum {
		v.fail("minimum", "must be >= "+fmtNum(*s.Minimum))
	}
	if s.Maximum != nil && f > *s.Maximum {
		v.fail("maximum", "must be <= "+fmtNum(*s.Maximum))
	}
	if s.ExMinimum != nil && f <= *s.ExMinimum {
		v.fail("exclusiveMinimum", "must be > "+fmtNum(*s.ExMinimum))
	}
	if s.ExMaximum != nil && f >= *s.ExMaximum {
		v.fail("exclusiveMaximum", "must be < "+fmtNum(*s.ExMaximum))
	}
}

func (v *validator) checkString(s *Schema, str string) {
	if s.MinLength != nil || s.MaxLength != nil {
		n := utf8.RuneCountInString(str)
		if s.MinLength != nil && n < *s.MinLength {
			v.fail("minLength", fmt.Sprintf("must be at least %d characters", *s.MinLength))
		}
		if s.MaxLength != nil && n > *s.MaxLength {
			v.fail("maxLength", fmt.Sprintf("must be at most %d characters", *s.MaxLength))
		}
	}
	if s.Pattern != nil && !s.Pattern.MatchString(str) {
		v.fail("pattern", "does not match pattern "+s.Pattern.String())
	}
	if s.Format != "" && !checkFormat(s.Format, str) {
		v.fail("format", "is not a valid "+s.Format)
	}
}

func (v *validator) checkObject(s *Schema, m map[string]any, depth int) {
	for _, r := range s.Required {
		if _, ok := m[r]; !ok {
			v.push(r)
			v.fail("required", "required property missing")
			v.pop()
			if !v.collect {
				return
			}
		}
	}
	if s.Addl == nil && !s.AddlFalse {
		// Extra keys need no check: walk the declared properties only.
		for _, name := range s.PropOrder {
			if val, ok := m[name]; ok {
				v.push(name)
				v.check(s.Properties[name], val, depth+1)
				v.pop()
				if !v.collect && v.failed {
					return
				}
			}
		}
		return
	}
	if v.collect {
		// Deterministic error order.
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v.checkProp(s, k, m[k], depth)
		}
		return
	}
	for k, val := range m {
		v.checkProp(s, k, val, depth)
		if v.failed {
			return
		}
	}
}

func (v *validator) checkProp(s *Schema, k string, val any, depth int) {
	sub, ok := s.Properties[k]
	if !ok {
		if s.AddlFalse {
			v.push(k)
			v.fail("additionalProperties", "property is not allowed")
			v.pop()
			return
		}
		sub = s.Addl
	}
	if sub == nil {
		return
	}
	v.push(k)
	v.check(sub, val, depth+1)
	v.pop()
}

func (v *validator) checkArray(s *Schema, a []any, depth int) {
	if s.MinItems != nil && len(a) < *s.MinItems {
		v.fail("minItems", fmt.Sprintf("must have at least %d items", *s.MinItems))
	}
	if s.MaxItems != nil && len(a) > *s.MaxItems {
		v.fail("maxItems", fmt.Sprintf("must have at most %d items", *s.MaxItems))
		if !v.collect {
			return
		}
	}
	for i, e := range a {
		var sub *Schema
		if i < len(s.PrefixItems) {
			sub = s.PrefixItems[i]
		} else {
			sub = s.Items
		}
		if sub == nil {
			continue
		}
		v.push(strconv.Itoa(i))
		v.check(sub, e, depth+1)
		v.pop()
		if !v.collect && v.failed {
			return
		}
	}
}

func checkFormat(format, s string) bool {
	switch format {
	case "date-time":
		_, err := time.Parse(time.RFC3339Nano, s)
		return err == nil
	case "date":
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	case "email":
		if strings.ContainsAny(s, " \t\r\n") {
			return false
		}
		a, err := mail.ParseAddress(s)
		return err == nil && a.Address == s && strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".")
	case "uuid":
		if len(s) != 36 {
			return false
		}
		for i := 0; i < 36; i++ {
			c := s[i]
			switch i {
			case 8, 13, 18, 23:
				if c != '-' {
					return false
				}
			default:
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					return false
				}
			}
		}
		return true
	}
	return true
}

func typeName(v any) string {
	t := TypeOf(v)
	switch {
	case t&TInt != 0:
		return "integer"
	case t == TNum:
		return "number"
	}
	return t.String()
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func compactJSON(v any) string {
	switch t := v.(type) {
	case string:
		return strconv.Quote(t)
	case nil:
		return "null"
	}
	return fmt.Sprint(v)
}

// Equal compares decoded JSON values (numbers numerically).
func Equal(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		fb, ok := toFloat(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case nil:
		return b == nil
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !Equal(xv, yv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
