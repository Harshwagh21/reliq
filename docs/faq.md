# FAQ

Answers that follow from [DESIGN.md](DESIGN.md). Phase 6 expands this page; do not treat it as a second contract.

## When should I not use RELIQ?

- You need hedging, fallback, sliding-window breakers, distributed / Redis state, or metrics adapters. Those are out of scope for v0.1.
- You need a call forcibly stopped when `fn` ignores `context.Context`. Timeout is cooperative.
- You need cluster-wide rate limits or bulkheads. Limits are in-process only.
- You need exactly-once side effects. Retry can duplicate work; use your own idempotency keys.
- You need a larger, battle-tested toolkit. failsafe-go already covers this space.

## When should I not use each policy?

| Policy | Skip it when |
| --- | --- |
| Timeout | The operation cannot honor `ctx`. A late success or error is returned as-is. |
| Retry | The call is not idempotent and you cannot set `Retryable` (or accept duplicates). Default retries `DeadlineExceeded` while retry’s own `ctx` is live. |
| Circuit breaker | You need a sliding window, shared state across processes, or reconfiguration after `New`. |
| Bulkhead | You want callers to wait in a queue. A full bulkhead rejects immediately. |
| Rate limiter | You want waiting, refunds on failure, or one limit shared by many processes. Tokens are taken before `fn` and never refunded. |

## Why is `DeadlineExceeded` retried by default?

A live retry context plus `DeadlineExceeded` means the deadline came from below (per-attempt timeout or a downstream client). If it were not retryable, the recommended inner timeout would never retry. Override `Retryable` if that is wrong for your call. `context.Canceled` is never retried.

## Why does `Get` return a zero value on error?

Failed attempts must not leak a partial `T`. A successful retry returns the value from the successful attempt only.

## How do I change behavior?

Write an ADR under [`docs/adr/`](adr/README.md), update [DESIGN.md](DESIGN.md), then change code and tests.
