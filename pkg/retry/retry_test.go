package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

// New rejects MaxAttempts below 1, BaseDelay that is not positive, and MaxDelay below BaseDelay.
func TestNewValidation(t *testing.T) {
	validDelay := time.Millisecond
	tests := []struct {
		name  string
		c     Config
		field string
	}{
		{
			name:  "MaxAttempts zero",
			c:     Config{MaxAttempts: 0, BaseDelay: validDelay, MaxDelay: validDelay},
			field: "MaxAttempts",
		},
		{
			name:  "MaxAttempts negative",
			c:     Config{MaxAttempts: -1, BaseDelay: validDelay, MaxDelay: validDelay},
			field: "MaxAttempts",
		},
		{
			name:  "BaseDelay zero",
			c:     Config{MaxAttempts: 1, BaseDelay: 0, MaxDelay: validDelay},
			field: "BaseDelay",
		},
		{
			name:  "BaseDelay negative",
			c:     Config{MaxAttempts: 1, BaseDelay: -time.Nanosecond, MaxDelay: validDelay},
			field: "BaseDelay",
		},
		{
			name:  "MaxDelay below BaseDelay",
			c:     Config{MaxAttempts: 1, BaseDelay: validDelay, MaxDelay: validDelay - time.Nanosecond},
			field: "MaxDelay",
		},
		{
			name:  "MaxDelay negative",
			c:     Config{MaxAttempts: 1, BaseDelay: validDelay, MaxDelay: -time.Nanosecond},
			field: "MaxDelay",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.c)
			if got != nil {
				t.Fatalf("Retry = %#v, want nil", got)
			}
			if !errors.Is(err, chain.ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), tt.field+":") {
				t.Fatalf("err = %q, want field %s", err, tt.field)
			}
		})
	}
}

// New accepts MaxAttempts 1 with MaxDelay equal to the smallest positive BaseDelay.
func TestNewAcceptsBoundaries(t *testing.T) {
	got, err := New(Config{MaxAttempts: 1, BaseDelay: 1, MaxDelay: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy() == nil {
		t.Fatal("Policy() = nil")
	}
}

// The caller's Config is copied at New.
func TestNewCopiesConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := Config{MaxAttempts: 1, BaseDelay: time.Hour, MaxDelay: time.Hour}
		r := mustNew(t, cfg)
		cfg.MaxAttempts = 5
		cfg.Retryable = func(error) bool { return false }
		dep := errors.New("dep")
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return dep
		}, r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, dep) {
			t.Fatalf("err = %v", err)
		}
	})
}

// Persistent failure uses every attempt and wraps the last error as retries exhausted.
func TestPersistentFailureExhausted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			maxAttempts = 3
			base        = time.Second
			maxDelay    = time.Minute
		)
		dep := errors.New("dep")
		var attempts []int
		r := mustNew(t, Config{
			MaxAttempts: maxAttempts,
			BaseDelay:   base,
			MaxDelay:    maxDelay,
			OnRetry: func(attempt int, err error, delay time.Duration) {
				attempts = append(attempts, attempt)
				if err != dep {
					t.Fatalf("OnRetry err = %v, want %v", err, dep)
				}
				limit := cappedDelay(base, maxDelay, attempt)
				if delay < 0 || delay > limit {
					t.Fatalf("delay = %v, want [0, %v]", delay, limit)
				}
			},
		})
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return dep
		}, r.Policy())
		if calls != maxAttempts {
			t.Fatalf("fn calls = %d, want %d", calls, maxAttempts)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, dep) {
			t.Fatalf("err = %v", err)
		}
		wantText := fmt.Errorf("%w: %w", chain.ErrRetriesExhausted, dep).Error()
		if err.Error() != wantText {
			t.Fatalf("err = %q, want %q", err, wantText)
		}
		if len(attempts) != maxAttempts-1 || attempts[0] != 1 || attempts[1] != 2 {
			t.Fatalf("OnRetry attempts = %v, want [1 2]", attempts)
		}
	})
}

// Success on the second attempt does not start another attempt.
func TestSuccessOnSecondAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		onRetry := 0
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Second,
			MaxDelay:    time.Minute,
			OnRetry: func(attempt int, err error, delay time.Duration) {
				onRetry++
				if attempt != 1 || err == nil || delay < 0 {
					t.Fatalf("OnRetry(%d, %v, %v)", attempt, err, delay)
				}
			},
		})
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("fail")
			}
			return nil
		}, r.Policy())
		if err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("fn calls = %d, want 2", calls)
		}
		if onRetry != 1 {
			t.Fatalf("OnRetry calls = %d, want 1", onRetry)
		}
	})
}

// MaxAttempts 1 does not wait or call OnRetry.
func TestMaxAttemptsOneNoWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dep := errors.New("dep")
		onRetry := 0
		r := mustNew(t, Config{
			MaxAttempts: 1,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			OnRetry:     func(int, error, time.Duration) { onRetry++ },
		})
		calls := 0
		start := time.Now()
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return dep
		}, r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if onRetry != 0 {
			t.Fatalf("OnRetry calls = %d, want 0", onRetry)
		}
		if time.Since(start) != 0 {
			t.Fatalf("elapsed = %v, want 0", time.Since(start))
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, dep) {
			t.Fatalf("err = %v", err)
		}
		synctest.Wait()
		if time.Since(start) != 0 {
			t.Fatal("clock advanced after return")
		}
	})
}

// A non-retryable error is returned as-is.
func TestNonRetryableReturnedAsIs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dep := errors.New("dep")
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable:   func(error) bool { return false },
		})
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return dep
		}, r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if err != dep {
			t.Fatalf("err = %v, want %v", err, dep)
		}
		if errors.Is(err, chain.ErrRetriesExhausted) {
			t.Fatal("non-retryable error was wrapped")
		}
	})
}

// The default predicate does not retry cancellation or admission errors.
func TestDefaultPredicateNoRetry(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		maxAttempts int
	}{
		{name: "canceled", err: context.Canceled, maxAttempts: 3},
		{name: "circuit open", err: chain.ErrCircuitOpen, maxAttempts: 3},
		{name: "bulkhead full", err: chain.ErrBulkheadFull, maxAttempts: 3},
		{name: "rate limited", err: chain.ErrRateLimited, maxAttempts: 3},
		{name: "canceled on the only attempt", err: context.Canceled, maxAttempts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := mustNew(t, Config{
					MaxAttempts: tt.maxAttempts,
					BaseDelay:   time.Hour,
					MaxDelay:    time.Hour,
				})
				calls := 0
				err := chain.Do(context.Background(), func(context.Context) error {
					calls++
					return tt.err
				}, r.Policy())
				if calls != 1 {
					t.Fatalf("fn calls = %d, want 1", calls)
				}
				if err != tt.err {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				if errors.Is(err, chain.ErrRetriesExhausted) {
					t.Fatal("non-retryable error was wrapped")
				}
			})
		})
	}
}

// DeadlineExceeded from a per-attempt timeout is retried.
func TestPerAttemptTimeoutRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const maxAttempts = 3
		tm, err := timeout.New(time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		r := mustNew(t, Config{
			MaxAttempts: maxAttempts,
			BaseDelay:   time.Millisecond,
			MaxDelay:    time.Millisecond,
		})
		calls := 0
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		}, r.Policy(), tm.Policy())
		if calls != maxAttempts {
			t.Fatalf("fn calls = %d, want %d", calls, maxAttempts)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	})
}

// An outer timeout stops retry and joins the context error with the last attempt error.
func TestOuterTimeoutJoinsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const outer = 50 * time.Millisecond
		tm, err := timeout.New(outer)
		if err != nil {
			t.Fatal(err)
		}
		dep := errors.New("dep")
		consulted := false
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable: func(error) bool {
				consulted = true
				return true
			},
		})
		calls := 0
		start := time.Now()
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return dep
		}, tm.Policy(), r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if consulted {
			t.Fatal("Retryable consulted after the context was done")
		}
		if time.Since(start) != outer {
			t.Fatalf("elapsed = %v, want %v", time.Since(start), outer)
		}
		assertJoined(t, err, context.DeadlineExceeded, dep)
	})
}

// Cancellation after OnRetry has run does not start the next attempt.
func TestCancelDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dep := errors.New("dep")
		onRetry := 0
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			OnRetry: func(int, error, time.Duration) {
				onRetry++
				cancel()
			},
		})
		calls := 0
		start := time.Now()
		err := chain.Do(ctx, func(context.Context) error {
			calls++
			return dep
		}, r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if onRetry != 1 {
			t.Fatalf("OnRetry calls = %d, want 1", onRetry)
		}
		if time.Since(start) != 0 {
			t.Fatalf("elapsed = %v, want 0", time.Since(start))
		}
		assertJoined(t, err, context.Canceled, dep)
	})
}

// Cancellation observed before a retry is scheduled does not call OnRetry.
func TestCancelBeforeScheduling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dep := errors.New("dep")
		onRetry := 0
		consulted := false
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable: func(error) bool {
				consulted = true
				return true
			},
			OnRetry: func(int, error, time.Duration) { onRetry++ },
		})
		calls := 0
		err := chain.Do(ctx, func(context.Context) error {
			calls++
			cancel()
			return dep
		}, r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if onRetry != 0 || consulted {
			t.Fatalf("OnRetry = %d, Retryable consulted = %v", onRetry, consulted)
		}
		assertJoined(t, err, context.Canceled, dep)
	})
}

// Cancellation observed before the first attempt does not call fn.
func TestCancelBeforeFirstAttempt(t *testing.T) {
	r := mustNew(t, Config{MaxAttempts: 3, BaseDelay: time.Hour, MaxDelay: time.Hour})
	outer := chain.Policy(func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			cancel()
			return next(ctx)
		}
	})
	calls := 0
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return nil
	}, outer, r.Policy())
	if calls != 0 {
		t.Fatalf("fn calls = %d, want 0", calls)
	}
	if err != context.Canceled {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

// An ordinary error returned after the retry context has expired is not retried.
func TestIgnoreCancelReturnsJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tm, err := timeout.New(time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		dep := errors.New("ordinary")
		consulted := false
		onRetry := 0
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable: func(error) bool {
				consulted = true
				return true
			},
			OnRetry: func(int, error, time.Duration) { onRetry++ },
		})
		calls := 0
		err = chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			fakeSleep(time.Hour)
			if ctx.Err() == nil {
				t.Fatal("retry context still live")
			}
			return dep
		}, tm.Policy(), r.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if onRetry != 0 || consulted {
			t.Fatalf("OnRetry = %d, Retryable consulted = %v", onRetry, consulted)
		}
		assertJoined(t, err, context.DeadlineExceeded, dep)
	})
}

// Backoff is min(MaxDelay, BaseDelay*2^(k-1)) and does not overflow.
func TestCappedDelay(t *testing.T) {
	base := time.Millisecond
	maxDelay := time.Hour
	tests := []struct {
		k    int
		want time.Duration
	}{
		{k: 1, want: base},
		{k: 2, want: 2 * base},
		{k: 3, want: 4 * base},
		{k: 11, want: 1024 * base},
	}
	for _, tt := range tests {
		if got := cappedDelay(base, maxDelay, tt.k); got != tt.want {
			t.Fatalf("k=%d delay = %v, want %v", tt.k, got, tt.want)
		}
	}
	if got := cappedDelay(base, base, 1); got != base {
		t.Fatalf("k=1 at cap = %v, want %v", got, base)
	}
	if got := cappedDelay(100*time.Millisecond, 150*time.Millisecond, 2); got != 150*time.Millisecond {
		t.Fatalf("clamp = %v, want 150ms", got)
	}
	if got := cappedDelay(time.Hour, 2*time.Hour, 63); got != 2*time.Hour {
		t.Fatalf("k=63 = %v, want 2h", got)
	}
	if got := cappedDelay(time.Second, time.Minute, 1000); got != time.Minute {
		t.Fatalf("k=1000 = %v, want 1m", got)
	}
}

// Full jitter stays inside the closed interval from zero through the capped delay.
func TestJitterBounds(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Fatalf("jitter(0) = %v, want 0", got)
	}
	const samples = 32
	const delay = 10 * time.Millisecond
	for range samples {
		got := jitter(delay)
		if got < 0 || got > delay {
			t.Fatalf("jitter = %v, want [0, %v]", got, delay)
		}
	}
	limit := time.Duration(math.MaxInt64)
	for range samples {
		got := jitter(limit)
		if got < 0 || got > limit {
			t.Fatalf("jitter(MaxInt64) = %v, want [0, MaxInt64]", got)
		}
	}
}

// A panic from Retryable propagates unchanged.
func TestRetryablePanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "retryable"
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable:   func(error) bool { panic(boom) },
		})
		calls := 0
		defer func() {
			got := recover()
			if got != boom {
				t.Fatalf("panic = %v, want %q", got, boom)
			}
			if calls != 1 {
				t.Fatalf("fn calls = %d, want 1", calls)
			}
		}()
		_ = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return errors.New("dep")
		}, r.Policy())
		t.Fatal("Do returned")
	})
}

// A panic from OnRetry propagates unchanged.
func TestOnRetryPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "onretry"
		r := mustNew(t, Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			OnRetry:     func(int, error, time.Duration) { panic(boom) },
		})
		calls := 0
		defer func() {
			got := recover()
			if got != boom {
				t.Fatalf("panic = %v, want %q", got, boom)
			}
			if calls != 1 {
				t.Fatalf("fn calls = %d, want 1", calls)
			}
		}()
		_ = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return errors.New("dep")
		}, r.Policy())
		t.Fatal("Do returned")
	})
}

// A canceled wait stops the timer and returns without advancing the clock.
func TestWaitStopsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel()
		start := time.Now()
		err := wait(ctx, time.Hour)
		if err != context.Canceled {
			t.Fatalf("err = %v, want Canceled", err)
		}
		if time.Since(start) != 0 {
			t.Fatalf("elapsed = %v, want 0", time.Since(start))
		}
		synctest.Wait()
		if time.Since(start) != 0 {
			t.Fatal("timer still pending")
		}
	})
}

// OnRetry from concurrent Do calls only touches its own atomic state.
func TestOnRetryConcurrent(t *testing.T) {
	const n = 32
	var retries atomic.Int32
	r := mustNew(t, Config{
		MaxAttempts: 2,
		BaseDelay:   1,
		MaxDelay:    1,
		OnRetry: func(int, error, time.Duration) {
			retries.Add(1)
		},
	})
	policy := r.Policy()
	var calls atomic.Int32
	var bad atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			err := chain.Do(context.Background(), func(context.Context) error {
				calls.Add(1)
				return errors.New("dep")
			}, policy)
			if !errors.Is(err, chain.ErrRetriesExhausted) {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("error count = %d, want 0", bad.Load())
	}
	if got := calls.Load(); got != n*2 {
		t.Fatalf("fn calls = %d, want %d", got, n*2)
	}
	if got := retries.Load(); got != n {
		t.Fatalf("OnRetry calls = %d, want %d", got, n)
	}
}

func mustNew(t *testing.T, c Config) *Retry {
	t.Helper()
	r, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func assertJoined(t *testing.T, err, ctxErr, last error) {
	t.Helper()
	if !errors.Is(err, ctxErr) || !errors.Is(err, last) {
		t.Fatalf("err = %v, want %v and %v", err, ctxErr, last)
	}
	want := fmt.Errorf("%w (last error: %w)", ctxErr, last).Error()
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

// fakeSleep advances the synctest bubble clock by d.
// synctest.Sleep exists only in Go 1.27; time.Sleep plus Wait is that step on Go 1.26.
func fakeSleep(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}
