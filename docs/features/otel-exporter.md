# `klyne otel emit` — OTel-shaped JSONL exporter

> Status: shipped. Explicit-invocation only — never an always-on push. The user owns when (and whether) data leaves the machine.

OpenTelemetry-shaped JSON Lines exporter. One span per assistant message
turn, with `gen_ai.*` attributes per the OTel GenAI working group draft
plus `klyne.*` resource fields so consumers can join back to klyne's
local DB.

Inspired by [`ColeMurray/claude-code-otel`](https://github.com/ColeMurray/claude-code-otel),
but kept dependency-free: no OpenTelemetry SDK, no network in the default path.

## CLI

```bash
klyne otel emit --since=24h --out spans.jsonl
# then upload spans.jsonl to your collector of choice
```

## Sample span

```json
{
  "trace_id": "acb1f71f0f3ac571721c49f0c5c4357c",
  "span_id": "390091ad23781d3e",
  "name": "gen_ai.completion",
  "kind": "SPAN_KIND_INTERNAL",
  "start_time": "2026-05-12T02:21:21.489Z",
  "end_time": "2026-05-12T02:21:29.030Z",
  "attributes": {
    "gen_ai.system": "anthropic",
    "gen_ai.request.model": "claude-opus-4-7",
    "gen_ai.usage.input_tokens": 228687,
    "gen_ai.usage.output_tokens": 1234,
    "gen_ai.cost.usd": 4.36784625,
    "klyne.session_id": "...",
    "klyne.project_path": "..."
  },
  "resource": {"service.name": "klyne", "service.namespace": "ai-coding-cli"}
}
```

## Implementation

- `cmd/klyne/otel.go` — CLI surface
- `internal/otelexport/` — span construction + JSONL serialiser
- Trace ID + span ID are deterministic FNV-64a hashes of session/message IDs — re-exporting the same data produces identical IDs (useful when streaming spans into a backing store the user doesn't fully control).
