// Package migrations exposes the on-disk SQL migration files via embed.FS
// so the store layer (W1) can apply them without filesystem access at
// runtime.
//
// W0-FROZEN CONTRACT
// ------------------
// The migration filenames, ordering, and the PRAGMA list documented below
// are part of the cross-workstream contract. Add new migrations as
// 004_*.sql, 005_*.sql, ... — never edit a landed migration. Renaming or
// reordering existing migrations requires a `contract-change` PR.
//
// Per-connection PRAGMAs (spec §5; W1 applies these on every *sql.DB
// connection via a connection hook):
//
//	PRAGMA journal_mode = WAL;
//	PRAGMA synchronous  = NORMAL;
//	PRAGMA temp_store   = MEMORY;
//	PRAGMA mmap_size    = 268435456;  -- 256 MB
//	PRAGMA busy_timeout = 5000;
//	PRAGMA foreign_keys = ON;
package migrations

import "embed"

// FS holds every *.sql migration file in this directory, in lexicographic
// (and therefore application) order.
//
//go:embed *.sql
var FS embed.FS

// PerConnectionPRAGMAs is the canonical PRAGMA list that the store layer
// MUST execute on every newly-opened SQLite connection (spec §5). It is
// declared here so the store layer (W1) and any future tooling share one
// source of truth.
var PerConnectionPRAGMAs = []string{
	"PRAGMA journal_mode = WAL;",
	"PRAGMA synchronous = NORMAL;",
	"PRAGMA temp_store = MEMORY;",
	"PRAGMA mmap_size = 268435456;",
	"PRAGMA busy_timeout = 5000;",
	"PRAGMA foreign_keys = ON;",
}
