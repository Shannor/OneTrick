package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/domain"
	"testing"
)

func TestDestinyConverters(t *testing.T) {
	if got := ToActivityQueryMode(api.Competitive); got != domain.ActivityQueryCompetitive {
		t.Fatalf("expected competitive, got %q", got)
	}
	if got := ToActivityQueryMode(api.Quickplay); got != domain.ActivityQueryQuickplay {
		t.Fatalf("expected quickplay, got %q", got)
	}
	if got := ToActivityQueryMode(api.IronBanner); got != domain.ActivityQueryIronBanner {
		t.Fatalf("expected ironBanner, got %q", got)
	}
	if got := ToActivityQueryMode(api.AllPvP); got != domain.ActivityQueryAllPvP {
		t.Fatalf("expected allPvP, got %q", got)
	}

	chars := ToAPICharacters([]domain.Character{{ID: "c-1", Class: "Hunter", Light: 2010}})
	if len(chars) != 1 || chars[0].Id != "c-1" || chars[0].Class != "Hunter" {
		t.Fatalf("unexpected converted characters: %+v", chars)
	}

	histories := ToAPIActivityHistories([]domain.ActivityInfo{
		{InstanceID: "555", Mode: "Control", EstimatedGameMode: "quickplay", HasIsPrivate: true, IsPrivate: false},
	})
	if len(histories) != 1 || histories[0].InstanceID != "555" || histories[0].EstimatedGameMode == nil || *histories[0].EstimatedGameMode != api.GameModeQuickPlay {
		t.Fatalf("unexpected converted histories: %+v", histories)
	}
}
