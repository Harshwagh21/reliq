# Semantics

How RELIQ behaves around every policy. Start with the [guide](guide.md) if you are wiring a call for the first time. The binding rules are [DESIGN.md](DESIGN.md). This page shows how to apply them. It is not a second contract. If this page and DESIGN disagree, DESIGN wins.

## Construction

[DESIGN.md — Construction](DESIGN.md#construction) and [Validation](DESIGN.md#5-validation): `New` validates once and returns `(*T, error)`. Invalid numbers wrap `chain.ErrInvalidConfig` and name the field. Nil function fields get the documented defaults. Numeric zeros do not. `Policy()` cannot fail. Config structs are copied at `New`, so later changes to the caller's struct have no effect.

`timeout.New(0)` returns a nil `*Timeout` and an error that matches `chain.ErrInvalidConfig` and names `d`. The same shape holds for every other `New`.

## `Do` and `Get`

[DESIGN.md — Nil arguments](DESIGN.md#nil-arguments), [Pre-cancelled context](DESIGN.md#pre-cancelled-context), [Chain construction](DESIGN.md#chain-construction), and [`Get[T]` result](DESIGN.md#gett-result).

Checks run in this order: context, then function, then policies. The first failure returns, and `fn` is not called.

A canceled context with a nil function returns `ErrNilArgument`, not the context error. A real function passed with a nil policy does the same: the nil policy is rejected before the cancel check. A canceled context with a real function and no nil policy returns `ctx.Err()`, calls `fn` zero times, and leaves policy state alone. Those cases are [pkg/chain/executor_test.go](../pkg/chain/executor_test.go).

A live context builds the chain for that call. The first policy is outermost. There is no cached `Chain` type.

On any error, `Get[T]` returns the zero `T`, even when `fn` also returned a value. A function that returns `"partial"` and an error produces the zero string. A successful retry returns the value from the successful attempt only. The happy path is [pkg/chain/example_test.go](../pkg/chain/example_test.go).

## Cancellation

[DESIGN.md — Cancellation before an attempt](DESIGN.md#cancellation-before-an-attempt) and [Retry](DESIGN.md#retry).

Retry checks `ctx` before every attempt, including the first, and again after an attempt before scheduling another. An expired retry context stops the loop. When an attempt error already exists, the return value is:

```text
fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)
```

`errors.Is` matches both the context error and `lastErr`. Timeout used alone still returns a late result from a `ctx`-ignoring `fn` unchanged. See [Timeout](DESIGN.md#timeout).

## Panics and Goexit

[DESIGN.md — Panics and Goexit](DESIGN.md#panics-and-goexit) and [Panic origin](DESIGN.md#panic-origin).

RELIQ never swallows a panic. Policies restore permits, probe slots, and timeout cancellation with `defer`.

| Source | Breaker records failure? | What the caller sees |
| --- | --- | --- |
| Dependency `fn` panic | Yes | Original panic value |
| `runtime.Goexit` in `fn` | Yes | Goroutine exits; no panic value |
| Panic in an **inner** policy callback (`Retryable`, `OnRetry`) | Yes | Original panic value |
| Panic in the breaker’s `IsFailure` / `OnStateChange` | No | Original panic value; committed transitions stay |

Origin beneath the breaker is not tracked. A breaker wrapped outside retry cannot tell an `OnRetry` panic from a panic in `fn`. Both count as one failure, any held probe slot is released, and the original value reaches the caller. No helper package is authorized for that classification.

## Callbacks

[DESIGN.md — Callbacks](DESIGN.md#callbacks).

`OnRetry`, `OnStateChange`, `Retryable`, and `IsFailure` run on the calling goroutine, outside locks, and must not block. Concurrent `Do` calls may invoke them concurrently and may observe `OnStateChange` out of order. Mutable state a callback owns has to be concurrency-safe. RELIQ does not serialize callbacks.

## Errors

[DESIGN.md — Errors](DESIGN.md#errors).

Use `errors.Is` on sentinels. Wrap with `%w`. Never match error strings. Context errors stay `context.Canceled` or `context.DeadlineExceeded`, possibly wrapped, and are never replaced with a RELIQ sentinel.

| Sentinel | When `errors.Is` matches |
| --- | --- |
| `ErrNilArgument` | Nil context, nil function, or nil policy |
| `ErrInvalidConfig` | A `New` rejected a number. The wrapping text names the field |
| `ErrCircuitOpen` | Breaker rejected the call. `fn` was not called |
| `ErrBulkheadFull` | No permit. `fn` was not called |
| `ErrRateLimited` | No token. `fn` was not called |
| `ErrRetriesExhausted` | Every attempt failed and was retryable. Also matches the last attempt error |

A non-retryable error is returned as-is. It is not wrapped in `ErrRetriesExhausted`.

## Concurrency

[DESIGN.md — Concurrency](DESIGN.md#concurrency).

Every `*T` from `New` is safe for concurrent `Do`. `Do` is reentrant. Policy values are immutable after `New`.
