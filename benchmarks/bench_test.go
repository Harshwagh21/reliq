package benchmarks_test

import (
	"context"
	"testing"
	"time"

	"github.com/harshwagh21/reliq/pkg/bulkhead"
	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/circuitbreaker"
	"github.com/harshwagh21/reliq/pkg/ratelimit"
	"github.com/harshwagh21/reliq/pkg/retry"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

// call is a variable so the baseline stays an indirect call.
var call = func(context.Context) error { return nil }

var errSink error

func BenchmarkDirectCall(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		errSink = call(ctx)
	}
}

func BenchmarkDoZeroPolicies(b *testing.B) {
	run(b)
}

func BenchmarkRetryFirstAttempt(b *testing.B) {
	run(b, newRetry(b).Policy())
}

func BenchmarkBreakerClosed(b *testing.B) {
	run(b, closedBreaker(b).Policy())
}

func BenchmarkBreakerParallel(b *testing.B) {
	policy := closedBreaker(b).Policy()
	ctx := context.Background()
	if err := chain.Do(ctx, call, policy); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var err error
		for pb.Next() {
			err = chain.Do(ctx, call, policy)
		}
		if err != nil {
			b.Error(err)
		}
	})
}

func BenchmarkFullComposition(b *testing.B) {
	run(b, composition(b)...)
}

func run(b *testing.B, policies ...chain.Policy) {
	b.Helper()
	ctx := context.Background()
	if err := chain.Do(ctx, call, policies...); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		errSink = chain.Do(ctx, call, policies...)
	}
}

func closedBreaker(b *testing.B) *circuitbreaker.Breaker {
	b.Helper()
	br, err := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 5,
		OpenTimeout:      time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	return br
}

func composition(b *testing.B) []chain.Policy {
	b.Helper()
	return []chain.Policy{
		newTimeout(b).Policy(),
		newRetry(b).Policy(),
		newLimiter(b).Policy(),
		closedBreaker(b).Policy(),
		newBulkhead(b).Policy(),
		newTimeout(b).Policy(),
	}
}

func newRetry(b *testing.B) *retry.Retry {
	b.Helper()
	r, err := retry.New(retry.Config{
		MaxAttempts: 3,
		BaseDelay:   time.Nanosecond,
		MaxDelay:    time.Nanosecond,
	})
	if err != nil {
		b.Fatal(err)
	}
	return r
}

func newTimeout(b *testing.B) *timeout.Timeout {
	b.Helper()
	tm, err := timeout.New(time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	return tm
}

func newLimiter(b *testing.B) *ratelimit.Limiter {
	b.Helper()
	// 1e9 tokens/s and burst 1<<20 keep the timed loop on the admit path.
	l, err := ratelimit.New(1e9, 1<<20)
	if err != nil {
		b.Fatal(err)
	}
	return l
}

func newBulkhead(b *testing.B) *bulkhead.Bulkhead {
	b.Helper()
	bulk, err := bulkhead.New(1)
	if err != nil {
		b.Fatal(err)
	}
	return bulk
}
