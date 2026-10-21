package chain_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/harshwagh21/reliq/pkg/chain"
)

// Nil ctx, nil fn, or a nil policy returns ErrNilArgument. fn and policies are not called.
func TestDoNilArguments(t *testing.T) {
	calls := 0
	fn := func(context.Context) error {
		calls++
		return nil
	}
	entered := false
	policy := chain.Policy(func(next chain.Func) chain.Func {
		entered = true
		return next
	})

	tests := []struct {
		name     string
		ctx      context.Context
		fn       chain.Func
		policies []chain.Policy
	}{
		{name: "nil ctx", fn: fn, policies: []chain.Policy{policy}},
		{name: "nil fn", ctx: context.Background(), policies: []chain.Policy{policy}},
		{name: "nil ctx and nil fn", policies: []chain.Policy{policy}},
		{name: "nil policy", ctx: context.Background(), fn: fn, policies: []chain.Policy{nil}},
		{name: "nil policy after real", ctx: context.Background(), fn: fn, policies: []chain.Policy{policy, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls = 0
			entered = false
			err := chain.Do(tt.ctx, tt.fn, tt.policies...)
			if err != chain.ErrNilArgument {
				t.Fatalf("err = %v, want ErrNilArgument", err)
			}
			if calls != 0 {
				t.Fatalf("fn calls = %d, want 0", calls)
			}
			if entered {
				t.Fatal("policy was invoked")
			}
		})
	}
}

// A nil policy is rejected before a canceled context is returned.
func TestDoNilPolicyBeforePreCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := chain.Do(ctx, func(context.Context) error {
		calls++
		return nil
	}, nil)
	if err != chain.ErrNilArgument {
		t.Fatalf("err = %v, want ErrNilArgument", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("returned context error")
	}
	if calls != 0 {
		t.Fatalf("fn calls = %d, want 0", calls)
	}
}

// A canceled context returns context.Canceled, with zero calls and the policy unentered.
func TestDoPreCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	entered := false
	policy := chain.Policy(func(next chain.Func) chain.Func {
		entered = true
		return next
	})
	err := chain.Do(ctx, func(context.Context) error {
		calls++
		return nil
	}, policy)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("fn calls = %d, want 0", calls)
	}
	if entered {
		t.Fatal("policy was invoked")
	}
}

// No policies calls fn once and returns its error.
func TestDoZeroPolicies(t *testing.T) {
	sentinel := errors.New("dep")
	calls := 0
	err := chain.Do(context.Background(), func(context.Context) error {
		calls++
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
}

// Get returns the value on success, zero T on error, and zero T when the context is already canceled.
func TestGet(t *testing.T) {
	ctx := context.Background()

	v, err := chain.Get(ctx, func(context.Context) (int, error) {
		return 7, nil
	})
	if err != nil || v != 7 {
		t.Fatalf("Get success = (%d, %v), want (7, nil)", v, err)
	}

	v, err = chain.Get(ctx, func(context.Context) (int, error) {
		return 7, errors.New("nope")
	})
	if err == nil || v != 0 {
		t.Fatalf("Get error = (%d, %v), want (0, error)", v, err)
	}

	calls := 0
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	v, err = chain.Get(cancelled, func(context.Context) (int, error) {
		calls++
		return 7, nil
	})
	if err != context.Canceled || v != 0 || calls != 0 {
		t.Fatalf("Get pre-cancel = (%d, %v), calls %d", v, err, calls)
	}
}

// Get rejects a nil ctx, a nil fn, and a nil policy without calling fn.
func TestGetNilArguments(t *testing.T) {
	calls := 0
	fn := func(context.Context) (int, error) {
		calls++
		return 1, nil
	}
	entered := false
	policy := chain.Policy(func(next chain.Func) chain.Func {
		entered = true
		return next
	})

	// nilCtx is a nil context held in a variable so this test can pass one
	// without a nil literal (staticcheck SA1012).
	var nilCtx context.Context
	v, err := chain.Get(nilCtx, fn, policy)
	if err != chain.ErrNilArgument || v != 0 || calls != 0 || entered {
		t.Fatalf("nil ctx = (%d, %v), calls %d, entered %v", v, err, calls, entered)
	}

	v, err = chain.Get[int](context.Background(), nil, policy)
	if err != chain.ErrNilArgument || v != 0 || entered {
		t.Fatalf("nil fn = (%d, %v), entered %v", v, err, entered)
	}

	v, err = chain.Get[int](nilCtx, nil, policy)
	if err != chain.ErrNilArgument || v != 0 || entered {
		t.Fatalf("nil ctx and fn = (%d, %v), entered %v", v, err, entered)
	}

	v, err = chain.Get(context.Background(), fn, policy, nil)
	if err != chain.ErrNilArgument || v != 0 || calls != 0 || entered {
		t.Fatalf("nil policy = (%d, %v), calls %d, entered %v", v, err, calls, entered)
	}
}

// Get enters policies outermost first.
func TestGetPolicyOrder(t *testing.T) {
	var order []string
	wrap := func(name string) chain.Policy {
		return func(next chain.Func) chain.Func {
			return func(ctx context.Context) error {
				order = append(order, name)
				return next(ctx)
			}
		}
	}
	v, err := chain.Get(context.Background(), func(context.Context) (int, error) {
		order = append(order, "fn")
		return 1, nil
	}, wrap("a"), wrap("b"), wrap("c"))
	if err != nil || v != 1 {
		t.Fatalf("Get = (%d, %v)", v, err)
	}
	want := []string{"a", "b", "c", "fn"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// A panic from fn reaches the caller unchanged, after the policy defer runs.
func TestDoPanicValueUnchanged(t *testing.T) {
	const want = "boom"
	cleaned := false
	policy := chain.Policy(func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			defer func() { cleaned = true }()
			return next(ctx)
		}
	})
	defer func() {
		r := recover()
		if r != want {
			t.Fatalf("panic = %v, want %q", r, want)
		}
		if !cleaned {
			t.Fatal("cleanup did not run")
		}
	}()
	_ = chain.Do(context.Background(), func(context.Context) error {
		panic(want)
	}, policy)
	t.Fatal("Do returned")
}

// Concurrent Do calls share one Policy, and fn is called once per call.
func TestDoConcurrentSharedPolicy(t *testing.T) {
	const n = 32
	var calls atomic.Int32
	policy := chain.Policy(func(next chain.Func) chain.Func {
		return func(ctx context.Context) error {
			return next(ctx)
		}
	})
	var wg sync.WaitGroup
	var bad atomic.Int32
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			err := chain.Do(context.Background(), func(context.Context) error {
				calls.Add(1)
				return nil
			}, policy)
			if err != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("error count = %d, want 0", bad.Load())
	}
	if got := calls.Load(); got != n {
		t.Fatalf("fn calls = %d, want %d", got, n)
	}
}
