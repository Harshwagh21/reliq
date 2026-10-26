package chain

import "context"

// Do calls fn under policies, outermost first. The first policy wraps every
// policy after it.
//
// Nil checks run in order: ctx, then fn, then each policy. A nil returns
// [ErrNilArgument] and does not call fn. If ctx is already done, Do returns
// that error as-is and does not call fn or build the chain.
func Do(ctx context.Context, fn Func, policies ...Policy) error {
	if ctx == nil {
		return ErrNilArgument
	}
	if fn == nil {
		return ErrNilArgument
	}
	for _, p := range policies {
		if p == nil {
			return ErrNilArgument
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	call := fn
	for i := len(policies) - 1; i >= 0; i-- {
		call = policies[i](call)
	}
	return call(ctx)
}

// Get calls fn under policies and returns its value. Policies are applied the
// same way as [Do]. On any error the returned T is the zero value, even when
// fn also returned a value. A successful retry returns the value from the
// successful attempt.
func Get[T any](ctx context.Context, fn func(context.Context) (T, error), policies ...Policy) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrNilArgument
	}
	if fn == nil {
		return zero, ErrNilArgument
	}
	var result T
	err := Do(ctx, func(ctx context.Context) error {
		var err error
		result, err = fn(ctx)
		return err
	}, policies...)
	if err != nil {
		return zero, err
	}
	return result, nil
}
