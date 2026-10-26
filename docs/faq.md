# FAQ

Answers that follow from [DESIGN.md](DESIGN.md). This page is not a second contract. If it and DESIGN disagree, DESIGN wins. The [guide](guide.md) is the path for a first call. This page is when to leave a policy off.

## When should I not use RELIQ?

[DESIGN.md — Out of scope](DESIGN.md#9-out-of-scope-v01).

- You need hedging, fallback, a sliding-window breaker, distributed state, reconfiguration, or metrics adapters. v0.1 does not have them.
- You need a call forcibly stopped when `fn` ignores `context.Context`. [Timeout](DESIGN.md#timeout) is cooperative: a late success or error is returned as-is.
- You need one limit shared by many processes. [Rate limiter](DESIGN.md#rate-limiter) and [bulkhead](DESIGN.md#bulkhead) state live in the process that called `New`.
- You need exactly-once side effects. [Retry](DESIGN.md#retry) can run the same work again. Callers that need uniqueness use their own idempotency keys.
- You need a larger toolkit. failsafe-go already covers this space. RELIQ stays a small stdlib composition layer.

`v0.x` may still change. The export list is [section 4](DESIGN.md#4-public-api).

## When should I not use each policy?

| Policy | Skip it when | What you would get if you used it anyway |
| --- | --- | --- |
| [Timeout](DESIGN.md#timeout) | The operation cannot honor `ctx`, and a late result would be wrong. | A late success or error is returned unchanged. RELIQ does not cancel the goroutine. |
| [Retry](DESIGN.md#retry) | The call is not idempotent and you cannot set `Retryable` to refuse the dangerous errors. | The default predicate retries `context.DeadlineExceeded` while retry’s own context is live, and it retries other errors except `context.Canceled`, `ErrCircuitOpen`, `ErrBulkheadFull`, and `ErrRateLimited`. Side effects can run once per attempt. |
| [Circuit breaker](DESIGN.md#circuit-breaker) | You need a sliding window, state shared across processes, or a threshold you can change after `New`. | Consecutive failures open the circuit. Further calls return `ErrCircuitOpen` until `OpenTimeout`, then one probe runs. |
| [Bulkhead](DESIGN.md#bulkhead) | Callers should wait for a permit. | A full bulkhead returns `ErrBulkheadFull` immediately. There is no queue. |
| [Rate limiter](DESIGN.md#rate-limiter) | You want waiting, a refund when `fn` fails, or one bucket for many processes. | One token is taken before `fn` and is never returned. An empty bucket returns `ErrRateLimited` immediately. |

## Why is `DeadlineExceeded` retried by default?

[DESIGN.md — Retry](DESIGN.md#retry). A live retry context plus `context.DeadlineExceeded` means the deadline came from below: a per-attempt timeout or a downstream client. If that error were not retryable, the recommended inner timeout would never be retried. Set `Retryable` when that default is wrong for the call.

`context.Canceled` is not retried. An expired retry context stops the loop before `Retryable` runs, and the caller gets the context error joined with the last attempt error when one exists.

## Why does an outer deadline not count as a breaker failure?

[DESIGN.md — Circuit breaker](DESIGN.md#circuit-breaker). Classification checks a nil error first, then whether the error matches the breaker's own done context. That second case is neutral: caller cancel, or an outer timeout whose context is the one the breaker received. Nothing is recorded. `IsFailure` is not called.

A per-attempt timeout placed inside the breaker has a different context. Its `DeadlineExceeded` does not match the breaker's context, so the default `IsFailure` counts it.

## Why does `Get` return a zero value on error?

[DESIGN.md — `Get[T]` result](DESIGN.md#gett-result). A failed attempt must not leak a partial `T`. On success, including a success after retry, the value is the one from the successful attempt.

## Why did my callback panic open the breaker?

[DESIGN.md — Panic origin](DESIGN.md#panic-origin). RELIQ does not record where beneath the breaker a panic started. A panic in `fn`, or in an inner `Retryable` or `OnRetry`, is one failure. The original panic still reaches the caller. A panic in the breaker's own `IsFailure` or `OnStateChange` is not recorded.

## How do I change behavior?

Write an ADR under [`docs/adr/`](adr/README.md), update [DESIGN.md](DESIGN.md), then change code and tests. A new export needs that decision first. Section 4 is the full v0.1 list.
