# RELIQ

[![Go Reference](https://img.shields.io/badge/reference-pkg.go.dev-00ADD8?logo=go&logoColor=white)](https://pkg.go.dev/github.com/harshwagh21/reliq) [![CI](https://img.shields.io/badge/CI-GitHub_Actions-2088FF?logo=githubactions&logoColor=white)](https://github.com/harshwagh21/reliq/actions/workflows/ci.yml) [![Go 1.26+](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/) [![License: MIT](https://img.shields.io/badge/license-MIT-yellow?logo=opensourceinitiative&logoColor=white)](LICENSE)


**Resilience for Go.**

RELIQ protects work that can fail, stall, or overload whatever it depends on. It stays inside your program and uses only the Go standard library.

## How it works

Place it around one operation: a request, a database query, or a step in your own program. For that operation, choose the protections that fit.

A retry gives a failure another chance. A timeout ends an operation that has waited too long, including the pause between attempts. A circuit breaker stops sending work to a dependency that is already failing, then allows it through again after recovery. A bulkhead caps how many operations run at the same time. A rate limit caps how often a new operation may start.

One protection is enough. Several can be used together, in the order you choose. They apply only to that operation and finish when it finishes.

## When to use it

- **Retry** when repeating the work is safe.
- **Timeout** when a late result is no longer useful.
- **Circuit breaker** when a failing dependency should stop receiving work until it recovers.
- **Bulkhead** when one dependency should not be allowed to occupy every available worker.
- **Rate limit** when new work must stay under a fixed pace.

## Prerequisites

Go 1.26 or newer. RELIQ doesn't pull in any other libraries. If you're comfortable with `context` and `errors.Is`, you're fine.

## Install

```bash
go get github.com/harshwagh21/reliq@v0.1.0
```

This is **v0.1.0**. The API can still change while we're on v0.

## Start here

The [guide](docs/guide.md) is the easiest way in. The [reference](docs/reference.md) lists every function if you already know what you want. The [FAQ](docs/faq.md) is for the moment you're not sure a protection belongs. [Semantics](docs/semantics.md) covers the sharp edges: nils, cancellation, panics. [Composition](docs/policy-composition.md) is what changes when you stack them in a different order. [DESIGN.md](docs/DESIGN.md) is the contract, if you're changing the library itself.

Prefer to read a program?

- [A retry](examples/retry/main.go) that fails once, then succeeds
- [A circuit breaker](examples/circuitbreaker/main.go) that rejects the next call
- [An HTTP call](examples/http-client/main.go) with a time limit
- [All of them together](examples/combined-policies/main.go)

## Limitations

RELIQ won't hedge, fall back to a second option, or make people wait in a queue. A limit stays inside one process. If your function ignores its context, a timeout can't force it to stop. A retry really can run the work twice. A panic is still a panic. The longer list is in the [FAQ](docs/faq.md) and in [DESIGN.md](docs/DESIGN.md#9-out-of-scope-v01).

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) has the setup and the tests. If you want to change how something behaves, [DESIGN.md](docs/DESIGN.md) changes first, then the code. Report security issues through [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
