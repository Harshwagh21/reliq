# Policy composition

Policies wrap a `chain.Func`. `Do` / `Get` take them **outermost first**. The contract is [DESIGN.md — Composition](DESIGN.md#composition). The order below is that contract. The program that runs is [examples/combined-policies](../examples/combined-policies/main.go). The walkthrough at the end is what the integration tests already assert. Read the [guide](guide.md) first if you have not composed policies before.

## Recommended order

```go
chain.Do(ctx, call,
    total.Policy(),      // one deadline for attempts and backoff
    retry.Policy(),      // repeats eligible failures
    limiter.Policy(),    // one token per attempt, never refunded
    breaker.Policy(),    // sees each attempt
    bulkhead.Policy(),   // permit only while the attempt runs
    perAttempt.Policy(), // optional timeout, innermost
)
```

| Layer | Role |
| --- | --- |
| Outer timeout | Caps the whole `Do`, including retry waits |
| Retry | Re-enters everything inside it |
| Rate limiter | Charges a token before each attempt |
| Breaker | Opens on consecutive per-attempt failures |
| Bulkhead | Limits in-flight attempts; released before backoff |
| Inner timeout | Per-attempt deadline; expiry is a dependency failure for the breaker |

## Two breaker placements

Tests must keep both and not mix their claims:

- **Retry outside breaker** (default above): each attempt reaches the breaker. Consecutive failures can open it between attempts.
- **Breaker outside retry:** the breaker sees one logical operation. The recorded outcome is the retry’s final result, including the [panic-origin](DESIGN.md#panic-origin) rule (inner-callback panics count as failures).

## Exact counts

Composition tests assert attempts, breaker records, tokens consumed, and permits released — not only the final error. The cases below are those tests. Counts are not a new rule.

## Integration walkthrough

`test/integration/composition_test.go` builds the order in the snippet above unless a case says the breaker is outside retry. The dependency is an `httptest` server, or a function that returns an error, as the case describes. The limiter in that stack starts with 3 tokens (`New(1, 3)`). The default breaker threshold is 5. Stored breaker failures are checked by how many further failures it takes to open that threshold, because the counter is not exported.

Rules cited here: [Composition](DESIGN.md#composition), [Retry](DESIGN.md#retry), [Circuit breaker](DESIGN.md#circuit-breaker), [Timeout](DESIGN.md#timeout), [Panic origin](DESIGN.md#panic-origin), and [Panics and Goexit](DESIGN.md#panics-and-goexit).

**Persistent failure** (`TestPersistentFailureCounts`). The server returns an error. `MaxAttempts` is 3. `fn` runs 3 times, the bucket is empty, the breaker holds 3 failures and stays Closed, the bulkhead permit is free, and the error matches both `ErrRetriesExhausted` and the dependency error.

**Threshold 2** (`TestThresholdTwoStopsOnOpen`). The third attempt would be another try, but the breaker opens on the second failure. `fn` runs twice. The third entry is `ErrCircuitOpen`, the default predicate does not retry it, and the error does not match `ErrRetriesExhausted`. State is Open.

**Outer timeout during backoff** (`TestTotalTimeoutDuringBackoff`). The outer deadline is shorter than a positive jittered delay. `fn` runs once. Elapsed time is that deadline. The error joins `context.DeadlineExceeded` with the last attempt error and does not match `ErrRetriesExhausted`.

**Outer timeout, context-aware call** (`TestOuterTimeoutNeutral`). The breaker already holds 3 failures. `fn` waits on `ctx` and returns `ctx.Err()`. One call. Elapsed time is the outer timeout. The joined deadline error is neutral for the breaker, so the stored count stays 3.

**Late success** (`TestLateSuccessRecordsSuccess`). The same priming, but `fn` ignores `ctx` and returns nil after the deadline. Timeout used around a late success returns that success. The breaker classifies nil first, records success, and the stored count is 0.

**Per-attempt timeout** (`TestPerAttemptTimeoutIsFailure`). The inner timeout is inside the breaker, `MaxAttempts` is 1, and the threshold is 1. `fn` waits on the inner context. One call, `context.DeadlineExceeded`, state Open. That deadline is not the breaker's own context, so it is a failure.

**Per-attempt timeout under retry** (`TestPerAttemptTimeoutRetryThreeFailures`). The same inner timeout with 3 attempts and threshold 5. `fn` runs 3 times. The error matches `ErrRetriesExhausted` and `context.DeadlineExceeded`. The breaker holds 3 failures.

**Breaker outside retry** (`TestBreakerOutsideRetryOneFailure`). Order is breaker, then retry. `fn` still runs 3 times and the error matches `ErrRetriesExhausted`, but the breaker records one failure for the whole operation. This case does not claim per-attempt breaker accounting.

**Inner callback panic** (`TestBreakerOutsideRetryOnRetryPanic`, `TestBreakerOutsideRetryRetryablePanic`). The breaker is outside retry, already Open, and `OpenTimeout` has elapsed, so the call is the half-open probe. `OnRetry` or `Retryable` panics. `fn` has run once. The caller receives the original panic value. The probe is a failure, state is Open again, and a follow-up call does not run: it gets `ErrCircuitOpen`. Origin under the breaker is not tracked.

**Cleanup** (`TestCleanupOnExit`). On success, exhaustion, and a dependency panic, the timeout context passed to `fn` is canceled and the bulkhead permit is free. After `synctest.Wait`, the fake clock does not advance, so the timers were stopped. A neutral half-open probe (outer timeout around the breaker, `fn` returns the done context) stays HalfOpen, and the next call is admitted.

**No library goroutine** (`TestNoLibraryGoroutine`). After a successful call in the default order, no leftover goroutine has a stack in `pkg/` that belongs to the library.
