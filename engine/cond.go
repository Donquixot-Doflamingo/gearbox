// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// condFn evaluates a compiled condition against a node input.
type condFn func(r *run, in any) (bool, error)

// Field operators (FieldOperator in the contract).
var fieldOps = []string{
	"EQ", "NEQ", "EQ_CASE_INSENSITIVE", "NEQ_CASE_INSENSITIVE",
	"GT", "GTE", "LT", "LTE", "BETWEEN",
	"IN", "NOT_IN", "IN_CASE_INSENSITIVE", "NOT_IN_CASE_INSENSITIVE", "IN_NUMERIC", "NOT_IN_NUMERIC",
	"CONTAINS", "NOT_CONTAINS", "CONTAINS_CASE_INSENSITIVE", "NOT_CONTAINS_CASE_INSENSITIVE",
	"CONTAINS_NUMERIC", "NOT_CONTAINS_NUMERIC", "CONTAINS_ANY", "CONTAINS_NONE",
	"STARTS_WITH", "ENDS_WITH", "STARTS_WITH_CASE_INSENSITIVE", "ENDS_WITH_CASE_INSENSITIVE",
	"MATCHES", "NOT_MATCHES", "MATCHES_CASE_INSENSITIVE", "NOT_MATCHES_CASE_INSENSITIVE",
	"EXISTS", "NOT_EXISTS", "IS_TRUE", "IS_FALSE",
}

type opKind int

const (
	vkNone opKind = iota
	vkScalar
	vkNumber
	vkString
	vkList
	vkRange
	vkRegex
)

var opValueKind = map[string]opKind{
	"EQ": vkScalar, "NEQ": vkScalar, "EQ_CASE_INSENSITIVE": vkString, "NEQ_CASE_INSENSITIVE": vkString,
	"GT": vkNumber, "GTE": vkNumber, "LT": vkNumber, "LTE": vkNumber, "BETWEEN": vkRange,
	"IN": vkList, "NOT_IN": vkList, "IN_CASE_INSENSITIVE": vkList, "NOT_IN_CASE_INSENSITIVE": vkList,
	"IN_NUMERIC": vkList, "NOT_IN_NUMERIC": vkList,
	"CONTAINS": vkScalar, "NOT_CONTAINS": vkScalar, "CONTAINS_CASE_INSENSITIVE": vkString, "NOT_CONTAINS_CASE_INSENSITIVE": vkString,
	"CONTAINS_NUMERIC": vkNumber, "NOT_CONTAINS_NUMERIC": vkNumber, "CONTAINS_ANY": vkList, "CONTAINS_NONE": vkList,
	"STARTS_WITH": vkString, "ENDS_WITH": vkString, "STARTS_WITH_CASE_INSENSITIVE": vkString, "ENDS_WITH_CASE_INSENSITIVE": vkString,
	"MATCHES": vkRegex, "NOT_MATCHES": vkRegex, "MATCHES_CASE_INSENSITIVE": vkRegex, "NOT_MATCHES_CASE_INSENSITIVE": vkRegex,
	"EXISTS": vkNone, "NOT_EXISTS": vkNone, "IS_TRUE": vkNone, "IS_FALSE": vkNone,
}

// condCtx carries what conditions need besides the scope.
type condCtx struct {
	sc *scope
	// defaultKeyUnit: rollout segments without key use ctx.unit.
}

// cond compiles a Condition. Returns nil (with problems) on error.
func (b *builder) cond(raw json.RawMessage, path string, sc *scope) condFn {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		b.errorf("INVALID_SPEC", path, "condition is not valid JSON")
		return nil
	}
	return b.condValue(v, path, sc, 0)
}

func (b *builder) condValue(v any, path string, sc *scope, depth int) condFn {
	if depth > 32 {
		b.errorf("INVALID_SPEC", path, "condition nests deeper than 32 levels")
		return nil
	}
	switch t := v.(type) {
	case string:
		// A bare string is an Expr condition.
		return b.exprCond(t, path, sc)
	case bool:
		val := t
		return func(*run, any) (bool, error) { return val, nil }
	case map[string]any:
		return b.condObject(t, path, sc, depth)
	case nil:
		b.errorf("INVALID_SPEC", path, "condition is required")
		return nil
	}
	b.errorf("INVALID_SPEC", path, "condition must be an object")
	return nil
}

func allowedKeys(b *builder, m map[string]any, path string, keys ...string) bool {
	ok := true
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if !allowed[k] {
			b.errorf("INVALID_SPEC", path+"/"+ptrEscape(k), "unexpected property %q in condition", k)
			ok = false
		}
	}
	return ok
}

func (b *builder) condObject(m map[string]any, path string, sc *scope, depth int) condFn {
	switch {
	case m["all"] != nil || m["any"] != nil:
		k := "all"
		if m["any"] != nil {
			k = "any"
		}
		if !allowedKeys(b, m, path, k) {
			return nil
		}
		list, ok := m[k].([]any)
		if !ok || len(list) == 0 {
			b.errorf("INVALID_SPEC", path+"/"+k, "%s must be a non-empty list of conditions", k)
			return nil
		}
		fns := make([]condFn, 0, len(list))
		failed := false
		for i, c := range list {
			f := b.condValue(c, fmt.Sprintf("%s/%s/%d", path, k, i), sc, depth+1)
			if f == nil {
				failed = true
			}
			fns = append(fns, f)
		}
		if failed {
			return nil
		}
		if k == "all" {
			if len(fns) == 1 {
				return fns[0]
			}
			return func(r *run, in any) (bool, error) {
				for _, f := range fns {
					ok, err := f(r, in)
					if err != nil || !ok {
						return false, err
					}
				}
				return true, nil
			}
		}
		return func(r *run, in any) (bool, error) {
			for _, f := range fns {
				ok, err := f(r, in)
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
			return false, nil
		}
	case m["not"] != nil:
		if !allowedKeys(b, m, path, "not") {
			return nil
		}
		f := b.condValue(m["not"], path+"/not", sc, depth+1)
		if f == nil {
			return nil
		}
		return func(r *run, in any) (bool, error) {
			ok, err := f(r, in)
			return !ok && err == nil, err
		}
	case m["field"] != nil:
		if !allowedKeys(b, m, path, "field", "op", "value", "valueTo") {
			return nil
		}
		return b.fieldCond(m, path, sc)
	case m["segment"] != nil:
		if !allowedKeys(b, m, path, "segment", "op", "key", "attributes") {
			return nil
		}
		return b.segmentCond(m, path, sc)
	case m["gate"] != nil:
		if !allowedKeys(b, m, path, "gate", "op") {
			return nil
		}
		return b.gateCond(m, path)
	case m["experiment"] != nil:
		if !allowedKeys(b, m, path, "experiment", "variants") {
			return nil
		}
		return b.variantCond(m, path)
	case m["expr"] != nil:
		if !allowedKeys(b, m, path, "expr") {
			return nil
		}
		src, ok := m["expr"].(string)
		if !ok {
			b.errorf("INVALID_SPEC", path+"/expr", "expr must be a string")
			return nil
		}
		return b.exprCond(src, path+"/expr", sc)
	}
	b.errorf("INVALID_SPEC", path, "condition needs one of all, any, not, field, segment, gate, experiment, expr")
	return nil
}

func (b *builder) exprCond(src, path string, sc *scope) condFn {
	p, _, ok := b.expr(src, path, sc)
	if !ok {
		return nil
	}
	return func(r *run, in any) (bool, error) {
		v, err := r.eval(p, in)
		if err != nil {
			return false, err
		}
		switch t := v.(type) {
		case bool:
			return t, nil
		case nil:
			return false, nil
		}
		return false, fmt.Errorf("condition %q returned %s, not a boolean", src, kindName(v))
	}
}

// ---- field conditions

func (b *builder) fieldCond(m map[string]any, path string, sc *scope) condFn {
	field, ok := m["field"].(string)
	if !ok || field == "" {
		b.errorf("INVALID_SPEC", path+"/field", "field must be a dotted path string")
		return nil
	}
	op, _ := m["op"].(string)
	kind, known := opValueKind[op]
	if !known {
		b.errorf("INVALID_SPEC", path+"/op", "unknown operator %q", op)
		return nil
	}
	parts, err := parseFieldPath(field)
	if err != nil {
		b.errorf("PARSE_ERROR", path+"/field", "%s", err)
		return nil
	}
	value, hasValue := m["value"]
	valueTo, hasTo := m["valueTo"]

	// Static checks against the input schema.
	fs, fknown, msg := fieldSchema(sc.input, parts)
	if msg != "" {
		b.errorf("UNKNOWN_FIELD", path+"/field", "%s", msg)
		return nil
	}
	var ftypes jsonschema.TypeSet
	if fknown {
		ftypes = fs.EffectiveTypes()
	}
	fieldDesc := func() string {
		return fmt.Sprintf("%s is %s in %s", field, typeWord(ftypes), labelOr(sc.input.label))
	}
	mismatch := func(format string, args ...any) condFn {
		b.errorf("TYPE_MISMATCH", path, "%s; %s", fieldDesc(), fmt.Sprintf(format, args...))
		return nil
	}

	switch kind {
	case vkNone:
		if hasValue && value != nil {
			b.warnf("INVALID_SPEC", path+"/value", "%s takes no value; it is ignored", op)
		}
		if (op == "IS_TRUE" || op == "IS_FALSE") && ftypes != 0 && !ftypes.Any(jsonschema.TBool|jsonschema.TStr) {
			return mismatch("%s needs a boolean", op)
		}
	case vkScalar, vkString, vkNumber, vkRegex:
		if !hasValue || value == nil {
			b.errorf("MISSING_VALUE", path+"/value", "%s needs a value", op)
			return nil
		}
		switch value.(type) {
		case []any, map[string]any:
			b.errorf("TYPE_MISMATCH", path+"/value", "%s needs a single value, not a list or object", op)
			return nil
		}
	case vkList:
		l, ok := value.([]any)
		if !ok {
			b.errorf("TYPE_MISMATCH", path+"/value", "%s needs a list value", op)
			return nil
		}
		for i, e := range l {
			switch e.(type) {
			case []any, map[string]any:
				b.errorf("TYPE_MISMATCH", fmt.Sprintf("%s/value/%d", path, i), "list values must be scalars")
				return nil
			}
		}
	case vkRange:
		lo, ok1 := toNumber(value)
		hi, ok2 := toNumber(valueTo)
		if !hasValue || !hasTo || !ok1 || !ok2 {
			b.errorf("MISSING_VALUE", path, "BETWEEN needs numeric value and valueTo")
			return nil
		}
		if lo > hi {
			b.warnf("INVALID_SPEC", path, "BETWEEN lower bound %v is above upper bound %v; it never matches", value, valueTo)
		}
	}
	if hasTo && kind != vkRange {
		b.warnf("INVALID_SPEC", path+"/valueTo", "valueTo is only used by BETWEEN")
	}

	numericField := ftypes == 0 || ftypes.Any(jsonschema.TNum|jsonschema.TInt|jsonschema.TStr)
	stringField := ftypes == 0 || ftypes.Any(jsonschema.TStr)
	isNum := func(v any) bool { _, ok := toFloat(v); return ok }
	isStr := func(v any) bool { _, ok := v.(string); return ok }
	if ftypes != 0 {
		switch op {
		case "GT", "GTE", "LT", "LTE", "BETWEEN":
			if !numericField {
				return mismatch("%s needs a number", op)
			}
			if !isNum(value) {
				return mismatch("cannot compare with %s %s", kindName(value), compact(value))
			}
		case "EQ", "NEQ":
			if !valueFitsTypes(value, ftypes) {
				return mismatch("cannot compare with %s %s", kindName(value), compact(value))
			}
		case "IN", "NOT_IN":
			for _, e := range value.([]any) {
				if !valueFitsTypes(e, ftypes) {
					return mismatch("list value %s does not fit", compact(e))
				}
			}
		case "IN_NUMERIC", "NOT_IN_NUMERIC":
			if !numericField {
				return mismatch("%s needs a number", op)
			}
			for _, e := range value.([]any) {
				if _, ok := toNumber(e); !ok {
					return mismatch("%s needs numeric list values, got %s", op, compact(e))
				}
			}
		case "IN_CASE_INSENSITIVE", "NOT_IN_CASE_INSENSITIVE":
			if !stringField {
				return mismatch("%s needs a string", op)
			}
		case "EQ_CASE_INSENSITIVE", "NEQ_CASE_INSENSITIVE", "STARTS_WITH", "ENDS_WITH", "STARTS_WITH_CASE_INSENSITIVE", "ENDS_WITH_CASE_INSENSITIVE",
			"MATCHES", "NOT_MATCHES", "MATCHES_CASE_INSENSITIVE", "NOT_MATCHES_CASE_INSENSITIVE":
			if !stringField {
				return mismatch("%s needs a string", op)
			}
		case "CONTAINS", "NOT_CONTAINS", "CONTAINS_CASE_INSENSITIVE", "NOT_CONTAINS_CASE_INSENSITIVE", "CONTAINS_ANY", "CONTAINS_NONE":
			if !ftypes.Any(jsonschema.TStr | jsonschema.TArr) {
				return mismatch("%s needs a string or a list", op)
			}
		case "CONTAINS_NUMERIC", "NOT_CONTAINS_NUMERIC":
			if !ftypes.Any(jsonschema.TArr) {
				return mismatch("%s needs a list of numbers", op)
			}
		}
	}
	switch kind {
	case vkString, vkRegex:
		if !isStr(value) {
			b.errorf("TYPE_MISMATCH", path+"/value", "%s needs a string value", op)
			return nil
		}
	case vkNumber:
		if _, ok := toNumber(value); !ok {
			b.errorf("TYPE_MISMATCH", path+"/value", "%s needs a numeric value", op)
			return nil
		}
	}

	test, err := fieldTest(op, value, valueTo)
	if err != nil {
		b.errorf("PARSE_ERROR", path+"/value", "%s", err)
		return nil
	}
	notExists := op == "NOT_EXISTS"
	if len(parts) == 1 && parts[0].index < 0 {
		k := parts[0].key
		return func(_ *run, in any) (bool, error) {
			m, ok := in.(map[string]any)
			if !ok {
				return notExists, nil
			}
			v, ok := m[k]
			if !ok || v == nil {
				return notExists, nil
			}
			return test(v), nil
		}
	}
	return func(_ *run, in any) (bool, error) {
		v, ok := getPath(in, parts)
		if !ok || v == nil {
			return notExists, nil
		}
		return test(v), nil
	}
}

func typeWord(t jsonschema.TypeSet) string {
	if t == 0 {
		return "any"
	}
	if t&jsonschema.TInt != 0 && t&jsonschema.TNum == 0 {
		return "integer"
	}
	return t.String()
}

func compact(v any) string {
	return string(encodeJSON(v))
}

func valueFitsTypes(v any, t jsonschema.TypeSet) bool {
	vt := jsonschema.TypeOf(v)
	if v == nil {
		return t&jsonschema.TNull != 0
	}
	if vt&jsonschema.TNum != 0 {
		return t.Any(jsonschema.TNum | jsonschema.TInt)
	}
	return t.Any(vt)
}

// fieldTest returns the predicate for a present, non-null field value.
func fieldTest(op string, value, valueTo any) (func(any) bool, error) {
	str, _ := scalarString(value)
	lowStr := strings.ToLower(str)
	num, numOK := toNumber(value)
	list, _ := value.([]any)

	// Pre-built sets for list operators.
	strSet := map[string]bool{}
	lowerSet := map[string]bool{}
	numSet := map[float64]bool{}
	for _, e := range list {
		if s, ok := e.(string); ok {
			strSet[s] = true
			lowerSet[strings.ToLower(s)] = true
		}
		if f, ok := toNumber(e); ok {
			numSet[f] = true
		}
	}
	inList := func(x any) bool {
		if s, ok := x.(string); ok {
			if strSet[s] {
				return true
			}
		}
		if f, ok := toFloat(x); ok {
			for _, e := range list {
				if g, ok := toFloat(e); ok && g == f {
					return true
				}
			}
			return false
		}
		for _, e := range list {
			if valuesEqual(x, e) {
				return true
			}
		}
		return false
	}
	eq := func(x any) bool {
		if s, ok := x.(string); ok {
			if vs, ok := value.(string); ok {
				return s == vs
			}
			if numOK {
				if f, ok := toNumber(s); ok {
					return f == num
				}
			}
			return false
		}
		if f, ok := toFloat(x); ok {
			return numOK && f == num
		}
		return valuesEqual(x, value)
	}
	ciEq := func(x any) bool {
		s, ok := scalarString(x)
		return ok && strings.EqualFold(s, str)
	}
	containsElem := func(x any, match func(any) bool) bool {
		switch t := x.(type) {
		case []any:
			for _, e := range t {
				if e != nil && match(e) {
					return true
				}
			}
		}
		return false
	}
	switch op {
	case "EQ":
		return eq, nil
	case "NEQ":
		return func(x any) bool { return !eq(x) }, nil
	case "EQ_CASE_INSENSITIVE":
		return ciEq, nil
	case "NEQ_CASE_INSENSITIVE":
		return func(x any) bool { return !ciEq(x) }, nil
	case "GT", "GTE", "LT", "LTE":
		return func(x any) bool {
			f, ok := toNumber(x)
			if !ok || !numOK {
				return false
			}
			switch op {
			case "GT":
				return f > num
			case "GTE":
				return f >= num
			case "LT":
				return f < num
			}
			return f <= num
		}, nil
	case "BETWEEN":
		hi, _ := toNumber(valueTo)
		return func(x any) bool {
			f, ok := toNumber(x)
			return ok && f >= num && f <= hi
		}, nil
	case "IN":
		return inList, nil
	case "NOT_IN":
		return func(x any) bool { return !inList(x) }, nil
	case "IN_CASE_INSENSITIVE", "NOT_IN_CASE_INSENSITIVE":
		neg := op == "NOT_IN_CASE_INSENSITIVE"
		return func(x any) bool {
			s, ok := scalarString(x)
			return ok && lowerSet[strings.ToLower(s)] != neg
		}, nil
	case "IN_NUMERIC", "NOT_IN_NUMERIC":
		neg := op == "NOT_IN_NUMERIC"
		return func(x any) bool {
			f, ok := toNumber(x)
			return ok && numSet[f] != neg
		}, nil
	case "CONTAINS", "NOT_CONTAINS":
		neg := op == "NOT_CONTAINS"
		return func(x any) bool {
			if s, ok := x.(string); ok {
				return strings.Contains(s, str) != neg
			}
			if _, ok := x.([]any); ok {
				return containsElem(x, func(e any) bool { return eq(e) }) != neg
			}
			return false
		}, nil
	case "CONTAINS_CASE_INSENSITIVE", "NOT_CONTAINS_CASE_INSENSITIVE":
		neg := op == "NOT_CONTAINS_CASE_INSENSITIVE"
		return func(x any) bool {
			if s, ok := x.(string); ok {
				return strings.Contains(strings.ToLower(s), lowStr) != neg
			}
			if _, ok := x.([]any); ok {
				return containsElem(x, ciEq) != neg
			}
			return false
		}, nil
	case "CONTAINS_NUMERIC", "NOT_CONTAINS_NUMERIC":
		neg := op == "NOT_CONTAINS_NUMERIC"
		return func(x any) bool {
			if _, ok := x.([]any); !ok {
				return false
			}
			return containsElem(x, func(e any) bool {
				f, ok := toNumber(e)
				return ok && numOK && f == num
			}) != neg
		}, nil
	case "CONTAINS_ANY", "CONTAINS_NONE":
		neg := op == "CONTAINS_NONE"
		return func(x any) bool {
			switch t := x.(type) {
			case []any:
				return containsElem(t, inList) != neg
			case string:
				for s := range strSet {
					if strings.Contains(t, s) {
						return !neg
					}
				}
				return neg
			}
			return false
		}, nil
	case "STARTS_WITH":
		return func(x any) bool { s, ok := x.(string); return ok && strings.HasPrefix(s, str) }, nil
	case "ENDS_WITH":
		return func(x any) bool { s, ok := x.(string); return ok && strings.HasSuffix(s, str) }, nil
	case "STARTS_WITH_CASE_INSENSITIVE":
		return func(x any) bool {
			s, ok := x.(string)
			return ok && len(s) >= len(str) && strings.EqualFold(s[:len(str)], str)
		}, nil
	case "ENDS_WITH_CASE_INSENSITIVE":
		return func(x any) bool {
			s, ok := x.(string)
			return ok && len(s) >= len(str) && strings.EqualFold(s[len(s)-len(str):], str)
		}, nil
	case "MATCHES", "NOT_MATCHES", "MATCHES_CASE_INSENSITIVE", "NOT_MATCHES_CASE_INSENSITIVE":
		pattern := str
		if strings.HasSuffix(op, "CASE_INSENSITIVE") {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid RE2 regular expression: %v", err)
		}
		neg := strings.HasPrefix(op, "NOT_")
		return func(x any) bool {
			s, ok := scalarString(x)
			return ok && re.MatchString(s) != neg
		}, nil
	case "EXISTS":
		return func(any) bool { return true }, nil
	case "NOT_EXISTS":
		return func(any) bool { return false }, nil
	case "IS_TRUE", "IS_FALSE":
		want := op == "IS_TRUE"
		return func(x any) bool {
			switch t := x.(type) {
			case bool:
				return t == want
			case string:
				return strings.EqualFold(t, fmt.Sprint(want))
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("unknown operator %s", op)
}

// ---- segment, gate and variant conditions

func (b *builder) segmentCond(m map[string]any, path string, sc *scope) condFn {
	name, _ := m["segment"].(string)
	op, _ := m["op"].(string)
	if op != "IN_SEGMENT" && op != "NOT_IN_SEGMENT" {
		b.errorf("INVALID_SPEC", path+"/op", "segment conditions use IN_SEGMENT or NOT_IN_SEGMENT")
		return nil
	}
	seg := b.segment(name, path+"/segment")
	if seg == nil {
		return nil
	}
	neg := op == "NOT_IN_SEGMENT"
	var keyProg *program
	if k, ok := m["key"]; ok {
		src, isStr := k.(string)
		if !isStr {
			b.errorf("INVALID_SPEC", path+"/key", "key must be an expression string")
			return nil
		}
		p, _, ok := b.expr(src, path+"/key", sc)
		if !ok {
			return nil
		}
		keyProg = p
	}
	type attrProg struct {
		name string
		p    *program
	}
	var attrs []attrProg
	if a, ok := m["attributes"]; ok {
		am, isMap := a.(map[string]any)
		if !isMap {
			b.errorf("INVALID_SPEC", path+"/attributes", "attributes must be a map of attribute name to expression")
			return nil
		}
		names := make([]string, 0, len(am))
		for n := range am {
			names = append(names, n)
		}
		sort.Strings(names)
		failed := false
		for _, n := range names {
			src, isStr := am[n].(string)
			ap := path + "/attributes/" + ptrEscape(n)
			if !isStr {
				b.errorf("INVALID_SPEC", ap, "attribute expressions must be strings")
				failed = true
				continue
			}
			if seg.attrs != nil {
				if _, known := seg.attrs.Property(n); !known {
					b.errorf("UNKNOWN_FIELD", ap, "attribute %q is not in %s", n, seg.attrsLabel)
					failed = true
					continue
				}
			}
			p, _, ok := b.expr(src, ap, sc)
			if !ok {
				failed = true
				continue
			}
			attrs = append(attrs, attrProg{n, p})
		}
		if failed {
			return nil
		}
		if seg.attrs != nil {
			for _, req := range seg.attrs.Resolve().Required {
				if _, ok := am[req]; !ok {
					b.errorf("MISSING_VALUE", path+"/attributes", "attribute %q is required by %s", req, seg.attrsLabel)
					return nil
				}
			}
		}
	}
	switch seg.typ {
	case "static":
		if keyProg == nil {
			b.errorf("MISSING_VALUE", path+"/key", "static segment %q needs a key expression", name)
			return nil
		}
	case "dynamic":
		if attrs == nil {
			b.errorf("MISSING_VALUE", path+"/attributes", "dynamic segment %q needs attributes matching %s", name, seg.attrsLabel)
			return nil
		}
		if keyProg != nil {
			b.warnf("INVALID_SPEC", path+"/key", "dynamic segments ignore key")
		}
	}
	return func(r *run, in any) (bool, error) {
		var key any
		if keyProg != nil {
			k, err := r.eval(keyProg, in)
			if err != nil {
				return false, err
			}
			key = k
		} else if seg.typ == "rollout" {
			key = r.cc.Unit
		}
		var attrVal any
		if attrs != nil {
			am := make(map[string]any, len(attrs))
			for _, a := range attrs {
				v, err := r.eval(a.p, in)
				if err != nil {
					return false, err
				}
				if v != nil {
					am[a.name] = normalize(v)
				}
			}
			attrVal = am
		}
		member, _, err := seg.member(r, key, attrVal)
		if err != nil {
			return false, err
		}
		return member != neg, nil
	}
}

func (b *builder) gateCond(m map[string]any, path string) condFn {
	name, _ := m["gate"].(string)
	op, _ := m["op"].(string)
	if op != "GATE_PASSES" && op != "GATE_FAILS" {
		b.errorf("INVALID_SPEC", path+"/op", "gate conditions use GATE_PASSES or GATE_FAILS")
		return nil
	}
	if b.gate(name, path+"/gate") == nil {
		return nil
	}
	want := op == "GATE_PASSES"
	return func(r *run, in any) (bool, error) {
		res, err := r.gate(name, in)
		if err != nil {
			return false, err
		}
		return res.Value == want, nil
	}
}

func (b *builder) variantCond(m map[string]any, path string) condFn {
	name, _ := m["experiment"].(string)
	exp := b.experiment(name, path+"/experiment")
	if exp == nil {
		return nil
	}
	list, ok := m["variants"].([]any)
	if !ok || len(list) == 0 {
		b.errorf("INVALID_SPEC", path+"/variants", "variants must be a non-empty list of variant names")
		return nil
	}
	want := map[string]bool{}
	for i, v := range list {
		s, _ := v.(string)
		if exp.byName[s] == nil {
			b.errorf("UNKNOWN_VARIANT", fmt.Sprintf("%s/variants/%d", path, i), "experiment %q has no variant %q", name, s)
			return nil
		}
		want[s] = true
	}
	return func(r *run, in any) (bool, error) {
		res, err := r.variant(name, in)
		if err != nil {
			return false, err
		}
		return res.InExperiment && want[res.Variant], nil
	}
}
