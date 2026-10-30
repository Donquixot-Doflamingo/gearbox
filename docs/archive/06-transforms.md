# 06 · Transforms

A transform node turns one JSON object into another. It replaces Jolt: each output field is written as `target.path: expression`, so moving fields and computing them happen in one place, and every line is type-checked at publish.

## 1. Shape

```yaml
- id: normalize
  type: transform
  transform:
    mode: build                 # build: start from {}   · patch: start from a copy of `source`
    source: input               # what `patch` copies (default: input)
    fields:
      leadId:             input.lead.id
      customer.name:      input.lead.first_name + " " + input.lead.last_name
      customer.phone:     trimPrefix(input.lead.phone, "+91-")
      customer.city:      upper(input.lead.city)
      vehicle.title:      input.car.make + " " + input.car.model
      vehicle.ageYears:   ctx.now.Year() - input.car.year
      vehicle.priceLakh:  input.car.price / 100000
      inspection.avg:     mean(map(input.inspections, .score))
      inspection.weak:    map(filter(input.inspections, .score < 6), .part)
      inspection.items:   map(input.inspections, ({"part": .part, "ok": .score >= 6}))
    defaults:                   # data, not code: literal values (see 05 §0)
      customer.country:   "IN"
    drop: []                    # patch mode: paths to remove, e.g. ["lead.internal_notes", "car.vin"]
    omitNil: true               # leave out fields whose expression is nil
```

Output for the sample lead:

```json
{
  "leadId": "L-1029384",
  "customer": { "name": "Rahul Sharma", "phone": "9876543210", "city": "GURGAON", "country": "IN" },
  "vehicle": { "title": "Maruti Swift VXI", "ageYears": 7, "priceLakh": 5.25 },
  "inspection": { "avg": 7.1, "weak": ["body", "ac"], "items": [ { "part": "engine", "ok": true }, … ] }
}
```

## 2. Rules

- Target paths use dots for nesting; `items[0].name` and `items[].name` (append) are allowed.
- Fields are assigned in file order; a later field may read an earlier one through `out`: `customer.greeting: "Hi " + out.customer.name`.
- `fields` values are expressions (code); `defaults` values are literal JSON (data), with `=` for an expression.
- `defaults` apply only to paths still missing after `fields`.
- `drop` applies last.
- The whole node compiles to one program; measured ≈ 16 µs for this 12-field example.
- Fields that copy or derive from an input field marked `x-gearbox-pii` stay marked in the output (see [08](08-ai-node.md) §4), so a transform cannot launder PII into logs or prompts.
- A transform can also be a single expression: `expression: '{"name": input.lead.first_name, ...}'` for small shapes.

## 3. Jolt to Gearbox

| Jolt operation | Gearbox |
|---|---|
| `shift` (move/rename) | `target.path: input.source.path` |
| `default` | `defaults:` |
| `remove` | `drop:` in `patch` mode |
| `modify-overwrite-beta` functions (`concat`, `toUpper`, `divide`…) | ordinary expressions: `+`, `upper()`, `/` |
| `cardinality` | `first(list)` or `[value]` |
| wildcard array shifts (`items[*].id → ids[]`) | `ids: map(input.items, .id)` |
| `sort` | `sort()` / `sortBy()` |

## 4. Testing a transform

Every decision ships test cases (input + expected output). The UI's mapper shows the live output for the selected test case as you type; publish fails if any case fails.

```yaml
tests:
  - name: certified swift in Gurgaon
    ctx: { now: "2026-10-30T10:00:00Z", unit: L-1029384 }   # fixed so the test never depends on the clock
    input: { $ref: tests/lead-1029384.json }
    expect:
      customer.name: "Rahul Sharma"
      inspection.weak: ["body", "ac"]
```
