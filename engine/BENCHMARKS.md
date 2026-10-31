# Engine benchmarks

The benchmarks are defined in [`bench_test.go`](bench_test.go) and run with `make bench-engine` (`go test -run '^$' -bench . -benchmem -count 3 .`). They exclude JSON decode of the input (`Snapshot.DecideParsed` with a pre-decoded document), except `LeadRoutingWithDecode`. They include the rules node's runtime input-schema check, condition evaluation, rollout bucketing, output-schema checks and JSON encoding of the output.

Measured numbers, the machine they ran on and the raw output are in [docs/07 · Benchmarks](../docs/07-benchmarks.md). They are kept only there so they cannot drift apart.

## Why it is fast

- Structured conditions compile to Go closures over pre-split field paths
  (no expression VM, no string building); list operators use pre-built sets.
- Expressions are compiled once (Expr bytecode); each call reuses pooled
  `vm.VM`s and run state (`sync.Pool`), so the hot path takes no locks.
- Constant `then`/`default`/fallback values are encoded to JSON at compile time;
  when the final output is such a constant, the cached bytes are returned.
- Schema validation of valid documents allocates nothing.
