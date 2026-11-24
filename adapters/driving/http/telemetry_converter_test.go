package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/telemetry"
	"testing"
	"time"
)

func TestToTelemetryQuery(t *testing.T) {
	from := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	q := ToTelemetryQuery(api.GetCharacterTelemetryRequestObject{
		UserID:      "u1",
		CharacterID: "c1",
		Params: api.GetCharacterTelemetryParams{
			From:       &from,
			To:         &to,
			GameMode:   new(api.GameModeTrials),
			SnapshotID: new("snap-1"),
			Limit:      new(125),
		},
	})

	if q.UserID != "u1" || q.CharacterID != "c1" || !q.From.Equal(from) || !q.To.Equal(to) || q.GameMode != telemetry.GameModeTrials || q.SnapshotID != "snap-1" || q.Limit != 125 {
		t.Fatalf("unexpected converted query: %+v", q)
	}

	empty := ToTelemetryQuery(api.GetCharacterTelemetryRequestObject{
		UserID:      "u1",
		CharacterID: "c1",
	})
	if !empty.From.IsZero() || !empty.To.IsZero() || empty.GameMode != "" || empty.SnapshotID != "" || empty.Limit != 0 {
		t.Fatalf("expected zero optional query fields, got %+v", empty)
	}
}

func TestToAPITelemetryResponse(t *testing.T) {
	period := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	resp := ToAPITelemetryResponse(telemetry.Result{
		Matches: []telemetry.Match{
			{
				ActivityID:           "act-1",
				Period:               period,
				Location:             "Javelin-4",
				Activity:             "Trials of Osiris",
				EstimatedGameMode:    telemetry.GameModeTrials,
				Kills:                14,
				Deaths:               5,
				Assists:              6,
				Won:                  true,
				TimePlayedSeconds:    512,
				HasTimePlayedSeconds: true,
				SessionID:            "sess-1",
				SnapshotID:           "snap-1",
				SnapshotConfidence:   telemetry.ConfidenceHigh,
			},
			{
				ActivityID: "act-2",
				Period:     period.Add(time.Minute),
				Location:   "Burnout",
				Activity:   "Control",
			},
		},
		Snapshots: map[string]telemetry.SnapshotSummary{
			"snap-1": {ID: "snap-1", Name: "Solar Hand Cannon"},
		},
		TotalCount: 42,
		Truncated:  true,
	})

	if resp.TotalCount != 42 || !resp.Truncated || len(resp.Matches) != 2 || len(resp.Snapshots) != 1 {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
	m0 := resp.Matches[0]
	if m0.EstimatedGameMode == nil || *m0.EstimatedGameMode != api.GameModeTrials ||
		m0.TimePlayedSeconds == nil || *m0.TimePlayedSeconds != 512 ||
		m0.SessionID == nil || *m0.SessionID != "sess-1" ||
		m0.SnapshotID == nil || *m0.SnapshotID != "snap-1" ||
		m0.SnapshotConfidence == nil || *m0.SnapshotConfidence != api.HighConfidenceLevel {
		t.Fatalf("unexpected populated match DTO: %+v", m0)
	}
	m1 := resp.Matches[1]
	if m1.EstimatedGameMode != nil || m1.TimePlayedSeconds != nil || m1.SessionID != nil || m1.SnapshotID != nil || m1.SnapshotConfidence != nil {
		t.Fatalf("expected nil optional pointers on sparse match DTO, got %+v", m1)
	}
	if resp.Snapshots["snap-1"].ID != "snap-1" || resp.Snapshots["snap-1"].Name != "Solar Hand Cannon" {
		t.Fatalf("unexpected snapshots DTO: %+v", resp.Snapshots)
	}
}
