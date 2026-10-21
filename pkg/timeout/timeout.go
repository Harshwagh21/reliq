package timeout

import (
	"context"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Timeout cancels a call after the duration passed to [New].
type Timeout struct {
	d time.Duration
}

// New returns a Timeout of duration d.
// d must be greater than zero. Otherwise New returns an error wrapping
// [chain.ErrInvalidConfig] that names d.
func New(d time.Duration) (*Timeout, error) {
	if d <= 0 {
		return nil, fmt.Errorf("timeout: d: %w", chain.ErrInvalidConfig)
	}
	return &Timeout{d: d}, nil
}

// Policy wraps next with [context.WithTimeout] for the duration from [New].
// cancel is deferred on every exit, including panic and runtime.Goexit.
// An earlier parent deadline is left unchanged. The value returned by next,
// including a late success or error, is returned unchanged.
func (t *Timeout) Policy() chain.Policy {
	d := t.d
	return func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx)
		}
	}
}
