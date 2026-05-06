# 01 · Workstreams Overview

Single-table view of all 19 workstreams. Click into [`workstreams/`](./workstreams/) for the paste-ready brief.

Effort sizes: **XS** ≤ 0.5d · **S** ≤ 1d · **M** ≤ 1.5d · **L** ≤ 2.5d · **XL** ≤ 4d (single-agent-equivalent).

| ID | Name | Wave | Effort | Owns | Depends on | Brief |
|---|---|---|---|---|---|---|
| **W0**  | Bootstrap & shared contracts          | 0 | L  | repo skeleton, all contract files, CI, fixtures | — | [`workstreams/W00-bootstrap.md`](./workstreams/W00-bootstrap.md) |
| **W1**  | Store: open + migrate + dual handles   | 1 | S  | `internal/store/db.go`, `migrate.go` | W0 | [`workstreams/W01-store-db.md`](./workstreams/W01-store-db.md) |
| **W2**  | Store: messages + sessions DAOs        | 2 | S  | `internal/store/messages.go`, `sessions.go` | W1 | [`workstreams/W02-store-daos.md`](./workstreams/W02-store-daos.md) |
| **W3**  | Store: search (FTS5) + summaries DAO   | 2 | S  | `internal/store/search.go`, `summaries.go` | W1, W2 | [`workstreams/W03-store-search.md`](./workstreams/W03-store-search.md) |
| **W4**  | Claude Code connector                  | 1 | M  | `internal/connectors/claude/**` | W0 | [`workstreams/W04-connector-claude.md`](./workstreams/W04-connector-claude.md) |
| **W5**  | Codex CLI connector                    | 1 | M  | `internal/connectors/codex/**` | W0 | [`workstreams/W05-connector-codex.md`](./workstreams/W05-connector-codex.md) |
| **W6**  | Config loader                          | 1 | XS | `internal/config/**` | W0 | [`workstreams/W06-config.md`](./workstreams/W06-config.md) |
| **W7**  | HTTP API + read-only handlers          | 2 | M  | `internal/api/http.go`, `handlers/**` (no events/restore/wizard) | W0, W2, W3, W6 | [`workstreams/W07-api-handlers.md`](./workstreams/W07-api-handlers.md) |
| **W8**  | SSE hub + `/events` endpoint           | 2 | S  | `internal/api/sse.go`, `handlers/events.go` | W0, W7 | [`workstreams/W08-sse-hub.md`](./workstreams/W08-sse-hub.md) |
| **W9**  | Cost engine + pricing table            | 1 | S  | `internal/cost/**` | W0, W6 | [`workstreams/W09-cost-engine.md`](./workstreams/W09-cost-engine.md) |
| **W10** | AI providers (Anthropic/OpenAI/Gemini/Ollama) | 1 | M  | `internal/ai/provider.go`, `providers/**` | W0 | [`workstreams/W10-ai-providers.md`](./workstreams/W10-ai-providers.md) |
| **W11** | Smart selector + summarize/title tasks | 2 | M  | `internal/ai/selector.go`, `tasks/**` | W0, W3, W8, W10 | [`workstreams/W11-selector-summarize.md`](./workstreams/W11-selector-summarize.md) |
| **W12** | Wiring layer + cobra commands          | 3 | M  | `internal/app/**`, `cmd/agentdeck/**` | W1–W11 | [`workstreams/W12-app-wiring.md`](./workstreams/W12-app-wiring.md) |
| **W13** | Frontend foundation (shell + lib)      | 1 | M  | `ui/` shell, `ui/src/lib/**` | W0 | [`workstreams/W13-ui-shell.md`](./workstreams/W13-ui-shell.md) |
| **W14** | Frontend components + routes           | 2 | L  | `ui/src/lib/components/**` (non-wizard), business routes | W13, W7, W8 | [`workstreams/W14-ui-components.md`](./workstreams/W14-ui-components.md) |
| **W15** | Compact recovery + wizard + restore    | 3 | L  | restore/wizard handlers, compact heuristic, wizard UI | W4, W7, W11, W14 | [`workstreams/W15-compact-wizard.md`](./workstreams/W15-compact-wizard.md) |
| **W16** | Performance bench + perf-regression    | 4 | S  | `internal/bench/**`, `scripts/bench.sh`, `docs/perf.md` | W12 | [`workstreams/W16-perf-bench.md`](./workstreams/W16-perf-bench.md) |
| **W17** | Release pipeline + install + landing   | 4 | M  | `.github/workflows/release.yml`, `scripts/install.*`, `Formula/`, `winget/`, `README.md`, `docs/landing/` | W12, W14, W16 | [`workstreams/W17-release.md`](./workstreams/W17-release.md) |
| **W18** | Alpha bug-bash + final cut             | 5 | S  | ad-hoc per bug | W17 | [`workstreams/W18-alpha.md`](./workstreams/W18-alpha.md) |

## Recommended agent type per workstream

| Workstream | Recommended Claude Code skill / agent profile |
|---|---|
| W0 | `general-purpose` (opus). Single agent. Most leverage in the project. |
| W1, W2, W3 | `golang-patterns` + `tdd-guide` |
| W4, W5 | `golang-patterns` + `tdd-guide` + `regex-vs-llm-structured-text` (parsing JSONL) |
| W6 | `golang-patterns` + `tdd-guide` |
| W7 | `golang-patterns` + `tdd-guide` |
| W8 | `golang-patterns` + `tdd-guide` |
| W9 | `golang-patterns` + `tdd-guide` |
| W10 | `general-purpose` + `claude-api` skill |
| W11 | `general-purpose` + `cost-aware-llm-pipeline` + `claude-api` |
| W12 | `general-purpose` (opus). Single agent — touches every package's exported API. |
| W13 | `coding-standards` + `frontend-patterns` + `tdd-guide` |
| W14 | `frontend-patterns` + `coding-standards` + `tdd-guide` |
| W15 | `general-purpose` + `regex-vs-llm-structured-text` + `frontend-patterns` |
| W16 | `golang-patterns` + `optimize` |
| W17 | `general-purpose` + `deployment-patterns` |
| W18 | human + `general-purpose` ad-hoc per bug |
