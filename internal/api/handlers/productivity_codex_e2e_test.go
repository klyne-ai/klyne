package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/hooks"
	"github.com/klyne-ai/klyne/internal/productivity"
)

// TestCodexClientSummaryFlowsToProductivityDashboard pins the complete
// production path: Codex JSONL -> Stop hook -> stop_summaries -> productivity
// API -> services[].what_was_done. The short summary is intentionally written
// with recap_visible=0 to prove the dashboard does not discard a valid
// KLYNE_SUMMARY merely because the worklog suppression heuristic was stricter.
func TestCodexClientSummaryFlowsToProductivityDashboard(t *testing.T) {
	db := newReflectTestStore(t)
	projectDir := filepath.Join(t.TempDir(), "codex-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	const (
		sessionID     = "codex-dashboard-e2e"
		clientSummary = "Fixed login validation in auth.go."
	)
	transcript := stageCodexDashboardTranscript(t, sessionID, projectDir, clientSummary)
	payload, err := json.Marshal(map[string]any{
		"session_id":      sessionID,
		"transcript_path": transcript,
		"cwd":             projectDir,
	})
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}

	result := hooks.SessionEnd(context.Background(), strings.NewReader(string(payload)), db)
	if result.ExitCode != 0 || len(result.Stderr) > 0 {
		t.Fatalf("codex session-end failed: exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}

	var (
		cli          string
		stored       string
		filesJSON    string
		recapVisible int
		ts           int64
	)
	if err := db.Read().QueryRowContext(context.Background(), `
SELECT cli, COALESCE(ai_drafted_summary,''), files_json, recap_visible, ts
  FROM stop_summaries
 WHERE session_id = ?
 ORDER BY ts DESC LIMIT 1`, sessionID).Scan(&cli, &stored, &filesJSON, &recapVisible, &ts); err != nil {
		t.Fatalf("read captured codex summary: %v", err)
	}
	if cli != "codex" || stored != clientSummary {
		t.Fatalf("captured row = cli:%q summary:%q, want codex/%q", cli, stored, clientSummary)
	}
	if !strings.Contains(filesJSON, "auth.go") {
		t.Errorf("files_json = %s, want Codex apply_patch target auth.go", filesJSON)
	}
	if recapVisible != 0 {
		t.Fatalf("recap_visible = %d, want 0 so this test exercises the dashboard's independent summary floor", recapVisible)
	}

	dayStart := time.UnixMilli(ts).Local()
	dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, dayStart.Location())
	req := httptest.NewRequest("GET", "/api/productivity?since="+
		strconv.FormatInt(dayStart.UnixMilli(), 10)+"&until="+
		strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10), nil)
	rec := httptest.NewRecorder()
	NewProductivityHandler(db).Get(rec, req)
	if rec.Code != 200 {
		t.Fatalf("productivity status = %d; body=%s", rec.Code, rec.Body.String())
	}

	var report productivity.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode productivity report: %v", err)
	}
	for _, service := range report.Services {
		if service.ProjectPath != projectDir || service.WhatWasDone == nil {
			continue
		}
		for _, detail := range service.WhatWasDone.Tier2.Details {
			if detail.Text == clientSummary {
				return
			}
		}
	}
	t.Fatalf("Codex client summary missing from productivity dashboard: %+v", report.Services)
}

func stageCodexDashboardTranscript(t *testing.T, sessionID, projectDir, summary string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".codex", "sessions", "2026", "07", "24")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir codex transcript root: %v", err)
	}
	path := filepath.Join(root, "rollout-"+sessionID+".jsonl")
	lines := []any{
		map[string]any{
			"timestamp": "2026-07-24T10:00:00.000Z",
			"type":      "session_meta",
			"payload": map[string]any{
				"id": sessionID, "cwd": projectDir,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T10:00:01.000Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "Fix login validation"}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T10:00:02.000Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "name": "apply_patch", "call_id": "patch-1",
				"input": "*** Begin Patch\n*** Update File: auth.go\n@@\n-old\n+new\n*** End Patch\n",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T10:00:03.000Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": "Implemented the fix.\nKLYNE_SUMMARY: " + summary,
				}},
			},
		},
	}
	var body strings.Builder
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal codex transcript line: %v", err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write codex transcript: %v", err)
	}
	return path
}
