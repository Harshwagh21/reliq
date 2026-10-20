.PHONY: test race vet lint bench examples

GOLANGCI_LINT_VERSION ?= v2.15.0

test:
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo 'no packages yet; skipping test'; else go test ./...; fi

race:
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo 'no packages yet; skipping race'; else go test -race -count=1 ./...; fi

vet:
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo 'no packages yet; skipping vet'; else go vet ./...; fi

lint:
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then \
		echo 'no packages yet; skipping lint'; \
	else \
		command -v golangci-lint >/dev/null 2>&1 || { \
			echo "golangci-lint $(GOLANGCI_LINT_VERSION) required; install from https://golangci-lint.run"; \
			exit 1; \
		}; \
		golangci-lint run; \
	fi

bench:
	@pkgs=$$(go list ./... 2>/dev/null); \
	if [ -z "$$pkgs" ]; then echo 'no packages yet; skipping bench'; else go test -bench=. -benchmem ./...; fi

# Builds example packages when present (Phase 5+). No-op until then.
examples:
	@if compgen -G 'examples/*/*.go' > /dev/null; then \
		go build ./examples/...; \
	else \
		echo 'no example packages yet; skipping'; \
	fi
