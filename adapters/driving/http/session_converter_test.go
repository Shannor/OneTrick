package httpadapter

import (
	"errors"
	"oneTrick/api"
	"oneTrick/services/session"
	"testing"
)

func TestToAPISessionAggregatesResponse(t *testing.T) {
	t.Run("maps aggregates, snapshots, and participants with optional class and emblemUrl", func(t *testing.T) {
		resp := ToAPISessionAggregatesResponse(session.SessionAggregatesResult{
			Aggregates: []api.Aggregate{{ID: "agg-1"}},
			Snapshots:  map[string]api.CharacterSnapshot{"snap-1": {ID: "snap-1"}},
			Participants: map[string]session.SessionParticipant{
				"char-b": {
					UserID:         "user-b",
					DisplayName:    "Bravo#2222",
					CharacterID:    "char-b",
					CharacterClass: "Hunter",
					EmblemURL:      "https://www.bungie.net/emblem.jpg",
				},
				"char-c": {
					UserID:      "user-c",
					DisplayName: "Charlie#3333",
					CharacterID: "char-c",
				},
			},
		})

		if len(resp.Aggregates) != 1 || len(resp.Snapshots) != 1 || len(resp.Participants) != 2 {
			t.Fatalf("unexpected response sizes: %+v", resp)
		}
		b := resp.Participants["char-b"]
		if b.UserID != "user-b" || b.DisplayName != "Bravo#2222" || b.CharacterID != "char-b" ||
			b.Class == nil || *b.Class != "Hunter" || b.EmblemURL == nil || *b.EmblemURL != "https://www.bungie.net/emblem.jpg" {
			t.Fatalf("unexpected char-b participant DTO: %+v", b)
		}
		c := resp.Participants["char-c"]
		if c.Class != nil || c.EmblemURL != nil {
			t.Fatalf("expected nil Class/EmblemURL when empty, got %+v", c)
		}
	})

	t.Run("normalizes nil collections to non-nil empty collections", func(t *testing.T) {
		resp := ToAPISessionAggregatesResponse(session.SessionAggregatesResult{})
		if resp.Aggregates == nil || resp.Snapshots == nil || resp.Participants == nil {
			t.Fatalf("expected non-nil empty collections, got %+v", resp)
		}
	})
}

func TestToAPISessionAggregatesError(t *testing.T) {
	if _, ok := ToAPISessionAggregatesError(session.ErrNotFound).(api.GetSessionAggregates404JSONResponse); !ok {
		t.Fatalf("expected 404 response for session.ErrNotFound")
	}
	if _, ok := ToAPISessionAggregatesError(errors.New("boom")).(api.GetSessionAggregates500JSONResponse); !ok {
		t.Fatalf("expected 500 response for generic error")
	}
}

func TestToAPIBackfillSessionSummariesResponse(t *testing.T) {
	resp := ToAPIBackfillSessionSummariesResponse(session.BackfillSessionSummariesResult{
		DryRun:            true,
		AggregatesScanned: 10,
		AggregatesUpdated: 4,
		AggregatesFailed:  1,
		SessionsScanned:   6,
		SessionsUpdated:   3,
		SessionsFailed:    2,
	})
	if !resp.DryRun || resp.AggregatesScanned != 10 || resp.AggregatesUpdated != 4 || resp.AggregatesFailed != 1 ||
		resp.SessionsScanned != 6 || resp.SessionsUpdated != 3 || resp.SessionsFailed != 2 {
		t.Fatalf("unexpected backfill DTO: %+v", resp)
	}
}
