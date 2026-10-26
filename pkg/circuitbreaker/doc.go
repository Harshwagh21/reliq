// Package circuitbreaker opens after consecutive failures and then rejects
// calls until OpenTimeout has elapsed.
//
// [New] rejects a [Config] whose FailureThreshold is below 1 or whose
// OpenTimeout is not greater than zero. The error wraps [chain.ErrInvalidConfig]
// and names the field. A nil IsFailure treats a non-nil error as a failure
// unless it is context.Canceled. A nil OnStateChange is not called.
//
// A nil error is success, including a late success after the breaker's context
// is already done. An error that matches that done context is neutral.
// DeadlineExceeded is also neutral once the breaker's own deadline has been
// reached, so two deadlines that expire together record nothing. IsFailure is
// not called for a neutral outcome. Any other error is classified by IsFailure.
// A deadline from a shorter per-attempt timeout below the breaker counts as a
// failure. The breaker does not start a goroutine. OpenTimeout is checked on
// the next call.
package circuitbreaker
