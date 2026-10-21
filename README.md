# RELIQ

**Resilience toolkit for Go.**

A lightweight, standard-library-only library for composing retries,
timeouts, circuit breakers, rate limits, and bulkheads around dependency calls.

> Resilience, engineered.

## Status

**Pre-release.** The public API is not published yet (`v0.x` may change). Implementation follows [`docs/DESIGN.md`](docs/DESIGN.md).

## Install

```bash
go get github.com/harshwagh21/reliq@latest
```

Packages land in later phases; prefer reading the design until `v0.1.0`.

## Docs

- [`docs/DESIGN.md`](docs/DESIGN.md) — API and semantics contract
- [`docs/semantics.md`](docs/semantics.md) — nil, cancel, panic, and error rules
- [`docs/policy-composition.md`](docs/policy-composition.md) — recommended policy order
- [`docs/faq.md`](docs/faq.md) — when not to use RELIQ or a policy
- [`docs/benchmarks.md`](docs/benchmarks.md) — how numbers will be recorded
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — setup and ADR-first changes

## License

[MIT](LICENSE)
