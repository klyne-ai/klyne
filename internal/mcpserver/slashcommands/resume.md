---
description: Resume a past session with a token-cost receipt — rank candidates and inject the chosen one
---

Run `klyne resume list` in a shell to see ranked past sessions for this working directory.

By default the list is capped at 3 high-score candidates. To browse more:

```
klyne resume list --limit 10   # raise the cap, keep the score threshold
klyne resume list --all        # every ingested session, no threshold, no cap
```

Each candidate shows:
- The project path and how long ago the session was active
- The number of recorded decisions
- Two resume commands with estimated token sizes

Pick a session and run one of:

```
klyne resume hydrate <session-id>                # full payload, ~6–8K tokens
klyne resume hydrate <session-id> --decisions-only  # decisions only, ~800 tokens
klyne resume hydrate <session-id> --last-N=10    # last 10 turns only
klyne resume hydrate <session-id> --budget=4096  # trim to 4K tokens
klyne resume hydrate <session-id> --dry-run      # preview without injecting
```

The command always prints a **receipt** (estimated token cost + what was trimmed) before emitting the payload so you can confirm the size.

Copy the output into your next prompt or pipe it directly:

```
klyne resume hydrate <session-id> | pbcopy
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--decisions-only` | off | Include only recorded decisions (~800 tokens) |
| `--last-N=<n>` | all turns | Include only the last N conversation turns |
| `--budget=<tokens>` | 8192 | Trim payload to this many tokens |
| `--dry-run` | off | Preview payload without injecting |
