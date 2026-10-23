package bulkhead_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/harshwagh21/reliq/pkg/bulkhead"
	"github.com/harshwagh21/reliq/pkg/chain"
)

// New rejects maxConcurrent below 1 with ErrInvalidConfig and names maxConcurrent.
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "negative", n: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bulkhead.New(tt.n)
			if got != nil {
				t.Fatalf("Bulkhead = %#v, want nil", got)
			}
			if !errors.Is(err, chain.ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), "maxConcurrent:") {
				t.Fatalf("err = %q, want field maxConcurrent", err)
			}
		})
	}
}

// New accepts maxConcurrent 1.
func TestNewAcceptsOne(t *testing.T) {
	got, err := bulkhead.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy() == nil {
		t.Fatal("Policy() = nil")
	}
}

// The call past maxConcurrent returns ErrBulkheadFull and does not run fn.
func TestFullRejectsWithoutCalling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 2
		b := mustNew(t, n)
		policy := b.Policy()
		releaseCh := make(chan struct{})
		var calls atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				err := chain.Do(context.Background(), func(context.Context) error {
					calls.Add(1)
					<-releaseCh
					return nil
				}, policy)
				if err != nil {
					t.Errorf("held call: %v", err)
				}
			}()
		}
		synctest.Wait()
		extra := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			extra++
			return nil
		}, policy)
		if !errors.Is(err, chain.ErrBulkheadFull) {
			t.Fatalf("err = %v, want ErrBulkheadFull", err)
		}
		if extra != 0 {
			t.Fatalf("fn calls on rejected = %d, want 0", extra)
		}
		if got := calls.Load(); got != n {
			t.Fatalf("fn calls = %d, want %d", got, n)
		}
		close(releaseCh)
		wg.Wait()
	})
}

// A successful call releases its permit before the next call.
func TestPermitReleasedOnSuccess(t *testing.T) {
	b := mustNew(t, 1)
	policy := b.Policy()
	for range 2 {
		if err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, policy); err != nil {
			t.Fatal(err)
		}
	}
}

// A failing call releases its permit before the next call.
func TestPermitReleasedOnError(t *testing.T) {
	b := mustNew(t, 1)
	policy := b.Policy()
	dep := errors.New("dependency")
	err := chain.Do(context.Background(), func(context.Context) error {
		return dep
	}, policy)
	if !errors.Is(err, dep) {
		t.Fatalf("err = %v, want %v", err, dep)
	}
	if err := chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, policy); err != nil {
		t.Fatalf("after error: %v", err)
	}
}

// A panicking call releases its permit and keeps the original panic value.
func TestPermitReleasedOnPanic(t *testing.T) {
	b := mustNew(t, 1)
	policy := b.Policy()
	const panicVal = "dependency"
	defer func() {
		if got := recover(); got != panicVal {
			t.Fatalf("panic = %#v, want %q", got, panicVal)
		}
		if err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, policy); err != nil {
			t.Fatalf("after panic: %v", err)
		}
	}()
	_ = chain.Do(context.Background(), func(context.Context) error {
		panic(panicVal)
	}, policy)
}

// A cancelled call releases its permit after it returns.
func TestPermitReleasedOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := mustNew(t, 1)
		policy := b.Policy()
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() {
			errc <- chain.Do(ctx, func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, policy)
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if err := chain.Do(context.Background(), func(context.Context) error {
			return nil
		}, policy); err != nil {
			t.Fatalf("after cancel: %v", err)
		}
	})
}

// After one full wave finishes, a second wave of the same size all run.
func TestCapacityRecoversAfterBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 4
		b := mustNew(t, n)
		policy := b.Policy()
		for wave := range 2 {
			releaseCh := make(chan struct{})
			var calls atomic.Int32
			var wg sync.WaitGroup
			wg.Add(n)
			for range n {
				go func() {
					defer wg.Done()
					err := chain.Do(context.Background(), func(context.Context) error {
						calls.Add(1)
						<-releaseCh
						return nil
					}, policy)
					if err != nil {
						t.Errorf("wave %d: %v", wave, err)
					}
				}()
			}
			synctest.Wait()
			if got := calls.Load(); got != int32(n) {
				t.Fatalf("wave %d in flight = %d, want %d", wave, got, n)
			}
			close(releaseCh)
			wg.Wait()
		}
	})
}

// Concurrent callers never have more than maxConcurrent calls inside fn.
func TestConcurrentStress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 8
		const extra = 24
		b := mustNew(t, n)
		policy := b.Policy()
		releaseCh := make(chan struct{})
		var inFlight atomic.Int32
		var rejected atomic.Int32
		var admitted atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n + extra)
		for range n + extra {
			go func() {
				defer wg.Done()
				err := chain.Do(context.Background(), func(context.Context) error {
					if cur := inFlight.Add(1); cur > int32(n) {
						t.Errorf("in flight = %d, want <= %d", cur, n)
					}
					<-releaseCh
					inFlight.Add(-1)
					return nil
				}, policy)
				if errors.Is(err, chain.ErrBulkheadFull) {
					rejected.Add(1)
					return
				}
				if err != nil {
					t.Errorf("err = %v", err)
					return
				}
				admitted.Add(1)
			}()
		}
		synctest.Wait()
		if got := inFlight.Load(); got != int32(n) {
			t.Fatalf("in flight = %d, want %d", got, n)
		}
		if got := rejected.Load(); got != extra {
			t.Fatalf("rejected = %d, want %d", got, extra)
		}
		close(releaseCh)
		wg.Wait()
		if got := admitted.Load(); got != n {
			t.Fatalf("admitted = %d, want %d", got, n)
		}
		if got := inFlight.Load(); got != 0 {
			t.Fatalf("in flight after release = %d, want 0", got)
		}
	})
}

func mustNew(t *testing.T, n int) *bulkhead.Bulkhead {
	t.Helper()
	b, err := bulkhead.New(n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
