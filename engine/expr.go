// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"
)

// MaxExprBytes is the size limit of one expression.
const MaxExprBytes = 4096

// exprCtx is the `ctx` variable of every expression.
type exprCtx struct {
	Now       time.Time `expr:"now"`
	Unit      string    `expr:"unit"`
	RequestID string    `expr:"requestId"`
	Env       string    `expr:"env"`
	r         *run
}

// program is one compiled expression.
type program struct {
	src string
	p   *vm.Program
}

// Gearbox functions that need per-call state get `$env` appended by a patch.
var statefulFuncs = map[string]bool{"lookup": true, "inSegment": true, "gate": true, "variant": true, "bucket": true}

// gearboxFuncs are the functions Gearbox adds (or replaces) on top of Expr.
var gearboxFuncs = map[string]bool{
	"lookup": true, "inSegment": true, "gate": true, "variant": true, "bucket": true,
	"daysBetween": true, "coalesce": true, "date": true, "keys": true, "values": true, "toPairs": true,
}

var cmpFuncs = map[string]string{"<": "_lt", "<=": "_le", ">": "_gt", ">=": "_ge"}

// patcher makes member access nil-safe, comparisons nil-safe and passes the
// environment to stateful Gearbox functions.
type patcher struct{}

func (patcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.MemberNode:
		if n.Method {
			return
		}
		n.Optional = true
		ast.Patch(node, &ast.ChainNode{Node: n})
	case *ast.BinaryNode:
		if fn, ok := cmpFuncs[n.Operator]; ok {
			ast.Patch(node, &ast.CallNode{Callee: &ast.IdentifierNode{Value: fn}, Arguments: []ast.Node{n.Left, n.Right}})
		}
	case *ast.CallNode:
		if id, ok := n.Callee.(*ast.IdentifierNode); ok && statefulFuncs[id.Value] {
			n.Arguments = append(n.Arguments, &ast.IdentifierNode{Value: "$env"})
		}
	}
}

var exprOptions = buildExprOptions()

func envSample() map[string]any {
	return map[string]any{
		"input":   nil,
		"request": nil,
		"nodes":   map[string]any{},
		"out":     map[string]any{},
		"value":   nil,
		"ctx":     (*exprCtx)(nil),
	}
}

func buildExprOptions() []expr.Option {
	opts := []expr.Option{
		expr.Env(types.Map{
			"input":   types.Any,
			"request": types.Any,
			"nodes":   types.TypeOf(map[string]any{}),
			"out":     types.TypeOf(map[string]any{}),
			"value":   types.Any,
			"ctx":     types.TypeOf((*exprCtx)(nil)),
		}),
		expr.DisableBuiltin("now"),
		expr.Function("date", fnDate),
		expr.Patch(patcher{}),
		expr.Function("_lt", cmpFn("<")),
		expr.Function("_le", cmpFn("<=")),
		expr.Function("_gt", cmpFn(">")),
		expr.Function("_ge", cmpFn(">=")),
		expr.Function("keys", fnKeys),
		expr.Function("values", fnValues),
		expr.Function("toPairs", fnToPairs),
		expr.Function("coalesce", fnCoalesce),
		expr.Function("daysBetween", fnDaysBetween),
		expr.Function("lookup", fnLookup),
		expr.Function("inSegment", fnInSegment),
		expr.Function("gate", fnGate),
		expr.Function("variant", fnVariant),
		expr.Function("bucket", fnBucket),
	}
	return opts
}

func compileProgram(src string) (*program, error) {
	if len(src) > MaxExprBytes {
		return nil, fmt.Errorf("expression is longer than %d bytes", MaxExprBytes)
	}
	p, err := expr.Compile(src, exprOptions...)
	if err != nil {
		return nil, err
	}
	return &program{src: src, p: p}, nil
}

// ---- runtime functions

func runOf(params []any) (*run, []any, error) {
	if len(params) == 0 {
		return nil, nil, errors.New("internal: missing environment")
	}
	env, ok := params[len(params)-1].(map[string]any)
	if !ok {
		return nil, nil, errors.New("internal: bad environment")
	}
	c, _ := env["ctx"].(*exprCtx)
	if c == nil || c.r == nil {
		return nil, nil, errors.New("function is not available here")
	}
	return c.r, params[:len(params)-1], nil
}

func nameArg(fn string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: first argument must be a name string", fn)
	}
	return s, nil
}

func fnLookup(params ...any) (any, error) {
	r, args, err := runOf(params)
	if err != nil {
		return nil, err
	}
	if len(args) < 2 || len(args) > 3 {
		return nil, errors.New("lookup(table, key[, default]) takes 2 or 3 arguments")
	}
	name, err := nameArg("lookup", args[0])
	if err != nil {
		return nil, err
	}
	l := r.c.lookups[name]
	if l == nil {
		return nil, fmt.Errorf("lookup %q is not in the bundle", name)
	}
	key, ok := scalarString(args[1])
	if ok {
		if v, found := l.rows[key]; found {
			return v, nil
		}
	}
	if len(args) == 3 {
		return args[2], nil
	}
	return nil, nil
}

func fnInSegment(params ...any) (any, error) {
	r, args, err := runOf(params)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, errors.New("inSegment(segment, key) takes 2 arguments")
	}
	name, err := nameArg("inSegment", args[0])
	if err != nil {
		return nil, err
	}
	seg := r.c.segments[name]
	if seg == nil {
		return nil, fmt.Errorf("segment %q is not in the bundle", name)
	}
	m, _, err := seg.member(r, args[1], args[1])
	return m, err
}

func fnGate(params ...any) (any, error) {
	r, args, err := runOf(params)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, errors.New("gate(name) takes 1 argument")
	}
	name, err := nameArg("gate", args[0])
	if err != nil {
		return nil, err
	}
	env := params[len(params)-1].(map[string]any)
	res, err := r.gate(name, env["input"])
	if err != nil {
		return nil, err
	}
	return res.Value, nil
}

func fnVariant(params ...any) (any, error) {
	r, args, err := runOf(params)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, errors.New("variant(experiment) takes 1 argument")
	}
	name, err := nameArg("variant", args[0])
	if err != nil {
		return nil, err
	}
	env := params[len(params)-1].(map[string]any)
	res, err := r.variant(name, env["input"])
	if err != nil {
		return nil, err
	}
	return res.Variant, nil
}

func fnBucket(params ...any) (any, error) {
	r, args, err := runOf(params)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, errors.New("bucket(salt) takes 1 argument")
	}
	salt, ok := args[0].(string)
	if !ok {
		return nil, errors.New("bucket: salt must be a string")
	}
	if r.cc.Unit == "" {
		return nil, nil
	}
	return r.c.bucket(salt, r.cc.Unit), nil
}

func fnCoalesce(params ...any) (any, error) {
	for _, p := range params {
		if p != nil {
			return p, nil
		}
	}
	return nil, nil
}

func fnDaysBetween(params ...any) (any, error) {
	if len(params) != 2 {
		return nil, errors.New("daysBetween(a, b) takes 2 arguments")
	}
	if params[0] == nil || params[1] == nil {
		return nil, nil
	}
	a, ok1 := timeOf(params[0])
	b, ok2 := timeOf(params[1])
	if !ok1 || !ok2 {
		return nil, errors.New("daysBetween: arguments must be times or RFC 3339 dates")
	}
	return int(math.Floor(b.Sub(a).Hours() / 24)), nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mapArg(fn string, params []any) (map[string]any, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("%s(map) takes 1 argument", fn)
	}
	if params[0] == nil {
		return nil, nil
	}
	m, ok := params[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: argument must be a map", fn)
	}
	return m, nil
}

func fnKeys(params ...any) (any, error) {
	m, err := mapArg("keys", params)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, k)
	}
	return out, nil
}

func fnValues(params ...any) (any, error) {
	m, err := mapArg("values", params)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out, nil
}

func fnToPairs(params ...any) (any, error) {
	m, err := mapArg("toPairs", params)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, []any{k, m[k]})
	}
	return out, nil
}

func cmpFn(op string) func(params ...any) (any, error) {
	return func(params ...any) (any, error) {
		a, b := params[0], params[1]
		if a == nil || b == nil {
			return false, nil
		}
		c, err := compareValues(a, b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		switch op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	}
}

func compareValues(a, b any) (int, error) {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			switch {
			case fa < fb:
				return -1, nil
			case fa > fb:
				return 1, nil
			}
			return 0, nil
		}
	}
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), nil
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y), nil
		}
	case time.Duration:
		if y, ok := b.(time.Duration); ok {
			switch {
			case x < y:
				return -1, nil
			case x > y:
				return 1, nil
			}
			return 0, nil
		}
	}
	return 0, fmt.Errorf("cannot compare %s with %s", kindName(a), kindName(b))
}

func kindName(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case bool:
		return "bool"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "map"
	case time.Time:
		return "time"
	case time.Duration:
		return "duration"
	}
	if _, ok := toFloat(v); ok {
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

// knownFunction reports whether name can be called in a Gearbox expression.
func knownFunction(name string) bool {
	if name == "now" {
		return false
	}
	if gearboxFuncs[name] {
		return true
	}
	_, ok := builtin.Index[name]
	return ok
}

var dateLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850, time.ANSIC, time.UnixDate,
}

// fnDate replaces Expr's date(): strings are parsed in UTC unless they carry
// an offset, or a third argument names a time zone. date(s, layout[, tz]).
func fnDate(params ...any) (any, error) {
	if len(params) == 0 || len(params) > 3 {
		return nil, errors.New("date(value[, layout[, timezone]]) takes 1 to 3 arguments")
	}
	if params[0] == nil {
		return nil, nil
	}
	if t, ok := params[0].(time.Time); ok {
		return t, nil
	}
	s, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("date: cannot parse %s", kindName(params[0]))
	}
	loc := time.UTC
	if len(params) == 3 {
		name, _ := params[2].(string)
		l, err := time.LoadLocation(name)
		if err != nil {
			return nil, fmt.Errorf("date: unknown time zone %q", name)
		}
		loc = l
	}
	if len(params) >= 2 {
		layout, _ := params[1].(string)
		return time.ParseInLocation(layout, s, loc)
	}
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return nil, fmt.Errorf("date: cannot parse %q", s)
}
