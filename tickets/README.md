# Tickets

Gearbox has no external tracker yet. [`TICKETS.md`](TICKETS.md) is the single list of work for the whole system. All parts are built at the same time; there is no phased roadmap.

## Format

```markdown
### GBX-042 · Short imperative title
**Status:** todo · **Depends on:** GBX-010, GBX-025
Two to four lines: what to build and why.
**Acceptance:**
- testable criterion
- testable criterion
```

| Field | Rule |
|---|---|
| ID | `GBX-NNN`, never reused. IDs are grouped by area in blocks (engine 010–029, sidecar 030–039…); new tickets take the next free number in their block, or the next number after 119 |
| Title | Imperative, one line |
| Status | `todo` · `in progress` · `review` (built, needs verification on a machine with full network) · `partial` · `done` · `blocked` (say on what) |
| Depends on | Ticket IDs that must be `done` first, or `–` |
| Acceptance | Testable statements; a ticket is `done` when all hold and the code is merged |

User stories (`US-…` in [`docs/02-user-stories.md`](../docs/02-user-stories.md)) say *what* users need; tickets say *what to build*. Tickets reference stories where useful.

## Working with tickets

- Change status in the same commit as the work (`feat(sidecar): sync loop (GBX-032)`).
- Contract problems found while working go to `contracts/CHANGES-REQUESTED.md`, plus a ticket in the Contracts block if the work is non-trivial.
- Keep the summary table at the top of `TICKETS.md` in sync with the ticket bodies.
