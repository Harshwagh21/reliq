// Package retry repeats a failed call with exponential backoff and full jitter.
//
// [New] rejects a [Config] whose MaxAttempts is below 1, whose BaseDelay is not
// greater than zero, or whose MaxDelay is below BaseDelay. The error wraps
// [chain.ErrInvalidConfig] and names the field. A nil Retryable retries every
// error except context.Canceled, chain.ErrCircuitOpen, chain.ErrBulkheadFull,
// and chain.ErrRateLimited. context.DeadlineExceeded is retried while this
// policy's context is still live.
//
// Retries can run the same side effect more than once. RELIQ does not provide
// exactly-once delivery. Callers that need uniqueness use their own idempotency
// keys.
package retry
