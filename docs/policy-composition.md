# Policy composition

Policies wrap a `chain.Func`. `Do` / `Get` take them **outermost first**. The contract is [DESIGN.md — Composition](DESIGN.md#composition). Phase 6 adds the integration walkthrough; this page states the order and why it matters.

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

Composition tests assert attempts, breaker records, tokens consumed, and permits released — not only the final error. See the Phase 5 integration plan.
