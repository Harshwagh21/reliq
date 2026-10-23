// Package ratelimit admits calls from a token bucket.
//
// [New] rejects a ratePerSecond that is not finite and greater than zero, and
// a burst below 1. The error wraps [chain.ErrInvalidConfig] and names the
// field. The bucket starts full. One token is taken before the function runs
// and is not returned. An empty bucket returns [chain.ErrRateLimited]
// immediately. Refill uses elapsed time on the next call and never exceeds
// burst. There is no goroutine.
package ratelimit
