# Example definitions: `lead_service`

Definitions for the used-car lead example used throughout the docs. One file per resource, in the shape described in [docs/04-reference.md §10](../../docs/04-reference.md#10-definition-files-and-gearboxctl) (engine `Resource`: `kind`, `name`, `version`, `description`, `spec`, plus `segmentMembers` for static segments). Every `spec` matches the OpenAPI component for its kind.

| File | Kind | What |
|---|---|---|
| `schemas/lead_input.yaml` | schema | Request input: nested lead and car, inspections array (`maxItems`), nullable names, PII phone |
| `schemas/eligibility_input.yaml` | schema | Flat shape built by the `normalize` transform |
| `schemas/eligibility_output.yaml` | schema | Output of the `eligibility` rules |
| `schemas/routing_output.yaml` | schema | Decision response |
| `schemas/segment_attrs_city_price.yaml` | schema | Attributes of the dynamic segment |
| `segments/blocked_cities.yaml` | static segment | Cities not served |
| `segments/metro_premium.yaml` | dynamic segment | Metro city and price > 8 lakh, evaluated per call |
| `segments/beta_10pct.yaml` | rollout segment | Stable 10% of units |
| `decisions/lead_routing.yaml` | decision | transform → rules → output, with tests |

Run them without a control plane:

```bash
go run ./sidecar/cmd/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned --watch
```

Push them to a server (drafts), then cut versions and publish as usual:

```bash
gearboxctl --server http://localhost:8080 --app lead_service push examples/definitions
```
