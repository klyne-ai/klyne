.PHONY: build build-ui dev test vet lint tidy ci clean release release-snapshot proof

# Default target. CGO is off because modernc.org/sqlite is pure-Go (spec §5);
# leaving CGO on with the macOS 26 + Go 1.21 internal linker can produce
# binaries dyld rejects ("missing LC_UUID load command"). Bump to Go 1.23+
# and drop this flag once the toolchain catches up to the spec.
#
# VERSION is injected via -ldflags so `klyne doctor` / `klyne --version`
# reports the commit the binary was built from. Plain `go build` (without
# this Makefile) keeps the v0.0.0-bootstrap default in cmd/klyne/main.go.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build: build-ui
	GOTOOLCHAIN=local CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/klyne ./cmd/klyne

# Build the SvelteKit UI if ui/package.json exists.
# The UI build output is embedded via embed.FS (ui/build/).
# If ui/build/ does not exist, create an empty stub so go:embed does not fail.
build-ui:
	@if [ -f ui/package.json ]; then \
		cd ui && npm ci && npm run build; \
	else \
		echo "ui/package.json not found — skipping UI build (W13 wires this)"; \
		mkdir -p ui/build; \
	fi

# Dev: run backend and frontend in separate terminals.
# W13 wires real hot-reload; for now this is a placeholder.
dev:
	@echo "Run the following in separate terminals:"
	@echo "  go run ./cmd/klyne"
	@echo "  cd ui && npm run dev"

# Run all Go tests with race detector and coverage output.
# CGO_ENABLED=0: see comment on the build target.
test:
	GOTOOLCHAIN=local CGO_ENABLED=0 go test ./... -race -coverprofile=cov.out

# Run go vet.
vet:
	GOTOOLCHAIN=local CGO_ENABLED=0 go vet ./...

# Run golangci-lint.
# Requires golangci-lint to be installed: https://golangci-lint.run/usage/install/
lint:
	golangci-lint run

# Tidy go.mod and go.sum.
tidy:
	GOTOOLCHAIN=local go mod tidy

# Full CI check: vet, lint, test, and optionally UI checks.
ci: vet lint test
	@if [ -f ui/package.json ]; then \
		cd ui && npm ci && npm run check && npm run test; \
	else \
		echo "ui/package.json not found — skipping UI checks"; \
	fi

# Remove build artifacts.
clean:
	rm -rf bin/ cov.out ui/build/

# Release: orchestrated by .github/workflows/release.yml on a `v*.*.*`
# tag push. The CI workflow runs `goreleaser release --clean` with the
# GITHUB_TOKEN + HOMEBREW_TAP_GITHUB_TOKEN secrets in scope.
#
# For local testing without publishing, use `release-snapshot` below.
release:
	@echo "Release runs in CI on a v*.*.* tag push (see .github/workflows/release.yml)."
	@echo "For a local dry-run that does not publish, run: make release-snapshot"

# Local dry-run of the release pipeline. Produces artifacts under dist/
# without pushing anything to GitHub or the Homebrew tap.
# Requires goreleaser on PATH (https://goreleaser.com/install/).
release-snapshot:
	goreleaser release --snapshot --clean

# Run every reproducible-proof test under docs/proof/. Each subdirectory
# is a self-contained scenario with a fixture + a Go test asserting the
# claim made in claim.md. This target is what readers of the README run
# to verify Klyne does what it says.
#
# GOTOOLCHAIN=auto: project requires Go 1.25 (per go.mod); auto lets
# Go fetch the toolchain when the system Go is older.
proof:
	@echo "Running reproducible-proof tests (docs/proof/...)"
	@GOTOOLCHAIN=auto CGO_ENABLED=0 go test -v -count=1 ./docs/proof/...
