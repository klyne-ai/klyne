# W2 · Store: Messages + Sessions DAOs

> **Wave:** 2 · **Effort:** S · **Depends on:** W0, W1 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else.

---

## Goal

Implement DAOs for `messages` and `sessions` tables: insert, get, list-with-pagination, plus the rollup updates (`last_msg_at`, `msg_count`, `tokens_in`, `tokens_out`, `cost_usd`) on `sessions`, triggered inside `messages.Insert` (single transaction).

---

## Spec sections

- §7 (schema, ingest pipeline)

---

## Owned paths

```
internal/store/messages.go
internal/store/messages_test.go
internal/store/sessions.go
internal/store/sessions_test.go
```

---

## Inputs

- W0's `Message`, `Session` types from `internal/connectors/connector.go`.
- W1's `*store.DB` with `Read()` / `Write()` handles.

---

## Outputs

```go
package store

// messages.go
func InsertMessage(ctx context.Context, db *DB, m *connectors.Message) error
// Same tx: insert into messages, then UPDATE sessions SET ... WHERE id = m.SessionID

func ListMessagesBySession(ctx context.Context, db *DB, sessionID string, limit int, before int64) ([]*connectors.Message, error)
// Cursor pagination by (ts, id); ordered ts ASC

// sessions.go
func UpsertSession(ctx context.Context, db *DB, s *connectors.Session) error

func ListSessions(ctx context.Context, db *DB, filter SessionFilter) ([]*connectors.Session, error)

func GetSession(ctx context.Context, db *DB, id string) (*connectors.Session, error)
```

`SessionFilter` includes `cli`, `project_path`, `limit`, `before`.

---

## Acceptance criteria

- [ ] `InsertMessage` and `UpsertSession` use the **write** handle (`db.Write()`).
- [ ] All read functions use the **read** handle (`db.Read()`).
- [ ] `InsertMessage` updates session counters in the **same transaction** — no two-phase write.
- [ ] `ListMessagesBySession` returns messages in `ts ASC` order; pagination cursor stable across concurrent inserts.
- [ ] 80%+ coverage.
- [ ] Inserting 5,000 messages in one tx completes in <1s on dev laptop (proxy for spec §12 ingest budget; full bench in W16).

---

## Testing requirements (TDD-first)

Use a real (in-tempdir) SQLite DB, **not mocks**. Test cases before implementation:

1. `TestInsertMessage_BumpsSession` — insert message; assert `sessions.msg_count`, `tokens_in/out`, `cost_usd`, `last_msg_at` updated.
2. `TestInsertMessage_NewSession` — insert when no row in sessions yet — should fail with FK error (W4/W5 must Upsert session before first message).
3. `TestListMessagesBySession_Order` — insert 100 messages; list in pages of 20; assert order stable.
4. `TestListMessagesBySession_Pagination` — cursor-based pagination across the 100 messages.
5. `TestUpsertSession_Idempotent` — call twice with same data, no error, no duplicate.
6. `TestListSessions_FilterByCLI` — mix of claude+codex; filter returns only matches.
7. `TestBulkInsert_5K` — 5000 inserts in one tx; <1s on dev laptop.

---

## Hard boundaries

- Do **NOT** touch `messages_fts` (DDL is in W0 migrations; FTS reads are W3).
- Do **NOT** add summary code — `summaries.go` is W3.
- Do **NOT** add search code — `search.go` is W3.
- Do **NOT** modify `db.go`, `migrate.go` — those are W1.

---

## Done

When W3, W7, W11 can `import "<module>/internal/store"` and reliably insert/read messages and sessions.
