# RELIQ design contract

This is the authoritative specification for RELIQ. Implementation, tests, and later docs must match it. If a behavior is not written here, it is not decided: stop and ask; do not invent it.

Behavior or public-API changes require an [ADR](adr/README.md) and an update to this file **before** code.


| Audience     | Use this file to                      |
| ------------ | ------------------------------------- |
| Contributors | Implement `pkg/` exactly as specified |
| Reviewers    | Check diffs against the contract      |
| Readers      | Understand the v0.1 API and semantics |


Related pages (narratives filled in Phase 6): [Semantics](semantics.md), [Policy composition](policy-composition.md), [Benchmarks](benchmarks.md), [FAQ](faq.md).

---



## Contents

1. [What RELIQ is](#1-what-reliq-is)
2. [Go version](#2-go-version)
3. [Repository layout](#3-repository-layout)
4. [Public API](#4-public-api)
5. [Validation](#5-validation)
6. [Global contract](#6-global-contract)
7. [Policy semantics](#7-policy-semantics)
8. [Composition](#8-composition)
9. [Out of scope (v0.1)](#9-out-of-scope-v01)

---



## 1. What RELIQ is

RELIQ is a standard-library-only toolkit for wrapping a dependency call with timeout, retry, circuit breaker, bulkhead, and rate limit. Behavior is documented and tested. No infrastructure is required.

It is a small, readable core with one API style. failsafe-go already covers this space; do not claim RELIQ is faster without a recorded benchmark.

**Module:** `github.com/harshwagh21/reliq`

**License:** MIT

---



## 2. Go version

- Local and CI toolchain: **Go 1.27.2**.
- `go.mod` language version: `go 1.26`. Raise it only when a 1.27 feature is genuinely required.
- `go test` on 1.27 runs the `stdversion` vet check, so 1.27-only stdlib APIs cannot sneak in.
- No generic methods in v0.1. `Get[T]` stays a package-level function.
- Timing tests use `testing/synctest`. There is no clock package and no `internal/clock`. If a test cannot use synctest, add a private `now` field in that one package and record an ADR first.

---



## 3. Repository layout

The module is a library only: no `cmd/`, `configs/`, or `deployments/`.

```
reliq/                           module: github.com/harshwagh21/reliq
├── pkg/
│   ├── chain/                  policy.go executor.go errors.go doc.go + tests
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
│   ├── DESIGN.md               this file — implementation contract
│   ├── adr/                    architecture decision records
│   └── semantics.md  policy-composition.md  benchmarks.md  faq.md
├── .github/                    workflows/ci.yml ISSUE_TEMPLATE/ PULL_REQUEST_TEMPLATE.md dependabot.yml
├── .golangci.yml .editorconfig .gitignore
├── CHANGELOG.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md LICENSE README.md Makefile go.mod
```



### Layout rules

- `pkg/chain` never imports a policy package. Policy packages import `chain`.
- Per-package Go docs are `doc.go` plus `example_test.go`. No per-package README.
- Create a Go package when its phase starts. No empty Go directories or placeholder `.go` files.
- v0.1 documentation is the root README, godoc, and markdown under `docs/`. No docs website (Fumadocs/Next or similar) until after v0.1, and only if users ask.
- Change this file and any ADR first, then update other markdown under `docs/`.
- Optional adapters (`otel`, `prometheus`, HTTP client helpers) are later separate modules, not v0.1.

---



## 4. Public API

This is the complete v0.1 export surface. Nothing else is exported. Adding an export needs a decision (ADR) first.

```go
// pkg/chain
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
func (t *Timeout) Policy() chain.Policy

// pkg/retry
type Config struct {
    MaxAttempts int
    BaseDelay, MaxDelay time.Duration
    Retryable func(error) bool                                 // nil = default predicate
    OnRetry   func(attempt int, err error, delay time.Duration) // nil = none
}
func New(c Config) (*Retry, error)
func (r *Retry) Policy() chain.Policy

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
func (b *Breaker) Policy() chain.Policy
func (b *Breaker) State() State

// pkg/bulkhead
func New(maxConcurrent int) (*Bulkhead, error)
func (b *Bulkhead) Policy() chain.Policy

// pkg/ratelimit
func New(ratePerSecond float64, burst int) (*Limiter, error)
func (l *Limiter) Policy() chain.Policy
```



### Construction

Uniform shape: New(...) (*T, error) validates once, then `(*T).Policy()` returns the wrapper and cannot fail. Configs are copied at `New`; later mutation of the caller’s struct has no effect.

---



## 5. Validation

Invalid input: `New` returns an error wrapping `ErrInvalidConfig` that names the field. There are no silent numeric defaults. Only nil functions receive defaults.


| Package        | Rule                                                                                                   |
| -------------- | ------------------------------------------------------------------------------------------------------ |
| timeout        | `d > 0`                                                                                                |
| retry          | `MaxAttempts >= 1`; `BaseDelay > 0`; `MaxDelay >= BaseDelay`. `MaxAttempts == 1` is valid (no retries) |
| circuitbreaker | `FailureThreshold >= 1`; `OpenTimeout > 0`                                                             |
| bulkhead       | `maxConcurrent >= 1`                                                                                   |
| ratelimit      | `ratePerSecond > 0` and finite (no NaN/Inf); `burst >= 1`                                              |


---



## 6. Global contract

These rules apply to every policy.

### Nil arguments

`Do` / `Get` with a nil `ctx`, nil `fn`, or any nil policy return `ErrNilArgument` and never panic. Checks run in this order: **ctx, then fn, then policies**. `fn` is never called.

### Pre-cancelled context

After the nil checks, `Do` / `Get` check `ctx.Err()` before building or entering the chain. If it is non-nil: return that error as-is, call `fn` zero times, and touch no policy state.

### Cancellation before an attempt

Retry re-checks `ctx.Err()` before every attempt, including the first. If cancelled: do not call `fn`; return the context error, joined with the last attempt error when one exists.

### Chain construction

The chain is built per `Do` / `Get` call from the policy list (cheap closures). No cached or pre-composed `Chain` API in v0.1. Optimize only after a benchmark shows it matters.

### Concurrency

Every `*T` from `New` is safe for concurrent use. `Do` is reentrant. Policy values are immutable after `New`.

### Panics and Goexit

RELIQ never swallows or suppresses a panic. Every policy releases resources and restores invariants with `defer` on every exit path (bulkhead permits, breaker probe slots, timeout cancellation). Panic and `runtime.Goexit` are distinct:

- **Dependency panic:** the breaker records a failure, then the original panic value propagates to the caller unchanged. Prefer a completion flag checked in `defer`, without `recover`. If recovery is necessary to record the failure, re-panic with the same value.
- `runtime.Goexit`**:** deferred cleanup runs and the breaker records a failure; the goroutine continues terminating. There is no panic value to propagate.
- **User-callback panic** (`OnRetry`, `OnStateChange`, `Retryable`, `IsFailure`): propagates unchanged. A panic in the breaker’s own callbacks is never a dependency failure. A panic in an inner policy’s callback beneath the breaker is indistinguishable from a dependency panic and counts as one (see [Panic origin](#panic-origin)). The breaker releases any held probe slot; an already committed transition remains committed.



### Panic origin

RELIQ does not distinguish where beneath the breaker a panic originated. Any panic from the chain below the breaker (the dependency `fn`, or a callback of an inner policy such as retry’s `Retryable` / `OnRetry`) is recorded as a failure, the probe slot is released, and the original panic value propagates unchanged. Panics in the breaker’s own callbacks (`IsFailure`, `OnStateChange`) are never recorded.

Rationale: tracking origin needs either a wrapper value (changes what the caller observes) or recover + re-panic (loses the original stack). A callback panic is a programmer bug. No `internal/` helper is authorized for classifying panic source.

### Callbacks

`OnRetry`, `OnStateChange`, `Retryable`, and `IsFailure` run synchronously on the calling goroutine, never while holding an internal lock, and must not block. They may run concurrently from many goroutines, so any mutable state a callback owns must be concurrency-safe. RELIQ does not serialize callbacks. Under concurrency, `OnStateChange` calls may be observed out of order relative to real transitions.

### Errors

Sentinels work with `errors.Is`. Original errors are preserved with `%w`. Never match error strings. Context errors are returned as `context.Canceled` / `context.DeadlineExceeded` (possibly wrapped), never replaced.

### `Get[T]` result

On any error the returned `T` is the zero value, even if `fn` returned a partial value. With retry, a successful result comes from the successful attempt.

---



## 7. Policy semantics



### Timeout

- Uses `context.WithTimeout(ctx, d)`. An earlier parent deadline wins and is never extended.
- No goroutine. Always `defer cancel()`.
- **Cooperative.** RELIQ cannot stop code that ignores `ctx`. If `fn` ignores the deadline and returns success late, the policy returns that success. If it returns an error late, that error passes through unchanged. RELIQ does not rewrite results after the fact.



### Retry

- `MaxAttempts` is the **total** number of attempts. `3` means 1 try + 2 retries.
- Delay before retry number `k` (`k = 1` is the first retry): `delay_k = min(MaxDelay, BaseDelay * 2^(k-1))`, computed overflow-safe (cap the shift, clamp). Then full jitter: uniform random in `[0, delay_k]` (zero is allowed).
- Wait with `select` on a `time.Timer` and `ctx.Done()`. Stop the timer on every path. Cancelled during wait: return promptly as `fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)`.
- `Retryable(err)` decides whether to retry and is consulted only while the retry policy’s own `ctx` is live. Once `ctx` is done, retry stops per the cancellation rules.
- **Default predicate:** retry any error except `context.Canceled`, `ErrCircuitOpen`, `ErrBulkheadFull`, and `ErrRateLimited`. `context.DeadlineExceeded` **is** retried by default: with a live `ctx` it came from below (per-attempt timeout or a downstream client timeout). An expired outer deadline stops retry before the predicate runs.
- **Cancellation after an attempt.** After an attempt returns, check the retry policy’s context before consulting `Retryable` or scheduling another attempt. If the context has expired, do not retry. Return the context error, joined with the last attempt error when one exists (`fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)`). This applies when an attempt returns after ignoring cancellation. Timeout used alone still returns a late dependency result unchanged.
- `OnRetry(attempt, err, delay)`: `attempt` is the 1-based number of the attempt that just failed; `delay` is the jittered wait about to start. Called only when a retry has been scheduled — after the context check and after determining that another attempt is eligible — before waiting. Not called on the final failure. Not called when cancellation is observed before scheduling. If cancellation arrives after `OnRetry` has started or returned, the callback may already have run; the next attempt must not start once the context is observed as cancelled.
- Exhaustion: `fmt.Errorf("%w: %w", ErrRetriesExhausted, lastErr)`. `errors.Is` matches both.
- A non-retryable error is returned as-is after that attempt, not wrapped in `ErrRetriesExhausted`.
- No extra attempt after success. Retries can duplicate side effects; RELIQ does not provide exactly-once delivery. Callers that need uniqueness use their own idempotency keys.



### Circuit breaker

Consecutive-failure model. States: Closed, Open, HalfOpen.

- **Closed:** success resets the counter; failure increments; counter == `FailureThreshold` transitions to Open.
- **Open:** reject with `ErrCircuitOpen` (`fn` not called, nothing recorded). After `OpenTimeout`, the next call moves to HalfOpen lazily (no timer goroutine).
- **HalfOpen:** exactly one probe in flight; other callers get `ErrCircuitOpen`. Probe success → Closed (counter 0). Probe failure → Open (timer restarts).
- **Generation:** every transition increments a generation. Each admitted call remembers its generation; if it differs at completion, the result is ignored so a stale slow call cannot corrupt a newer state.
- **Outcome classification** (this order, using the `ctx` the breaker received):
  1. `err == nil` → success.
  2. `ctx.Err() != nil && errors.Is(err, ctx.Err())` → **neutral**. The caller or an outer policy ended the call (caller cancellation, outer total timeout). Nothing is recorded. In HalfOpen the probe slot is released and the state stays HalfOpen. `IsFailure` is not consulted.
  3. Otherwise `IsFailure(err)` decides. Default: `err != nil && !errors.Is(err, context.Canceled)`. A `context.DeadlineExceeded` that did not come from the breaker’s own `ctx` (for example a per-attempt timeout inside the breaker) **counts as a failure**.
- Put a per-attempt timeout innermost, below the breaker, so its expiry counts against the dependency while an outer total deadline stays neutral. If both deadlines expire together, the outcome is neutral.
- One `sync.Mutex` per breaker, held only for state math. Callbacks run after unlock.
- Reconfiguration after `New` is not supported.
- `State.String()`: `StateClosed` → `"Closed"`, `StateOpen` → `"Open"`, `StateHalfOpen` → `"HalfOpen"`. Any other value → stable `"State(<n>)"` (for example `State(99)` → `"State(99)"`).



### Bulkhead

- Buffered-channel semaphore with capacity `maxConcurrent`.
- Full: immediate `ErrBulkheadFull`. No queue, no waiting.
- Release via `defer` on every path, including panic.
- Distinct from rate limiting: bulkhead bounds simultaneous work.



### Rate limiter

- Token bucket: `ratePerSecond`, `burst`. Starts full. Lazy refill from elapsed monotonic time; tokens capped at `burst`. No goroutine.
- One token is consumed **before** `fn` runs and is not refunded on failure.
- Empty: immediate `ErrRateLimited`. No waiting.
- In-process only. Separate instances and processes have separate limits.
- Mutex-protected. Very long idle periods cannot create tokens beyond `burst`.

---



## 8. Composition

`Do` / `Get` apply policies **outermost first** (first argument wraps everything inside it).

```go
chain.Do(ctx, call,
    total.Policy(),      // timeout: one deadline for everything including backoff
    retry.Policy(),      // repeats eligible failures
    limiter.Policy(),    // each attempt consumes a token
    breaker.Policy(),    // sees each attempt
    bulkhead.Policy(),   // permit held only during the attempt, released before backoff
    perAttempt.Policy(), // optional second timeout, innermost
)
```

Order changes behavior. Tests must cover both orderings and state their purpose:

- **Retry outside breaker** (recommended default above): each attempt reaches the breaker; consecutive failures can open it between attempts.
- **Breaker outside retry:** the breaker sees one logical operation; its outcome reflects the final result of the retry policy, subject to the panic and cancellation rules (including panic origin: inner-callback panics beneath the breaker count as failures).

Tests assert exact counts (attempts, breaker failures, tokens consumed, permits released), not only the final error.

---



## 9. Out of scope (v0.1)

Fallback, hedging, sliding-window breaker, waiting limiter or bulkhead, pre-composed chains, metrics adapters, cross-language spec, distributed state, reconfiguration, panic recovery, builders, global registry, background goroutines in library code, and a documentation website.