package handlers

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
)

const openPRCacheKey = "open-associated-v1"

var productivityPRNumberRe = regexp.MustCompile(`(?i)\bPR\s*#(\d+)\b`)

// enrichPullRequests attaches current open/review PR state and verifies PR
// references embedded in What-was-done cards.
func (h *ProductivityHandler) enrichPullRequests(
	ctx context.Context, rep *productivity.Report, force bool,
) {
	byRepo := map[string][]int{}
	for i := range rep.Services {
		if slug := githubRepoSlug(rep.Services[i].ProjectPath); slug != "" {
			byRepo[slug] = append(byRepo[slug], i)
		}
	}
	if len(byRepo) == 0 {
		return
	}

	type result struct {
		prs       []productivity.PullRequest
		fetchedAt time.Time
	}
	results := map[string]result{}
	var fetch []string
	for slug := range byRepo {
		if prs, fetchedAt, ok := h.cachedPullRequests(ctx, slug); ok {
			results[slug] = result{prs: prs, fetchedAt: fetchedAt}
			if !force && time.Since(fetchedAt) < prCacheTTL() {
				continue
			}
		}
		fetch = append(fetch, slug)
	}

	if len(fetch) > 0 && ghAvailable(ctx) {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, slug := range fetch {
			wg.Add(1)
			go func(slug string) {
				defer wg.Done()
				prs, ok := queryOpenPullRequests(fctx, slug)
				if !ok {
					return
				}
				now := time.Now()
				mu.Lock()
				results[slug] = result{prs: prs, fetchedAt: now}
				mu.Unlock()
				h.storePullRequests(context.Background(), slug, prs)
			}(slug)
		}
		wg.Wait()
	}

	for slug, indexes := range byRepo {
		r := results[slug]
		for _, i := range indexes {
			svc := &rep.Services[i]
			relevant := relevantPullRequests(*svc, r.prs)
			for _, merged := range svc.MergedPRs {
				relevant = append(relevant, productivity.PullRequest{
					Number: merged.Number, Title: merged.Title, URL: merged.URL,
					HeadRef: merged.HeadRef, State: "MERGED",
					ReviewDecision: merged.ReviewDecision, Author: merged.Author,
					CreatedAt: merged.OpenedAt, UpdatedAt: merged.UpdatedAt,
					MergedAt: merged.MergedAt,
				})
			}
			svc.PullRequests = dedupePullRequests(relevant)
			if !r.fetchedAt.IsZero() {
				svc.PullRequestsAsOf = r.fetchedAt
			}
			reconcileCardPRRefs(svc)
		}
	}
}

func queryOpenPullRequests(ctx context.Context, slug string) ([]productivity.PullRequest, bool) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", slug,
		"--state", "open",
		"--limit", "100",
		"--json", "number,title,url,headRefName,state,isDraft,reviewDecision,author,createdAt,updatedAt",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var raw []struct {
		Number         int       `json:"number"`
		Title          string    `json:"title"`
		URL            string    `json:"url"`
		HeadRefName    string    `json:"headRefName"`
		State          string    `json:"state"`
		IsDraft        bool      `json:"isDraft"`
		ReviewDecision string    `json:"reviewDecision"`
		CreatedAt      time.Time `json:"createdAt"`
		UpdatedAt      time.Time `json:"updatedAt"`
		Author         struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, false
	}
	prs := make([]productivity.PullRequest, 0, len(raw))
	for _, p := range raw {
		prs = append(prs, productivity.PullRequest{
			Number: p.Number, Title: p.Title, URL: p.URL, HeadRef: p.HeadRefName,
			State: p.State, IsDraft: p.IsDraft, ReviewDecision: p.ReviewDecision,
			Author: p.Author.Login, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return prs, true
}

func relevantPullRequests(
	svc productivity.Service, candidates []productivity.PullRequest,
) []productivity.PullRequest {
	branches := map[string]bool{}
	tickets := map[string]bool{}
	numbers := map[int]bool{}
	for _, b := range svc.Branches {
		branches[b.Name] = true
		if b.TicketID != "" {
			tickets[b.TicketID] = true
		}
	}
	if svc.WhatWasDone != nil {
		if svc.WhatWasDone.Narrative != nil {
			for _, c := range svc.WhatWasDone.Narrative.Cards {
				if c.TicketID != "" {
					tickets[c.TicketID] = true
				}
				collectPRNumbers(c.Title+" "+c.Body, numbers)
				for _, ref := range c.Refs {
					if ref.Type == "pr" {
						collectPRNumbers(ref.Text, numbers)
					}
					if ref.Type == "branch" {
						branches[ref.Text] = true
					}
				}
			}
		}
		for _, d := range svc.WhatWasDone.Tier2.Details {
			collectPRNumbers(d.Text+" "+strings.Join(d.Evidence, " "), numbers)
		}
	}

	out := []productivity.PullRequest{}
	for _, pr := range candidates {
		relevant := numbers[pr.Number] || branches[pr.HeadRef]
		if !relevant {
			for ticket := range tickets {
				if strings.Contains(strings.ToLower(pr.HeadRef), strings.ToLower(ticket)) ||
					strings.Contains(strings.ToLower(pr.Title), strings.ToLower(ticket)) {
					relevant = true
					break
				}
			}
		}
		if relevant {
			out = append(out, pr)
		}
	}
	return out
}

func reconcileCardPRRefs(svc *productivity.Service) {
	if svc.WhatWasDone == nil || svc.WhatWasDone.Narrative == nil {
		return
	}
	byNumber := map[int]productivity.PullRequest{}
	for _, pr := range svc.PullRequests {
		byNumber[pr.Number] = pr
	}
	for i := range svc.WhatWasDone.Narrative.Cards {
		card := &svc.WhatWasDone.Narrative.Cards[i]
		seen := map[int]bool{}
		for j := range card.Refs {
			ref := &card.Refs[j]
			if ref.Type != "pr" {
				continue
			}
			if number, ok := firstPRNumber(ref.Text); ok {
				seen[number] = true
				if pr, found := byNumber[number]; found {
					applyVerifiedPRRef(ref, pr)
				}
			}
		}
		for _, pr := range svc.PullRequests {
			if seen[pr.Number] || card.TicketID == "" {
				continue
			}
			if strings.Contains(strings.ToLower(pr.HeadRef), strings.ToLower(card.TicketID)) ||
				strings.Contains(strings.ToLower(pr.Title), strings.ToLower(card.TicketID)) {
				ref := productivity.WWDRef{Type: "pr", Text: "PR #" + strconv.Itoa(pr.Number)}
				applyVerifiedPRRef(&ref, pr)
				card.Refs = append(card.Refs, ref)
				seen[pr.Number] = true
			}
		}
	}
}

func applyVerifiedPRRef(ref *productivity.WWDRef, pr productivity.PullRequest) {
	ref.Verified = true
	ref.URL = pr.URL
	ref.PRState = pr.State
	ref.ReviewDecision = pr.ReviewDecision
}

func collectPRNumbers(text string, out map[int]bool) {
	for _, match := range productivityPRNumberRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		if n, err := strconv.Atoi(match[1]); err == nil {
			out[n] = true
		}
	}
}

func firstPRNumber(text string) (int, bool) {
	match := productivityPRNumberRe.FindStringSubmatch(text)
	if len(match) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	return n, err == nil
}

func dedupePullRequests(in []productivity.PullRequest) []productivity.PullRequest {
	byNumber := map[int]productivity.PullRequest{}
	for _, pr := range in {
		existing, ok := byNumber[pr.Number]
		if !ok || pr.UpdatedAt.After(existing.UpdatedAt) || pr.State == "MERGED" {
			byNumber[pr.Number] = pr
		}
	}
	out := make([]productivity.PullRequest, 0, len(byNumber))
	for _, pr := range byNumber {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State == "OPEN"
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (h *ProductivityHandler) cachedPullRequests(
	ctx context.Context, slug string,
) ([]productivity.PullRequest, time.Time, bool) {
	const q = `SELECT payload_json, fetched_at FROM github_pr_cache WHERE slug = ? AND window_key = ?`
	var payload string
	var fetchedMs int64
	if err := h.db.Read().QueryRowContext(ctx, q, slug, openPRCacheKey).Scan(&payload, &fetchedMs); err != nil {
		return nil, time.Time{}, false
	}
	var prs []productivity.PullRequest
	if err := json.Unmarshal([]byte(payload), &prs); err != nil {
		return nil, time.Time{}, false
	}
	if prs == nil {
		prs = []productivity.PullRequest{}
	}
	return prs, time.UnixMilli(fetchedMs), true
}

func (h *ProductivityHandler) storePullRequests(
	ctx context.Context, slug string, prs []productivity.PullRequest,
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
	_, _ = h.db.Write().ExecContext(ctx, q, slug, openPRCacheKey, string(payload), time.Now().UnixMilli())
}
