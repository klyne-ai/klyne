# W1 · Store: Open + Migrate + Dual Handles

> **Wave:** 1 · **Effort:** S (~1d) · **Depends on:** W0 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. **§18 decisions are LOCKED.** TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths" below. Need to change anything else? Open a `contract-change` PR against W0's contract files.

---

## Goal

Implement the SQLite store layer: `Open(path)` returns a `*store.DB` with separate read (`MaxOpenConns=N`) and write (`MaxOpenConns=1`) handles, applying all 6 PRAGMAs from spec §5. Migrations from `internal/store/migrations/` apply idempotently and track version in `schema_migrations`.

---

## Spec sections

- §5 (PRAGMAs, dual-handle pattern, WAL rationale)
- §7 (schema your store will host)

---

## Owned paths

```
internal/store/db.go
internal/store/db_test.go
internal/store/migrate.go
internal/store/migrate_test.go
```

---

## Inputs

- W0's `internal/store/migrations/*.sql` (read-only — these are W0-owned).
- W0's `internal/store/migrations/embed.go` (provides `migrations.FS embed.FS`).

---

## Outputs (interface for W2 / W3 / W7 / W12)

```go
package store

type DB struct { /* unexported fields */ }

func Open(path string) (*DB, error)

func (db *DB) Read() *sql.DB    // MaxOpenConns = N (e.g., 8)
func (db *DB) Write() *sql.DB   // MaxOpenConns = 1
func (db *DB) Close() error
func (db *DB) SchemaVersion(ctx context.Context) (int, error)
```

`migrate.Apply(ctx, db.Write())` is idempotent — applies all migrations from `migrations.FS` not yet recorded in `schema_migrations`.

---

## PRAGMAs to apply (spec §5, all 6, on every connection via SQLite connection hook)

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 268435456;     -- 256 MB
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
```

Verify each by querying `PRAGMA <name>` after open in a unit test.

---

## Acceptance criteria

- [ ] All 6 PRAGMAs verifiable by querying `PRAGMA <name>` post-open.
- [ ] Migrations idempotent — open twice, no error, `schema_migrations` count unchanged second time.
- [ ] 80%+ coverage in `internal/store/`.
- [ ] Concurrent-write stress test: 10 goroutines × 1000 inserts via `Write()`, **no `SQLITE_BUSY`** (busy_timeout handles it).
- [ ] `Close()` waits for in-flight writes, then closes both handles cleanly.
- [ ] Cross-platform: passes CI on macOS / Linux / Windows.

---

## Testing requirements (TDD-first)

Write `db_test.go` with these test cases **before** implementation:

1. `TestOpen_CreatesDB` — file is created if missing.
2. `TestOpen_AppliesPRAGMAs` — query each PRAGMA, assert expected value.
3. `TestOpen_RunsMigrations` — `schema_version` matches latest migration ID.
4. `TestOpen_Idempotent` — open, close, reopen — no error, no duplicate work.
5. `TestRead_Concurrent` — 10 goroutines reading, no errors.
6. `TestWrite_Serialized` — verify `MaxOpenConns(write) == 1`.
7. `TestWrite_Stress` — 10 goroutines × 1000 inserts via the write handle, no `SQLITE_BUSY`.

Use `t.TempDir()` for DB paths. No globals.

---

## Hard boundaries

- Do **NOT** add any DAOs (`messages.go`, `sessions.go`, `summaries.go`, `search.go` are owned by W2 / W3).
- Do **NOT** call any provider/connector packages.
- Do **NOT** modify `internal/store/migrations/*.sql` — those are W0-owned.

---

## Done

When the W2 agent can `import "<module>/internal/store"` and call `db.Write()` and `db.Read()` against a real SQLite DB and you've shipped the stress test green.
