package chain

import "context"

// Func is one dependency call.
type Func func(ctx context.Context) error

// Policy wraps next. The first policy passed to [Do] or [Get] is outermost.
type Policy func(next Func) Func
