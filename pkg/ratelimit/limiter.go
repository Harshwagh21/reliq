package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Limiter is a token bucket.
// A Limiter is safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// New returns a Limiter that refills ratePerSecond tokens, capped at burst.
// The bucket starts full. ratePerSecond must be finite and greater than zero.
// burst must be at least 1. Otherwise New returns an error wrapping
// [chain.ErrInvalidConfig] that names the field.
func New(ratePerSecond float64, burst int) (*Limiter, error) {
	if ratePerSecond <= 0 || math.IsNaN(ratePerSecond) || math.IsInf(ratePerSecond, 0) {
		return nil, fmt.Errorf("ratelimit: ratePerSecond: %w", chain.ErrInvalidConfig)
	}
	if burst < 1 {
		return nil, fmt.Errorf("ratelimit: burst: %w", chain.ErrInvalidConfig)
	}
	return &Limiter{
		rate:   ratePerSecond,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   time.Now(),
	}, nil
}

// Policy takes one token and then calls next.
// The token is not returned. When the bucket has less than one token, Policy
// returns [chain.ErrRateLimited] and does not call next.
func (l *Limiter) Policy() chain.Policy {
	return func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			if !l.take() {
				return chain.ErrRateLimited
			}
			return next(ctx)
		}
	}
}

func (l *Limiter) take() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens += float64(elapsed.Nanoseconds()) * l.rate / 1e9
		if l.tokens > l.burst || math.IsInf(l.tokens, 0) {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
