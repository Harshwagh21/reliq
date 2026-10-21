# Semantics

How RELIQ behaves around every policy. The binding rules live in [DESIGN.md](DESIGN.md). This page is the reader-facing map; Phase 6 expands examples.

## Construction

`New` validates once and returns `(*T, error)`. Invalid numbers wrap `chain.ErrInvalidConfig` and name the field. Nil function fields get documented defaults; numeric zeros do not. `Policy()` cannot fail. Config structs are copied at `New`.

## `Do` and `Get`

1. Reject nil `ctx`, then nil `fn`, then any nil policy, with `ErrNilArgument`. Never panic. Never call `fn`.
2. If `ctx` is already done, return `ctx.Err()` as-is. Zero calls. No policy state changes.
3. Build the policy chain for this call (outermost first). There is no cached `Chain` type.
4. On any error, `Get[T]` returns the zero `T`. A retry success is the value from the successful attempt.

## Cancellation

Retry checks `ctx` before every attempt, including the first, and again after an attempt before scheduling another. An expired retry context stops the loop and returns the context error joined with the last attempt error when one exists:

```text
fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)
```

Timeout used **alone** still returns a late result from a `ctx`-ignoring `fn` unchanged. See [DESIGN.md — Timeout](DESIGN.md#timeout) and [Retry](DESIGN.md#retry).

## Panics and Goexit

RELIQ never swallows a panic. Policies restore permits, probe slots, and timeout cancellation with `defer`.

| Source | Breaker records failure? | What the caller sees |
| --- | --- | --- |
| Dependency `fn` panic | Yes | Original panic value |
| `runtime.Goexit` in `fn` | Yes | Goroutine exits; no panic value |
| Panic in an **inner** policy callback | Yes | Original panic value |
| Panic in the breaker’s `IsFailure` / `OnStateChange` | No | Original panic value; committed transitions stay |

Origin beneath the breaker is not tracked. No helper package is authorized for that classification.

## Callbacks

`OnRetry`, `OnStateChange`, `Retryable`, and `IsFailure` run on the calling goroutine, outside locks, and must not block. Concurrent `Do` calls may invoke them concurrently and may observe `OnStateChange` out of order.

## Errors

Use `errors.Is` on sentinels. Wrap with `%w`. Never match error strings. Context errors stay `Canceled` / `DeadlineExceeded` (possibly wrapped).
