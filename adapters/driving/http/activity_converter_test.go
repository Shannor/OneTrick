package httpadapter

import (
	"errors"
	"oneTrick/api"
	"oneTrick/core/activity"
	"testing"
	"time"
)

func TestToAPIActivityResponse_FullAndMissingAggregate(t *testing.T) {
	now := time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)
	report := activity.MatchReport{
		Activity: activity.ActivityInfo{
			ReferenceID:       100,
			ActivityHash:      200,
			InstanceID:        "999",
			Location:          "Javelin-4",
			Activity:          "Control",
			Mode:              "Control",
			EstimatedGameMode: "quickplay",
			Period:            now,
			HasIsPrivate:      true,
			IsPrivate:         false,
		},
		Teams: []activity.Team{
			{ID: "16", Standing: "Victory", Score: "150", TeamName: "Alpha"},
		},
		Participants: []activity.Participant{
			{
				CharacterID:     "char-1",
				MembershipID:    "mem-1",
				MembershipType:  3,
				DisplayName:     "Weegee",
				DisplayNameCode: "1234",
				CharacterClass:  "Hunter",
				LightLevel:      2020,
				EmblemIconURL:   "https://www.bungie.net/img/e1.png",
				TeamID:          "16",
				Standing:        0,
				Score:           activity.StatValuePair{Value: 25, DisplayValue: "25", Present: true},
				Completed:       true,
				FireteamID:      "ft-1",
				UserID:          "u-1",
				SessionID:       "ses-1",
				SnapshotID:      "snap-1",
				ConfidenceLevel: activity.ConfidenceHigh,
				Performance: activity.Performance{
					PlayerStats: activity.PlayerStats{
						Kills: activity.StatValuePair{Value: 20, DisplayValue: "20", Present: true},
					},
					Weapons: map[string]activity.WeaponMetrics{
						"1001": {
							ReferenceID:         1001,
							HasDisplay:          true,
							Display:             activity.WeaponDisplay{Name: "Rose", Description: "Legendary Hand Cannon", Icon: "https://www.bungie.net/img/rose.png", HasIcon: true},
							BucketHash:          1498876634,
							ItemTypeDisplayName: "Hand Cannon",
						},
					},
				},
			},
			{
				CharacterID:    "char-untracked",
				MembershipID:   "mem-2",
				MembershipType: 1,
				DisplayName:    "Opponent",
				CharacterClass: "Titan",
				LightLevel:     2000,
				Standing:       1,
				Completed:      false,
			},
		},
		Aggregate: nil,
		Snapshots: map[string]activity.CharacterSnapshot{},
		Users: map[string]activity.User{
			"char-1": {ID: "u-1", DisplayName: "Weegee"},
		},
	}

	resp := ToAPIActivityResponse(report)
	if resp.Aggregate != nil {
		t.Fatalf("expected nil Aggregate when report.Aggregate is nil, got %+v", resp.Aggregate)
	}
	if resp.Snapshots == nil || resp.Users == nil || resp.Teams == nil || resp.Participants == nil {
		t.Fatal("expected non-nil required collections on GetActivity200JSONResponse")
	}
	if len(resp.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(resp.Participants))
	}
	p0 := resp.Participants[0]
	if p0.UserID == nil || *p0.UserID != "u-1" || p0.SnapshotID == nil || *p0.SnapshotID != "snap-1" || p0.ConfidenceLevel == nil || *p0.ConfidenceLevel != api.HighConfidenceLevel {
		t.Fatalf("unexpected mapped participant 0: %+v", p0)
	}
	rose := p0.Performance.Weapons["1001"]
	if rose.BucketHash == nil || *rose.BucketHash != 1498876634 || rose.ItemTypeDisplayName == nil || *rose.ItemTypeDisplayName != "Hand Cannon" {
		t.Fatalf("unexpected mapped weapon metadata: %+v", rose)
	}

	p1 := resp.Participants[1]
	if p1.UserID != nil || p1.SessionID != nil || p1.SnapshotID != nil || p1.ConfidenceLevel != nil || p1.TeamID != nil || p1.FireteamID != nil || p1.DisplayNameCode != nil {
		t.Fatalf("expected optional fields nil on untracked participant, got %+v", p1)
	}
}

func TestToAPIActivityError(t *testing.T) {
	if _, ok := ToAPIActivityError(activity.ErrInvalidActivityID).(api.GetActivity400JSONResponse); !ok {
		t.Fatalf("expected 400 response for ErrInvalidActivityID")
	}
	if _, ok := ToAPIActivityError(activity.ErrActivityNotFound).(api.GetActivity404JSONResponse); !ok {
		t.Fatalf("expected 404 response for ErrActivityNotFound")
	}
	res500, ok := ToAPIActivityError(activity.ErrDestinyUnavailable).(api.GetActivity500JSONResponse)
	if !ok || res500.Status != api.ErrDestinyServerDown {
		t.Fatalf("expected 500 with ErrDestinyServerDown, got %#v", res500)
	}
	resGeneric, ok := ToAPIActivityError(errors.New("boom")).(api.GetActivity500JSONResponse)
	if !ok || resGeneric.Status != api.ErrInternalSystemDown {
		t.Fatalf("expected 500 with ErrInternalSystemDown, got %#v", resGeneric)
	}
}
