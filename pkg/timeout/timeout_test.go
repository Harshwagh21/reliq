package timeout_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

// New rejects d <= 0 with ErrInvalidConfig and names field d.
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
	}{
		{name: "zero", d: 0},
		{name: "negative", d: -time.Nanosecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := timeout.New(tt.d)
			if got != nil {
				t.Fatalf("Timeout = %#v, want nil", got)
			}
			if !errors.Is(err, chain.ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), "d:") {
				t.Fatalf("err = %q, want field d", err)
			}
		})
	}
}

// New accepts the smallest positive duration.
func TestNewAcceptsSmallestPositive(t *testing.T) {
	got, err := timeout.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy() == nil {
		t.Fatal("Policy() = nil")
	}
}

// An already expired parent returns ctx.Err() and calls fn zero times.
func TestExpiredParentZeroCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		tm, err := timeout.New(time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		err = chain.Do(ctx, func(context.Context) error {
			calls++
			return nil
		}, tm.Policy())
		if calls != 0 {
			t.Fatalf("fn calls = %d, want 0", calls)
		}
		if err != ctx.Err() || err != context.DeadlineExceeded {
			t.Fatalf("err = %v, want %v", err, ctx.Err())
		}
	})
}

// A shorter parent deadline is not extended.
func TestParentDeadlineNotExtended(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const parent = 5 * time.Millisecond
		tm, err := timeout.New(time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), parent)
		defer cancel()
		start := time.Now()
		calls := 0
		err = chain.Do(ctx, func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		}, tm.Policy())
		elapsed := time.Since(start)
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if err != context.DeadlineExceeded {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if elapsed != parent {
			t.Fatalf("elapsed = %v, want %v", elapsed, parent)
		}
	})
}

// A function that honors ctx returns context.DeadlineExceeded.
func TestHonorsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const d = time.Second
		tm, err := timeout.New(d)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		start := time.Now()
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		}, tm.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if err != context.DeadlineExceeded {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != d {
			t.Fatalf("elapsed = %v, want %v", elapsed, d)
		}
	})
}

// A function that ignores ctx and returns success after the deadline keeps that success.
func TestIgnoreContextLateSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const d = time.Second
		tm, err := timeout.New(d)
		if err != nil {
			t.Fatal(err)
		}
		sawDeadline := false
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			fakeSleep(d + time.Second)
			sawDeadline = ctx.Err() != nil
			return nil
		}, tm.Policy())
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !sawDeadline {
			t.Fatal("deadline did not fire before return")
		}
	})
}

// A function that ignores ctx and returns an error after the deadline keeps that error.
func TestIgnoreContextLateError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const d = time.Second
		late := errors.New("late")
		tm, err := timeout.New(d)
		if err != nil {
			t.Fatal(err)
		}
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			fakeSleep(d + time.Second)
			if ctx.Err() == nil {
				t.Fatal("deadline did not fire before return")
			}
			return late
		}, tm.Policy())
		if err != late {
			t.Fatalf("err = %v, want %v", err, late)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("late error was rewritten")
		}
	})
}

// After a successful call, the context passed to fn is canceled.
func TestCancelAfterSuccess(t *testing.T) {
	err, innerErr := runCancelled(t, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if innerErr != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want Canceled", innerErr)
	}
}

// After fn returns an error, that error is kept and the context passed to fn is canceled.
func TestCancelAfterError(t *testing.T) {
	dep := errors.New("dep")
	err, innerErr := runCancelled(t, func(context.Context) error { return dep })
	if err != dep {
		t.Fatalf("err = %v, want %v", err, dep)
	}
	if innerErr != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want Canceled", innerErr)
	}
}

// After fn panics, the panic value is unchanged and the context passed to fn is canceled.
func TestCancelAfterPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tm, err := timeout.New(time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		var inner context.Context
		defer func() {
			r := recover()
			if r != "boom" {
				t.Fatalf("panic = %v, want boom", r)
			}
			if inner == nil || inner.Err() != context.Canceled {
				t.Fatalf("ctx.Err() = %v, want Canceled", ctxErr(inner))
			}
		}()
		_ = chain.Do(context.Background(), func(ctx context.Context) error {
			inner = ctx
			panic("boom")
		}, tm.Policy())
		t.Fatal("Do returned")
	})
}

// fakeSleep advances the synctest bubble clock by d.
// synctest.Sleep exists only in Go 1.27; time.Sleep plus Wait is that step on Go 1.26.
func fakeSleep(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func runCancelled(t *testing.T, fn chain.Func) (error, error) {
	t.Helper()
	var callErr, innerErr error
	synctest.Test(t, func(t *testing.T) {
		tm, err := timeout.New(time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		var inner context.Context
		callErr = chain.Do(context.Background(), func(ctx context.Context) error {
			inner = ctx
			return fn(ctx)
		}, tm.Policy())
		innerErr = ctxErr(inner)
	})
	return callErr, innerErr
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}
