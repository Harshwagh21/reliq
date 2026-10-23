package ratelimit_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/ratelimit"
)

// New rejects a non-finite or non-positive rate and a burst below 1, and names that field.
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name  string
		rate  float64
		burst int
		field string
	}{
		{name: "NaN", rate: math.NaN(), burst: 1, field: "ratePerSecond"},
		{name: "+Inf", rate: math.Inf(1), burst: 1, field: "ratePerSecond"},
		{name: "-Inf", rate: math.Inf(-1), burst: 1, field: "ratePerSecond"},
		{name: "zero", rate: 0, burst: 1, field: "ratePerSecond"},
		{name: "negative", rate: -1, burst: 1, field: "ratePerSecond"},
		{name: "burst zero", rate: 1, burst: 0, field: "burst"},
		{name: "burst negative", rate: 1, burst: -1, field: "burst"},
		{name: "both invalid", rate: math.NaN(), burst: 0, field: "ratePerSecond"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ratelimit.New(tt.rate, tt.burst)
			if got != nil {
				t.Fatalf("Limiter = %#v, want nil", got)
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

// New accepts burst 1 with a positive finite rate.
func TestNewAcceptsBurstOne(t *testing.T) {
	got, err := ratelimit.New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy() == nil {
		t.Fatal("Policy() = nil")
	}
}

// A full bucket admits burst calls and rejects the next one.
func TestStartsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const burst = 3
		l := mustNew(t, 1, burst)
		policy := l.Policy()
		var calls atomic.Int32
		for range burst {
			if err := chain.Do(context.Background(), func(context.Context) error {
				calls.Add(1)
				return nil
			}, policy); err != nil {
				t.Fatal(err)
			}
		}
		err := chain.Do(context.Background(), func(context.Context) error {
			calls.Add(1)
			return nil
		}, policy)
		if !errors.Is(err, chain.ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
		if got := calls.Load(); got != burst {
			t.Fatalf("fn calls = %d, want %d", got, burst)
		}
	})
}

// Refill follows elapsed time, including a fraction that is not yet one token.
func TestRefillProportional(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := mustNew(t, 10, 1)
		policy := l.Policy()
		var calls atomic.Int32
		fn := func(context.Context) error {
			calls.Add(1)
			return nil
		}
		if err := chain.Do(context.Background(), fn, policy); err != nil {
			t.Fatal(err)
		}
		fakeSleep(50 * time.Millisecond)
		err := chain.Do(context.Background(), fn, policy)
		if !errors.Is(err, chain.ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("fn calls = %d, want 1", got)
		}
		fakeSleep(50 * time.Millisecond)
		if err := chain.Do(context.Background(), fn, policy); err != nil {
			t.Fatalf("after 100ms: %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("fn calls = %d, want 2", got)
		}
	})
}

// A long idle refills at most burst tokens.
func TestIdleCappedAtBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const burst = 4
		l := mustNew(t, 1, burst)
		policy := l.Policy()
		for range burst {
			if err := chain.Do(context.Background(), func(context.Context) error {
				return nil
			}, policy); err != nil {
				t.Fatal(err)
			}
		}
		fakeSleep(time.Hour)
		var calls atomic.Int32
		for range burst {
			if err := chain.Do(context.Background(), func(context.Context) error {
				calls.Add(1)
				return nil
			}, policy); err != nil {
				t.Fatal(err)
			}
		}
		err := chain.Do(context.Background(), func(context.Context) error {
			calls.Add(1)
			return nil
		}, policy)
		if !errors.Is(err, chain.ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
		if got := calls.Load(); got != burst {
			t.Fatalf("fn calls = %d, want %d", got, burst)
		}
	})
}

// A token taken before fn is not returned when fn fails.
func TestTokenNotRefunded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := mustNew(t, 1, 1)
		policy := l.Policy()
		dep := errors.New("dependency")
		var calls atomic.Int32
		err := chain.Do(context.Background(), func(context.Context) error {
			calls.Add(1)
			return dep
		}, policy)
		if !errors.Is(err, dep) {
			t.Fatalf("err = %v, want %v", err, dep)
		}
		err = chain.Do(context.Background(), func(context.Context) error {
			calls.Add(1)
			return nil
		}, policy)
		if !errors.Is(err, chain.ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("fn calls = %d, want 1", got)
		}
	})
}

// Concurrent callers with no elapsed time admit exactly burst calls.
func TestConcurrentAdmittedBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const burst = 5
		const n = 32
		l := mustNew(t, 1, burst)
		policy := l.Policy()
		var calls atomic.Int32
		var admitted atomic.Int32
		var rejected atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				err := chain.Do(context.Background(), func(context.Context) error {
					calls.Add(1)
					return nil
				}, policy)
				if err == nil {
					admitted.Add(1)
					return
				}
				if errors.Is(err, chain.ErrRateLimited) {
					rejected.Add(1)
					return
				}
				t.Errorf("err = %v", err)
			}()
		}
		wg.Wait()
		if got := admitted.Load(); got != burst {
			t.Fatalf("admitted = %d, want %d", got, burst)
		}
		if got := calls.Load(); got != burst {
			t.Fatalf("fn calls = %d, want %d", got, burst)
		}
		if got := rejected.Load(); got != n-burst {
			t.Fatalf("rejected = %d, want %d", got, n-burst)
		}
	})
}

// Two limiters do not share a bucket.
func TestLimitersIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := mustNew(t, 1, 1)
		b := mustNew(t, 1, 1)
		if err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, a.Policy()); err != nil {
			t.Fatal(err)
		}
		err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, a.Policy())
		if !errors.Is(err, chain.ErrRateLimited) {
			t.Fatalf("first limiter err = %v, want ErrRateLimited", err)
		}
		if err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, b.Policy()); err != nil {
			t.Fatalf("second limiter: %v", err)
		}
	})
}

func mustNew(t *testing.T, rate float64, burst int) *ratelimit.Limiter {
	t.Helper()
	l, err := ratelimit.New(rate, burst)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// fakeSleep advances the synctest bubble clock by d.
// synctest.Sleep exists only in Go 1.27; time.Sleep plus Wait is that step on Go 1.26.
func fakeSleep(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}
