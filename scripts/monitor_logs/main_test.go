package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAnalyzeEntriesAndSourceIndex(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	idx := buildSourceIndex(repoRoot)

	if _, ok := idx.handlersByEvent["get-best-performing-loadouts"]; !ok {
		t.Errorf("Expected GetBestPerformingLoadouts handler in sourceIndex, got none")
	}
	if _, ok := idx.jobsByName["update-user-characters-bg"]; !ok {
		t.Errorf("Expected update-user-characters-bg job in sourceIndex, got none")
	}
	if _, ok := idx.serviceGroups["statsService.getBestPerformingLoadouts"]; !ok {
		t.Errorf("Expected statsService.getBestPerformingLoadouts in sourceIndex, got none")
	}

	entries := []GCPLogEntry{
		// 1. Scanner 404 (should be suppressed by default)
		{
			InsertID:  "scan-1",
			Severity:  "ERROR",
			Timestamp: "2026-11-15T23:20:40Z",
			Labels:    map[string]string{"logger": "onetrick-service"},
			JSONPayload: map[string]any{
				"message": "validate-request-failure",
				"method":  "GET",
				"path":    "/api/session/properties",
				"status":  float64(404),
				"latency": "81.8µs",
				"error": map[string]any{
					"err": "no matching operation was found",
				},
			},
		},
		// 2. Application 500 error
		{
			InsertID:  "err-1",
			Severity:  "ERROR",
			Timestamp: "2026-11-15T11:10:46Z",
			Labels: map[string]string{
				"logger":     "onetrick-service",
				"commit-sha": "91dd8b4f287b",
			},
			JSONPayload: map[string]any{
				"message": "get-best-performing-loadouts-failure",
				"method":  "GET",
				"path":    "/metrics/best-performing-loadouts?characterId=2305843009493304557",
				"status":  float64(500),
				"latency": "103.9ms",
				"error": map[string]any{
					"err": "no loadouts found",
				},
				"request": map[string]any{
					"characterId": "2305843009493304557",
				},
				"statsService": map[string]any{
					"getBestPerformingLoadouts": map[string]any{
						"characterId": "2305843009493304557",
					},
				},
			},
		},
		// 3. Slow background job (>2s)
		{
			InsertID:  "slow-1",
			Severity:  "INFO",
			Timestamp: "2026-11-15T22:29:14Z",
			Labels:    map[string]string{"logger": "onetrick-service"},
			JSONPayload: map[string]any{
				"message": "update-user-characters-bg-success",
				"job":     "update-user-characters-bg",
				"latency": "2.42s",
				"userService": map[string]any{
					"updateCharacters": map[string]any{
						"characterCount": 3,
					},
				},
			},
		},
	}

	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	report := analyzeEntries(entries, idx, "gruntt-destiny", "onetrick", "24h", now.Add(-24*time.Hour), 2*time.Second, false, false, now)

	if report.SuppressedScanner404 != 1 {
		t.Errorf("Expected 1 suppressed scanner 404, got %d", report.SuppressedScanner404)
	}
	if report.TotalFlaggedCalls != 2 {
		t.Errorf("Expected 2 flagged calls, got %d", report.TotalFlaggedCalls)
	}
	if len(report.IssueGroups) != 2 {
		t.Fatalf("Expected 2 issue groups, got %d", len(report.IssueGroups))
	}

	first := report.IssueGroups[0]
	if first.Category != "ERROR" || first.OperationID != "GetBestPerformingLoadouts" {
		t.Errorf("Expected first group to be ERROR on GetBestPerformingLoadouts, got %+v", first)
	}
	if len(first.ServiceLocations) == 0 {
		t.Errorf("Expected ServiceLocations to be populated for statsService.getBestPerformingLoadouts")
	}

	second := report.IssueGroups[1]
	if second.Category != "SLOW" || second.JobName != "update-user-characters-bg" {
		t.Errorf("Expected second group to be SLOW on update-user-characters-bg, got %+v", second)
	}
}

func TestNormalizeRoute(t *testing.T) {
	got := normalizeRoute("/sessions/uinesWx6Aa5BhOX8xLSs/aggregates?foo=bar")
	if got != "/sessions/:id/aggregates" {
		t.Errorf("Expected '/sessions/:id/aggregates', got %q", got)
	}
	gotKebab := normalizeRoute("/metrics/best-performing-loadouts?characterId=2305843009493304557")
	if gotKebab != "/metrics/best-performing-loadouts" {
		t.Errorf("Expected '/metrics/best-performing-loadouts', got %q", gotKebab)
	}
}
