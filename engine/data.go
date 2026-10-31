// SPDX-License-Identifier: MIT

package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ORG/gearbox/engine/internal/jsonschema"
)

// dataSlot is a compiled data slot: literal JSON where a string starting with
// "=" is an expression and "==" escapes a literal "=".
type dataSlot struct {
	constant bool
	val      any             // constant value
	raw      json.RawMessage // constant value encoded
	build    func(r *run, in any) (any, error)
	taint    *taint
	present  bool
}

func (d *dataSlot) eval(r *run, in any) (any, error) {
	if d.constant {
		return d.val, nil
	}
	return d.build(r, in)
}

// data compiles a data slot. It returns nil (with problems) on error and an
// absent slot (present=false) when raw is empty.
func (b *builder) data(raw json.RawMessage, path string, sc *scope) *dataSlot {
	if len(raw) == 0 {
		return &dataSlot{constant: true}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		b.errorf("INVALID_SPEC", path, "data slot is not valid JSON")
		return nil
	}
	return b.dataValue(v, path, sc)
}

func (b *builder) dataValue(v any, path string, sc *scope) *dataSlot {
	ok := true
	t := &taint{}
	build := b.dataNode(v, path, sc, nil, t, &ok)
	if !ok {
		return nil
	}
	if t.empty() {
		t = nil
	}
	if build == nil {
		c := unescapeData(v)
		return &dataSlot{constant: true, val: c, raw: encodeJSON(c), present: true}
	}
	return &dataSlot{build: build, taint: t, present: true}
}

// dataNode returns nil when the subtree is constant.
func (b *builder) dataNode(v any, path string, sc *scope, tpath []string, t *taint, ok *bool) func(r *run, in any) (any, error) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "=") && !strings.HasPrefix(x, "==") {
			p, info, good := b.expr(x[1:], path, sc)
			if !good {
				*ok = false
				return nil
			}
			if info.tainted {
				t.add(tpath)
			}
			return func(r *run, in any) (any, error) {
				out, err := r.eval(p, in)
				if err != nil {
					return nil, err
				}
				return normalize(out), nil
			}
		}
		return nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		type entry struct {
			k string
			f func(r *run, in any) (any, error)
			c any
		}
		var entries []entry
		dynamic := false
		for _, k := range keys {
			f := b.dataNode(x[k], path+"/"+ptrEscape(k), sc, append(append([]string(nil), tpath...), k), t, ok)
			if f != nil {
				dynamic = true
			}
			entries = append(entries, entry{k: k, f: f, c: unescapeData(x[k])})
		}
		if !dynamic {
			return nil
		}
		return func(r *run, in any) (any, error) {
			out := make(map[string]any, len(entries))
			for _, e := range entries {
				if e.f == nil {
					out[e.k] = e.c
					continue
				}
				v, err := e.f(r, in)
				if err != nil {
					return nil, err
				}
				out[e.k] = v
			}
			return out, nil
		}
	case []any:
		fns := make([]func(r *run, in any) (any, error), len(x))
		consts := make([]any, len(x))
		dynamic := false
		for i, e := range x {
			fns[i] = b.dataNode(e, fmt.Sprintf("%s/%d", path, i), sc, append(append([]string(nil), tpath...), "[]"), t, ok)
			consts[i] = unescapeData(e)
			if fns[i] != nil {
				dynamic = true
			}
		}
		if !dynamic {
			return nil
		}
		return func(r *run, in any) (any, error) {
			out := make([]any, len(x))
			for i := range x {
				if fns[i] == nil {
					out[i] = consts[i]
					continue
				}
				v, err := fns[i](r, in)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	}
	return nil
}

// unescapeData turns "==x" into "=x" throughout a constant value.
func unescapeData(v any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "==") {
			return x[1:]
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = unescapeData(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = unescapeData(e)
		}
		return out
	}
	return v
}

// checkConstAgainst validates a constant data slot against a schema.
func (b *builder) checkConstAgainst(d *dataSlot, s *jsonschema.Schema, label, path string) {
	if d == nil || !d.constant || !d.present || s == nil {
		return
	}
	for _, fe := range s.Validate(d.val) {
		b.errorf("TYPE_MISMATCH", path+fe.Path, "does not match %s: %s", label, fe.Message)
	}
}
