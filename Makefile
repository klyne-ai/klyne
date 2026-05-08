.PHONY: build build-ui dev test vet lint tidy ci clean release

# Default target. CGO is off because modernc.org/sqlite is pure-Go (spec §5);
# leaving CGO on with the macOS 26 + Go 1.21 internal linker can produce
# binaries dyld rejects ("missing LC_UUID load command"). Bump to Go 1.23+
# and drop this flag once the toolchain catches up to the spec.
build: build-ui
	GOTOOLCHAIN=local CGO_ENABLED=0 go build -o bin/klyne ./cmd/klyne

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

# Release placeholder — W17 wires goreleaser.
# goreleaser release --clean
release:
	@echo "Release is wired in W17 (goreleaser). Run: goreleaser release --clean"
