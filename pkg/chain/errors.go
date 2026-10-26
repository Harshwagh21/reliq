package chain

import "errors"

// ErrNilArgument is returned by [Do] and [Get] when ctx, fn, or a policy is nil.
var ErrNilArgument = errors.New("chain: nil argument")

// ErrInvalidConfig is wrapped by every policy constructor that rejects a
// configuration, and the wrapping error names the field.
var ErrInvalidConfig = errors.New("chain: invalid config")

// ErrCircuitOpen is returned when a circuit breaker rejects a call.
var ErrCircuitOpen = errors.New("chain: circuit open")

// ErrBulkheadFull is returned when a bulkhead has no permit left.
var ErrBulkheadFull = errors.New("chain: bulkhead full")

// ErrRateLimited is returned when a rate limiter has no token left.
var ErrRateLimited = errors.New("chain: rate limited")

// ErrRetriesExhausted is wrapped around the last attempt error when every
// attempt failed and was retryable. A non-retryable error is returned as-is.
var ErrRetriesExhausted = errors.New("chain: retries exhausted")
