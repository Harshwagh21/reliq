package retry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Config is the retry configuration copied by [New].
// Later changes to the caller's value have no effect.
type Config struct {
	// MaxAttempts is the total number of attempts, including the first.
	// It must be at least 1. A value of 1 does not retry.
	MaxAttempts int

	// BaseDelay is the uncapped delay before the first retry.
	// It must be greater than zero.
	BaseDelay time.Duration

	// MaxDelay caps the exponential delay.
	// It must be at least BaseDelay.
	MaxDelay time.Duration

	// Retryable reports whether an attempt error should be retried.
	// A nil Retryable uses the default predicate.
	Retryable func(error) bool

	// OnRetry is called after a retry is scheduled and before the wait.
	// attempt is the 1-based number of the attempt that just failed.
	// delay is the jittered wait about to start.
	// A nil OnRetry is not called.
	OnRetry func(attempt int, err error, delay time.Duration)
}

// Retry repeats eligible failures up to MaxAttempts.
type Retry struct {
	maxAttempts int
	base        time.Duration
	maxDelay    time.Duration
	retryable   func(error) bool
	onRetry     func(attempt int, err error, delay time.Duration)
}

// New copies c and validates it.
// MaxAttempts must be at least 1, BaseDelay must be greater than zero, and
// MaxDelay must be at least BaseDelay. Otherwise New returns an error wrapping
// [chain.ErrInvalidConfig] that names the field.
// A nil Retryable retries any error except context.Canceled,
// [chain.ErrCircuitOpen], [chain.ErrBulkheadFull], and [chain.ErrRateLimited].
// context.DeadlineExceeded is retried while this policy's context is still live.
func New(c Config) (*Retry, error) {
	if c.MaxAttempts < 1 {
		return nil, fmt.Errorf("retry: MaxAttempts: %w", chain.ErrInvalidConfig)
	}
	if c.BaseDelay <= 0 {
		return nil, fmt.Errorf("retry: BaseDelay: %w", chain.ErrInvalidConfig)
	}
	if c.MaxDelay < c.BaseDelay {
		return nil, fmt.Errorf("retry: MaxDelay: %w", chain.ErrInvalidConfig)
	}
	retryable := c.Retryable
	if retryable == nil {
		retryable = defaultRetryable
	}
	return &Retry{
		maxAttempts: c.MaxAttempts,
		base:        c.BaseDelay,
		maxDelay:    c.MaxDelay,
		retryable:   retryable,
		onRetry:     c.OnRetry,
	}, nil
}

func defaultRetryable(err error) bool {
	return !errors.Is(err, context.Canceled) &&
		!errors.Is(err, chain.ErrCircuitOpen) &&
		!errors.Is(err, chain.ErrBulkheadFull) &&
		!errors.Is(err, chain.ErrRateLimited)
}

// Policy wraps next and repeats eligible failures.
// The context is checked before every attempt, including the first. If it is
// done, Policy returns that error, joined with the last attempt error when one
// exists. The same join is returned when the context expires during the wait.
// After an attempt returns, a done context stops the retry before Retryable
// and before OnRetry.
//
// When attempts remain and Retryable allows it, OnRetry runs and then Policy
// waits. The wait uses a timer and ctx.Done, and stops the timer on both paths.
// Exhausted retryable attempts wrap the last error with [chain.ErrRetriesExhausted].
// A non-retryable error is returned as-is. Panics from next, Retryable, and
// OnRetry propagate unchanged.
func (r *Retry) Policy() chain.Policy {
	maxAttempts := r.maxAttempts
	base := r.base
	maxDelay := r.maxDelay
	retryable := r.retryable
	onRetry := r.onRetry
	return func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				if err := ctx.Err(); err != nil {
					return joinCancel(err, lastErr)
				}
				err := next(ctx)
				if err == nil {
					return nil
				}
				lastErr = err
				if err := ctx.Err(); err != nil {
					return joinCancel(err, lastErr)
				}
				if !retryable(lastErr) {
					return lastErr
				}
				if attempt == maxAttempts {
					return fmt.Errorf("%w: %w", chain.ErrRetriesExhausted, lastErr)
				}
				delay := jitter(cappedDelay(base, maxDelay, attempt))
				if onRetry != nil {
					onRetry(attempt, lastErr, delay)
				}
				if err := wait(ctx, delay); err != nil {
					return joinCancel(err, lastErr)
				}
			}
			return fmt.Errorf("%w: %w", chain.ErrRetriesExhausted, lastErr)
		}
	}
}

func joinCancel(ctxErr, lastErr error) error {
	if lastErr == nil {
		return ctxErr
	}
	return fmt.Errorf("%w (last error: %w)", ctxErr, lastErr)
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
