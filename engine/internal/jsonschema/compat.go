// SPDX-License-Identifier: MIT

package jsonschema

import (
	"sort"
)

// Change is one difference between two schema versions.
type Change struct {
	Path     string
	Kind     string
	Breaking bool
}

// Change kinds.
const (
	PropertyAdded      = "property_added"
	PropertyRemoved    = "property_removed"
	BecameRequired     = "became_required"
	BecameOptional     = "became_optional"
	TypeNarrowed       = "type_narrowed"
	TypeWidened        = "type_widened"
	NullRemoved        = "null_removed"
	NullAdded          = "null_added"
	EnumNarrowed       = "enum_narrowed"
	EnumWidened        = "enum_widened"
	ConstraintNarrowed = "constraint_narrowed"
	ConstraintWidened  = "constraint_widened"
	ClosedObject       = "additional_properties_removed"
	PIIChanged         = "pii_changed"
)

// Compare lists the changes from prev to next. A change is breaking when a
// document valid under prev (or a decision written against prev) may no
// longer work with next: removed property, newly required property, narrowed
// type or enum, removed null, tighter bounds, closed object.
func Compare(prev, next *Schema) []Change {
	c := &comparer{seen: map[[2]*Schema]bool{}}
	c.cmp(prev, next, "", 0)
	sort.SliceStable(c.out, func(i, j int) bool { return c.out[i].Path < c.out[j].Path })
	return c.out
}

type comparer struct {
	out  []Change
	seen map[[2]*Schema]bool
}

func (c *comparer) add(path, kind string, breaking bool) {
	if path == "" {
		path = "/"
	}
	c.out = append(c.out, Change{Path: path, Kind: kind, Breaking: breaking})
}

func (c *comparer) cmp(a, b *Schema, path string, depth int) {
	a, b = a.Resolve(), b.Resolve()
	if a == nil || b == nil || depth > MaxDepth {
		return
	}
	if c.seen[[2]*Schema{a, b}] {
		return
	}
	c.seen[[2]*Schema{a, b}] = true

	ta, tb := a.EffectiveTypes(), b.EffectiveTypes()
	if ta != tb {
		na, nb := ta&TNull != 0 || ta == 0, tb&TNull != 0 || tb == 0
		if na && !nb {
			c.add(path, NullRemoved, true)
		} else if !na && nb {
			c.add(path, NullAdded, false)
		}
		nonNullA, nonNullB := ta&^TNull, tb&^TNull
		if ta == 0 {
			nonNullA = 0
		}
		if tb == 0 {
			nonNullB = 0
		}
		if nonNullA != nonNullB {
			switch {
			case nonNullB == 0 || (nonNullA != 0 && nonNullB.Has(nonNullA)):
				c.add(path, TypeWidened, false)
			default:
				c.add(path, TypeNarrowed, true)
			}
		}
	}
	if a.Enum != nil || b.Enum != nil {
		switch {
		case b.Enum == nil:
			c.add(path, EnumWidened, false)
		case a.Enum == nil:
			c.add(path, EnumNarrowed, true)
		default:
			removed, added := false, false
			for _, x := range a.Enum {
				if !contains(b.Enum, x) {
					removed = true
				}
			}
			for _, x := range b.Enum {
				if !contains(a.Enum, x) {
					added = true
				}
			}
			if removed {
				c.add(path, EnumNarrowed, true)
			}
			if added {
				c.add(path, EnumWidened, false)
			}
		}
	}
	if a.HasConst != b.HasConst || (a.HasConst && !Equal(a.Const, b.Const)) {
		c.add(path, ConstraintNarrowed, b.HasConst)
	}
	c.bound(path, a.Minimum, b.Minimum, true)
	c.bound(path, a.ExMinimum, b.ExMinimum, true)
	c.bound(path, a.Maximum, b.Maximum, false)
	c.bound(path, a.ExMaximum, b.ExMaximum, false)
	c.ibound(path, a.MinLength, b.MinLength, true)
	c.ibound(path, a.MaxLength, b.MaxLength, false)
	c.ibound(path, a.MinItems, b.MinItems, true)
	c.ibound(path, a.MaxItems, b.MaxItems, false)
	if (a.Pattern == nil) != (b.Pattern == nil) || (a.Pattern != nil && a.Pattern.String() != b.Pattern.String()) {
		c.add(path, ConstraintNarrowed, b.Pattern != nil)
	}
	if a.Format != b.Format {
		c.add(path, ConstraintNarrowed, b.Format != "")
	}
	if a.IsPII() != b.IsPII() {
		c.add(path, PIIChanged, false)
	}
	if !a.AddlFalse && b.AddlFalse {
		c.add(path, ClosedObject, true)
	}

	// Properties.
	names := map[string]bool{}
	for n := range a.Properties {
		names[n] = true
	}
	for n := range b.Properties {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		p := path + "/" + escapePtr(n)
		pa, inA := a.Properties[n]
		pb, inB := b.Properties[n]
		switch {
		case inA && !inB:
			c.add(p, PropertyRemoved, true)
		case !inA && inB:
			c.add(p, PropertyAdded, b.IsRequired(n))
			if b.IsRequired(n) {
				c.add(p, BecameRequired, true)
			}
		default:
			ra, rb := a.IsRequired(n), b.IsRequired(n)
			if !ra && rb {
				c.add(p, BecameRequired, true)
			} else if ra && !rb {
				c.add(p, BecameOptional, false)
			}
			c.cmp(pa, pb, p, depth+1)
		}
	}
	// Required names without a property declaration.
	for _, r := range b.Required {
		if _, declared := b.Properties[r]; declared {
			continue
		}
		if !a.IsRequired(r) {
			c.add(path+"/"+escapePtr(r), BecameRequired, true)
		}
	}
	if a.Items != nil || b.Items != nil {
		c.cmp(a.Items, b.Items, path+"/items", depth+1)
	}
	if a.Addl != nil && b.Addl != nil {
		c.cmp(a.Addl, b.Addl, path+"/additionalProperties", depth+1)
	}
}

func (c *comparer) bound(path string, a, b *float64, lower bool) {
	switch {
	case a == nil && b == nil:
	case a == nil:
		c.add(path, ConstraintNarrowed, true)
	case b == nil:
		c.add(path, ConstraintWidened, false)
	case *a == *b:
	case lower == (*b > *a):
		c.add(path, ConstraintNarrowed, true)
	default:
		c.add(path, ConstraintWidened, false)
	}
}

func (c *comparer) ibound(path string, a, b *int, lower bool) {
	var fa, fb *float64
	if a != nil {
		f := float64(*a)
		fa = &f
	}
	if b != nil {
		f := float64(*b)
		fb = &f
	}
	c.bound(path, fa, fb, lower)
}

func contains(list []any, v any) bool {
	for _, x := range list {
		if Equal(x, v) {
			return true
		}
	}
	return false
}

// Accepts reports whether every document the producer schema allows is
// (structurally) acceptable to the consumer schema: the consumer's required
// properties are required by the producer, types of shared properties are
// compatible, and a closed consumer object does not receive unknown
// properties. It returns the JSON pointer of the first mismatch.
func Accepts(consumer, producer *Schema) (ok bool, where, why string) {
	return accepts(consumer, producer, "", 0, map[[2]*Schema]bool{})
}

func accepts(c, p *Schema, path string, depth int, seen map[[2]*Schema]bool) (bool, string, string) {
	c, p = c.Resolve(), p.Resolve()
	if c == nil || p == nil || depth > MaxDepth || seen[[2]*Schema{c, p}] {
		return true, "", ""
	}
	seen[[2]*Schema{c, p}] = true
	tc, tp := c.EffectiveTypes(), p.EffectiveTypes()
	if tc != 0 {
		if tp == 0 {
			return false, path, "producer type is unconstrained, consumer expects " + tc.String()
		}
		if !tc.Has(tp) {
			return false, path, "producer type " + tp.String() + " is not accepted by consumer type " + tc.String()
		}
	}
	if c.Enum != nil && p.Enum == nil && !p.HasConst {
		return false, path, "consumer restricts values with enum; producer does not"
	}
	if c.Enum != nil && p.Enum != nil {
		for _, x := range p.Enum {
			if !contains(c.Enum, x) {
				return false, path, "producer enum value " + compactJSON(x) + " is not accepted"
			}
		}
	}
	for _, r := range c.Required {
		if !p.IsRequired(r) {
			return false, path + "/" + escapePtr(r), "required by the consumer but optional or absent in the producer"
		}
	}
	names := make([]string, 0, len(c.Properties))
	for n := range c.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		pp, ok := p.Properties[n]
		if !ok {
			continue
		}
		if ok, w, why := accepts(c.Properties[n], pp, path+"/"+escapePtr(n), depth+1, seen); !ok {
			return false, w, why
		}
	}
	if c.AddlFalse {
		for _, n := range p.PropOrder {
			if _, ok := c.Properties[n]; !ok {
				return false, path + "/" + escapePtr(n), "producer property is not allowed by the consumer"
			}
		}
	}
	if c.Items != nil && p.Items != nil {
		if ok, w, why := accepts(c.Items, p.Items, path+"/items", depth+1, seen); !ok {
			return false, w, why
		}
	}
	return true, "", ""
}
