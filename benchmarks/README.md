# Benchmarks

On-demand checks of policy overhead. They are not a CI job. The recorded baseline, hardware, and command are in [`docs/benchmarks.md`](../docs/benchmarks.md).

```bash
go test -bench=. -benchmem -count=10 ./benchmarks/
```

`make bench` runs one pass of `go test -bench=. -benchmem ./...`.
