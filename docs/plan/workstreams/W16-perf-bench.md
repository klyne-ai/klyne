# W16 · Performance Bench + Perf-Regression Test

> **Wave:** 4 · **Effort:** S · **Depends on:** W12 · **Recommended skills:** `golang-patterns` + `optimize`

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage on bench harness code (the benches themselves are the tests). After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". If you find a hotspot in another package, open a scoped PR to that workstream's owner — don't fix it in their files yourself.

---

## Goal

Validate every performance budget in spec §12 with a CI-runnable bench. Add a perf-regression check that fails CI if a budget is violated.

---

## Spec §12 budgets to validate

| Metric | Budget | How |
|---|---|---|
| Idle RAM (daemon) | < 40 MB | `ps -o rss=` after 1h idle (OS-gated test, skip on `-short`) |
| Active RAM (1 live session, 5K msg DB) | < 80 MB | same with session |
| Cold-start to UI paint | < 200 ms | scripted (puppeteer-light or `curl + chromedp`) |
| FTS5 search p95 | < 50 ms | Go bench over 100K msgs |
| JSONL ingest throughput | ≥ 5,000 msg/sec | bulk back-fill bench |
| Summary turnaround | < 3 s | (mocked provider — skip in CI; document) |
| Disk footprint | < 2× JSONL size | sum DB + FTS index, divide by JSONL size |
| SSE latency (event → browser) | < 100 ms p95 | `httptest` + custom EventSource client, percentile collector |
| Binary size | < 25 MB | `stat` on built binary |

---

## Owned paths

```
internal/bench/bench_test.go             (Go benchmarks: search, ingest, SSE latency, summary turnaround stub)
internal/bench/ram_test.go               (RAM idle + active simulation; OS-gated)
internal/bench/cold_start_test.go        (cold-start measurement)
internal/bench/disk_test.go              (disk footprint vs JSONL size)
internal/bench/binary_test.go            (binary size check)
internal/bench/fixtures/100k_msgs.go     (synthetic message generator)
scripts/bench.sh                         (runs all benches, emits JSON to stdout)
scripts/bench-ci.sh                      (CI wrapper: fail on budget violation)
docs/perf.md                             (results table, methodology, how to reproduce)
.github/workflows/perf.yml               (CI job: nightly + on-demand via workflow_dispatch)
```

---

## Inputs

- W12 working daemon (you can `app.BuildOnly()` for unit-style benches).
- W3 search.
- W2 messages DAO for ingest bench.

---

## Outputs

- JSON bench report uploaded as CI artifact.
- `docs/perf.md` with the latest numbers committed (regenerate on every release).
- Status check: green if all budgets met, red otherwise.

---

## Acceptance criteria

- [ ] Every spec §12 row has a corresponding bench (with documented exceptions for "summary turnaround" which depends on real provider latency).
- [ ] Bench output is structured JSON (one row per metric).
- [ ] CI uploads JSON as artifact; visible in workflow summary.
- [ ] **All budgets met on a plain GitHub Actions runner** (`ubuntu-latest`).
- [ ] If any budget fails, `scripts/bench-ci.sh` exits non-zero with a clear message naming the offending metric.
- [ ] `docs/perf.md` is reproducible — running `bash scripts/bench.sh > docs/perf-output.json` produces consistent numbers.

---

## Testing requirements

Each bench is itself a test. Wrap them in Go subtests so they run with `go test`. Use `-bench` for the search and ingest benches. Use `-short` to skip the 1-hour idle RAM test on dev runs.

---

## Hard boundaries

- Do **NOT** fix performance issues in other packages directly. If a bench shows W2's ingest is slow, file an issue / open a PR scoped to W2 with the bench data — don't edit W2's files.
- Do **NOT** add fixtures larger than 50 MB to the repo. Use generators (`fixtures/100k_msgs.go`).
- Do **NOT** depend on real network calls (e.g., real Anthropic API) in any bench that runs in CI.

---

## Done

When `bash scripts/bench-ci.sh` runs green on a plain GitHub Actions runner and `docs/perf.md` has the latest numbers committed.
