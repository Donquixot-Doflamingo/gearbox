// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Runtime values are decoded JSON: nil, bool, float64, string, []any,
// map[string]any. Expressions may also produce Go ints, time.Time and
// time.Duration; those are normalised when encoded.

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case int32:
		return float64(n), true
	case int16:
		return float64(n), true
	case int8:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint8:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// toNumber converts numbers and numeric strings (for numeric operators).
func toNumber(v any) (float64, bool) {
	if f, ok := toFloat(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// scalarString renders a scalar for string operators and keys.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case nil:
		return "", false
	}
	if f, ok := toFloat(v); ok {
		return formatNumber(f), true
	}
	return "", false
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// valuesEqual compares two runtime values; numbers compare numerically.
func valuesEqual(a, b any) bool {
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
			if !valuesEqual(x[i], y[i]) {
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
			if !ok || !valuesEqual(xv, yv) {
				return false
			}
		}
		return true
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	return reflect.DeepEqual(a, b)
}

// deepCopy copies maps and slices (scalars are immutable).
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, e := range t {
			l[i] = deepCopy(e)
		}
		return l
	}
	return v
}

// normalize converts expression results into plain JSON values.
func normalize(v any) any {
	switch t := v.(type) {
	case nil, bool, float64, string:
		return v
	case map[string]any:
		for k, e := range t {
			switch e.(type) {
			case nil, bool, float64, string:
			default:
				t[k] = normalize(e)
			}
		}
		return t
	case []any:
		for i, e := range t {
			switch e.(type) {
			case nil, bool, float64, string:
			default:
				t[i] = normalize(e)
			}
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return t.String()
	case json.RawMessage:
		var out any
		if json.Unmarshal(t, &out) == nil {
			return out
		}
		return nil
	}
	if f, ok := toFloat(v); ok {
		return f
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Bool:
		return rv.Bool()
	case reflect.String:
		return rv.String()
	case reflect.Slice, reflect.Array:
		l := make([]any, rv.Len())
		for i := range l {
			l[i] = normalize(rv.Index(i).Interface())
		}
		return l
	case reflect.Map:
		m := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			m[fmt.Sprint(iter.Key().Interface())] = normalize(iter.Value().Interface())
		}
		return m
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalize(rv.Elem().Interface())
	case reflect.Struct:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var out any
		_ = json.Unmarshal(b, &out)
		return out
	}
	return fmt.Sprint(v)
}

// appendJSON encodes a runtime value with sorted object keys (deterministic).
func appendJSON(dst []byte, v any) []byte {
	switch t := v.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		if t {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case string:
		return appendString(dst, t)
	case float64:
		return appendFloat(dst, t)
	case map[string]any:
		if len(t) == 0 {
			return append(dst, "{}"...)
		}
		var kbuf [16]string
		keys := kbuf[:0]
		for k := range t {
			keys = append(keys, k)
		}
		if len(keys) > 1 {
			sortStrings(keys)
		}
		dst = append(dst, '{')
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, k)
			dst = append(dst, ':')
			dst = appendJSON(dst, t[k])
		}
		return append(dst, '}')
	case []any:
		dst = append(dst, '[')
		for i, e := range t {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSON(dst, e)
		}
		return append(dst, ']')
	case json.RawMessage:
		if len(t) == 0 {
			return append(dst, "null"...)
		}
		return append(dst, t...)
	}
	if f, ok := toFloat(v); ok {
		return appendFloat(dst, f)
	}
	return appendJSON(dst, normalize(v))
}

func sortStrings(s []string) {
	if len(s) < 12 {
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j] < s[j-1]; j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
		return
	}
	sort.Strings(s)
}

func appendFloat(dst []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.AppendInt(dst, int64(f), 10)
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, 64)
	if format == 'e' {
		// clean up e-09 to e-9 like encoding/json
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}

const hexDigits = "0123456789abcdef"

func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, `�`...)
			i += size
			start = i
			continue
		}
		if r == ' ' || r == ' ' {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

func encodeJSON(v any) json.RawMessage {
	return appendJSON(make([]byte, 0, 128), v)
}

// pathPart is one segment of a dotted field path.
type pathPart struct {
	key   string
	index int // >= 0 when the segment is an array index
}

// parseFieldPath splits "a.b[0].c" / "a.b.0.c" into parts.
func parseFieldPath(p string) ([]pathPart, error) {
	if p == "" {
		return nil, fmt.Errorf("empty field path")
	}
	var parts []pathPart
	for _, seg := range strings.Split(p, ".") {
		if seg == "" {
			return nil, fmt.Errorf("empty segment in %q", p)
		}
		name := seg
		var idx []int
		for {
			open := strings.IndexByte(name, '[')
			if open < 0 {
				break
			}
			close := strings.IndexByte(name[open:], ']')
			if close < 0 {
				return nil, fmt.Errorf("unclosed [ in %q", p)
			}
			n, err := strconv.Atoi(name[open+1 : open+close])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid index in %q", p)
			}
			if rest := name[open+close+1:]; rest != "" && rest[0] != '[' {
				return nil, fmt.Errorf("unexpected %q after index in %q", rest, p)
			}
			idx = append(idx, n)
			name = name[:open] + name[open+close+1:]
		}
		if name != "" {
			parts = append(parts, pathPart{key: name, index: -1})
		}
		for _, i := range idx {
			parts = append(parts, pathPart{index: i})
		}
	}
	return parts, nil
}

// getPath reads a field path; found=false when any segment is missing.
func getPath(v any, parts []pathPart) (any, bool) {
	for _, p := range parts {
		switch t := v.(type) {
		case map[string]any:
			if p.index >= 0 {
				var ok bool
				v, ok = t[strconv.Itoa(p.index)]
				if !ok {
					return nil, false
				}
				continue
			}
			var ok bool
			v, ok = t[p.key]
			if !ok {
				return nil, false
			}
		case []any:
			i := p.index
			if i < 0 {
				n, err := strconv.Atoi(p.key)
				if err != nil {
					return nil, false
				}
				i = n
			}
			if i >= len(t) {
				return nil, false
			}
			v = t[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// timeOf accepts time.Time or RFC 3339 / date strings.
func timeOf(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return ts, true
		}
		if ts, err := time.Parse("2006-01-02", t); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}
