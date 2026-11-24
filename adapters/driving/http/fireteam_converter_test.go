package httpadapter

import (
	"oneTrick/core/fireteam"
	"testing"
	"time"
)

func TestToAPIFireteamMembers(t *testing.T) {
	startedAt := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)

	got := ToAPIFireteamMembers([]fireteam.Member{
		{
			ID:           "u-1",
			MembershipID: "4611686018467484863",
			DisplayName:  "GuardianOne",
			Characters: []fireteam.Character{
				{
					ID:                  "char-1",
					Class:               "Hunter",
					Race:                "Exo",
					CurrentTitle:        "Flawless",
					Light:               2010,
					EmblemURL:           "/emblem.png",
					EmblemBackgroundURL: "/bg.png",
					EmblemColor:         fireteam.Color{Red: 10, Green: 20, Blue: 30, Alpha: 255},
					Stats: map[string]fireteam.ClassStat{
						"mobility": {
							Name:            "Mobility",
							Description:     "Move speed",
							Icon:            "/icon.png",
							HasIcon:         true,
							StatCategory:    1,
							AggregationType: 2,
							Value:           100,
						},
					},
				},
			},
			ActiveSessions: []fireteam.ActiveSession{
				{
					SessionID:   "ses-1",
					UserID:      "u-1",
					CharacterID: "char-1",
					StartedAt:   startedAt,
					TrackComp:   true,
				},
			},
		},
		{
			ID:             "u-2",
			MembershipID:   "4611686018467484002",
			DisplayName:    "GuardianTwo",
			Characters:     nil,
			ActiveSessions: nil,
		},
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 members, got %d", len(got))
	}
	m0 := got[0]
	if m0.ID != "u-1" || len(m0.Characters) != 1 || m0.Characters[0].Id != "char-1" {
		t.Fatalf("unexpected first member: %+v", m0)
	}
	if m0.Characters[0].EmblemColor.Red != 10 || m0.Characters[0].Stats == nil || (*m0.Characters[0].Stats)["mobility"].Value != 100 {
		t.Fatalf("unexpected character conversion: %+v", m0.Characters[0])
	}
	if m0.ActiveSessions == nil || len(*m0.ActiveSessions) != 1 {
		t.Fatalf("expected 1 active session, got %+v", m0.ActiveSessions)
	}
	s0 := (*m0.ActiveSessions)[0]
	if s0.SessionID != "ses-1" || s0.CharacterID != "char-1" || !s0.StartedAt.Equal(startedAt) || s0.TrackComp == nil || !*s0.TrackComp {
		t.Fatalf("unexpected active session conversion: %+v", s0)
	}

	m1 := got[1]
	if m1.Characters == nil || len(m1.Characters) != 0 {
		t.Fatalf("expected non-nil empty characters slice, got %#v", m1.Characters)
	}
	if m1.ActiveSessions == nil || len(*m1.ActiveSessions) != 0 {
		t.Fatalf("expected non-nil empty activeSessions slice, got %#v", m1.ActiveSessions)
	}
}
