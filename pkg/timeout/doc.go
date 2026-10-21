// Package timeout bounds one dependency call with [context.WithTimeout].
//
// [New] rejects a duration that is not greater than zero and returns an error
// wrapping [chain.ErrInvalidConfig] that names d. The policy is cooperative.
// A function that ignores its context can return after the deadline, and that
// result is passed through unchanged. An earlier parent deadline is never
// extended.
package timeout
