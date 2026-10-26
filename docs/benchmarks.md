# Benchmarks

Baseline only. There is no performance target. Benchmarks are on demand. They are not a default CI job. `make bench` runs one pass of `go test -bench=. -benchmem ./...`.

## Environment

| | |
| --- | --- |
| Go | `go1.27.2 darwin/arm64` |
| CPU | Apple M4, 10 cores (`hw.ncpu`) |
| OS | Darwin 25.4.0 arm64 |

`GOMAXPROCS` was 10. The benchmark names end in `-10`.

## Command

```bash
go test -bench=. -benchmem -count=10 ./benchmarks/
```

`benchstat` on that output (`golang.org/x/perf` at `v0.0.0-20261009192801-be2c69fb417e`). `benchstat` is not a module dependency.

## Method

Six success-path cases. The dependency is an indirect function that returns nil. Policies are built once, before the timed loop. One untimed `Do` must succeed. `b.Loop` excludes that setup. The retry case returns nil on the first attempt, so it does not wait. The breaker stays Closed (`FailureThreshold` 5, `OpenTimeout` one hour). The parallel case shares that breaker across `b.RunParallel`.

The full composition is the [recommended order](policy-composition.md#recommended-order): outer timeout, retry, limiter, breaker, bulkhead, inner timeout. Both timeouts are one hour. Retry allows 3 attempts with a one-nanosecond base and cap. The limiter is `New(1e9, 1<<20)`, so the timed loop stays on the admit path. The bulkhead allows one call at a time and releases it before the next iteration. No `httptest` and no `time.Sleep`.

## Results

`benchstat` medians. The `sec/op` column is printed in nanoseconds. Byte and allocation geomeans are absent because some cases are zero.

```text
                     │        sec/op        │
DirectCall-10                  0.9222n ± 2%
DoZeroPolicies-10               2.595n ± 1%
RetryFirstAttempt-10            13.89n ± 5%
BreakerClosed-10                20.45n ± 3%
BreakerParallel-10              189.8n ± 1%
FullComposition-10              455.8n ± 1%
geomean                         19.72n

                     │         B/op         │
DirectCall-10                  0.000 ± 0%
DoZeroPolicies-10              0.000 ± 0%
RetryFirstAttempt-10           64.00 ± 0%
BreakerClosed-10               24.00 ± 0%
BreakerParallel-10             24.00 ± 0%
FullComposition-10             920.0 ± 0%

                     │      allocs/op       │
DirectCall-10                  0.000 ± 0%
DoZeroPolicies-10              0.000 ± 0%
RetryFirstAttempt-10           1.000 ± 0%
BreakerClosed-10               1.000 ± 0%
BreakerParallel-10             1.000 ± 0%
FullComposition-10             15.00 ± 0%
```
