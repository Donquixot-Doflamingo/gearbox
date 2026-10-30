# 05 · Rules and expressions

Gearbox expressions use **Expr** (`github.com/expr-lang/expr`): a small, typed, side-effect-free language. Every expression is compiled once when the bundle loads; a request only runs bytecode.

## 0. Data slots and code slots

Every place in a definition is either **data** or **code**, and the rule is the same everywhere:

| Kind | Where | How a value is read |
|---|---|---|
| Code | `when`, switch `cases[].when`, transform `fields`, `output.fields`, `lookup.key`, `subdecision.input`, table `inputs[].expr` | Always an Expr expression. A literal string needs quotes: `'"reject"'` |
| Data | rule `then`, rule-set `default`, decision `fallback`, transform `defaults`, experiment parameters, test `expect` | Literal JSON. A string starting with `=` is an expression: `"=input.lead.id"`. Use `"=="` to write a literal string that starts with `=` |

The editors show code slots in a code font with autocomplete and data slots as JSON, so authors do not have to remember this.

## 1. Rule sets

A rule set is an ordered list of rules. Each rule has a condition (`when`, code) and an output (`then`, data).

```yaml
- id: eligibility
  type: rules
  rules:
    mode: first_match          # or all_matches
    rules:
      - id: r1
        when: inSegment("blocked_cities", input.lead.city)
        then: { eligible: false, reason: city_not_served }
      - id: r2
        when: ctx.now.Year() - input.car.year > 12
        then: { eligible: false, reason: car_too_old }
      - id: r4
        rollout: { percent: 25, salt: eligibility.r4 }
        when:                  # structured condition, built by the UI
          all:
            - { field: input.car.price, op: BETWEEN, value: 300000, valueTo: 2500000 }
            - { field: input.lead.source, op: IN, value: [app, web] }
            - { field: nodes.normalize.inspection.avg, op: GTE, value: 7 }
        then: { eligible: true, lane: fast, reason: "=\"fast lane, avg \" + string(nodes.normalize.inspection.avg)" }
    default: { eligible: true, lane: standard, reason: default }
```

- Rules run in list order; optional `priority` (higher first) reorders them, equal priorities keep list order.
- `first_match` returns the first matching rule's `then`; `all_matches` returns every match in order (`output.matches[]`).
- Disabled rules (`enabled: false`) are skipped.
- `rollout: { percent: 25, salt: "eligibility.r4" }` limits a rule to a stable 25% of units (`bucket(salt) < 2500`, murmur3, same as Switchboard). Calls without `unit_id` never match a rule with a rollout.

### Structured conditions (all Switchboard operators)

The UI builds conditions as `{field, op, value}`; they compile to Expr. Groups nest with `all` (AND), `any` (OR) and `not`. The 35 Switchboard operators are kept with the same meaning, and four are new (segment and gate).

| Operator | Meaning | Value |
|---|---|---|
| `EQ`, `NEQ` | equal / not equal | scalar |
| `EQ_CASE_INSENSITIVE`, `NEQ_CASE_INSENSITIVE` | string equality ignoring case | string |
| `GT`, `GTE`, `LT`, `LTE` | numeric comparison (numeric strings are parsed) | number |
| `BETWEEN` | `value ≤ x ≤ valueTo` | number, `valueTo` |
| `IN`, `NOT_IN` | in list | list |
| `IN_CASE_INSENSITIVE`, `NOT_IN_CASE_INSENSITIVE` | in list, ignoring case | list of strings |
| `IN_NUMERIC`, `NOT_IN_NUMERIC` | numeric membership | list of numbers |
| `CONTAINS`, `NOT_CONTAINS` | substring, or element of a list | scalar |
| `CONTAINS_CASE_INSENSITIVE`, `NOT_CONTAINS_CASE_INSENSITIVE` | same, ignoring case | string |
| `CONTAINS_NUMERIC`, `NOT_CONTAINS_NUMERIC` | numeric element of a list | number |
| `CONTAINS_ANY`, `CONTAINS_NONE` | list shares any / no element with value | list |
| `STARTS_WITH`, `ENDS_WITH` (+ `_CASE_INSENSITIVE`) | prefix / suffix | string |
| `MATCHES`, `NOT_MATCHES` (+ `_CASE_INSENSITIVE`) | RE2 regular expression (compiled at publish) | string |
| `EXISTS`, `NOT_EXISTS` | field present / absent | – |
| `IS_TRUE`, `IS_FALSE` | boolean, or `"true"`/`"false"` strings | – |
| `IN_SEGMENT`, `NOT_IN_SEGMENT` *(new)* | field value is a member of a static segment | segment name |
| `GATE_PASSES`, `GATE_FAILS` *(new)* | another gate's result for this unit | gate name |

Switchboard's condition types map as: FIELD → the operators above; SEGMENT → `IN_SEGMENT`; FEATURE_GATE → `GATE_PASSES`; DEPENDENCY (another config's answer) → a `subdecision` node earlier in the graph, then a condition on `nodes.<id>`.

Regular expressions use Go's RE2 syntax (no backreferences or lookarounds); patterns are validated at publish.

## 2. Missing fields and nil

The same rule applies to structured conditions and to free-form Expr, because both go through one compiler:

- **Paths never fail on a missing parent.** Gearbox compiles every `a.b.c` as `a?.b?.c`, so `input.missing.year` is `nil` instead of an error.
- **Comparisons with `nil` are `false`.** `<`, `<=`, `>`, `>=` and every positive operator return `false` when either side is `nil`; `==` compares normally (`nil == nil` is true).
- **Negative operators are also `false` on a missing field** (`NEQ`, `NOT_IN`, `NOT_CONTAINS`, `NOT_MATCHES`). `NOT_EXISTS` is `true`. This keeps "missing" from accidentally matching; use `NOT_EXISTS` when you mean it.
- **Arithmetic with `nil` is an error** (`nil + 1`), which takes the node's `onError` path. Use `?? 0` where a default makes sense.

This is implemented as a compile-time patch over the Expr AST (optional member access plus nil-safe comparison functions) and covered by the parity suite.

## 3. Expression environment

| Name | Type | Contents |
|---|---|---|
| `input` | map | The request's input document (types preserved: numbers, strings, booleans, lists, objects) |
| `nodes` | map | Outputs of nodes already executed in this decision, by node ID: `nodes.normalize.customer.name` |
| `out` | map | Transform nodes only: fields already assigned in this node |
| `ctx.now` | time | `DecideOptions.now` if the caller set it, otherwise the sidecar clock at call start; echoed as `evaluated_at` |
| `ctx.unit` | string | `unit_id` from the request (empty if not sent) |
| `ctx.requestId` | string | Request ID |
| `ctx.env` | string | Environment (`dev`, `stage`, `prod`) |

`now()` is disabled so a decision can be replayed exactly; use `ctx.now` (`ctx.now.Year()`, `ctx.now.Hour()`, `daysBetween(...)`).

## 4. Expr language in one page

| Category | Syntax |
|---|---|
| Literals | `42`, `3.14`, `"text"`, `true`, `nil`, `[1, 2]`, `{"a": 1, b: 2}` |
| Arithmetic | `+ - * / % ^` |
| Comparison | `== != < <= > >=` |
| Logic | `and` / `&&`, `or` / `\|\|`, `not` / `!` |
| Conditional | `a ? b : c`, `x ?? "default"`, `if cond { a } else { b }` |
| Access | `input.car.year`, `input["car"]["year"]`, `list[0]`, `list[-1]` |
| Membership | `x in [..]`, `"key" in map` |
| Strings | `+`, `contains`, `startsWith`, `endsWith`, `matches` |
| Slices and ranges | `list[1:3]`, `1..5` |
| Pipe | `input.lead.city \| lower()` |
| Variables | `let age = ctx.now.Year() - input.car.year; age < 8` |
| Predicates | `filter(input.items, .score < 6)`, `all(list, # > 0)` (`#` is the element; `.field` is shorthand for `#.field`) |

Built-in functions (Expr 1.17): `len type abs ceil floor round int float string max min mean median sum count trim trimPrefix trimSuffix upper lower split splitAfter replace repeat join indexOf lastIndexOf hasPrefix hasSuffix all any one none map filter find findIndex findLast findLastIndex groupBy sortBy sort reduce first last take reverse uniq concat flatten keys values toPairs fromPairs get toJSON fromJSON toBase64 fromBase64 duration date timezone`.

Changed in Gearbox for determinism:

- `keys`, `values`, `toPairs` return results **sorted by key** (Expr's own versions follow Go map order, which is random).
- `toJSON` writes object keys sorted.
- `now` is removed. `date(s)` parses in UTC unless the string or a third argument names a time zone.

### Gearbox functions

| Function | Returns | Example |
|---|---|---|
| `lookup(table, key)` / `lookup(table, key, default)` | value from a bundle lookup table | `lookup("city_zone", input.lead.city, "other")` |
| `inSegment(segment, id)` | bool | `inSegment("vip_dealers", input.dealer.id)` |
| `gate(name)` | bool, for `ctx.unit` | `gate("new_pricing")` |
| `variant(experiment)` | string, for `ctx.unit` | `variant("checkout_v2") == "treatment"` |
| `bucket(salt)` | int 0–9999, stable for `ctx.unit`; `nil` without a unit, so comparisons on it are `false` | `bucket("r1-2026-10") < 2000` |
| `daysBetween(a, b)` | int | `daysBetween(date(input.lead.created_at), ctx.now) > 30` |
| `coalesce(a, b, ...)` | first non-nil | `coalesce(input.lead.alt_phone, input.lead.phone)` |

Custom functions are Go code added to the engine build (pure, typed, deterministic). They become available to every caller in every language, and adding one raises the engine version that bundles using it require.

## 5. Type checking

- At publish, expressions are compiled against the decision's input JSON Schema. Unknown fields, wrong types (`"a" + 1`) and non-boolean `when` expressions are rejected before release.
- Optional fields (not in the schema's `required`) are typed as nullable, so arithmetic on them without `??` is rejected at publish.
- Fields typed `any` in the schema skip static checks; their errors surface in tests.

## 6. Gotchas

- Inside a predicate, `{...}` is a closure, not an object. To build an object per element, wrap it in parentheses: `map(input.items, ({"part": .part, "ok": .score >= 6}))`. The transform editor does this for you.
- Numbers from JSON are floats: `input.car.year == 2019` works, `int(input.car.year)` when you need an integer.
- `contains` on a string is substring; on a list use `x in list`.

## 7. Examples

```expr
// Eligible if recent, low km, in a metro, and certified
ctx.now.Year() - input.car.year <= 9 && input.car.km < 60000
  && input.lead.city in ["Gurgaon", "Delhi", "Noida"] && "certified" in input.tags

// Weak inspection parts
map(filter(input.inspections, .score < 6), .part)

// Price band via lookup with a default
lookup("price_band", string(int(input.car.price / 100000)), "unbanded")

// 20% rollout of a new route for this unit
bucket("fast-track-2026-10") < 2000 ? "fast_track" : "standard"
```

## 8. Limits and cost

Expr always terminates, but one evaluation cannot be interrupted, so cost is bounded before a bundle ships:

- **Static limits** (configurable): expression ≤ 4 KB, ≤ 200 nodes per decision, input ≤ 256 KB, nesting depth ≤ 32.
- **Cost check at publish.** Every loop (`all`, `map`, `filter`, `reduce`…) over an input array needs a `maxItems` in the schema. The compiler multiplies `maxItems` along nested loops; a decision whose worst case exceeds 1 M element steps is rejected. Input arrays longer than `maxItems` fail with `INVALID_ARGUMENT`.
- **Benchmark at publish.** The publisher runs each decision's test cases 1,000 times and rejects the release if p99 exceeds the decision's budget (default 200 µs, AI time excluded).
- **Circuit breaker at runtime.** If a decision's evaluation exceeds 10× its budget repeatedly, the sidecar returns its fallback for 30 s, counts `gearbox_decision_breaker_open_total` and alerts.

Expr's own memory budget (`MemoryBudget`) stays on, so large ranges or lists fail instead of exhausting memory.
