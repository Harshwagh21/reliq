# Guide

How to use RELIQ. You should already know Go, `context.Context`, and `errors.Is`.

This page is the path through the library. The binding rules are [DESIGN.md](DESIGN.md). If this page and DESIGN disagree, DESIGN wins. Every program named here builds with `go build ./examples/...`.

## Contents

- [Install](#install)
- [The shape of a call](#the-shape-of-a-call)
- [Timeout](#timeout)
- [Retry](#retry)
- [Circuit breaker](#circuit-breaker)
- [Bulkhead](#bulkhead)
- [Rate limiter](#rate-limiter)
- [Several policies](#several-policies)
- [When a call surprises you](#when-a-call-surprises-you)

## Install

Go 1.26 or newer.

```bash
go get github.com/harshwagh21/reliq@v0.1.0
```

From a clone of this repo, run an example with `go run ./examples/retry`.

## The shape of a call

Every policy has the same shape. `New` checks the numbers once and returns `(*T, error)`. A rejected number wraps `chain.ErrInvalidConfig` and names the field. `Policy()` cannot fail. You pass that policy to `chain.Do` or `chain.Get`.

`Do` takes an error-returning function. `Get` takes a function that returns a value and an error. On any error, `Get` returns the zero value. The first policy you pass is the outermost one.

There is no registry and no global policy. Each `New` is one value, safe for concurrent `Do`.

The smallest working program is [examples/retry](../examples/retry/main.go). It fails once, then succeeds.

## Timeout

A timeout bounds one call with `context.WithTimeout`. An earlier deadline on the parent context is left as it is. The child context is canceled when the call returns, including after a panic.

The timeout is cooperative. If the function ignores `ctx` and returns late, that result is returned unchanged. RELIQ does not stop the goroutine.

[examples/http-client](../examples/http-client/main.go) is one GET under a timeout. The handler returns immediately.

## Retry

`MaxAttempts` is the total number of attempts. `3` means one try and two retries. The delay before a retry grows exponentially from `BaseDelay` and is capped by `MaxDelay`, then jittered down to anywhere from zero up to that cap. The wait stops when the context is canceled.

The default predicate retries `context.DeadlineExceeded` while the retry's own context is still live. It does not retry `context.Canceled`, an open circuit, a full bulkhead, or a rate limit. Set `Retryable` when that default is wrong. A call that is not safe to repeat needs that, or it needs to stay off retry. RELIQ does not make the work exactly-once.

[examples/retry](../examples/retry/main.go) uses a one-nanosecond delay so the wait is at most that long.

## Circuit breaker

The breaker counts consecutive failures. At `FailureThreshold` it opens. While it is open, `Do` returns `chain.ErrCircuitOpen` and does not call the function. After `OpenTimeout`, the next call is one probe. Success closes the breaker. Failure opens it again.

A nil error is success. An error that matches the breaker's own done context is neutral: caller cancel, or an outer deadline, records nothing. `DeadlineExceeded` is also neutral once the breaker's own deadline has been reached, so two deadlines that expire together record nothing. A shorter deadline from a timeout placed inside the breaker counts as a failure.

[examples/circuitbreaker](../examples/circuitbreaker/main.go) uses a threshold of 1, so the second call is rejected while `OpenTimeout` is still in the future.

## Bulkhead

A bulkhead limits how many calls run at the same time. A full bulkhead returns `chain.ErrBulkheadFull` immediately. There is no queue. The permit is released when the call returns, including after a panic.

The tested example is [pkg/bulkhead/example_test.go](../pkg/bulkhead/example_test.go). The combined program below uses a bulkhead of 1.

## Rate limiter

The limiter is a token bucket. It starts full. One token is taken before the function runs and is not returned if the function fails. An empty bucket returns `chain.ErrRateLimited` immediately. There is no wait. The bucket refills on the next call, and it never holds more than `burst` tokens. Two limiter values do not share a bucket.

The tested example is [pkg/ratelimit/example_test.go](../pkg/ratelimit/example_test.go).

## Several policies

Pass policies outermost first. The order that matches the usual dependency call is: one deadline for the whole operation, then retry, then the rate limiter, then the breaker, then the bulkhead, then an optional per-attempt timeout. That order is drawn and explained in [Policy composition](policy-composition.md).

[examples/combined-policies](../examples/combined-policies/main.go) is that stack around one successful GET. The first attempt succeeds, so retry does not wait.

Putting the breaker outside retry changes the count. The breaker then sees one logical operation, not each attempt. Both placements are in the composition page. Do not mix their claims.

## When a call surprises you

- A nil argument, a canceled context, a panic, or a sentinel: [Semantics](semantics.md).
- You are unsure the policy belongs on this call: [FAQ](faq.md).
- You need the full argument list for one function: [Reference](reference.md), then the godoc on [pkg.go.dev](https://pkg.go.dev/github.com/harshwagh21/reliq).
