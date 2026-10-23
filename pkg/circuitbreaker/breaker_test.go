package circuitbreaker

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

var errDep = errors.New("dep")

// New rejects FailureThreshold below 1 and OpenTimeout that is not positive.
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name  string
		c     Config
		field string
	}{
		{
			name:  "FailureThreshold zero",
			c:     Config{FailureThreshold: 0, OpenTimeout: time.Second},
			field: "FailureThreshold",
		},
		{
			name:  "FailureThreshold negative",
			c:     Config{FailureThreshold: -1, OpenTimeout: time.Second},
			field: "FailureThreshold",
		},
		{
			name:  "OpenTimeout zero",
			c:     Config{FailureThreshold: 1, OpenTimeout: 0},
			field: "OpenTimeout",
		},
		{
			name:  "OpenTimeout negative",
			c:     Config{FailureThreshold: 1, OpenTimeout: -time.Nanosecond},
			field: "OpenTimeout",
		},
		{
			name:  "both invalid names FailureThreshold",
			c:     Config{},
			field: "FailureThreshold",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.c)
			if got != nil {
				t.Fatalf("Breaker = %#v, want nil", got)
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

// New accepts FailureThreshold 1 and the smallest positive OpenTimeout.
func TestNewAcceptsBoundaries(t *testing.T) {
	got, err := New(Config{FailureThreshold: 1, OpenTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy() == nil {
		t.Fatal("Policy() = nil")
	}
	if got.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", got.State())
	}
}

// The caller's Config is copied at New.
func TestNewCopiesConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changes := 0
		cfg := Config{
			FailureThreshold: 1,
			OpenTimeout:      time.Hour,
			OnStateChange: func(State, State) {
				changes++
			},
		}
		b := mustNew(t, cfg)
		cfg.FailureThreshold = 100
		cfg.OpenTimeout = time.Nanosecond
		cfg.IsFailure = func(error) bool { return false }
		cfg.OnStateChange = func(State, State) { t.Fatal("mutated OnStateChange") }
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return errDep
		}, b.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if !errors.Is(err, errDep) {
			t.Fatalf("err = %v", err)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
		if changes != 1 {
			t.Fatalf("OnStateChange calls = %d, want 1", changes)
		}
		fakeSleep(time.Nanosecond)
		err = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, b.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
		fakeSleep(time.Hour - time.Nanosecond)
		err = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, b.Policy())
		if err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("fn calls = %d, want 2", calls)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// Consecutive failures open the circuit and a success resets the count.
func TestConsecutiveFailuresReset(t *testing.T) {
	b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: time.Hour})
	policy := b.Policy()
	for range 2 {
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatalf("err = %v", err)
		}
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatalf("err = %v", err)
		}
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatalf("err = %v", err)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

// An open circuit returns ErrCircuitOpen and does not call fn.
func TestOpenRejects(t *testing.T) {
	b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: time.Hour})
	policy := b.Policy()
	calls := 0
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return errDep
	}, policy)
	if !errors.Is(err, errDep) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
	for range 3 {
		err = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

// OpenTimeout elapsing admits one half-open probe on the next call.
func TestOpenTimeoutAdmitsProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor - time.Nanosecond)
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if calls != 0 || !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		fakeSleep(time.Nanosecond)
		err = chain.Do(context.Background(), func(context.Context) error {
			calls++
			if b.State() != StateHalfOpen {
				t.Fatalf("State() = %s, want HalfOpen", b.State())
			}
			return nil
		}, policy)
		if err != nil || calls != 1 {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// HalfOpen allows one probe when many callers arrive together.
func TestHalfOpenSingleProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 100
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		release := make(chan struct{})
		var inFlight atomic.Int32
		var rejected atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				err := chain.Do(context.Background(), func(context.Context) error {
					inFlight.Add(1)
					<-release
					return nil
				}, policy)
				if errors.Is(err, chain.ErrCircuitOpen) {
					rejected.Add(1)
				}
			}()
		}
		synctest.Wait()
		if got := inFlight.Load(); got != 1 {
			t.Fatalf("in flight = %d, want 1", got)
		}
		if got := rejected.Load(); got != n-1 {
			t.Fatalf("rejected = %d, want %d", got, n-1)
		}
		if b.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", b.State())
		}
		close(release)
		wg.Wait()
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// A successful probe closes the circuit and resets the failure count.
func TestProbeSuccessResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: openFor})
		policy := b.Policy()
		for range 3 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		fakeSleep(openFor)
		if err := call(policy, nil); err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		for range 2 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
	})
}

// A failed probe opens the circuit and restarts OpenTimeout.
func TestProbeFailureRestartsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: openFor})
		policy := b.Policy()
		for range 3 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		fakeSleep(openFor)
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
		fakeSleep(openFor - time.Nanosecond)
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if calls != 0 || !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		fakeSleep(time.Nanosecond)
		err = chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if err != nil || calls != 1 {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// Caller cancellation is neutral and leaves the closed failure count unchanged.
func TestNeutralCallerCancelClosed(t *testing.T) {
	b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: time.Hour})
	policy := b.Policy()
	for range 2 {
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := chain.Do(ctx, func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}, policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

// Caller cancellation during a probe releases the slot and stays HalfOpen.
func TestNeutralCallerCancelHalfOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		consulted := 0
		var events [][2]State
		b := mustNew(t, Config{
			FailureThreshold: 1,
			OpenTimeout:      openFor,
			IsFailure: func(error) bool {
				consulted++
				return true
			},
			OnStateChange: func(from, to State) {
				events = append(events, [2]State{from, to})
			},
		})
		policy := b.Policy()
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if consulted != 1 {
			t.Fatalf("IsFailure calls = %d, want 1", consulted)
		}
		fakeSleep(openFor)
		ctx, cancel := context.WithCancel(context.Background())
		err := chain.Do(ctx, func(ctx context.Context) error {
			if b.State() != StateHalfOpen {
				t.Fatalf("State() = %s, want HalfOpen", b.State())
			}
			cancel()
			return ctx.Err()
		}, policy)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want Canceled", err)
		}
		if consulted != 1 {
			t.Fatalf("IsFailure calls = %d, want 1", consulted)
		}
		if b.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", b.State())
		}
		if err := call(policy, nil); err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		want := [][2]State{
			{StateClosed, StateOpen},
			{StateOpen, StateHalfOpen},
			{StateHalfOpen, StateClosed},
		}
		if len(events) != len(want) {
			t.Fatalf("transitions = %v, want %v", events, want)
		}
		for i := range want {
			if events[i] != want[i] {
				t.Fatalf("transitions = %v, want %v", events, want)
			}
		}
	})
}

// An outer timeout that the call returns is neutral.
func TestNeutralOuterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: time.Hour})
		policy := b.Policy()
		for range 2 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		tm := mustTimeout(t, time.Second)
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, tm.Policy(), policy)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
	})
}

// A late nil from a function that ignores ctx is success.
func TestLateSuccessWhileContextDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const d = time.Second
		b := mustNew(t, Config{FailureThreshold: 3, OpenTimeout: time.Hour})
		policy := b.Policy()
		for range 2 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		tm := mustTimeout(t, d)
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			fakeSleep(d + time.Second)
			if ctx.Err() == nil {
				t.Fatal("deadline did not fire")
			}
			return nil
		}, tm.Policy(), policy)
		if err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// A per-attempt timeout below the breaker counts as a failure.
func TestPerAttemptTimeoutCountsAsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: time.Hour})
		tm := mustTimeout(t, time.Second)
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		}, b.Policy(), tm.Policy())
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
	})
}

// Matching outer and inner deadlines are neutral.
func TestBothDeadlinesNeutral(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const d = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: time.Hour})
		outer := mustTimeout(t, d)
		inner := mustTimeout(t, d)
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, outer.Policy(), b.Policy(), inner.Policy())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// IsFailure is not consulted for a nil error or a neutral outcome.
func TestIsFailureSkipsNeutral(t *testing.T) {
	consulted := 0
	b := mustNew(t, Config{
		FailureThreshold: 1,
		OpenTimeout:      time.Hour,
		IsFailure: func(error) bool {
			consulted++
			return true
		},
	})
	policy := b.Policy()
	if err := call(policy, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := chain.Do(ctx, func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}, policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	if consulted != 0 {
		t.Fatalf("IsFailure calls = %d, want 0", consulted)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if consulted != 1 {
		t.Fatalf("IsFailure calls = %d, want 1", consulted)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

// IsFailure false is success for the breaker.
func TestIsFailureFalseIsSuccess(t *testing.T) {
	n := 0
	b := mustNew(t, Config{
		FailureThreshold: 2,
		OpenTimeout:      time.Hour,
		IsFailure: func(error) bool {
			n++
			return n != 2
		},
	})
	policy := b.Policy()
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
}

// context.Canceled on a live breaker context resets the failure count.
func TestCanceledResetsWhenContextLive(t *testing.T) {
	b := mustNew(t, Config{FailureThreshold: 2, OpenTimeout: time.Hour})
	policy := b.Policy()
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if err := call(policy, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(policy, errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
}

// A slow call admitted in Closed is ignored after the circuit closes again.
func TestStaleClosedCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		errStale := errors.New("stale")
		ready := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- chain.Do(context.Background(), func(context.Context) error {
				close(ready)
				<-release
				return errStale
			}, policy)
		}()
		<-ready
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		if err := call(policy, nil); err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		close(release)
		if err := <-done; !errors.Is(err, errStale) {
			t.Fatalf("err = %v, want %v", err, errStale)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return errDep
		}, policy)
		if calls != 1 || !errors.Is(err, errDep) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
	})
}

// A slow Closed call that finishes during a newer probe does not take that slot.
func TestStaleCallDuringProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		errStale := errors.New("stale")
		slowReady := make(chan struct{})
		slowRelease := make(chan struct{})
		slowDone := make(chan error, 1)
		go func() {
			slowDone <- chain.Do(context.Background(), func(context.Context) error {
				close(slowReady)
				<-slowRelease
				return errStale
			}, policy)
		}()
		<-slowReady
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		probeReady := make(chan struct{})
		probeRelease := make(chan struct{})
		probeDone := make(chan error, 1)
		go func() {
			probeDone <- chain.Do(context.Background(), func(context.Context) error {
				close(probeReady)
				<-probeRelease
				return nil
			}, policy)
		}()
		<-probeReady
		if b.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", b.State())
		}
		close(slowRelease)
		if err := <-slowDone; !errors.Is(err, errStale) {
			t.Fatalf("err = %v, want %v", err, errStale)
		}
		if b.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", b.State())
		}
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if calls != 0 || !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
		close(probeRelease)
		if err := <-probeDone; err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// A half-open probe result is ignored when its generation is no longer current.
func TestStaleHalfOpenProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		ready := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- chain.Do(context.Background(), func(context.Context) error {
				close(ready)
				<-release
				return errDep
			}, policy)
		}()
		<-ready
		b.mu.Lock()
		b.generation++
		b.state = StateClosed
		b.failures = 0
		b.probing = true
		b.mu.Unlock()
		close(release)
		if err := <-done; !errors.Is(err, errDep) {
			t.Fatalf("err = %v, want %v", err, errDep)
		}
		b.mu.Lock()
		state, failures, probing := b.state, b.failures, b.probing
		b.mu.Unlock()
		if state != StateClosed || failures != 0 || !probing {
			t.Fatalf("state = %s, failures = %d, probing = %v", state, failures, probing)
		}
	})
}

// A panic from fn counts as one closed failure.
func TestFnPanic(t *testing.T) {
	const boom = "fn"
	b := mustNew(t, Config{FailureThreshold: 2, OpenTimeout: time.Hour})
	wantPanic(t, b, func(context.Context) error {
		panic(boom)
	}, boom)
	if b.State() != StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	if err := call(b.Policy(), errDep); !errors.Is(err, errDep) {
		t.Fatal(err)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

// A panic from a half-open probe opens the circuit and releases the slot.
func TestFnPanicProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "probe"
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 5, OpenTimeout: openFor})
		policy := b.Policy()
		for range 5 {
			if err := call(policy, errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		fakeSleep(openFor)
		wantPanic(t, b, func(context.Context) error {
			panic(boom)
		}, boom)
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, policy)
		if calls != 0 || !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
	})
}

// runtime.Goexit from fn counts as a failure and releases a held probe.
func TestFnGoexit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		closed := mustNew(t, Config{FailureThreshold: 2, OpenTimeout: openFor})
		if panicked := goexitDo(closed); panicked {
			t.Fatal("Goexit produced a panic")
		}
		if closed.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", closed.State())
		}
		if err := call(closed.Policy(), errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if closed.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", closed.State())
		}

		probe := mustNew(t, Config{FailureThreshold: 5, OpenTimeout: openFor})
		for range 5 {
			if err := call(probe.Policy(), errDep); !errors.Is(err, errDep) {
				t.Fatal(err)
			}
		}
		fakeSleep(openFor)
		if panicked := goexitDo(probe); panicked {
			t.Fatal("Goexit produced a panic")
		}
		if probe.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", probe.State())
		}
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return nil
		}, probe.Policy())
		if calls != 0 || !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
	})
}

// A panic from IsFailure is not a failure and releases a held probe.
func TestIsFailurePanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "isfailure"
		const openFor = time.Second
		errBoom := errors.New("boom")
		isFailure := func(err error) bool {
			if errors.Is(err, errBoom) {
				panic(boom)
			}
			return true
		}
		closed := mustNew(t, Config{
			FailureThreshold: 2,
			OpenTimeout:      openFor,
			IsFailure:        isFailure,
		})
		if err := call(closed.Policy(), errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		wantPanic(t, closed, func(context.Context) error {
			return errBoom
		}, boom)
		if closed.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", closed.State())
		}
		if err := call(closed.Policy(), errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if closed.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", closed.State())
		}

		probe := mustNew(t, Config{
			FailureThreshold: 1,
			OpenTimeout:      openFor,
			IsFailure:        isFailure,
		})
		if err := call(probe.Policy(), errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		calls := 0
		wantPanic(t, probe, func(context.Context) error {
			calls++
			return errBoom
		}, boom)
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if probe.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", probe.State())
		}
		if err := call(probe.Policy(), nil); err != nil {
			t.Fatal(err)
		}
		if probe.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", probe.State())
		}
	})
}

// A panic from OnStateChange leaves the open transition committed.
func TestOnStateChangePanicOpen(t *testing.T) {
	const boom = "open"
	b := mustNew(t, Config{
		FailureThreshold: 1,
		OpenTimeout:      time.Hour,
		OnStateChange: func(State, State) {
			panic(boom)
		},
	})
	calls := 0
	wantPanic(t, b, func(context.Context) error {
		calls++
		return errDep
	}, boom)
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return nil
	}, b.Policy())
	if calls != 1 || !errors.Is(err, chain.ErrCircuitOpen) {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

// A panic from OnStateChange on the way to HalfOpen releases the probe slot.
func TestOnStateChangePanicHalfOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "half"
		const openFor = time.Second
		b := mustNew(t, Config{
			FailureThreshold: 1,
			OpenTimeout:      openFor,
			OnStateChange: func(from, to State) {
				if from == StateOpen && to == StateHalfOpen {
					panic(boom)
				}
			},
		})
		if err := call(b.Policy(), errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		fakeSleep(openFor)
		calls := 0
		wantPanic(t, b, func(context.Context) error {
			calls++
			return nil
		}, boom)
		if calls != 0 {
			t.Fatalf("fn calls = %d, want 0", calls)
		}
		if b.State() != StateHalfOpen {
			t.Fatalf("State() = %s, want HalfOpen", b.State())
		}
		if err := call(b.Policy(), nil); err != nil {
			t.Fatal(err)
		}
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// OnStateChange runs once per transition after the lock is released.
func TestOnStateChangeAfterUnlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const openFor = time.Second
		var got [][2]State
		var b *Breaker
		b = mustNew(t, Config{
			FailureThreshold: 2,
			OpenTimeout:      openFor,
			OnStateChange: func(from, to State) {
				if b.State() != to {
					t.Fatalf("State() = %s, want %s", b.State(), to)
				}
				got = append(got, [2]State{from, to})
			},
		})
		policy := b.Policy()
		if err := call(policy, nil); err != nil {
			t.Fatal(err)
		}
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("transitions = %v, want none", got)
		}
		if err := call(policy, errDep); !errors.Is(err, errDep) {
			t.Fatal(err)
		}
		if err := call(policy, nil); !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
		fakeSleep(openFor)
		err := chain.Do(context.Background(), func(context.Context) error {
			if b.State() != StateHalfOpen {
				t.Fatalf("State() = %s, want HalfOpen", b.State())
			}
			return nil
		}, policy)
		if err != nil {
			t.Fatal(err)
		}
		want := [][2]State{
			{StateClosed, StateOpen},
			{StateOpen, StateHalfOpen},
			{StateHalfOpen, StateClosed},
		}
		if len(got) != len(want) {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("transitions = %v, want %v", got, want)
			}
		}
	})
}

// State.String returns Closed, Open, HalfOpen, or State(n).
func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{state: StateClosed, want: "Closed"},
		{state: StateOpen, want: "Open"},
		{state: StateHalfOpen, want: "HalfOpen"},
		{state: 99, want: "State(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Fatalf("State(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}

// Concurrent closed failures and half-open admission share one breaker.
func TestConcurrentTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 32
		const openFor = time.Second
		b := mustNew(t, Config{FailureThreshold: 1, OpenTimeout: openFor})
		policy := b.Policy()
		release := make(chan struct{})
		var arrived atomic.Int32
		var calls atomic.Int32
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				_ = b.State()
				err := chain.Do(context.Background(), func(context.Context) error {
					calls.Add(1)
					if arrived.Add(1) == int32(n) {
						close(release)
					}
					<-release
					return errDep
				}, policy)
				_ = b.State()
				if !errors.Is(err, errDep) {
					t.Errorf("err = %v", err)
				}
			}()
		}
		wg.Wait()
		if got := calls.Load(); got != n {
			t.Fatalf("fn calls = %d, want %d", got, n)
		}
		if b.State() != StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}

		fakeSleep(openFor)
		probeRelease := make(chan struct{})
		var inFlight atomic.Int32
		var rejected atomic.Int32
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				_ = b.State()
				err := chain.Do(context.Background(), func(context.Context) error {
					inFlight.Add(1)
					<-probeRelease
					return nil
				}, policy)
				if errors.Is(err, chain.ErrCircuitOpen) {
					rejected.Add(1)
				}
			}()
		}
		synctest.Wait()
		if got := inFlight.Load(); got != 1 {
			t.Fatalf("in flight = %d, want 1", got)
		}
		if got := rejected.Load(); got != n-1 {
			t.Fatalf("rejected = %d, want %d", got, n-1)
		}
		close(probeRelease)
		wg.Wait()
		if b.State() != StateClosed {
			t.Fatalf("State() = %s, want Closed", b.State())
		}
	})
}

// OnStateChange from concurrent Do calls only touches its own atomic state.
func TestOnStateChangeConcurrent(t *testing.T) {
	const n = 32
	var changes atomic.Int32
	b := mustNew(t, Config{
		FailureThreshold: 1,
		OpenTimeout:      time.Hour,
		OnStateChange: func(State, State) {
			changes.Add(1)
		},
	})
	policy := b.Policy()
	release := make(chan struct{})
	var arrived atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			err := chain.Do(context.Background(), func(context.Context) error {
				if arrived.Add(1) == int32(n) {
					close(release)
				}
				<-release
				return errDep
			}, policy)
			if !errors.Is(err, errDep) {
				t.Errorf("err = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := changes.Load(); got != 1 {
		t.Fatalf("OnStateChange calls = %d, want 1", got)
	}
	if b.State() != StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
	wg.Add(n)
	var rejected atomic.Int32
	for range n {
		go func() {
			defer wg.Done()
			err := chain.Do(context.Background(), func(context.Context) error {
				t.Error("fn called")
				return nil
			}, policy)
			if errors.Is(err, chain.ErrCircuitOpen) {
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := rejected.Load(); got != n {
		t.Fatalf("rejected = %d, want %d", got, n)
	}
	if got := changes.Load(); got != 1 {
		t.Fatalf("OnStateChange calls = %d, want 1", got)
	}
}

func mustNew(t *testing.T, c Config) *Breaker {
	t.Helper()
	b, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustTimeout(t *testing.T, d time.Duration) *timeout.Timeout {
	t.Helper()
	tm, err := timeout.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func call(policy chain.Policy, ret error) error {
	return chain.Do(context.Background(), func(context.Context) error {
		return ret
	}, policy)
}

func wantPanic(t *testing.T, b *Breaker, fn chain.Func, want any) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %v", got, want)
		}
	}()
	_ = chain.Do(context.Background(), fn, b.Policy())
	t.Fatal("Do returned")
}

func goexitDo(b *Breaker) (panicked bool) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = chain.Do(context.Background(), func(context.Context) error {
			runtime.Goexit()
			return nil
		}, b.Policy())
	}()
	wg.Wait()
	return panicked
}

// fakeSleep advances the synctest bubble clock by d.
// synctest.Sleep exists only in Go 1.27; time.Sleep plus Wait is that step on Go 1.26.
func fakeSleep(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}
