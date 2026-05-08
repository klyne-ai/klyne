package store

import (
	"context"
	"fmt"
	"strings"
)

// SearchHit represents a single full-text search result from the messages_fts
// virtual table. Snippet contains the FTS5 snippet() output centred around the
// matching tokens. Rank is the bm25() score — lower (more negative) values
// indicate a better match.
type SearchHit struct {
	MessageID   string  `json:"message_id"`
	SessionID   string  `json:"session_id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path"`
	TS          int64   `json:"ts"`
	Role        string  `json:"role"`
	Snippet     string  `json:"snippet"` // FTS5 snippet() output
	Rank        float64 `json:"rank"`    // bm25() — lower is better
}

// SearchSort enumerates the supported orderings for Search results.
type SearchSort string

const (
	// SearchSortRecent orders by message timestamp descending — newest hit
	// first. This is the default because users typically want "what did I
	// just discuss?" not "what's the densest match?".
	SearchSortRecent SearchSort = "recent"
	// SearchSortRelevance orders by FTS5 BM25 ascending (best match first).
	// Useful when looking up an old/forgotten thread by keyword density.
	SearchSortRelevance SearchSort = "relevance"
)

// Search performs a full-text search over the messages_fts virtual table and
// returns up to limit hits.
//
// Sort defaults to SearchSortRecent (ts DESC) — this matters in practice
// because BM25 favours short messages with high keyword density, which buries
// today's substantive matches under weeks-old one-liners. Pass
// SearchSortRelevance when the user explicitly asks for "best match".
//
// Special-character handling: FTS5 query syntax characters that would cause a
// parse error (' " * OR AND NOT NEAR) are sanitised before being passed to
// SQLite. The query is wrapped in double-quotes so it is treated as a phrase
// query after sanitisation, falling back to a simple match when quoting would
// produce an empty string.
//
// An empty query string returns an empty slice without error.
func Search(ctx context.Context, db *DB, q string, limit int, sort SearchSort) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SearchHit{}, nil
	}

	if limit <= 0 {
		limit = 20
	}

	// Sanitise the query for FTS5: escape double-quotes by doubling them, then
	// wrap the whole query in double-quotes so it is treated as a phrase. This
	// prevents FTS5 operator tokens (OR, AND, NOT, NEAR, *, ^) from being
	// interpreted as syntax.
	sanitised := sanitiseFTSQuery(q)

	orderClause := "f.ts DESC"
	if sort == SearchSortRelevance {
		orderClause = "rank ASC"
	}

	sqlTmpl := `
SELECT
    m.id        AS message_id,
    f.session_id,
    COALESCE(s.cli, '')          AS cli,
    COALESCE(s.project_path, '') AS project_path,
    f.ts,
    f.role,
    snippet(messages_fts, 0, '<mark>', '</mark>', '…', 64) AS snippet,
    bm25(messages_fts) AS rank
FROM messages_fts f
JOIN messages m  ON m.rowid = f.rowid
LEFT JOIN sessions s ON s.id = f.session_id
WHERE messages_fts MATCH ?
ORDER BY ` + orderClause + `
LIMIT ?`

	rows, err := db.Read().QueryContext(ctx, sqlTmpl, sanitised, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(
			&h.MessageID,
			&h.SessionID,
			&h.CLI,
			&h.ProjectPath,
			&h.TS,
			&h.Role,
			&h.Snippet,
			&h.Rank,
		); err != nil {
			return nil, fmt.Errorf("store: search scan: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search rows: %w", err)
	}

	if hits == nil {
		hits = []SearchHit{}
	}
	return hits, nil
}

// sanitiseFTSQuery escapes FTS5 special characters in q and wraps the result
// in double-quotes so SQLite treats the whole input as a phrase query. This
// prevents operator injection via user-supplied queries.
func sanitiseFTSQuery(q string) string {
	// Escape existing double-quotes by doubling them (FTS5 phrase-quoting rule).
	escaped := strings.ReplaceAll(q, `"`, `""`)
	// Wrap in double-quotes for phrase query semantics.
	return `"` + escaped + `"`
}
