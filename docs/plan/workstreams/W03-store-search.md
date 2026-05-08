# W3 · Store: Search (FTS5 BM25) + Summaries DAO

> **Wave:** 2 · **Effort:** S · **Depends on:** W0, W1, W2 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else.

---

## Goal

Implement FTS5 BM25 search over `messages_fts` and a CRUD DAO for `session_summaries` (auto-incrementing version per session, latest-by-session lookup).

---

## Spec sections

- §7 (schema, FTS5 design)
- §12 (search p95 budget)

---

## Owned paths

```
internal/store/search.go
internal/store/search_test.go
internal/store/summaries.go
internal/store/summaries_test.go
```

---

## Inputs

- W0 contracts (`Summary` / `SearchHit` types — add to `connector.go` if missing via a contract-change PR; otherwise keep local-to-store types and surface via accessors).
- W1 `*store.DB`.
- W2 messages DAO (FTS triggers fire on `messages` insert — ensure W2's `InsertMessage` triggers FTS row creation; this is automatic via the `messages_fts` triggers in W0's `002_fts.sql`).

---

## Outputs

```go
package store

// search.go
type SearchHit struct {
    MessageID string
    SessionID string
    TS        int64
    Role      string
    Snippet   string  // FTS5 snippet() output
    Rank      float64 // bm25() score
}

func Search(ctx context.Context, db *DB, q string, limit int) ([]SearchHit, error)

// summaries.go
type Summary struct {
    SessionID string
    Version   int
    Text      string
    Model     string
    TS        int64
}

func InsertSummary(ctx context.Context, db *DB, s *Summary) error
// Auto-increments version per session_id

func LatestSummary(ctx context.Context, db *DB, sessionID string) (*Summary, error)

func ListSummaries(ctx context.Context, db *DB, sessionID string) ([]*Summary, error)
```

---

## Acceptance criteria

- [ ] `Search` uses `bm25()` for ranking; results ordered by rank ASC (lower bm25 score = better match in SQLite FTS5).
- [ ] Query time <50 ms on 10K-msg fixture (sanity check; full p95 in W16).
- [ ] `Search` snippet length capped (e.g., 64 tokens around match) using `snippet(messages_fts, ...)`.
- [ ] `InsertSummary` increments version atomically — concurrent inserts for same session never produce duplicate version numbers.
- [ ] `LatestSummary` returns highest-version summary for the session.
- [ ] 80%+ coverage including: empty result set, single-session results, multi-session results, special characters in query (e.g., `'`, `"`, `*`).

---

## Testing requirements (TDD-first)

1. `TestSearch_RanksNeedle` — seed 1000 messages with one "needle" containing rare phrase; assert it ranks #1.
2. `TestSearch_EmptyQuery` — returns empty, not error.
3. `TestSearch_LimitRespected` — limit=10 returns at most 10 hits.
4. `TestSearch_SpecialChars` — query with single-quote, double-quote, FTS operators (`*`, `OR`, `NEAR`).
5. `TestSearch_Performance` — 10K-msg fixture; query time <50ms (skip on `-short`).
6. `TestInsertSummary_AutoVersions` — 5 concurrent inserts → versions 1..5, no duplicates.
7. `TestLatestSummary_ReturnsHighest` — insert v1, v2, v3 → `LatestSummary` returns v3.

---

## Hard boundaries

- Do **NOT** call any AI provider (W11 owns that). Search is pure SQL here.
- Do **NOT** touch `messages` / `sessions` DAOs (W2).
- Do **NOT** modify migrations (W0).

---

## Done

When W7's `/search` handler and W11's summary persistence can call your functions and the perf bench in W16 hits the spec §12 budget.
