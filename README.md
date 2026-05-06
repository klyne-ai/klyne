# agentdeck

> Local-first mission control for the AI coding CLIs you already run.

![Status](https://img.shields.io/badge/status-pre--alpha-orange)

## What is this?

agentdeck is a local-first, open-source dashboard that unifies every AI coding CLI
you already use — starting with Claude Code and Codex — into a single browser-based
mission control. It runs as a tiny Go daemon on your machine, tails the JSONL session
logs your CLIs already write, and gives you searchable summaries, cross-CLI threads,
cost tracking, and recovery from `/compact` events. You don't change your workflow.
You keep typing `claude` and `codex` exactly as you do today.

## Status

**Under construction.** v1.0 is in progress per [docs/plan/](docs/plan/).

Full launch README arrives in v1.0.0 release (W17).

## Build

### Prerequisites

- Go 1.23+
- Node 20+ (for the SvelteKit UI — optional until W13 lands)
- [golangci-lint](https://golangci-lint.run/usage/install/) — required for `make lint`

### Quick start

```sh
# Run all checks (vet + lint + test)
make ci

# Build the binary
make build

# Run (once the binary exists)
./bin/agentdeck start
```

### Troubleshooting

- If `make lint` fails with "golangci-lint not found", install it:
  `brew install golangci-lint` (macOS) or see https://golangci-lint.run/usage/install/
- If `go:embed` fails on a fresh clone, run `make build-ui` once to create `ui/build/`.
  If `ui/package.json` does not exist yet (W13 is not landed), the target creates an
  empty stub directory automatically.

## Plan and contracts

- Architecture and workstream plan: [docs/plan/README.md](docs/plan/README.md)
- Frozen cross-stream contracts: [docs/contracts.md](docs/contracts.md)

## License

MIT — see [LICENSE](LICENSE).

---

*Full launch README arrives in v1.0.0 release (W17).*
