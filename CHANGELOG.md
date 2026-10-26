# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-26

### Added

- `chain.Do` and `chain.Get` compose policies outermost first. Sentinels cover a nil argument, invalid config, an open circuit, a full bulkhead, a rate limit, and exhausted retries.
- Timeout, retry, circuit breaker, bulkhead, and token-bucket rate limiter. Each `New` returns `(*T, error)` and wraps `chain.ErrInvalidConfig` when a number is rejected.
- Examples for retry, a circuit breaker, an HTTP client, and the recommended composition.
- Reader docs for semantics, composition, and when not to use each policy, plus an on-demand benchmark baseline.

[0.1.0]: https://github.com/harshwagh21/reliq/releases/tag/v0.1.0
