# 07 · Decision graphs

A **decision** is a small directed graph of nodes. A rule set, a transform or a table on its own is just a one-node decision. Graphs add branching (decision trees), reuse and AI steps.

## 1. Definition format

Decisions are stored as JSON in the control plane; the same structure is written in YAML for offline mode and code review.

```yaml
apiVersion: gearbox/v1
kind: Decision
metadata:
  name: lead_routing
  app: lead-service
  description: Route a used-car lead to fast track, standard or review
  owners: [team-leads]
spec:
  input:
    schema: { $ref: schemas/lead.json }       # JSON Schema; used for validation and type checking
  output:
    schema: { $ref: schemas/route.json }
  fallback: { route: manual_review }          # returned on node errors when no edge handles them
  logging: { sample: 1.0 }                    # audit decision: log every call
  nodes:
    - id: normalize
      type: transform
      transform: { $ref: transforms/normalize.yaml }

    - id: eligibility
      type: rules
      rules: { $ref: rules/eligibility.yaml }

    - id: route
      type: switch
      cases:
        - when: nodes.eligibility.eligible
          next: price_band
      default: reject

    - id: price_band
      type: table
      table: { $ref: tables/price_band.yaml }
      next: priority

    - id: priority
      type: ai
      ai: { $ref: ai/priority.yaml }
      onError: default_priority                # timeout, Hub error, invalid answer
      next: output

    - id: default_priority
      type: rules
      rules: { $ref: rules/default_priority.yaml }
      next: output

    - id: reject
      type: transform
      transform:
        fields:
          route: '"reject"'
          reason: nodes.eligibility.reason
      next: end

    - id: output
      type: output
      fields:
        route: nodes.priority?.route ?? nodes.default_priority.route
        band: nodes.price_band.band
  tests:                                      # ctx fixed so tests never depend on the clock or a random unit
    - { name: fast track, ctx: { now: "2026-10-30T10:00:00Z", unit: L-1 }, input: { $ref: tests/fast.json }, expect: { route: fast_track } }
    - { name: too old,    ctx: { now: "2026-10-30T10:00:00Z", unit: L-2 }, input: { $ref: tests/old.json },  expect: { route: reject } }
```

### Flow rules

- Nodes run in list order unless `next` says otherwise; `next: end` stops and returns the current node's output.
- `switch` picks the first case whose `when` is true, else `default`.
- `onError` (any node) routes errors to another node; without it the decision's `fallback` is returned and `fallback_used=true`.
- Publish rejects: cycles, unreachable nodes, a path without an `output`/`end`, references to nodes that may not have run (checked per path), unknown functions, type errors.

## 2. Node types

| Type | Config | Output (`nodes.<id>`) |
|---|---|---|
| `rules` | [rule set](05-rules-and-expressions.md) | matched rule's `then` (or `matches[]` in all-matches mode) |
| `table` | decision table (below) | matched row's outputs |
| `transform` | [field mapping](06-transforms.md) | built object |
| `switch` | cases → next | `{case: "<id or default>"}` |
| `lookup` | `{table, key, default}` | looked-up value |
| `gate` | `{gate: name}` | `{value: bool, rule: id}` |
| `experiment` | `{experiment: name}` | `{variant, parameters, in_experiment}` |
| `subdecision` | `{decision: name, input: expr}` | the other decision's output (cycles rejected) |
| `ai` | [AI node](08-ai-node.md) | the model's validated JSON answer |
| `output` | `fields` like a transform | becomes the response |

## 3. Decision tables

Spreadsheet-style rules: input columns hold conditions, output columns hold values.

```yaml
inputs:
  - { name: price, expr: input.car.price }
  - { name: age,   expr: ctx.now.Year() - input.car.year }
  - { name: city,  expr: input.lead.city }
outputs: [band, max_discount]
hitPolicy: first            # first | unique | collect | priority
rows:
  - { id: row_1, when: { price: "< 300000", age: "<= 5", city: "-" },              then: { band: C, max_discount: 5000 } }
  - { id: row_2, when: { price: "[300000..800000]", age: "<= 8", city: '["Gurgaon","Delhi"]' }, then: { band: A, max_discount: 15000 } }
  - { id: row_3, when: { price: "[300000..800000]", age: "-", city: "-" },         then: { band: B, max_discount: 10000 } }
  - { id: row_4, when: { price: "-", age: "-", city: "-" },                        then: { band: D, max_discount: 0 } }
```

Cell syntax: `-` any; `< 300000`, `>= 5`; `[a..b]` inclusive range; `"text"`; a list means "one of"; anything else is a full Expr expression where `value` is the column value (`value % 2 == 0`). Import and export as CSV or XLSX from the UI.

Hit policies: `first` returns the first matching row; `unique` fails publish if rows can overlap (checked on test cases and simple ranges); `collect` returns all matches; `priority` uses a `priority` column.

## 4. Lookup tables and segments

Versioned key → value tables shipped in the bundle (city → zone, pincode → serviceability, SLA hours). Upload CSV in the UI or with `gearboxctl`; use with a `lookup` node or `lookup()` in any expression. Size counts toward the bundle limit. The rows live in the object store; MongoDB keeps only the version record and its hash.

**Data releases.** Segments and lookups change more often than logic. An app can allow *data releases*: a new version of a segment or lookup that passes every test case of every decision using it is published automatically (or by an API call from a pipeline), without a change request. Logic changes always need review. Each data release is still a new generation and appears in audit.

## 5. Gates, experiments, layers and holdouts

Kept from Switchboard with the same hashing (murmur3, salt per resource) so existing assignments do not move:

- **Gate**: ordered rules → boolean, kill switch, percentage rollout.
- **Experiment**: variants with weights and parameters, targeting rules, optional holdouts, lifecycle (draft → running → stopped).
- **Layer**: mutual exclusion between experiments sharing traffic.
- **Holdout**: units kept out of selected experiments.

Call them directly (`CheckGate`, `GetVariant`) or inside a decision (`gate` / `experiment` nodes, `gate()` / `variant()` functions).

**Shared across apps.** A gate or experiment can be used by several apps (for example the web backend and the lead service). The control plane records every app whose bundle contains it, and a release that changes it publishes all of those apps in the same release, so services never disagree on an assignment for longer than one sync.

**Exposure log.** Every `GetVariant` call and every `experiment` node writes an exposure record (unit, experiment, version, variant, reason) that is **never sampled**, separately from the sampled decision log. Experiment analysis reads only this log.

## 6. Versions, releases and rollouts

- Every saved change creates an immutable version (`lead_routing@v18`).
- A **release** publishes one or more versions to an environment, after approval where required.
- **Gradual rollout**: a release can send `p%` of units (by `bucket(release salt)`) to the new version and the rest to the previous one. Both versions are in the bundle, so every sidecar gives the same unit the same answer. Rollback is a new generation that points back to the old version.
- **Promotion**: a version tested in `stage` is promoted to `prod` as-is (same content hash).
