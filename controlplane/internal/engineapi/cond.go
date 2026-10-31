package engineapi

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"regexp"
	"strings"
)

// Murmur3 is MurmurHash3 x86 32-bit with seed 0.
func Murmur3(data []byte) uint32 {
	const c1, c2 = 0xcc9e2d51, 0x1b873593
	var h uint32
	n := len(data) / 4
	for i := 0; i < n; i++ {
		k := binary.LittleEndian.Uint32(data[i*4:])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
		h = bits.RotateLeft32(h, 13)
		h = h*5 + 0xe6546b64
	}
	tail := data[n*4:]
	var k uint32
	switch len(tail) {
	case 3:
		k ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k ^= uint32(tail[0])
		k *= c1
		k = bits.RotateLeft32(k, 15)
		k *= c2
		h ^= k
	}
	h ^= uint32(len(data))
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	h *= 0xc2b2ae35
	h ^= h >> 16
	return h
}

// Bucket returns murmur3(salt + key) mod 10000, the contract's bucketing.
func Bucket(salt, key string) int {
	return int(Murmur3([]byte(salt+key)) % 10000)
}

// fieldValue resolves a dotted path in a decoded JSON object.
func fieldValue(attrs any, path string) (any, bool) {
	cur := attrs
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

func toFloat(x any) (float64, bool) {
	f, ok := x.(float64)
	return f, ok
}

func toList(x any) []any {
	if l, ok := x.([]any); ok {
		return l
	}
	return []any{x}
}

func eqVal(a, b any, fold bool) bool {
	if fold {
		as, aok := a.(string)
		bs, bok := b.(string)
		if aok && bok {
			return strings.EqualFold(as, bs)
		}
	}
	return jsonEqual(a, b)
}

// evalCond evaluates a Condition over attributes. Only field conditions and
// all/any/not are supported; other leaves (segment, gate, variant, expr)
// evaluate to false and set *unsupported.
func evalCond(c any, attrs any, unsupported *bool) bool {
	m, ok := c.(map[string]any)
	if !ok {
		return false
	}
	if all, ok := m["all"].([]any); ok {
		for _, x := range all {
			if !evalCond(x, attrs, unsupported) {
				return false
			}
		}
		return true
	}
	if anyC, ok := m["any"].([]any); ok {
		for _, x := range anyC {
			if evalCond(x, attrs, unsupported) {
				return true
			}
		}
		return false
	}
	if n, ok := m["not"]; ok {
		return !evalCond(n, attrs, unsupported)
	}
	field, ok := m["field"].(string)
	if !ok {
		*unsupported = true
		return false
	}
	op, _ := m["op"].(string)
	val := m["value"]
	x, present := fieldValue(attrs, field)
	if op == "NOT_EXISTS" {
		return !present
	}
	if !present {
		return false
	}
	fold := strings.Contains(op, "CASE_INSENSITIVE")
	base := strings.TrimSuffix(strings.TrimSuffix(op, "_CASE_INSENSITIVE"), "_NUMERIC")
	switch base {
	case "EXISTS":
		return true
	case "IS_TRUE":
		return x == true
	case "IS_FALSE":
		return x == false
	case "EQ":
		return eqVal(x, val, fold)
	case "NEQ":
		return !eqVal(x, val, fold)
	case "GT", "GTE", "LT", "LTE", "BETWEEN":
		a, aok := toFloat(x)
		b, bok := toFloat(val)
		if !aok || !bok {
			return false
		}
		switch base {
		case "GT":
			return a > b
		case "GTE":
			return a >= b
		case "LT":
			return a < b
		case "LTE":
			return a <= b
		default:
			hi, ok := toFloat(m["valueTo"])
			return ok && a >= b && a <= hi
		}
	case "IN", "NOT_IN":
		in := false
		for _, v := range toList(val) {
			if eqVal(x, v, fold) {
				in = true
				break
			}
		}
		return in == (base == "IN")
	case "CONTAINS", "NOT_CONTAINS":
		in := false
		if s, ok := x.(string); ok {
			if vs, ok := val.(string); ok {
				if fold {
					in = strings.Contains(strings.ToLower(s), strings.ToLower(vs))
				} else {
					in = strings.Contains(s, vs)
				}
			}
		} else {
			for _, e := range toList(x) {
				if eqVal(e, val, fold) {
					in = true
					break
				}
			}
		}
		return in == (base == "CONTAINS")
	case "CONTAINS_ANY", "CONTAINS_NONE":
		hit := false
		for _, e := range toList(x) {
			for _, v := range toList(val) {
				if eqVal(e, v, false) {
					hit = true
				}
			}
		}
		return hit == (base == "CONTAINS_ANY")
	case "STARTS_WITH", "ENDS_WITH":
		s, ok1 := x.(string)
		vs, ok2 := val.(string)
		if !ok1 || !ok2 {
			return false
		}
		if fold {
			s, vs = strings.ToLower(s), strings.ToLower(vs)
		}
		if base == "STARTS_WITH" {
			return strings.HasPrefix(s, vs)
		}
		return strings.HasSuffix(s, vs)
	case "MATCHES", "NOT_MATCHES":
		s, ok1 := x.(string)
		p, ok2 := val.(string)
		if !ok1 || !ok2 {
			return false
		}
		if fold {
			p = "(?i)" + p
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return false
		}
		return re.MatchString(s) == (base == "MATCHES")
	}
	*unsupported = true
	return false
}

func decodeAttrs(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("attributes: %w", err)
	}
	return v, nil
}
