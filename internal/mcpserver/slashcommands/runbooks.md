---
description: Surface recurring Bash command sequences in this project as candidate runbooks
---

Call the `mcp__klyne__propose_runbooks` MCP tool with no arguments — let it auto-resolve the project from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered the ranked candidates, occurrence counts, last-seen timestamps, and the steps for each candidate.

Do not summarise, paraphrase, or editorialize — output the markdown field byte-for-byte and stop.

If the user picks a candidate to accept, call `mcp__klyne__accept_runbook` with the chosen signature. If they explicitly reject one, call `mcp__klyne__dismiss_runbook`. Confirm the action back to the user using the tool's structured response.
