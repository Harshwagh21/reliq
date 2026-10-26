# Contributing

Thanks for helping with RELIQ.

## Requirements

- Go **1.26+** in `go.mod` (`go 1.26`). CI installs the latest 1.26 and 1.27 patches, at least 1.26.10 and 1.27.3.
- No third-party library dependencies in the module

## Setup

```bash
git clone https://github.com/harshwagh21/reliq.git
cd reliq
go mod tidy
```

## Checks

```bash
make vet
make test
make race
make lint
```

`make examples` builds `./examples/...` when example packages exist (Phase 5+); until then it is a no-op.

## Behavior changes

1. Add an ADR under [`docs/adr/`](docs/adr/).
2. Update [`docs/DESIGN.md`](docs/DESIGN.md).
3. Then change code and tests.

Do not invent semantics that are not in `docs/DESIGN.md`. The Cursor rule in [`.cursor/rules/reliq.mdc`](.cursor/rules/reliq.mdc) applies the same contract.

## Style

- Stdlib only; constructors return `(T, error)` and never panic
- Tests ship with the code; prefer `testing/synctest` for timing
- Gate before merge: `go vet ./... && go test -race -count=1 ./...`
