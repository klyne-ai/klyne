# klyne: privacy and security

klyne is local-first by design. It reads JSONL files Claude Code and
Codex already wrote to your disk, stores them in a local SQLite database,
and serves a UI on `127.0.0.1`. The only outbound network call in the
default configuration is the Claude `/api/oauth/usage` endpoint, using
the OAuth token Claude Code already stored in your macOS Keychain.
Everything else stays on your machine. AI features (summary, advisor,
embeddings) are off-by-default and only call third-party providers when
you explicitly set a Bring-Your-Own-Key environment variable.

This document is the authoritative privacy and security reference. Every
claim below cites a code path so you can verify it yourself.

## What klyne reads

| Path / source | Why | Code reference |
|---|---|---|
| `~/.claude/projects/<encoded>/*.jsonl` | Claude Code session message ingestion (recursive, per-session JSONL files). Files are tailed read-only; offsets are tracked in memory. | `internal/connectors/claude/watch.go:60-83` (Watch entry), `:286-313` (discover), `:160-210` (replay), `:212-266` (tail) |
| `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | Codex CLI session ingestion. Date-partitioned directories walked up to 4 levels deep. | `internal/connectors/codex/watch.go:43-87` (discoverFiles), `:88-148` (emitNew tail), `:262-311` (watch loop) |
| `~/.codex/sessions/**/rollout-*.jsonl` (read-only, newest-first) | One-shot snapshot read for rate-limit badge — no network call. | `internal/usage/codex_snapshot.go:21-27` (defaultCodexSessionsDir), `:113-142` (readFresh), `:153-194` (listRolloutFiles), `:224-267` (scanFileForSnapshot) |
| macOS Keychain entry: service `Claude Code-credentials` (generic password) | Read on every refresh of the Claude rate-limit badge. Provides the OAuth access token Claude Code itself stored. Darwin only — non-Darwin returns `ErrUnsupportedPlatform` and the caller silently degrades. | `internal/claudeauth/credentials.go:69-75` (LoadClaudeCredentials), `internal/claudeauth/keychain_darwin.go:20-38` (shells out to `security find-generic-password -s "Claude Code-credentials" -w`), `internal/claudeauth/keychain_other.go:8-10` (returns ErrUnsupportedPlatform on every non-Darwin OS) |
| Environment variable `ANTHROPIC_API_KEY` | BYOK credential for the Anthropic provider, used only when the user opts into the summary / advisor / embedding feature with `provider=anthropic`. | `internal/ai/providers/anthropic.go:64-82` (NewAnthropic reads env), `internal/ai/providers/detect.go:62-80` (availability detection) |
| Environment variable `OPENAI_API_KEY` | BYOK credential for the OpenAI provider. | `internal/ai/providers/openai.go:60-75` (NewOpenAI reads env), `internal/ai/providers/detect.go:82-101` |
| Environment variable `GEMINI_API_KEY` | BYOK credential for the Gemini provider. | `internal/ai/providers/gemini.go:46-61` (NewGemini reads env), `internal/ai/providers/detect.go:103-121` |
| `~/.klyne/config.toml` | Optional user configuration — server bind addr, connector roots, AI model preferences. | `internal/config/paths.go:38-40` (ConfigFile), `internal/config/schema.go:84-109` (Defaults) |
| `~/.klyne/pricing.json` (optional) | User-supplied LiteLLM-style pricing override. | `internal/config/paths.go:49-51` (PricingOverridePath) |

klyne does **not** modify any source JSONL file. The watchers open
each file with `os.Open` (read-only) and only track byte offsets to
resume tailing — see `internal/connectors/claude/watch.go:173-178` and
`internal/connectors/codex/watch.go:103-108`.

## What klyne writes

| Path | Contents | Code reference |
|---|---|---|
| `~/.klyne/klyne.db` (and SQLite WAL sidecar files) | The local SQLite database holding sessions, messages, search index, costs. WAL journaling and a single-writer connection enforced. | `internal/config/paths.go:42-45` (DBPath), `internal/config/schema.go:90` (default), `internal/store/db.go:18-45` (PRAGMAs and pool config), `:78-118` (Open) |
| `~/.klyne/config.toml` | Persisted user configuration. | `internal/config/paths.go:38-40` |
| Stdout / stderr | Operational logs via `log.Printf` and `log/slog`. Logs identify file paths and event counts, never message bodies, tokens, or API keys. | e.g. `internal/connectors/claude/watch.go:70,106` and `internal/usage/oauth.go` (intentionally suppresses error context — see `:147-149`) |

The path resolves to `~/.klyne` on macOS, Linux, and Windows
(`%USERPROFILE%\.klyne` on Windows) — confirmed at
`internal/config/paths.go:21-35`. There is no separate Application
Support / XDG path.

klyne never writes to `~/.claude/` or `~/.codex/`. Both directories
are read-only inputs.

## Network calls (the entire allowlist)

This is every outbound network request the daemon can make. Verified by
searching for `http.NewRequest` and `https://` across `internal/`.

| URL | Method | When | Auth | Cache | Code reference |
|---|---|---|---|---|---|
| `https://api.anthropic.com/api/oauth/usage` | GET | Every `/usage` request, served from cache for up to 5 minutes. | `Authorization: Bearer <Claude OAuth token from Keychain>`, `anthropic-beta: oauth-2025-04-20`, identifying `User-Agent: klyne/0.1`. | 5-min TTL on success; ~2.5-min TTL on failure to suppress flapping. Returns `(nil, nil)` on missing creds, network error, or non-200 — never surfaces an error to the user. | `internal/usage/oauth.go:25-30` (constants), `:120-176` (fetchFresh) |
| `https://api.anthropic.com/v1/messages` | POST | Only when `ANTHROPIC_API_KEY` is set AND the user invokes summary / break-advisor with `provider=anthropic`. | `x-api-key: $ANTHROPIC_API_KEY`. | None. | `internal/ai/providers/anthropic.go:24-27` (URL constants), `:102-208` (Chat) |
| `https://api.openai.com/v1/chat/completions` | POST | Only when `OPENAI_API_KEY` is set AND the user invokes the AI feature with `provider=openai`. | `Authorization: Bearer $OPENAI_API_KEY`. | None. | `internal/ai/providers/openai.go:15-19`, `:153-247` (Chat) |
| `https://api.openai.com/v1/embeddings` | POST | Same condition as above, only on the embedding code path (off by default in v1 — see `internal/config/schema.go:106`). | `Authorization: Bearer $OPENAI_API_KEY`. | None. | `internal/ai/providers/openai.go:84-150` (Embed) |
| `https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent` | POST | Only when `GEMINI_API_KEY` is set AND the user invokes the AI feature with `provider=gemini`. | `?key=$GEMINI_API_KEY` query parameter. | None. | `internal/ai/providers/gemini.go:15-17`, `:104-226` (Chat) |
| `http://localhost:11434/api/tags` and `http://localhost:11434/api/chat` | GET / POST | Only when the user selects the Ollama provider. Loopback-only by default. | None. | None. | `internal/ai/providers/ollama.go:15-20` (constants), `:75-86` (IsReachable probe), `:110-194` (Chat) |

That is the complete list. There is no telemetry endpoint, no
analytics beacon, no crash reporter, no auto-update channel, and no
"phone home" of any kind.

The HTTP server itself binds to `127.0.0.1:7878` by default — see
`internal/config/schema.go:25-29` and `:87`. A `loopbackOnly`
middleware also rejects any incoming request whose source IP is not
loopback, with HTTP 403 — see `internal/api/http.go:90-112`. The
combination defends against the case where the OS routes an external
request to the port.

## Credential handling

**Claude OAuth token.** Read fresh from the macOS Keychain on every
expired-cache refresh of `/usage`. The token is held in memory only for
the duration of a single Anthropic request (see
`internal/usage/oauth.go:120-145`). klyne never writes the token
back to disk and never echoes it in API responses or logs. On non-Darwin
systems, the Keychain reader returns `ErrUnsupportedPlatform` and the
fetcher silently degrades to "OAuth data unavailable"
(`internal/usage/oauth.go:124-128`). The token leaves the machine only
in the single TLS request to `api.anthropic.com/api/oauth/usage`.

**BYOK API keys.** Environment variables only. Quoted verbatim from
`internal/ai/provider.go:5-9`:

> CRITICAL LEGAL CONSTRAINT (spec §8, enforced 2026-04-04):
> Anthropic enforces server-side that Claude Code OAuth tokens
> (~/.claude) cannot be reused by third-party tools. This package
> NEVER reads any file under ~/.claude or ~/.codex. API keys come
> exclusively from environment variables.

This constraint is repeated in every provider:
`internal/ai/providers/anthropic.go:1-9`,
`internal/ai/providers/openai.go:42-43,52-53`,
`internal/ai/providers/detect.go:46,62-63,82-83`. There is a unit test
that asserts the Anthropic provider does not open any file under a fake
`~/.claude` even with `ANTHROPIC_API_KEY` unset
(`internal/ai/providers/anthropic_test.go:168-...`).

If a key environment variable is unset, the provider returns
`ai.ErrNoCredential` immediately
(`internal/ai/provider.go:108-114`). It does not search auth files,
config files, or other locations.

**No echoing.** The provider-detection API returns availability and a
short reason string only — never the key value itself. See
`internal/ai/provider.go:91-106` (`ProviderInfo` shape) and
`internal/ai/providers/detect.go:60-121` for the exact reasons emitted
("`ANTHROPIC_API_KEY set`" / "`ANTHROPIC_API_KEY not set`"). The token
length, prefix, and value are never returned.

## Threat model

klyne guards against:

1. **Accidental cloud upload of session content.** No `POST` request in
   `internal/` targets any URL outside the BYOK provider list and the
   Anthropic OAuth-usage GET. Verified by searching `internal/` for
   `http.NewRequest` and `https://`. Session messages are never
   attached to any outbound request body except the one the user
   explicitly triggers via the AI provider configured.
2. **Credential exfiltration.** API keys come from environment
   variables only, are never persisted to the SQLite DB or config
   file, are never echoed in API responses (see `ProviderInfo` shape),
   and are not included in log lines.
3. **Cross-tenant data leakage.** The daemon is single-user by
   construction. There is no auth model, no multi-tenant data path,
   and the HTTP server is loopback-only — see
   `internal/api/http.go:97-112`.
4. **OAuth-token reuse for the wrong purpose.** The Claude OAuth token
   is sent only to `https://api.anthropic.com/api/oauth/usage` (a
   read-only quota endpoint). It is never used as the API key for any
   `/v1/messages` request — that path requires a separate
   `ANTHROPIC_API_KEY`, per the legal constraint above.

klyne does **not** guard against:

1. **A hostile process running as your user reading the SQLite DB.**
   The DB lives at `~/.klyne/klyne.db` with default user-only
   permissions. klyne does not enforce a stricter ACL, encrypt
   the DB at rest, or sandbox the process. If your user account is
   compromised, the DB is readable.
2. **Hostile JSONL injection.** The watchers parse whatever JSONL
   appears under `~/.claude/projects` and `~/.codex/sessions`. If an
   attacker can already write into those directories, they can craft
   content the UI will render. Mitigations: the API is loopback-only,
   the UI escapes message content, and message size is bounded
   (`internal/connectors/claude/watch.go:17-26`,
   `internal/connectors/codex/watch.go:117-148`). The threat surface
   requires a prior local compromise of the CLI directories.
3. **TLS man-in-the-middle on the Anthropic OAuth call.** The HTTP
   client uses Go's default TLS verification but does not pin
   certificates. A user-installed root CA that issues a fraudulent
   `api.anthropic.com` certificate would not be detected.
4. **An untrusted Ollama server.** If you point the Ollama base URL at
   a non-loopback host, prompt content is sent there. The default is
   `http://localhost:11434` (`internal/ai/providers/ollama.go:16`).

## Reporting a vulnerability

If you believe you have found a security vulnerability in klyne,
please open a private security advisory on the GitHub repository
(`https://github.com/klyne-ai/klyne` — see the User-Agent at
`internal/usage/oauth.go:144`). Please do not file a public issue for
exploitable vulnerabilities until a fix is available. If a maintainer
contact email is published in the repository root, that is also an
acceptable channel; otherwise, the GitHub advisory flow is preferred.

## Verifying claims yourself

Run these commands from the repository root. The output should match
the claims in this document; if it does not, please file an issue.

1. List every outbound HTTPS URL referenced by the daemon code:

   ```sh
   grep -rn "https://" internal/ --include="*.go" | grep -v "_test.go"
   ```

   Expected hosts: `api.anthropic.com` (OAuth-usage GET and Messages
   POST), `api.openai.com`, `generativelanguage.googleapis.com`. Plus
   pricing-source documentation comments in `internal/cost/pricing.go`
   (no calls — comments only).

2. List every place the daemon constructs an HTTP request:

   ```sh
   grep -rn "http.NewRequestWithContext\|http.NewRequest" internal/ --include="*.go" | grep -v "_test.go"
   ```

   Expected matches are confined to `internal/usage/oauth.go` and
   `internal/ai/providers/`. Anything else is unexpected and worth a
   bug report.

3. Inspect what is stored locally:

   ```sh
   sqlite3 ~/.klyne/klyne.db ".tables"
   sqlite3 ~/.klyne/klyne.db ".schema"
   ```

   The schema reflects sessions, messages, costs, and search indexes
   — no credential tables, no remote-account tables.

4. Confirm the HTTP server is loopback-only:

   ```sh
   lsof -nP -iTCP:7878 -sTCP:LISTEN
   ```

   The `NAME` column should show `127.0.0.1:7878`, never `*:7878`.

5. Verify the legal constraint that AI providers never read
   `~/.claude` or `~/.codex` for credentials:

   ```sh
   grep -rn "\.claude\|\.codex" internal/ai/ --include="*.go"
   ```

   The only matches are comments documenting the constraint and tests
   that assert the constraint holds.

## Cross-reference

For broader product context — how privacy fits into the value
proposition and what comparable tools do — see
`docs/marketing/comparison-and-gaps.md`, in particular the user-problem
row "I need proof this is local and safe."
