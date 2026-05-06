# 02 · Dependency Graph

## ASCII DAG (left → right)

```
                                                 ┌── W4 (claude connector) ──┐
                                                 │                            │
                                                 ├── W5 (codex connector)  ───┤
                                                 │                            │
                              ┌── W1 (store db) ─┼── W2 (msgs/sessions) ─┐    │
                              │                  │                       │    │
W0 (bootstrap & contracts) ───┼── W6 (config)  ──┤                       ├── W7 (api+handlers) ──┐
                              │                  │                       │                       │
                              ├── W9 (cost) ─────┘                       │                       │
                              │                  ┌── W3 (search/summ) ───┘                       │
                              │                  │                                                │
                              ├── W10 (providers) ──┐                                             │
                              │                    │                                              │
                              └── W13 (ui shell) ──┼─── W14 (ui components) ─┐                    │
                                                   │                          │                   │
                                                   │   ┌── W8 (sse hub) ──────┼───────────────────┤
                                                   │   │                      │                   │
                                                   │   └─── W11 (selector + summ tasks) ──┐       │
                                                   │                                       │       │
                                                   │                                       ├── W12 (app wiring) ── W16 (perf) ── W17 (release) ── W18 (alpha)
                                                   │                                       │
                                                   └── W15 (compact + wizard + restore) ──┘
```

## Critical path (longest chain)

`W0 → W1 → W2 → W3 → W11 → W15 → W12 → W16 → W17 → W18`

Sum of effort along the critical path:

| W0 | W1 | W2 | W3 | W11 | W15 | W12 | W16 | W17 | W18 | Total |
|---|---|---|---|---|---|---|---|---|---|---|
| L  | S  | S  | S  | M   | L   | M   | S   | M   | S   | ~12.5 days |

Everything else fits inside that window via parallelism.

## Non-critical tracks (absorb agents in parallel)

1. **Connectors track:** `W0 → {W4, W5}` → meets critical path at **W12**.
2. **Frontend track:** `W0 → W13 → W14` → meets critical path at **W15** (cross-cutting) and **W12**.
3. **Infra track:** `W0 → {W6, W7, W8, W9, W10}` → **W12**.

## Wide parallel layers (max concurrency)

| Wave | Concurrent workstreams | Max agents |
|---|---|---|
| 0 | W0 | 1 |
| 1 | W1, W4, W5, W6, W9, W10, W13 | **7** |
| 2 | W2, W3, W7, W8, W11, W14 | **6** |
| 3 | W12, W15 | 2 |
| 4 | W16, W17 | 2 |
| 5 | W18 | 1 + ad-hoc fan-out per bug |

## Direct dependency table

| Workstream | Depends on |
|---|---|
| W0  | — |
| W1  | W0 |
| W2  | W0, W1 |
| W3  | W0, W1, W2 |
| W4  | W0 |
| W5  | W0 |
| W6  | W0 |
| W7  | W0, W2, W3, W6 |
| W8  | W0, W7 |
| W9  | W0, W6 |
| W10 | W0 |
| W11 | W0, W3, W8, W10 |
| W12 | W1, W2, W3, W4, W5, W6, W7, W8, W9, W10, W11 |
| W13 | W0 |
| W14 | W7, W8, W13 |
| W15 | W4, W7, W11, W14 |
| W16 | W12 |
| W17 | W12, W14, W16 |
| W18 | W17 |
