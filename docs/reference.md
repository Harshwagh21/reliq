# Reference

Every v0.1 export, in one place. The generated text, with runnable examples, is the godoc on [pkg.go.dev/github.com/harshwagh21/reliq](https://pkg.go.dev/github.com/harshwagh21/reliq). This page adds the sentence a generated list does not: what the call is for, what it changes, and where the source is.

[DESIGN.md section 4](DESIGN.md#4-public-api) is the export list. If this page, the godoc, and DESIGN disagree, DESIGN wins. Nothing else is exported.

`New` always returns `(*T, error)`. A rejected number wraps `chain.ErrInvalidConfig` and names the field. `Policy()` cannot fail. Config structs are copied at `New`.

## chain

[pkg/chain](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/chain) · [policy.go](../pkg/chain/policy.go) · [executor.go](../pkg/chain/executor.go) · [errors.go](../pkg/chain/errors.go)

| Export | Role |
| --- | --- |
| `Func` | One dependency call: `func(context.Context) error`. |
| `Policy` | Wraps a `Func` and returns a `Func`. The first policy passed to `Do` or `Get` is outermost. |
| `Do` | Calls `fn` under the policies. Returns `fn`'s error, a sentinel, or a context error. Nil checks run context, then function, then policies, and `fn` is not called. An already-done context returns `ctx.Err()` and touches no policy state. Building the chain and running it can change breaker, limiter, and bulkhead state. |
| `Get` | Same as `Do` for a function that also returns `T`. On any error the returned `T` is the zero value. A successful retry returns the value from the successful attempt. |
| `ErrNilArgument` | `Do` or `Get` saw a nil context, a nil function, or a nil policy. |
| `ErrInvalidConfig` | A `New` rejected a number. The wrapping error names the field. |
| `ErrCircuitOpen` | A breaker rejected the call. `fn` was not called. |
| `ErrBulkheadFull` | A bulkhead had no permit. `fn` was not called. |
| `ErrRateLimited` | A limiter had no token. `fn` was not called. |
| `ErrRetriesExhausted` | Every attempt failed and was retryable. Also matches the last attempt error. A non-retryable error is returned as-is. |

Match sentinels with `errors.Is`. Context errors stay `context.Canceled` or `context.DeadlineExceeded`, possibly wrapped.

## timeout

[pkg/timeout](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/timeout) · [timeout.go](../pkg/timeout/timeout.go)

| Export | Role |
| --- | --- |
| `New` | `d` must be greater than zero. Returns a `*Timeout`. |
| `(*Timeout).Policy` | Wraps the next call with `context.WithTimeout`. Cancels that context on every return, including panic and `runtime.Goexit`. Does not extend an earlier parent deadline. A late result from a function that ignores `ctx` is returned unchanged. Starts no goroutine. |

## retry

[pkg/retry](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/retry) · [retry.go](../pkg/retry/retry.go)

| Export | Role |
| --- | --- |
| `Config` | `MaxAttempts` (total attempts, at least 1), `BaseDelay` (greater than zero), `MaxDelay` (at least `BaseDelay`). `Retryable` and `OnRetry` may be nil. |
| `New` | Copies `Config` and validates the numbers. A nil `Retryable` retries every error except `context.Canceled`, `ErrCircuitOpen`, `ErrBulkheadFull`, and `ErrRateLimited`. `context.DeadlineExceeded` is retried while this policy's context is still live. |
| `(*Retry).Policy` | Repeats eligible failures. Checks the context before every attempt, including the first. May call the next function once per attempt. Waits on a timer between attempts and stops that timer on every path. `OnRetry` runs only after a retry has been scheduled, on the calling goroutine, outside any lock. Panics from the next call, `Retryable`, and `OnRetry` propagate. |

Retries can run the same side effect more than once.

## circuitbreaker

[pkg/circuitbreaker](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/circuitbreaker) · [breaker.go](../pkg/circuitbreaker/breaker.go) · [state.go](../pkg/circuitbreaker/state.go)

| Export | Role |
| --- | --- |
| `State` | `StateClosed`, `StateOpen`, `StateHalfOpen`. |
| `State.String` | `"Closed"`, `"Open"`, `"HalfOpen"`. Any other value is `"State(<n>)"`. `State(99)` is `"State(99)"`. |
| `Config` | `FailureThreshold` at least 1. `OpenTimeout` greater than zero. `IsFailure` and `OnStateChange` may be nil. |
| `New` | Copies `Config` and validates the numbers. A nil `IsFailure` counts any error other than `context.Canceled` as a failure. A nil `OnStateChange` is not called. |
| `(*Breaker).State` | Returns the current state. |
| `(*Breaker).Policy` | Admits or rejects the next call. An open circuit returns `ErrCircuitOpen` and does not call next. After `OpenTimeout`, one half-open probe is allowed. A nil error is success. An error that matches the breaker's done context is neutral and records nothing. Otherwise `IsFailure` decides. A panic from next, including an inner policy callback, records a failure and the original value propagates. `runtime.Goexit` from next records a failure and the goroutine keeps exiting. A panic from `IsFailure` or `OnStateChange` is not a failure. A held probe slot is released. `OnStateChange` runs after the lock is released. Starts no goroutine. |

## bulkhead

[pkg/bulkhead](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/bulkhead) · [bulkhead.go](../pkg/bulkhead/bulkhead.go)

| Export | Role |
| --- | --- |
| `New` | `maxConcurrent` must be at least 1. Returns a `*Bulkhead`. |
| `(*Bulkhead).Policy` | Takes one permit, then calls next. When every permit is in use, returns `ErrBulkheadFull` and does not call next. Releases the permit on every return, including panic and `runtime.Goexit`. Does not queue and starts no goroutine. |

## ratelimit

[pkg/ratelimit](https://pkg.go.dev/github.com/harshwagh21/reliq/pkg/ratelimit) · [limiter.go](../pkg/ratelimit/limiter.go)

| Export | Role |
| --- | --- |
| `New` | `ratePerSecond` must be finite and greater than zero. `burst` must be at least 1. The bucket starts full. Returns a `*Limiter`. |
| `(*Limiter).Policy` | Takes one token, then calls next. The token is not returned. When the bucket has less than one token, returns `ErrRateLimited` and does not call next. Refills from elapsed time on the next call and never exceeds `burst`. Starts no goroutine. |
