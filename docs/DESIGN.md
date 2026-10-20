# Design

> Authoritative implementation contract for RELIQ. Cursor and contributors must follow this file. Behavior changes require an ADR under `docs/adr/` and an update here before code. The Obsidian vault note is a frozen planning copy only.

Contract rule: if behavior is not written here, it is not decided. Cursor must ask, not guess.

## 1. Go version
- Toolchain: Go 1.27.2 (local + CI).
- `go.mod`: `go 1.26`. Raise only when a 1.27 feature is genuinely needed.
- `go test` (1.27) runs the `stdversion` vet check, so 1.27-only stdlib APIs cannot sneak in.
- No generic methods in v0.1. `Get[T]` stays a package-level func.
- Timing tests use `testing/synctest`. No clock abstraction, no `internal/clock`. If a test truly cannot use synctest, add a private `now` field in that one package and record an ADR.

## 2. Repo layout (gokit convention)
```
reliq/                           module: github.com/harshwagh21/reliq
├── pkg/
│   ├── resilience/             policy.go executor.go errors.go doc.go + tests
│   ├── retry/                  retry.go backoff.go doc.go + tests
│   ├── timeout/                timeout.go doc.go + tests
│   ├── circuitbreaker/         breaker.go state.go doc.go + tests
│   ├── ratelimit/              limiter.go doc.go + tests
│   └── bulkhead/               bulkhead.go doc.go + tests
├── internal/testutil/          only if 2+ packages share a helper
├── test/integration/           cross-policy tests, flaky httptest server
├── examples/                   retry/ circuitbreaker/ http-client/ combined-policies/
├── benchmarks/                 cross-policy benchmarks + README
├── docs/
│   ├── DESIGN.md               authoritative implementation contract (for Cursor)
│   ├── adr/                    architecture decision records
│   └── semantics.md  policy-composition.md  benchmarks.md  faq.md
├── .github/                    workflows/ci.yml ISSUE_TEMPLATE/ PULL_REQUEST_TEMPLATE.md dependabot.yml
├── .golangci.yml .editorconfig .gitignore
├── CHANGELOG.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md LICENSE README.md Makefile go.mod
```
Rules:
- `pkg/resilience` never imports a policy package. Policy packages import `resilience`.
- Library only: no `cmd/`, `configs/`, `deployments/` for the Go module.
- Per-package Go docs = `doc.go` + `example_test.go`. No per-package README.
- Create a Go package when its phase starts. No empty Go dirs or placeholder files.
- Docs site (Fumadocs or similar): after v0.1, only if users ask. v0.1 docs = README, godoc, and the markdown under `docs/`.
- **Source of truth:** `docs/DESIGN.md` remains the implementation contract for Cursor and code. Change DESIGN/ADR first, then update other markdown under `docs/`.
- Optional adapters (otel, prometheus) later as separate modules. Not v0.1.

## 3. Public API (v0.1, complete surface)
Nothing else is exported. Adding an export needs a decision entry first.

```go
// pkg/resilience
type Func   func(ctx context.Context) error
type Policy func(next Func) Func
func Do(ctx context.Context, fn Func, policies ...Policy) error
func Get[T any](ctx context.Context, fn func(context.Context) (T, error), policies ...Policy) (T, error)

var (
    ErrNilArgument      // nil ctx, nil fn, or nil policy
    ErrInvalidConfig    // returned (wrapped, with field name) by every New
    ErrCircuitOpen
    ErrBulkheadFull
    ErrRateLimited
    ErrRetriesExhausted
)

// pkg/timeout
func New(d time.Duration) (*Timeout, error)
func (t *Timeout) Policy() resilience.Policy

// pkg/retry
type Config struct {
    MaxAttempts int
    BaseDelay, MaxDelay time.Duration
    Retryable func(error) bool                                 // nil = default predicate
    OnRetry   func(attempt int, err error, delay time.Duration) // nil = none
}
func New(c Config) (*Retry, error)
func (r *Retry) Policy() resilience.Policy

// pkg/circuitbreaker
type State int

const (
    StateClosed State = iota
    StateOpen
    StateHalfOpen
)

func (s State) String() string // "Closed"|"Open"|"HalfOpen"; invalid => "State(<n>)" e.g. "State(99)"
type Config struct {
    FailureThreshold int
    OpenTimeout      time.Duration
    IsFailure        func(error) bool          // nil = default
    OnStateChange    func(from, to State)      // nil = none
}
func New(c Config) (*Breaker, error)
func (b *Breaker) Policy() resilience.Policy
func (b *Breaker) State() State

// pkg/bulkhead
func New(maxConcurrent int) (*Bulkhead, error)
func (b *Bulkhead) Policy() resilience.Policy

// pkg/ratelimit
func New(ratePerSecond float64, burst int) (*Limiter, error)
func (l *Limiter) Policy() resilience.Policy
```
Uniform shape everywhere: `New(...) (*T, error)` validates once, then `.Policy()` returns the wrapper (no error). Configs are copied at `New`; later mutation of the caller's struct has no effect.

## 4. Validation (no silent defaults for numbers)
Invalid input => `New` returns an error wrapping `ErrInvalidConfig` naming the field. Only nil funcs get defaults.

| Package | Rule |
|---|---|
| timeout | `d > 0` |
| retry | `MaxAttempts >= 1`; `BaseDelay > 0`; `MaxDelay >= BaseDelay`. `MaxAttempts == 1` is valid (no retries) |
| circuitbreaker | `FailureThreshold >= 1`; `OpenTimeout > 0` |
| bulkhead | `maxConcurrent >= 1` |
| ratelimit | `ratePerSecond > 0` and finite (no NaN/Inf); `burst >= 1` |

## 5. Global contract (applies to every policy)

**Nil handling.** `Do`/`Get` with nil `ctx`, nil `fn`, or any nil policy return `ErrNilArgument` (never panic). Checks run in this order: ctx, fn, policies. `fn` is never called.

**Pre-cancelled context.** After the nil checks, `Do`/`Get` check `ctx.Err()` before building or entering the chain. If non-nil: return it as-is, call `fn` zero times, touch no policy state.

**Cancellation before an attempt.** Retry re-checks `ctx.Err()` before every attempt including the first. Cancelled => no call, return the context error (joined with last attempt error if one exists).

**Chain construction.** Built per call from the policy list (cheap closures). No cached/pre-composed chains in v0.1; optimize only after a benchmark shows it matters.

**Concurrency.** Every `*T` from `New` is safe for concurrent use by many goroutines. `Do` is reentrant. Policy values are immutable after `New`.

**Panics and Goexit.** RELIQ never swallows or suppresses a panic. Every policy releases resources and restores invariants with `defer` on every exit path (bulkhead permits, breaker probe slots, timeout cancellation). Panic and `runtime.Goexit` are distinct:

- **Dependency panic:** the breaker records a failure, then the original panic value propagates to the caller unchanged. Prefer a completion flag checked in `defer`, without `recover`. If recovery is necessary to record the failure, re-panic with the same value.
- **`runtime.Goexit`:** deferred cleanup runs and the breaker records a failure; the goroutine continues terminating. There is no panic value to propagate.
- **User-callback panic** (`OnRetry`, `OnStateChange`, `Retryable`, `IsFailure`): propagates unchanged. A panic in the breaker's own callbacks is never a dependency failure. A panic in an inner policy's callback beneath the breaker is indistinguishable from a dependency panic and counts as one (see Panic origin). The breaker releases any held probe slot; an already committed transition remains committed.

**Panic origin (decided).** RELIQ does not distinguish where beneath the breaker a panic originated. Any panic from the chain below the breaker (the dependency `fn`, or a callback of an inner policy such as retry's `Retryable`/`OnRetry`) is recorded as a failure, the probe slot is released, and the original panic value propagates unchanged. Panics in the breaker's own callbacks (`IsFailure`, `OnStateChange`) are never recorded. Rationale: tracking origin needs either a wrapper value (changes what the caller observes) or recover + re-panic (loses the original stack); a callback panic is a programmer bug. No `internal/` helper is authorized.

**Callbacks** (`OnRetry`, `OnStateChange`, `Retryable`, `IsFailure`). Run synchronously on the calling goroutine, never while holding an internal lock, and must not block. They may run concurrently from many goroutines, so any mutable state a callback owns must be concurrency-safe. RELIQ does not serialize callbacks. Under concurrency, `OnStateChange` calls may be observed out of order relative to real transitions.

**Errors.** Sentinels work with `errors.Is`. Original errors are preserved with `%w`. No string matching anywhere. Context errors are returned as `context.Canceled` / `context.DeadlineExceeded` (possibly wrapped), never replaced.

**`Get[T]` result.** On any error the returned `T` is the zero value, even if `fn` returned a partial value. With retry, a successful result comes from the successful attempt.

## 6. Policy semantics

### Timeout
- `context.WithTimeout(ctx, d)`; an earlier parent deadline wins (never extended).
- No goroutine. `defer cancel()`.
- **Cooperative.** RELIQ cannot stop code that ignores `ctx`. If `fn` ignores the deadline and returns success late, the policy returns that success. If it returns an error late, that error passes through unchanged. RELIQ does not rewrite results after the fact. Tested and documented.

### Retry
- `MaxAttempts` = total attempts. 3 = 1 try + 2 retries.
- Delay before retry number k (k = 1 is the first retry): `delay_k = min(MaxDelay, BaseDelay * 2^(k-1))`, computed overflow-safe (cap the shift, clamp). Then full jitter: uniform random in `[0, delay_k]` (can be 0; documented).
- Wait: `select` on `time.Timer` and `ctx.Done()`; stop the timer on every path. Cancelled during wait => return promptly: `fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)`.
- `Retryable(err)` decides retry and is consulted only while the retry policy's own `ctx` is live (once `ctx` is done, retry stops per the cancellation rules). Default: retry any error EXCEPT `context.Canceled`, `ErrCircuitOpen`, `ErrBulkheadFull`, `ErrRateLimited`. `context.DeadlineExceeded` IS retried by default: with a live `ctx` it came from below (per-attempt timeout or a downstream client timeout), while an expired outer deadline stops retry before the predicate runs.
- **Cancellation after an attempt.** After an attempt returns, check the retry policy's context before consulting `Retryable` or scheduling another attempt. If the context has expired, do not retry. Return the context error, joined with the last attempt error when one exists, using the documented cancellation error-wrapping convention (`fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)`). This applies when an attempt returns after ignoring cancellation; the timeout policy by itself still returns a late dependency result unchanged.
- `OnRetry(attempt, err, delay)`: `attempt` is the 1-based number of the attempt that just failed; `delay` is the jittered wait about to start. Called only when a retry has been scheduled—after the context check and after determining that another attempt is eligible—before waiting. Not called on the final failure. Not called when cancellation is observed before scheduling. If cancellation arrives after `OnRetry` has started or returned, the callback may already have run; the next attempt must not start once the context is observed as cancelled.
- Exhaustion: `fmt.Errorf("%w: %w", ErrRetriesExhausted, lastErr)`; `errors.Is` matches both.
- Non-retryable error: returned as-is after that attempt, not wrapped in `ErrRetriesExhausted`.
- No extra attempt after success. Retries can duplicate side effects; RELIQ gives no exactly-once. Docs show idempotency keys.

### Circuit breaker
States: Closed, Open, HalfOpen. Consecutive-failure model.
- **Closed:** success resets the counter; failure increments; counter == `FailureThreshold` => Open.
- **Open:** reject with `ErrCircuitOpen` (fn not called, nothing recorded). After `OpenTimeout` the next call moves to HalfOpen lazily (no timer, no goroutine).
- **HalfOpen:** exactly one probe in flight; other callers get `ErrCircuitOpen`. Probe success => Closed (counter 0). Probe failure => Open (timer restarts).
- **Generation:** every transition increments a generation. Each admitted call remembers its generation; if it differs at completion, the result is ignored (stale slow call cannot corrupt a newer state).
- **Outcome classification** (evaluated in this order, using the `ctx` the breaker received):
  1. `err == nil` => success.
  2. `ctx.Err() != nil && errors.Is(err, ctx.Err())` => **neutral**. The caller or an outer policy ended the call (caller cancellation, outer total timeout), so the dependency is not blamed. Nothing is recorded. In HalfOpen the probe slot is released and the state stays HalfOpen. `IsFailure` is not consulted for neutral outcomes.
  3. Otherwise `IsFailure(err)` decides. Default: `err != nil && !errors.Is(err, context.Canceled)`. So a `context.DeadlineExceeded` that did not come from the breaker's own `ctx` (e.g. a per-attempt timeout inside the breaker) COUNTS as a failure.
- Guidance: put a per-attempt `timeout` innermost, below the breaker. Its expiry then counts against the dependency, while the outer total deadline stays neutral.
- Locking: one `sync.Mutex` per breaker, held only for state math. Callbacks after unlock.
- Reconfiguration after `New` is not supported.
- **`State.String()`:** `StateClosed` → `"Closed"`, `StateOpen` → `"Open"`, `StateHalfOpen` → `"HalfOpen"`. Any other value → stable `"State(<n>)"` (e.g. `State(99)` → `"State(99)"`).

### Bulkhead
- Buffered-channel semaphore, capacity `maxConcurrent`.
- Full => immediate `ErrBulkheadFull`. No queue, no waiting.
- Release via `defer` on every path including panic.
- Distinct from rate limiting: bulkhead bounds simultaneous work.

### Rate limiter
- Token bucket: `ratePerSecond`, `burst`. Starts full. Lazy refill from elapsed monotonic time; tokens capped at `burst`. No goroutine.
- One token is consumed BEFORE `fn` runs and is not refunded on failure.
- Empty => immediate `ErrRateLimited`. No waiting.
- In-process only: separate instances and processes have separate limits. Say so in docs.
- Mutex-protected; very long idle periods cannot create tokens beyond `burst`.

## 7. Composition (outermost first)
```go
resilience.Do(ctx, call,
    total.Policy(),      // timeout: one deadline for everything incl. backoff
    retry.Policy(),      // repeats eligible failures
    limiter.Policy(),    // each attempt consumes a token
    breaker.Policy(),    // sees each attempt
    bulkhead.Policy(),   // permit held only during the attempt, released before backoff
    perAttempt.Policy(), // optional second timeout, innermost
)
```
Order changes behavior. Keep both orderings in tests and state their purpose explicitly:

- **Retry outside breaker** (recommended default above): each attempt reaches the breaker; consecutive failures can open it between attempts.
- **Breaker outside retry**: the breaker sees one logical operation; its outcome reflects the final result of the retry policy, subject to the documented panic and cancellation rules (including Panic origin: inner-callback panics beneath the breaker count as failures).

Tests assert exact counts (attempts, breaker failures, tokens consumed, permits released), not just final errors.

## 8. Out of scope (v0.1)
Fallback, hedging, sliding-window breaker, waiting limiter/bulkhead, pre-composed chains, metrics adapters, cross-language spec, distributed state, reconfiguration, panic recovery.
