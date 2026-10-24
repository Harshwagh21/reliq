package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/harshwagh21/reliq/pkg/bulkhead"
	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/circuitbreaker"
	"github.com/harshwagh21/reliq/pkg/ratelimit"
	"github.com/harshwagh21/reliq/pkg/retry"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

const breakerThreshold = 5

var errDep = errors.New("dep")

// Persistent failure under the default policy order.
func TestPersistentFailureCounts(t *testing.T) {
	callServer(t, failHandler, func(t *testing.T, client *http.Client, rawURL string) {
		s := newStack(t, stackConf{
			total: time.Hour, perAttempt: 2 * time.Hour,
			attempts: 3, threshold: breakerThreshold,
			delay: time.Millisecond, openFor: time.Hour,
		})
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			return roundTrip(ctx, client, rawURL)
		}, s.policies()...)
		if calls != 3 {
			t.Fatalf("fn calls = %d, want 3", calls)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, errDep) {
			t.Fatalf("err = %v, want ErrRetriesExhausted and dep", err)
		}
		proveTokensEmpty(t, s.limiter)
		proveThresholdFive(t, s.breaker, 3)
		provePermitFree(t, s.bulk)
	})
}

// Breaker threshold 2: the open-circuit attempt is not retried.
func TestThresholdTwoStopsOnOpen(t *testing.T) {
	callServer(t, failHandler, func(t *testing.T, client *http.Client, rawURL string) {
		s := newStack(t, stackConf{
			total: time.Hour, perAttempt: 2 * time.Hour,
			attempts: 3, threshold: 2,
			delay: time.Millisecond, openFor: time.Hour,
		})
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			return roundTrip(ctx, client, rawURL)
		}, s.policies()...)
		if calls != 2 {
			t.Fatalf("fn calls = %d, want 2", calls)
		}
		if !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
		if errors.Is(err, chain.ErrRetriesExhausted) {
			t.Fatal("ErrCircuitOpen was retried to exhaustion")
		}
		if s.breaker.State() != circuitbreaker.StateOpen {
			t.Fatalf("State() = %s, want Open", s.breaker.State())
		}
	})
}

// Total timeout shorter than backoff returns at that deadline.
func TestTotalTimeoutDuringBackoff(t *testing.T) {
	callServer(t, failHandler, func(t *testing.T, client *http.Client, rawURL string) {
		const deadline = time.Millisecond
		for range 8 {
			var calls int
			var decided, positive bool
			var last error
			start := time.Now()
			s := newStack(t, stackConf{
				total: deadline, perAttempt: time.Hour,
				attempts: 3, threshold: breakerThreshold,
				delay: time.Hour, openFor: time.Hour,
				onRetry: func(_ int, _ error, delay time.Duration) {
					if decided {
						return
					}
					decided = true
					if delay == 0 {
						return
					}
					positive = true
					if remain := deadline - time.Since(start); remain > 0 {
						fakeSleep(remain)
					}
				},
			})
			err := chain.Do(context.Background(), func(ctx context.Context) error {
				calls++
				last = roundTrip(ctx, client, rawURL)
				return last
			}, s.policies()...)
			if !positive {
				continue
			}
			if calls != 1 {
				t.Fatalf("fn calls = %d, want 1", calls)
			}
			if got := time.Since(start); got != deadline {
				t.Fatalf("elapsed = %v, want %v", got, deadline)
			}
			assertJoined(t, err, context.DeadlineExceeded, last)
			if errors.Is(err, chain.ErrRetriesExhausted) {
				t.Fatal("retries ran past the deadline")
			}
			return
		}
		t.Fatal("jitter stayed zero")
	})
}

// Outer timeout with a context-aware dependency is neutral for the breaker.
func TestOuterTimeoutNeutral(t *testing.T) {
	const outer = time.Second
	callServer(t, okHandler, func(t *testing.T, client *http.Client, rawURL string) {
		s := newStack(t, stackConf{
			total: outer, perAttempt: time.Hour,
			attempts: 3, threshold: breakerThreshold,
			delay: time.Hour, openFor: time.Hour,
		})
		primeFailures(t, s.breaker, 3)
		calls := 0
		var last error
		start := time.Now()
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			last = waitAfterGet(ctx, client, rawURL)
			return last
		}, s.policies()...)
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if got := time.Since(start); got != outer {
			t.Fatalf("elapsed = %v, want %v", got, outer)
		}
		assertJoined(t, err, context.DeadlineExceeded, last)
		proveThresholdFive(t, s.breaker, 3)
	})
}

// A dependency that ignores ctx and returns success after the deadline is a breaker success.
func TestLateSuccessRecordsSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const outer = time.Second
		s := newStack(t, stackConf{
			total: outer, perAttempt: time.Hour,
			attempts: 3, threshold: breakerThreshold,
			delay: time.Hour, openFor: time.Hour,
		})
		primeFailures(t, s.breaker, 3)
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			fakeSleep(outer + time.Second)
			if ctx.Err() == nil {
				t.Fatal("deadline did not fire")
			}
			return nil
		}, s.policies()...)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		proveThresholdFive(t, s.breaker, 0)
	})
}

// Per-attempt timeout below the breaker counts as a failure.
func TestPerAttemptTimeoutIsFailure(t *testing.T) {
	callServer(t, okHandler, func(t *testing.T, client *http.Client, rawURL string) {
		s := newStack(t, stackConf{
			total: time.Hour, perAttempt: time.Second,
			attempts: 1, threshold: 1,
			delay: time.Hour, openFor: time.Hour,
		})
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			return waitAfterGet(ctx, client, rawURL)
		}, s.policies()...)
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if s.breaker.State() != circuitbreaker.StateOpen {
			t.Fatalf("State() = %s, want Open", s.breaker.State())
		}
	})
}

// Per-attempt timeout below retry records one failure per attempt.
func TestPerAttemptTimeoutRetryThreeFailures(t *testing.T) {
	callServer(t, okHandler, func(t *testing.T, client *http.Client, rawURL string) {
		s := newStack(t, stackConf{
			total: time.Hour, perAttempt: time.Second,
			attempts: 3, threshold: breakerThreshold,
			delay: time.Millisecond, openFor: time.Hour,
		})
		calls := 0
		err := chain.Do(context.Background(), func(ctx context.Context) error {
			calls++
			return waitAfterGet(ctx, client, rawURL)
		}, s.policies()...)
		if calls != 3 {
			t.Fatalf("fn calls = %d, want 3", calls)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want ErrRetriesExhausted and DeadlineExceeded", err)
		}
		proveThresholdFive(t, s.breaker, 3)
	})
}

// Breaker outside retry counts one logical operation.
func TestBreakerOutsideRetryOneFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := mustBreaker(t, breakerThreshold, time.Hour)
		r := mustRetry(t, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   time.Millisecond,
			MaxDelay:    time.Millisecond,
		})
		calls := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			calls++
			return errDep
		}, b.Policy(), r.Policy())
		if calls != 3 {
			t.Fatalf("fn calls = %d, want 3", calls)
		}
		if !errors.Is(err, chain.ErrRetriesExhausted) || !errors.Is(err, errDep) {
			t.Fatalf("err = %v, want ErrRetriesExhausted and dep", err)
		}
		proveThresholdFive(t, b, 1)
	})
}

// OnRetry panic beneath a breaker outside retry records a failure and releases a held probe.
func TestBreakerOutsideRetryOnRetryPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "onretry"
		assertProbePanic(t, boom, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			OnRetry:     func(int, error, time.Duration) { panic(boom) },
		})
	})
}

// Retryable panic beneath a breaker outside retry records a failure and releases a held probe.
func TestBreakerOutsideRetryRetryablePanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const boom = "retryable"
		assertProbePanic(t, boom, retry.Config{
			MaxAttempts: 3,
			BaseDelay:   time.Hour,
			MaxDelay:    time.Hour,
			Retryable:   func(error) bool { panic(boom) },
		})
	})
}

// Cleanup runs on success, exhaustion, panic, and a neutral half-open probe.
func TestCleanupOnExit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newStack(t, stackConf{
				total: time.Hour, perAttempt: 2 * time.Hour,
				attempts: 3, threshold: breakerThreshold,
				delay: time.Hour, openFor: time.Hour,
			})
			var inner context.Context
			start := time.Now()
			err := chain.Do(context.Background(), func(ctx context.Context) error {
				inner = ctx
				return nil
			}, s.policies()...)
			if err != nil {
				t.Fatal(err)
			}
			if ctxErr(inner) == nil {
				t.Fatal("timeout context still live")
			}
			provePermitFree(t, s.bulk)
			synctest.Wait()
			if got := time.Since(start); got != 0 {
				t.Fatalf("clock advanced %v", got)
			}
		})
	})

	t.Run("exhausted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newStack(t, stackConf{
				total: time.Hour, perAttempt: 2 * time.Hour,
				attempts: 3, threshold: breakerThreshold,
				delay: time.Millisecond, openFor: time.Hour,
			})
			var inner context.Context
			err := chain.Do(context.Background(), func(ctx context.Context) error {
				inner = ctx
				return errDep
			}, s.policies()...)
			if !errors.Is(err, chain.ErrRetriesExhausted) {
				t.Fatal(err)
			}
			if ctxErr(inner) == nil {
				t.Fatal("timeout context still live")
			}
			provePermitFree(t, s.bulk)
			after := time.Now()
			synctest.Wait()
			if got := time.Since(after); got != 0 {
				t.Fatalf("clock advanced %v", got)
			}
		})
	})

	t.Run("panic", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const boom = "boom"
			s := newStack(t, stackConf{
				total: time.Hour, perAttempt: 2 * time.Hour,
				attempts: 3, threshold: breakerThreshold,
				delay: time.Hour, openFor: time.Hour,
			})
			var inner context.Context
			start := time.Now()
			defer func() {
				if got := recover(); got != boom {
					t.Fatalf("panic = %v, want %q", got, boom)
				}
				if ctxErr(inner) == nil {
					t.Fatal("timeout context still live")
				}
				provePermitFree(t, s.bulk)
				synctest.Wait()
				if got := time.Since(start); got != 0 {
					t.Fatalf("clock advanced %v", got)
				}
			}()
			_ = chain.Do(context.Background(), func(ctx context.Context) error {
				inner = ctx
				panic(boom)
			}, s.policies()...)
			t.Fatal("Do returned")
		})
	})

	t.Run("halfopen", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const openFor = time.Hour
			const outer = time.Second
			b := mustBreaker(t, 1, openFor)
			failBreaker(t, b)
			fakeSleep(openFor)
			tm := mustTimeout(t, outer)
			var inner context.Context
			calls := 0
			err := chain.Do(context.Background(), func(ctx context.Context) error {
				calls++
				inner = ctx
				<-ctx.Done()
				return ctx.Err()
			}, tm.Policy(), b.Policy())
			if calls != 1 {
				t.Fatalf("fn calls = %d, want 1", calls)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want DeadlineExceeded", err)
			}
			if b.State() != circuitbreaker.StateHalfOpen {
				t.Fatalf("State() = %s, want HalfOpen", b.State())
			}
			if ctxErr(inner) == nil {
				t.Fatal("timeout context still live")
			}
			after := time.Now()
			synctest.Wait()
			if got := time.Since(after); got != 0 {
				t.Fatalf("clock advanced %v", got)
			}
			next := 0
			if err := chain.Do(context.Background(), func(context.Context) error {
				next++
				return nil
			}, b.Policy()); err != nil {
				t.Fatal(err)
			}
			if next != 1 {
				t.Fatalf("fn calls = %d, want 1", next)
			}
		})
	})
}

// Library policies do not leave a goroutine behind.
func TestNoLibraryGoroutine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client, tr := newClient()
	defer tr.CloseIdleConnections()

	s := newStack(t, stackConf{
		total: time.Second, perAttempt: time.Second,
		attempts: 3, threshold: breakerThreshold,
		delay: time.Millisecond, openFor: time.Second,
	})
	err := chain.Do(context.Background(), func(ctx context.Context) error {
		return roundTrip(ctx, client, srv.URL)
	}, s.policies()...)
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	tr.CloseIdleConnections()
	if stacks := libraryGoroutines(); stacks != "" {
		t.Fatalf("library goroutine:\n%s", stacks)
	}
}

func assertProbePanic(t *testing.T, boom string, cfg retry.Config) {
	t.Helper()
	const openFor = time.Hour
	b := mustBreaker(t, 1, openFor)
	failBreaker(t, b)
	if b.State() != circuitbreaker.StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
	fakeSleep(openFor)
	r := mustRetry(t, cfg)
	calls := 0
	defer func() {
		if got := recover(); got != boom {
			t.Fatalf("panic = %v, want %q", got, boom)
		}
		if calls != 1 {
			t.Fatalf("fn calls = %d, want 1", calls)
		}
		if b.State() != circuitbreaker.StateOpen {
			t.Fatalf("State() = %s, want Open", b.State())
		}
		follow := 0
		err := chain.Do(context.Background(), func(context.Context) error {
			follow++
			return nil
		}, b.Policy())
		if follow != 0 {
			t.Fatalf("fn calls = %d, want 0", follow)
		}
		if !errors.Is(err, chain.ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
	}()
	_ = chain.Do(context.Background(), func(context.Context) error {
		calls++
		return errDep
	}, b.Policy(), r.Policy())
	t.Fatal("Do returned")
}

type stackConf struct {
	total      time.Duration
	perAttempt time.Duration
	attempts   int
	threshold  int
	delay      time.Duration
	openFor    time.Duration
	onRetry    func(int, error, time.Duration)
}

type stack struct {
	total      *timeout.Timeout
	retry      *retry.Retry
	limiter    *ratelimit.Limiter
	breaker    *circuitbreaker.Breaker
	bulk       *bulkhead.Bulkhead
	perAttempt *timeout.Timeout
}

func (s stack) policies() []chain.Policy {
	return []chain.Policy{
		s.total.Policy(),
		s.retry.Policy(),
		s.limiter.Policy(),
		s.breaker.Policy(),
		s.bulk.Policy(),
		s.perAttempt.Policy(),
	}
}

func newStack(t *testing.T, c stackConf) stack {
	t.Helper()
	return stack{
		total: mustTimeout(t, c.total),
		retry: mustRetry(t, retry.Config{
			MaxAttempts: c.attempts,
			BaseDelay:   c.delay,
			MaxDelay:    c.delay,
			OnRetry:     c.onRetry,
		}),
		limiter:    mustLimiter(t),
		breaker:    mustBreaker(t, c.threshold, c.openFor),
		bulk:       mustBulk(t),
		perAttempt: mustTimeout(t, c.perAttempt),
	}
}

func mustTimeout(t *testing.T, d time.Duration) *timeout.Timeout {
	t.Helper()
	tm, err := timeout.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func mustRetry(t *testing.T, c retry.Config) *retry.Retry {
	t.Helper()
	r, err := retry.New(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustBreaker(t *testing.T, threshold int, openFor time.Duration) *circuitbreaker.Breaker {
	t.Helper()
	b, err := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: threshold,
		OpenTimeout:      openFor,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustLimiter(t *testing.T) *ratelimit.Limiter {
	t.Helper()
	l, err := ratelimit.New(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func mustBulk(t *testing.T) *bulkhead.Bulkhead {
	t.Helper()
	b, err := bulkhead.New(1)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func primeFailures(t *testing.T, b *circuitbreaker.Breaker, n int) {
	t.Helper()
	for range n {
		failBreaker(t, b)
	}
	if b.State() != circuitbreaker.StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
}

// proveThresholdFive checks a threshold of 5 that already holds stored failures.
// The failure that reaches 5 opens the breaker. Earlier follow-ups stay Closed.
func proveThresholdFive(t *testing.T, b *circuitbreaker.Breaker, stored int) {
	t.Helper()
	if b.State() != circuitbreaker.StateClosed {
		t.Fatalf("State() = %s, want Closed", b.State())
	}
	need := breakerThreshold - stored
	for i := 1; i < need; i++ {
		failBreaker(t, b)
		if b.State() != circuitbreaker.StateClosed {
			t.Fatalf("after %d more failures State() = %s, want Closed", i, b.State())
		}
	}
	failBreaker(t, b)
	if b.State() != circuitbreaker.StateOpen {
		t.Fatalf("State() = %s, want Open", b.State())
	}
}

func failBreaker(t *testing.T, b *circuitbreaker.Breaker) {
	t.Helper()
	err := chain.Do(context.Background(), func(context.Context) error {
		return errDep
	}, b.Policy())
	if !errors.Is(err, errDep) {
		t.Fatalf("err = %v, want %v", err, errDep)
	}
}

func proveTokensEmpty(t *testing.T, l *ratelimit.Limiter) {
	t.Helper()
	calls := 0
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return nil
	}, l.Policy())
	if !errors.Is(err, chain.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if calls != 0 {
		t.Fatalf("fn calls = %d, want 0", calls)
	}
}

func provePermitFree(t *testing.T, b *bulkhead.Bulkhead) {
	t.Helper()
	calls := 0
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return nil
	}, b.Policy())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
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

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// callServer runs fn in a bubble against a server started outside it.
// Accept is network I/O, so it is not a durable block. A server goroutine
// inside the bubble would stop the fake clock.
func callServer(t *testing.T, h http.HandlerFunc, fn func(t *testing.T, client *http.Client, rawURL string)) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	synctest.Test(t, func(t *testing.T) {
		client, tr := newClient()
		defer tr.CloseIdleConnections()
		fn(t, client, srv.URL)
	})
}

func failHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// waitAfterGet reaches the server, then waits on ctx.
// The wait is a channel receive, so the bubble clock can advance.
// A handler blocked on the socket cannot: that read is not durable.
func waitAfterGet(ctx context.Context, client *http.Client, rawURL string) error {
	if err := roundTrip(ctx, client, rawURL); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func newClient() (*http.Client, *http.Transport) {
	tr := &http.Transport{DisableKeepAlives: true}
	return &http.Client{Transport: tr}, tr
}

func roundTrip(ctx context.Context, client *http.Client, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errDep
	}
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// libraryGoroutines returns stacks in reliq/pkg that are not a test function.
// Other packages run in parallel under go test ./..., and their _test.go frames
// are those tests, not a library goroutine.
func libraryGoroutines() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	var b strings.Builder
	for stack := range strings.SplitSeq(string(buf[:n]), "\n\n") {
		if stack == "" || strings.Contains(stack, "composition_test.go") || strings.Contains(stack, "_test.go") {
			continue
		}
		if strings.Contains(stack, "github.com/harshwagh21/reliq/pkg/") {
			b.WriteString(stack)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// fakeSleep advances the synctest bubble clock by d.
// synctest.Sleep exists only in Go 1.27; time.Sleep plus Wait is that step on Go 1.26.
func fakeSleep(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}
