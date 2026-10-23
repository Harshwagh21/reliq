package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Config is the circuit breaker configuration copied by [New].
// Later changes to the caller's value have no effect.
type Config struct {
	// FailureThreshold is the number of consecutive failures that open the
	// circuit. It must be at least 1.
	FailureThreshold int

	// OpenTimeout is how long an open circuit rejects calls before the next
	// call is allowed as a half-open probe. It must be greater than zero.
	OpenTimeout time.Duration

	// IsFailure reports whether an error counts as a failure.
	// A nil IsFailure uses the default predicate.
	IsFailure func(error) bool

	// OnStateChange is called after a state transition, with the lock released.
	// A nil OnStateChange is not called.
	OnStateChange func(from, to State)
}

// Breaker is a consecutive-failure circuit breaker.
// A Breaker is safe for concurrent use.
type Breaker struct {
	mu            sync.Mutex
	threshold     int
	openTimeout   time.Duration
	isFailure     func(error) bool
	onStateChange func(from, to State)

	state      State
	failures   int
	generation uint64
	openedAt   time.Time
	probing    bool
}

type outcome int

const (
	outcomeSuccess outcome = iota
	outcomeNeutral
	outcomeFailure
)

// New copies c and validates it.
// FailureThreshold must be at least 1 and OpenTimeout must be greater than
// zero. Otherwise New returns an error wrapping [chain.ErrInvalidConfig] that
// names the field.
// A nil IsFailure counts any error other than context.Canceled as a failure.
func New(c Config) (*Breaker, error) {
	if c.FailureThreshold < 1 {
		return nil, fmt.Errorf("circuitbreaker: FailureThreshold: %w", chain.ErrInvalidConfig)
	}
	if c.OpenTimeout <= 0 {
		return nil, fmt.Errorf("circuitbreaker: OpenTimeout: %w", chain.ErrInvalidConfig)
	}
	isFailure := c.IsFailure
	if isFailure == nil {
		isFailure = defaultIsFailure
	}
	return &Breaker{
		threshold:     c.FailureThreshold,
		openTimeout:   c.OpenTimeout,
		isFailure:     isFailure,
		onStateChange: c.OnStateChange,
	}, nil
}

func defaultIsFailure(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled)
}

// State returns the current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Policy wraps next with the breaker.
// An open circuit returns [chain.ErrCircuitOpen] without calling next.
// After OpenTimeout, the next call is one half-open probe. Admitted calls
// remember the generation at admission; a mismatch at completion drops the
// result. A nil error is success. An error that matches the done context is
// neutral. Otherwise IsFailure decides.
// A panic or runtime.Goexit from next counts as a failure, and the panic value
// propagates. A panic from IsFailure or OnStateChange propagates and is not a
// failure. OnStateChange runs after the lock is released.
func (b *Breaker) Policy() chain.Policy {
	return func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			return b.do(ctx, next)
		}
	}
}

func (b *Breaker) do(ctx context.Context, next chain.Func) error {
	generation, opened, err := b.admit()
	if err != nil {
		return err
	}
	if opened {
		entered := false
		defer func() {
			if !entered {
				b.dropProbe(generation)
			}
		}()
		b.fire(StateOpen, StateHalfOpen)
		entered = true
	}
	return b.run(ctx, generation, next)
}

func (b *Breaker) admit() (generation uint64, opened bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		return b.generation, false, nil
	case StateOpen:
		if time.Since(b.openedAt) < b.openTimeout {
			return 0, false, chain.ErrCircuitOpen
		}
		b.state = StateHalfOpen
		b.generation++
		b.probing = true
		return b.generation, true, nil
	case StateHalfOpen:
		if b.probing {
			return 0, false, chain.ErrCircuitOpen
		}
		b.probing = true
		return b.generation, false, nil
	default:
		return 0, false, chain.ErrCircuitOpen
	}
}

func (b *Breaker) run(ctx context.Context, generation uint64, next chain.Func) error {
	returned := false
	classified := false
	defer func() {
		if !returned {
			b.finish(generation, outcomeFailure)
			return
		}
		if !classified {
			b.dropProbe(generation)
		}
	}()
	err := next(ctx)
	returned = true
	result := b.classify(ctx, err)
	classified = true
	b.finish(generation, result)
	return err
}

func (b *Breaker) classify(ctx context.Context, err error) outcome {
	if err == nil {
		return outcomeSuccess
	}
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return outcomeNeutral
	}
	if b.isFailure(err) {
		return outcomeFailure
	}
	return outcomeSuccess
}

func (b *Breaker) finish(generation uint64, result outcome) {
	b.mu.Lock()
	from, to, notify := b.finishLocked(generation, result)
	b.mu.Unlock()
	if notify {
		b.fire(from, to)
	}
}

func (b *Breaker) finishLocked(generation uint64, result outcome) (from, to State, notify bool) {
	if b.generation != generation {
		return 0, 0, false
	}
	switch b.state {
	case StateClosed:
		switch result {
		case outcomeSuccess:
			b.failures = 0
		case outcomeFailure:
			b.failures++
			if b.failures == b.threshold {
				b.enterOpen()
				return StateClosed, StateOpen, true
			}
		}
	case StateHalfOpen:
		switch result {
		case outcomeNeutral:
			b.probing = false
		case outcomeSuccess:
			b.enterClosed()
			return StateHalfOpen, StateClosed, true
		case outcomeFailure:
			b.enterOpen()
			return StateHalfOpen, StateOpen, true
		}
	}
	return 0, 0, false
}

func (b *Breaker) enterOpen() {
	b.state = StateOpen
	b.generation++
	b.openedAt = time.Now()
	b.probing = false
	b.failures = 0
}

func (b *Breaker) enterClosed() {
	b.state = StateClosed
	b.generation++
	b.probing = false
	b.failures = 0
}

func (b *Breaker) dropProbe(generation uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.generation != generation || b.state != StateHalfOpen {
		return
	}
	b.probing = false
}

func (b *Breaker) fire(from, to State) {
	if b.onStateChange != nil {
		b.onStateChange(from, to)
	}
}
