package handlers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// --- merged-PR GitHub enrichment (spec §7.2 Layer-2) ----------------------
//
// The dashboard's "PRs merged" tile is the ONE place the productivity
// report crosses the deterministic boundary to an external API: it
// shells out to `gh pr list`. Everything here is best-effort — any
// failure (no gh, not authenticated, offline, repo not on GitHub)
// leaves Service.MergedPRs empty; the deterministic report is never
// blocked or destabilised (D8).
//
// To avoid hitting GitHub on every dashboard load each (repo, window)
// result is cached in github_pr_cache (migration 018) with its fetch
// time. A live `gh` call happens only when the cached row is missing
// or older than prCacheTTL. A stale row survives a failed refresh —
// last-known data beats no data.

// defaultPRCacheTTL is how long a cached `gh pr list` result stays
// fresh before a re-fetch. Overridable via KLYNE_PR_CACHE_TTL_MIN (an
// in-dashboard setting is a documented follow-up).
const defaultPRCacheTTL = 2 * time.Hour

// prCacheTTL resolves the merged-PR cache freshness window.
func prCacheTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KLYNE_PR_CACHE_TTL_MIN")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}
	return defaultPRCacheTTL
}

// enrichMergedPRs attaches the user's merged GitHub PRs to each Service
// in rep, serving the TTL cache and issuing a live `gh pr list` only
// for stale/missing entries. Safe to call unconditionally — when
// nothing can be resolved or fetched the report is left unchanged
// (Service.MergedPRs stays the deterministic empty slice).
func (h *ProductivityHandler) enrichMergedPRs(
	ctx context.Context, rep *productivity.Report, since, until time.Time,
) {
	// Resolve each service's GitHub repo; group service indices by slug
	// so worktrees of one repo trigger exactly one query.
	byRepo := map[string][]int{}
	for i := range rep.Services {
		if slug := githubRepoSlug(rep.Services[i].ProjectPath); slug != "" {
			byRepo[slug] = append(byRepo[slug], i)
		}
	}
	if len(byRepo) == 0 {
		return
	}

	ttl := prCacheTTL()
	loDay := since.AddDate(0, 0, -1).Format("2006-01-02")
	hiDay := until.AddDate(0, 0, 1).Format("2006-01-02")
	windowKey := loDay + ".." + hiDay

	type result struct {
		prs     []productivity.MergedPR
		fetched time.Time
	}
	results := map[string]result{}
	var toFetch []string

	for slug := range byRepo {
		if prs, fetchedAt, ok := h.cachedMergedPRs(ctx, slug, windowKey); ok {
			// Keep the cached value as a fallback regardless of age — a
			// failed refresh below should still serve last-known data.
			results[slug] = result{prs: prs, fetched: fetchedAt}
			if time.Since(fetchedAt) < ttl {
				continue // fresh — no fetch needed
			}
		}
		toFetch = append(toFetch, slug)
	}

	// Live-fetch only the stale/missing repos, concurrently.
	if len(toFetch) > 0 && ghAvailable(ctx) {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		var wg sync.WaitGroup
		var mu sync.Mutex
		fresh := map[string][]productivity.MergedPR{}
		for _, slug := range toFetch {
			wg.Add(1)
			go func(slug string) {
				defer wg.Done()
				prs, ok := queryMergedPRs(fctx, slug, since, until)
				if !ok {
					return // keep the stale fallback already in results
				}
				mu.Lock()
				fresh[slug] = prs
				mu.Unlock()
			}(slug)
		}
		wg.Wait()

		now := time.Now()
		for slug, prs := range fresh {
			results[slug] = result{prs: prs, fetched: now}
			h.storeMergedPRs(ctx, slug, windowKey, prs)
		}
	}

	// Attach to services. Repos with no resolvable PR data keep the
	// deterministic empty slice BuildReport already set.
	for slug, idxs := range byRepo {
		r, ok := results[slug]
		if !ok || r.prs == nil {
			continue
		}
		for _, i := range idxs {
			rep.Services[i].MergedPRs = r.prs
			rep.Services[i].MergedPRsAsOf = r.fetched
		}
	}
}

// ghAvailable reports whether the `gh` CLI is installed AND has a
// usable token configured (env var or keychain). Uses `gh auth token`
// because it is local-only and returns success on ANY working token
// source; `gh auth status` exits non-zero when ANY configured account
// fails its check, which falsely fails when one alt account is stale
// but the primary token works.
func ghAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	return exec.CommandContext(cctx, "gh", "auth", "token").Run() == nil
}

// githubRepoSlug resolves dir's `origin` remote to a GitHub
// "owner/name" slug, or "" when dir is not a git repo / has no origin.
func githubRepoSlug(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseRepoSlug(strings.TrimSpace(string(out)))
}

// parseRepoSlug extracts "owner/name" from a git remote URL — HTTPS
// (https://host/owner/name.git), SSH (ssh://git@host/owner/name), or
// scp-like with an optional host alias (git@host-alias:owner/name.git).
// Returns "" when no slug can be derived.
func parseRepoSlug(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:] // strip scheme → host/owner/name
	} else if i := strings.Index(raw, ":"); i >= 0 {
		raw = raw[:i] + "/" + raw[i+1:] // scp-like host:owner/name → host/owner/name
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" || strings.ContainsAny(owner, "@ ") {
		return ""
	}
	return owner + "/" + name
}

// queryMergedPRs runs `gh pr list` for PRs the user authored and merged
// within [since, until] for the GitHub repo slug. The second return is
// false on any failure (so the caller keeps a stale cache entry rather
// than overwriting it with nothing); a successful query with zero
// results returns (non-nil empty slice, true).
func queryMergedPRs(
	ctx context.Context, slug string, since, until time.Time,
) ([]productivity.MergedPR, bool) {
	// `gh`'s merged: search qualifier is day-granular — query a
	// day-padded range, then filter precisely by mergedAt in Go.
	lo := since.AddDate(0, 0, -1).Format("2006-01-02")
	hi := until.AddDate(0, 0, 1).Format("2006-01-02")
	// IMPORTANT: do NOT request `commits` here — gh expands it to a
	// GraphQL traversal of every commit's `authors` connection, which
	// trips GitHub's 500K node cap on repos with many PRs. We use
	// createdAt → mergedAt (PR open → merge) as the time-to-ship.
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", slug,
		"--state", "merged",
		"--author", "@me",
		"--search", "merged:"+lo+".."+hi,
		"--limit", "60",
		"--json", "number,title,headRefName,mergedAt,createdAt",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}

	var raw []struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		HeadRefName string    `json:"headRefName"`
		MergedAt    time.Time `json:"mergedAt"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, false
	}

	prs := []productivity.MergedPR{}
	for _, p := range raw {
		// gh's day-granular search can include PRs just outside the
		// real window — clamp precisely.
		if p.MergedAt.Before(since) || p.MergedAt.After(until) {
			continue
		}
		pr := productivity.MergedPR{
			Number:   p.Number,
			Title:    p.Title,
			HeadRef:  p.HeadRefName,
			MergedAt: p.MergedAt,
			OpenedAt: p.CreatedAt,
		}
		if !p.CreatedAt.IsZero() {
			if d := pr.MergedAt.Sub(p.CreatedAt); d > 0 {
				pr.TimeToShipMinutes = int(d.Minutes())
			}
		}
		prs = append(prs, pr)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].MergedAt.Before(prs[j].MergedAt) })
	return prs, true
}

// cachedMergedPRs reads a cached merged-PR list for (slug, windowKey).
// The third return is false on a cache miss or an unreadable row.
func (h *ProductivityHandler) cachedMergedPRs(
	ctx context.Context, slug, windowKey string,
) ([]productivity.MergedPR, time.Time, bool) {
	const q = `SELECT payload_json, fetched_at FROM github_pr_cache WHERE slug = ? AND window_key = ?`
	var payload string
	var fetchedMs int64
	if err := h.db.Read().QueryRowContext(ctx, q, slug, windowKey).Scan(&payload, &fetchedMs); err != nil {
		return nil, time.Time{}, false // sql.ErrNoRows or worse → treat as miss
	}
	var prs []productivity.MergedPR
	if err := json.Unmarshal([]byte(payload), &prs); err != nil {
		return nil, time.Time{}, false
	}
	if prs == nil {
		prs = []productivity.MergedPR{}
	}
	return prs, time.UnixMilli(fetchedMs), true
}

// storeMergedPRs upserts a freshly-fetched merged-PR list into the
// cache. Write failures are silent — the dashboard still renders with
// the live data it just fetched; the next load simply re-fetches.
func (h *ProductivityHandler) storeMergedPRs(
	ctx context.Context, slug, windowKey string, prs []productivity.MergedPR,
) {
	payload, err := json.Marshal(prs)
	if err != nil {
		return
	}
	const q = `
INSERT INTO github_pr_cache (slug, window_key, payload_json, fetched_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(slug, window_key) DO UPDATE SET
    payload_json = excluded.payload_json,
    fetched_at   = excluded.fetched_at`
	_, _ = h.db.Write().ExecContext(ctx, q, slug, windowKey, string(payload), time.Now().UnixMilli())
}
