package bulkhead

import (
	"context"
	"fmt"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Bulkhead limits how many calls run at the same time.
// A Bulkhead is safe for concurrent use.
type Bulkhead struct {
	sem chan struct{}
}

// New returns a Bulkhead that admits at most maxConcurrent calls at once.
// maxConcurrent must be at least 1. Otherwise New returns an error wrapping
// [chain.ErrInvalidConfig] that names maxConcurrent.
func New(maxConcurrent int) (*Bulkhead, error) {
	if maxConcurrent < 1 {
		return nil, fmt.Errorf("bulkhead: maxConcurrent: %w", chain.ErrInvalidConfig)
	}
	return &Bulkhead{sem: make(chan struct{}, maxConcurrent)}, nil
}

// Policy wraps next with the bulkhead limit.
// When every permit is in use, Policy returns [chain.ErrBulkheadFull] and does
// not call next. The permit is released on every return path, including panic
// and runtime.Goexit.
func (b *Bulkhead) Policy() chain.Policy {
	sem := b.sem
	return func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			select {
			case sem <- struct{}{}:
			default:
				return chain.ErrBulkheadFull
			}
			defer release(sem)
			return next(ctx)
		}
	}
}

func release(sem chan struct{}) {
	<-sem
}
