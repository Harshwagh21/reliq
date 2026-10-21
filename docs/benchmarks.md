# Benchmarks

No performance numbers or claims until Phase 6 records a baseline. Method is fixed here so later results stay comparable.

## Command

```bash
make bench
# or
go test -bench=. -benchmem ./...
```

Use `benchstat` over 10 runs when publishing numbers. Record Go version, CPU, OS, and the exact command in this file at release.

## Cases (Phase 6)

From the test plan:

- Direct call baseline
- `Do` with zero policies
- Retry, first-attempt success
- Breaker closed
- Breaker parallel (`b.RunParallel`)
- Full recommended composition

Do not add a performance target until those baselines exist. Benchmarks are on-demand locally; they are not a default CI job.
